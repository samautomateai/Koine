// Package ext hosts out-of-process extensions. The wire protocol is
// newline-delimited JSON-RPC 2.0 over the child's stdin/stdout, so an
// extension can be written in any language. The bundled JS sidecar is just one
// implementation of it (it adapts Pi extensions and opencode plugins).
//
// Host -> extension requests:
//
//	initialize      {protocol, host, cwd, trusted}   -> {name?, events?: [string]}
//	event           {name, payload}                  -> events.Decision
//	tool.execute    {name, args, callId}             -> {content, isError?}
//	command.execute {name, args}                     -> {output}
//	flags           (notification) {values: {name: value}}  command-line values of the registered flags
//	shutdown        (notification)
//
// Extension -> host notifications:
//
//	register_tool    {name, description, schema}
//	register_command {name, description}
//	register_flag    {name, description, type: "boolean"|"string", default}
//	log              {level, message}
//	send_user_message {text}
package ext

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"koine/core/events"
	"koine/core/tools"
)

type Spec struct {
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	Dir     string            `json:"dir,omitempty"` // working dir for the process (default: project cwd)
}

type Command struct {
	Name, Description string
	proc              *Proc
}

type Host struct {
	Cwd   string
	Tools *tools.Registry
	// Logf receives extension logs and stderr lines. May be nil.
	Logf func(format string, args ...any)
	// OnUserMessage is called when an extension asks to inject a user message.
	OnUserMessage func(text string)
	// Trusted is told to extensions as ctx.isProjectTrusted().
	Trusted bool

	mu       sync.Mutex
	procs    []*Proc
	commands map[string]Command
	compat   []CompatNote
	flags    []Flag
}

// Flag is a command-line option an extension registered (pi.registerFlag). Type is "boolean" or "string".
type Flag struct {
	Name, Description, Type, Extension string
	Default                            any
}

// Flags returns the options the extensions registered, in the order they did.
func (h *Host) Flags() []Flag {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Flag(nil), h.flags...)
}

// SetFlags tells every extension the command-line values of its options (a bool for a boolean flag, a string otherwise). It is sent
// after the extensions have loaded, because the command line cannot be read until their flags are known.
func (h *Host) SetFlags(values map[string]any) {
	if len(values) == 0 {
		return
	}
	params, err := json.Marshal(map[string]any{"values": values})
	if err != nil {
		return
	}
	h.mu.Lock()
	procs := append([]*Proc(nil), h.procs...)
	h.mu.Unlock()
	for _, p := range procs {
		p.write(msg{JSONRPC: "2.0", Method: "flags", Params: params})
	}
}

// CompatNote records an API an extension used that koine does not (fully)
// implement. Kind is "unsupported" (called a no-op stand-in) or "inert"
// (known API that is accepted but has no effect here).
type CompatNote struct{ Extension, Kind, API string }

// Compat returns every distinct compatibility note reported so far.
func (h *Host) Compat() []CompatNote {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]CompatNote(nil), h.compat...)
}

func NewHost(cwd string, reg *tools.Registry) *Host {
	return &Host{Cwd: cwd, Tools: reg, commands: map[string]Command{}}
}

func (h *Host) logf(f string, a ...any) {
	if h.Logf != nil {
		h.Logf(f, a...)
	}
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type Proc struct {
	Name   string
	host   *Host
	cmd    *exec.Cmd
	in     io.WriteCloser
	wmu    sync.Mutex
	nextID atomic.Int64
	pmu    sync.Mutex
	pend   map[int64]chan msg
	dead   chan struct{}
	events map[string]bool // nil = all
}

// Start launches an extension and completes the initialize handshake.
// Tools/commands it registers are available when Start returns.
func (h *Host) Start(ctx context.Context, s Spec) error {
	if len(s.Command) == 0 {
		return errors.New("empty command")
	}
	cmd := exec.Command(s.Command[0], s.Command[1:]...)
	cmd.Dir = h.Cwd
	if s.Dir != "" {
		cmd.Dir = s.Dir
	}
	cmd.Env = append(os.Environ(), "KOINE_CWD="+h.Cwd)
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	errp, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p := &Proc{Name: s.Name, host: h, cmd: cmd, in: in, pend: map[int64]chan msg{}, dead: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(errp)
		for sc.Scan() {
			h.logf("[%s] %s", p.Name, sc.Text())
		}
	}()
	go p.readLoop(out)
	go func() { _ = cmd.Wait(); close(p.dead) }()

	ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := p.call(ictx, "initialize", map[string]any{"protocol": 1, "host": "koine", "cwd": h.Cwd, "trusted": h.Trusted})
	if err != nil {
		p.kill()
		return fmt.Errorf("initialize: %w", err)
	}
	var init struct {
		Name   string   `json:"name"`
		Events []string `json:"events"`
	}
	_ = json.Unmarshal(res, &init)
	if init.Events != nil {
		p.events = map[string]bool{}
		for _, e := range init.Events {
			p.events[e] = true
		}
	}
	h.mu.Lock()
	h.procs = append(h.procs, p)
	h.mu.Unlock()
	return nil
}

func (p *Proc) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var m msg
			if json.Unmarshal(line, &m) == nil {
				p.dispatch(m)
			} else {
				p.host.logf("[%s] non-protocol output: %s", p.Name, strings.TrimSpace(string(line)))
			}
		}
		if err != nil {
			p.pmu.Lock()
			for id, ch := range p.pend {
				ch <- msg{Error: &rpcErr{Code: -32000, Message: "extension exited"}}
				delete(p.pend, id)
			}
			p.pmu.Unlock()
			return
		}
	}
}

