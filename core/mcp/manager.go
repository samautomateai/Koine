package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"koine/core/tools"
)

// Connection states of a server.
const (
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateFailed     = "failed"
	StateDisabled   = "disabled"
	StateNeedsLogin = "needs login"
)

// ConnectWait is how long Start waits for servers to connect before returning; slower servers go on connecting in the background. It is a
// variable so that tests can shorten it.
var ConnectWait = 10 * time.Second

// Manager owns the servers of one session and keeps their tools in a registry.
type Manager struct {
	Registry *tools.Registry
	Cwd      string
	Home     string
	Resolver Resolver
	// Log receives each server's stderr, prefixed with its name. Nil discards it.
	Log io.Writer
	// Store holds OAuth tokens; nil disables OAuth (a 401 is then an ordinary error).
	Store *TokenStore
	// HTTPClient is used for HTTP servers and OAuth requests. Nil means a client with sane timeouts.
	HTTPClient *http.Client
	// Notice receives problems that appear after Start returned (a slow server failing, a dropped connection).
	Notice func(string)

	mu       sync.Mutex
	servers  map[string]*Server
	used     map[string]bool // tool names taken
	deferred map[string]*mcpTool
	loaded   map[string]bool // deferred tools that tool_search has made visible
	wg       sync.WaitGroup
	closed   bool
}

// Server is one configured server and its connection.
type Server struct {
	Name  string
	Entry Entry
	mgr   *Manager

	mu     sync.Mutex
	state  string
	err    error
	client *Client
	tools  []*mcpTool
	connMu sync.Mutex // one connect at a time
	resCap bool
	info   InitResult
}

// Status is a snapshot of a server for display.
type Status struct {
	Name, State, Source, Exposure string
	Err                           string
	Tools                         int
	Overrides                     []string // tools whose exposure differs from the server's
	Resources                     bool
}

// NewManager returns a manager that registers tools in reg.
func NewManager(reg *tools.Registry, cwd, home string) *Manager {
	return &Manager{Registry: reg, Cwd: cwd, Home: home, Resolver: Resolver{Dir: cwd}, servers: map[string]*Server{},
		used: map[string]bool{}, deferred: map[string]*mcpTool{}, loaded: map[string]bool{}}
}

func (m *Manager) notice(f string, a ...any) {
	if m.Notice != nil {
		m.Notice(fmt.Sprintf(f, a...))
	}
}

func (m *Manager) httpClient() *http.Client {
	if m.HTTPClient != nil {
		return m.HTTPClient
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 0, IdleConnTimeout: 90 * time.Second}}
}

// Start connects the enabled entries in the background and waits up to wait (zero means ConnectWait) for them. It never fails: a server
// that cannot connect is recorded as failed and the others carry on.
func (m *Manager) Start(ctx context.Context, entries []Entry, wait time.Duration) {
	if wait <= 0 {
		wait = ConnectWait
	}
	done := make(chan struct{})
	var batch sync.WaitGroup
	for _, e := range entries {
		s := &Server{Name: e.Name, Entry: e, mgr: m, state: StateConnecting}
		if e.Disabled() {
			s.state = StateDisabled
		}
		m.mu.Lock()
		m.servers[e.Name] = s
		m.mu.Unlock()
		if e.Disabled() {
			continue
		}
		batch.Add(1)
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer batch.Done()
			if err := s.connect(ctx); err != nil {
				s.fail(err)
			}
		}()
	}
	go func() { batch.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(wait):
	case <-ctx.Done():
	}
}

// Wait blocks until every connection attempt has finished. Tests and the command line use it.
func (m *Manager) Wait() { m.wg.Wait() }

// Close shuts every server down.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	srv := make([]*Server, 0, len(m.servers))
	for _, s := range m.servers {
		srv = append(srv, s)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range srv {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.mu.Lock()
			c := s.client
			s.client = nil
			s.mu.Unlock()
			if c != nil {
				_ = c.Close()
			}
		}()
	}
	wg.Wait()
}

// Statuses lists the servers sorted by name.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	srv := make([]*Server, 0, len(m.servers))
	for _, s := range m.servers {
		srv = append(srv, s)
	}
	m.mu.Unlock()
	sort.Slice(srv, func(i, j int) bool { return srv[i].Name < srv[j].Name })
	out := make([]Status, 0, len(srv))
	for _, s := range srv {
		out = append(out, s.status())
	}
	return out
}

// Reconnect drops a server's connection and connects again.
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	m.mu.Lock()
	s := m.servers[name]
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("no MCP server named %q", name)
	}
	if s.Entry.Disabled() {
		return fmt.Errorf("MCP server %q is disabled", name)
	}
	s.drop()
	if err := s.connect(ctx); err != nil {
		s.fail(err)
		return err
	}
	return nil
}

func (s *Server) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Name: s.Name, State: s.state, Source: string(s.Entry.Source), Exposure: s.exposure(), Tools: len(s.tools), Resources: s.resCap}
	if s.err != nil {
		st.Err = s.err.Error()
	}
	for _, t := range s.tools {
		if mode := s.modeFor(t); mode != st.Exposure {
			st.Overrides = append(st.Overrides, t.def.Name+"="+mode)
		}
	}
	return st
}

func (s *Server) exposure() string {
	switch s.Entry.Config.Exposure {
	case "", "codemode", "codemode-deferred":
		if s.Entry.Config.Exposure == "" {
			return ExposureDirect // Koine's default; Pi's is codemode, which Koine does not have (compat/DIVERGENCES.md)
		}
		return ExposureDeferred
	}
	return s.Entry.Config.Exposure
}

