package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Transport carries JSON-RPC messages to one MCP server. Do sends a request and waits for its response; anything else the server says in
// the meantime (notifications, requests such as ping) goes to the handler.
type Transport interface {
	Start(ctx context.Context) error
	Do(ctx context.Context, id int64, payload []byte) (*Message, error)
	Send(ctx context.Context, payload []byte) error
	SetHandler(h func(*Message))
	// SetProtocolVersion tells the transport which revision was negotiated (HTTP sends it in a header).
	SetProtocolVersion(v string)
	// Alive is false once the connection is known to be gone, so the next call reconnects.
	Alive() bool
	Close() error
}

// ErrClosed is returned for calls on a connection that has ended.
var ErrClosed = errors.New("mcp: connection closed")

// StdioConfig says how to start a server process.
type StdioConfig struct {
	Command string
	Args    []string
	Env     []string // the complete environment of the process
	Dir     string
	Stderr  io.Writer // server log lines; nil discards them
	// StopGrace is how long Close waits after closing stdin, and again after SIGTERM, before escalating (default 2s).
	StopGrace time.Duration
}

type stdioTransport struct {
	cfg StdioConfig

	cmd   *exec.Cmd
	stdin io.WriteCloser
	wmu   sync.Mutex // serialises writes to stdin

	mu      sync.Mutex
	pending map[int64]chan *Message
	handler func(*Message)
	dead    chan struct{} // closed when the process's stdout ends
	waited  chan struct{} // closed when the process has been reaped
}

// NewStdio returns a transport that runs a server as a child process speaking newline-delimited JSON-RPC on stdin and stdout.
func NewStdio(cfg StdioConfig) Transport {
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = 2 * time.Second
	}
	return &stdioTransport{cfg: cfg, pending: map[int64]chan *Message{}}
}

func (s *stdioTransport) SetHandler(h func(*Message)) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
}

func (s *stdioTransport) SetProtocolVersion(string) {}

func (s *stdioTransport) Start(context.Context) error {
	cmd := exec.Command(s.cfg.Command, s.cfg.Args...)
	cmd.Env = s.cfg.Env
	cmd.Dir = s.cfg.Dir
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.cfg.Command, err)
	}
	s.cmd, s.stdin = cmd, stdin
	s.dead, s.waited = make(chan struct{}), make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		s.drainStderr(stderr)
	}()
	go func() {
		defer readers.Done()
		s.readLoop(stdout)
	}()
	go func() {
		readers.Wait()
		_ = cmd.Wait()
		close(s.waited)
	}()
	return nil
}

func (s *stdioTransport) drainStderr(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && s.cfg.Stderr != nil {
			_, _ = s.cfg.Stderr.Write(line)
		}
		if err != nil {
			return
		}
	}
}

func (s *stdioTransport) readLoop(r io.Reader) {
	defer close(s.dead)
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 1 {
			var m Message
			if json.Unmarshal(line, &m) == nil {
				s.deliver(&m)
			} else if s.cfg.Stderr != nil { // stdout is for protocol only, but servers do print banners; keep them in the log
				_, _ = s.cfg.Stderr.Write(line)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *stdioTransport) deliver(m *Message) {
	if m.IsResponse() {
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			return
		}
		s.mu.Lock()
		ch := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return
	}
	s.mu.Lock()
	h := s.handler
	s.mu.Unlock()
	if h != nil {
		h(m)
	}
}

func (s *stdioTransport) Alive() bool {
	if s.dead == nil {
		return false
	}
	select {
	case <-s.dead:
		return false
	default:
		return true
	}
}

func (s *stdioTransport) write(payload []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.stdin.Write(append(append([]byte{}, payload...), '\n'))
	return err
}

func (s *stdioTransport) Do(ctx context.Context, id int64, payload []byte) (*Message, error) {
	if !s.Alive() {
		return nil, ErrClosed
	}
	ch := make(chan *Message, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}
	if err := s.write(payload); err != nil {
		forget()
		return nil, errors.Join(ErrClosed, err)
	}
	select {
	case m := <-ch:
		return m, nil
	case <-ctx.Done():
		forget()
		return nil, context.Cause(ctx)
	case <-s.dead:
		forget()
		select { // a response may have been delivered just before the process went away
		case m := <-ch:
			return m, nil
		default:
		}
		return nil, ErrClosed
	}
}

func (s *stdioTransport) Send(_ context.Context, payload []byte) error {
	if !s.Alive() {
		return ErrClosed
	}
	return s.write(payload)
}

// Close shuts the server down the way Pi does: close its stdin, then SIGTERM, then SIGKILL to the whole process group.
func (s *stdioTransport) Close() error {
	if s.cmd == nil {
		return nil
	}
	_ = s.stdin.Close()
	for _, step := range []func(){func() {}, func() { signalGroup(s.cmd, false) }, func() { signalGroup(s.cmd, true) }} {
		step()
		select {
		case <-s.waited:
			return nil
		case <-time.After(s.cfg.StopGrace):
		}
	}
	return nil
}