func (p *Proc) dispatch(m msg) {
	switch {
	case m.Method == "" && m.ID != nil:
		p.pmu.Lock()
		ch := p.pend[*m.ID]
		delete(p.pend, *m.ID)
		p.pmu.Unlock()
		if ch != nil {
			ch <- m
		}
	case m.Method != "":
		p.notify(m)
		if m.ID != nil { // we implement no ext->host requests yet
			p.write(msg{JSONRPC: "2.0", ID: m.ID, Error: &rpcErr{Code: -32601, Message: "method not found: " + m.Method}})
		}
	}
}

func (p *Proc) notify(m msg) {
	h := p.host
	switch m.Method {
	case "register_tool":
		var t struct {
			Name, Description string
			Schema            json.RawMessage
		}
		if json.Unmarshal(m.Params, &t) != nil || t.Name == "" {
			return
		}
		if len(t.Schema) == 0 {
			t.Schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		h.Tools.Register(&procTool{p: p, name: t.Name, desc: t.Description, schema: t.Schema})
	case "register_command":
		var c struct{ Name, Description string }
		if json.Unmarshal(m.Params, &c) == nil && c.Name != "" {
			h.mu.Lock()
			h.commands[c.Name] = Command{Name: c.Name, Description: c.Description, proc: p}
			h.mu.Unlock()
		}
	case "register_flag":
		var f struct {
			Name, Description, Type string
			Default                 any
		}
		if json.Unmarshal(m.Params, &f) == nil && f.Name != "" {
			if f.Type != "string" {
				f.Type = "boolean"
			}
			h.mu.Lock()
			h.flags = append(h.flags, Flag{Name: f.Name, Description: f.Description, Type: f.Type, Extension: p.Name, Default: f.Default})
			h.mu.Unlock()
		}
	case "compat":
		var c struct{ Kind, API string }
		if json.Unmarshal(m.Params, &c) == nil && c.API != "" {
			h.mu.Lock()
			h.compat = append(h.compat, CompatNote{p.Name, c.Kind, c.API})
			h.mu.Unlock()
		}
	case "log":
		var l struct{ Level, Message string }
		_ = json.Unmarshal(m.Params, &l)
		h.logf("[%s] %s", p.Name, l.Message)
	case "send_user_message":
		var u struct{ Text string }
		if json.Unmarshal(m.Params, &u) == nil && h.OnUserMessage != nil {
			h.OnUserMessage(u.Text)
		}
	}
}

func (p *Proc) write(m msg) {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, _ = p.in.Write(append(b, '\n'))
}

func (p *Proc) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	id := p.nextID.Add(1)
	ch := make(chan msg, 1)
	p.pmu.Lock()
	p.pend[id] = ch
	p.pmu.Unlock()
	p.write(msg{ID: &id, Method: method, Params: raw})
	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, errors.New(r.Error.Message)
		}
		return r.Result, nil
	case <-p.dead:
		return nil, errors.New("extension exited")
	case <-ctx.Done():
		p.pmu.Lock()
		delete(p.pend, id)
		p.pmu.Unlock()
		return nil, ctx.Err()
	}
}

func (p *Proc) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// Emit implements events.Dispatcher.
func (h *Host) Emit(ctx context.Context, name string, payload any) events.Decision {
	h.mu.Lock()
	procs := append([]*Proc(nil), h.procs...)
	h.mu.Unlock()
	var out events.Decision
	for _, p := range procs {
		if p.events != nil && !p.events[name] {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, err := p.call(cctx, "event", map[string]any{"name": name, "payload": payload})
		cancel()
		if err != nil {
			h.logf("[%s] event %s: %v", p.Name, name, err)
			continue
		}
		var d events.Decision
		if len(res) > 0 && json.Unmarshal(res, &d) == nil {
			out.Merge(d)
		}
		if out.Block {
			break
		}
	}
	return out
}

func (h *Host) Commands() []Command {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Command, 0, len(h.commands))
	for _, c := range h.commands {
		out = append(out, c)
	}
	return out
}

// RunCommand runs an extension-registered slash command.
func (h *Host) RunCommand(ctx context.Context, name, args string) (string, bool, error) {
	h.mu.Lock()
	c, ok := h.commands[name]
	h.mu.Unlock()
	if !ok {
		return "", false, nil
	}
	res, err := c.proc.call(ctx, "command.execute", map[string]any{"name": name, "args": args})
	if err != nil {
		return "", true, err
	}
	var r struct{ Output string }
	_ = json.Unmarshal(res, &r)
	return r.Output, true, nil
}

func (h *Host) Close() {
	h.mu.Lock()
	procs := h.procs
	h.procs = nil
	h.mu.Unlock()
	for _, p := range procs {
		p.write(msg{Method: "shutdown"})
		_ = p.in.Close()
		select {
		case <-p.dead:
		case <-time.After(2 * time.Second):
			p.kill()
		}
	}
}

type procTool struct {
	p          *Proc
	name, desc string
	schema     json.RawMessage
	callSeq    atomic.Int64
}

func (t *procTool) Name() string            { return t.name }
func (t *procTool) Description() string     { return t.desc }
func (t *procTool) Schema() json.RawMessage { return t.schema }
func (t *procTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	res, err := t.p.call(ctx, "tool.execute", map[string]any{"name": t.name, "args": args, "callId": fmt.Sprintf("ext-%d", t.callSeq.Add(1))})
	if err != nil {
		return "", err
	}
	var r struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return "", err
	}
	if r.IsError {
		return r.Content, errors.New(r.Content)
	}
	return r.Content, nil
}