func (s *Server) modeFor(t *mcpTool) string {
	mode := s.exposure()
	if te := s.Entry.Config.ToolExposure; te != nil {
		mode = te.ModeFor(t.def.Name, t.name, mode)
	}
	if mode == "codemode" || mode == "codemode-deferred" {
		return ExposureDeferred
	}
	return mode
}

func (s *Server) instructions() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info.Instructions
}

func (s *Server) fail(err error) {
	s.mu.Lock()
	s.state, s.err = StateFailed, err
	var na *AuthRequiredError
	if errors.As(err, &na) {
		s.state = StateNeedsLogin
	}
	s.mu.Unlock()
}

// drop closes the connection. The tools stay registered, so the model can still call them and the call reconnects.
func (s *Server) drop() {
	s.mu.Lock()
	c := s.client
	s.client = nil
	s.state = StateConnecting
	s.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// connect starts the transport, runs the handshake and registers the tools.
func (s *Server) connect(ctx context.Context) error {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.alive() {
		return nil
	}
	cfg := s.Entry.Config
	t, err := s.mgr.transport(ctx, s)
	if err != nil {
		return err
	}
	c := NewClient(t)
	if cfg.Timeout > 0 {
		c.Timeout = time.Duration(cfg.Timeout * float64(time.Second))
	}
	c.OnToolsChanged = func() {
		bg, cancel := context.WithTimeout(context.Background(), c.timeout())
		defer cancel()
		if err := s.refreshTools(bg, c); err != nil {
			s.mgr.notice("MCP server %s: refreshing tools: %v", s.Name, err)
		}
	}
	if err := c.Initialize(ctx); err != nil {
		_ = c.Close()
		return err
	}
	s.mu.Lock()
	s.client, s.info, s.state, s.err = c, c.Info, StateConnected, nil
	s.resCap = c.Info.Capabilities.Resources != nil
	s.mu.Unlock()
	if c.Info.Capabilities.Tools != nil {
		if err := s.refreshTools(ctx, c); err != nil {
			_ = c.Close()
			s.mu.Lock()
			s.client = nil
			s.mu.Unlock()
			return fmt.Errorf("tools/list: %w", err)
		}
	}
	s.mgr.syncResourceTools()
	return nil
}

func (s *Server) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client != nil && s.client.T.Alive()
}

// callTool runs a tool, reconnecting first when the connection has dropped, and once more when an HTTP session had expired.
func (s *Server) callTool(ctx context.Context, name string, args []byte) (*CallResult, error) {
	for attempt := 0; ; attempt++ {
		c, err := s.ready(ctx)
		if err != nil {
			return nil, err
		}
		res, err := c.CallTool(ctx, name, args)
		if errors.Is(err, ErrSessionExpired) && attempt == 0 {
			s.drop()
			continue
		}
		return res, err
	}
}

// ready returns a live client, reconnecting if needed.
func (s *Server) ready(ctx context.Context) (*Client, error) {
	if !s.alive() {
		s.drop()
		if err := s.connect(ctx); err != nil {
			s.fail(err)
			return nil, fmt.Errorf("MCP server %s is not connected: %w", s.Name, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, fmt.Errorf("MCP server %s is not connected", s.Name)
	}
	return s.client, nil
}

// transport builds the transport for a server, resolving ${ENV} and !command values.
func (m *Manager) transport(ctx context.Context, s *Server) (Transport, error) {
	cfg := s.Entry.Config
	r := m.Resolver
	if r.Dir == "" {
		r.Dir = m.Cwd
	}
	var logw io.Writer
	if m.Log != nil {
		logw = prefixWriter{w: m.Log, prefix: "[" + s.Name + "] "}
	}
	if cfg.IsHTTP() {
		url, err := r.Resolve(ctx, cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("url: %w", err)
		}
		headers, err := r.ResolveMap(ctx, cfg.Headers)
		if err != nil {
			return nil, fmt.Errorf("headers: %w", err)
		}
		hc := HTTPConfig{URL: url, Headers: headers, Client: m.httpClient()}
		if m.Store != nil && !hasHeader(headers, "Authorization") {
			hc.Auth = NewOAuth(s.Name, url, cfg.OAuth, m.Store, m.httpClient(), r)
		}
		return NewHTTP(hc), nil
	}
	cmd, err := r.Resolve(ctx, cfg.Command)
	if err != nil {
		return nil, fmt.Errorf("command: %w", err)
	}
	args := make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		if args[i], err = r.Resolve(ctx, a); err != nil {
			return nil, fmt.Errorf("args: %w", err)
		}
	}
	env, err := r.ResolveMap(ctx, cfg.Env)
	if err != nil {
		return nil, fmt.Errorf("env: %w", err)
	}
	full := os.Environ()
	for k, v := range env {
		full = append(full, k+"="+v)
	}
	dir := m.Cwd
	if cfg.Cwd != "" {
		if dir, err = r.Resolve(ctx, cfg.Cwd); err != nil {
			return nil, fmt.Errorf("cwd: %w", err)
		}
		dir = expandHome(dir, m.Home)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(m.Cwd, dir)
		}
	}
	return NewStdio(StdioConfig{Command: cmd, Args: args, Env: full, Dir: dir, Stderr: logw}), nil
}

func hasHeader(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") && home != "" {
		return filepath.Join(home, p[2:])
	}
	return p
}

// prefixWriter tags each write (one log line) with the server's name.
type prefixWriter struct {
	w      io.Writer
	prefix string
}

func (p prefixWriter) Write(b []byte) (int, error) {
	line := p.prefix + strings.TrimRight(string(b), "\r\n") + "\n"
	if _, err := io.WriteString(p.w, line); err != nil {
		return 0, err
	}
	return len(b), nil
}
