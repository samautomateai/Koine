// Package ext hosts out-of-process extensions. The wire protocol is
// newline-delimited JSON-RPC 2.0 over the child's stdin/stdout, so an
// extension can be written in any language. The bundled JS sidecar is just one
// implementation of it (it adapts Pi extensions and opencode plugins).
//
// Host -> extension requests:
//
//	initialize      {protocol, host, cwd, trusted, mode, hasUI}   -> {name?, events?: [string]}
//	event           {name, payload}                  -> events.Decision
//	                  resources_discover {cwd, reason: "startup"|"reload"} answers {resources: {skillPaths, promptPaths, themePaths}};
//	                  relative paths are taken from the extension's working directory
//	tool.execute    {name, args, callId}             -> {content, isError?}
//	command.execute {name, args}                     -> {output}
//	command.complete {name, prefix}                  -> {items: [{value, label}]}
//	cancel          (notification) {id}              the host gave up on request id; the extension aborts the work
//	bus_event       (notification) {channel, data}   an event another extension emitted on pi.events
//	host_state      (notification) {commands, tools} every slash command and tool, for pi.getCommands and ctx.tools
//	flags           (notification) {values: {name: value}}  command-line values of the registered flags
//	shutdown        (notification)
//
// Extension -> host notifications:
//
//	register_tool    {name, description, schema, promptSnippet?, promptGuidelines?}
//	register_command {name, description}
//	register_flag    {name, description, type: "boolean"|"string", default}
//	log              {level, message}
//	send_user_message {text, deliverAs?: "steer"|"followUp"}
//	append_entry     {customType, data}     stored in the session as a custom entry (never sent to the model)
//	tool_update      {callId: "<tool>/<id of the tool.execute call>", text}   partial output of a running tool
//	ui               {method, ...}   fire-and-forget UI: notify, setStatus, setWidget, setTitle, setWorkingMessage, set_editor_text (UIRequest)
//	bus_emit         {channel, data}   pi.events.emit, relayed to the other extensions
//	send_message     {customType, content, display, details?, deliverAs?, triggerTurn?}   pi.sendMessage
//	ext_error        {event, error}   a handler raised an error; the extension carries on
//	shutdown_request {}              ctx.shutdown()
//
// Extension -> host requests (the host answers with a result or an error):
//
//	ui.dialog        UIRequest (select, confirm, input, editor)   -> UIResponse
//	tools.execute    {name, args}   -> {content, isError}   runs a tool for ctx.executeTool
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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"koine/core/events"
	"koine/core/srcinfo"
	"koine/core/tools"
)

type Spec struct {
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	Dir     string            `json:"dir,omitempty"` // working dir for the process (default: project cwd)
	// Source says where the extension was found (Pi's sourceInfo); the tools and commands it registers carry it. Set by the app, never
	// read from a settings file.
	Source srcinfo.Info `json:"-"`
}

type Host struct {
	Cwd   string
	Tools *tools.Registry
	// Logf receives extension logs and stderr lines. May be nil.
	Logf func(format string, args ...any)
	// OnUserMessage is called when an extension asks to inject a user message (pi.sendUserMessage). deliverAs is "steer", "followUp" or "".
	OnUserMessage func(text, deliverAs string)
	// OnAppendEntry is called when an extension stores data in the session (pi.appendEntry).
	OnAppendEntry func(customType string, data json.RawMessage)
	// Trusted is told to extensions as ctx.isProjectTrusted().
	Trusted bool
	// Mode is what the host runs as, told to extensions as ctx.mode: "tui", "rpc", "json" or "print" (the default). HasUI says that a
	// frontend answers dialogs (ctx.hasUI). Both must be set before the extensions start.
	Mode  string
	HasUI bool
	// Dialog answers select, confirm, input and editor requests; ctx ends when the extension gave up or the dialog timed out. Surface is
	// told of the fire-and-forget requests. Both may be nil: dialogs then come back cancelled.
	Dialog  func(ctx context.Context, r UIRequest) UIResponse
	Surface func(r UIRequest)
	// RunTool runs a tool on behalf of an extension (ctx.executeTool) and returns its output and whether it failed.
	RunTool func(ctx context.Context, name string, args json.RawMessage) (string, bool)
	// OnSendMessage receives pi.sendMessage; OnError a handler error; OnShutdown ctx.shutdown().
	OnSendMessage func(m events.CustomMessage, o SendOptions)
	OnError       func(e ExtError)
	OnShutdown    func()

	mu       sync.Mutex
	procs    []*Proc
	commands []Command
	compat   []CompatNote
	flags    []Flag
	updates  sync.Map // call id of a running extension tool -> the function that takes its partial output
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
	return &Host{Cwd: cwd, Tools: reg}
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
	src    srcinfo.Info
	dir    string // working directory of the process; relative paths the extension returns are taken from here
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
	p := &Proc{Name: s.Name, host: h, cmd: cmd, in: in, pend: map[int64]chan msg{}, dead: make(chan struct{}), src: s.Source, dir: cmd.Dir}
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
	mode := h.Mode
	if mode == "" {
		mode = "print"
	}
	res, err := p.call(ictx, "initialize", map[string]any{"protocol": 1, "host": "koine", "cwd": h.Cwd, "trusted": h.Trusted, "mode": mode, "hasUI": h.HasUI})
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
		if m.ID != nil {
			go p.serve(m)
			return
		}
		p.notify(m)
	}
}

func (p *Proc) notify(m msg) {
	h := p.host
	switch m.Method {
	case "register_tool":
		var t struct {
			Name, Description string
			Schema            json.RawMessage
			PromptSnippet     string
			PromptGuidelines  []string
		}
		if json.Unmarshal(m.Params, &t) != nil || t.Name == "" {
			return
		}
		if len(t.Schema) == 0 {
			t.Schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		h.Tools.Register(&procTool{p: p, name: t.Name, desc: t.Description, schema: t.Schema, snippet: t.PromptSnippet, guidelines: t.PromptGuidelines})
		if !p.src.IsZero() {
			h.Tools.SetSource(t.Name, p.src)
		}
	case "register_command":
		var c struct {
			Name, Description string
			HasCompletions    bool
		}
		if json.Unmarshal(m.Params, &c) == nil && c.Name != "" {
			h.addCommand(Command{Name: c.Name, Description: c.Description, HasCompletions: c.HasCompletions, Source: p.src, proc: p})
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
		var u struct{ Text, DeliverAs string }
		if json.Unmarshal(m.Params, &u) == nil && h.OnUserMessage != nil {
			h.OnUserMessage(u.Text, u.DeliverAs)
		}
	case "append_entry":
		var e struct {
			CustomType string
			Data       json.RawMessage
		}
		if json.Unmarshal(m.Params, &e) == nil && e.CustomType != "" && h.OnAppendEntry != nil {
			h.OnAppendEntry(e.CustomType, e.Data)
		}
	case "ui":
		h.surface(p, m.Params)
	case "bus_emit":
		h.relay("bus_event", p, m.Params)
	case "send_message":
		var s struct {
			events.CustomMessage
			DeliverAs   string
			TriggerTurn bool
		}
		if json.Unmarshal(m.Params, &s) == nil && s.Content != "" && h.OnSendMessage != nil {
			h.OnSendMessage(s.CustomMessage, SendOptions{DeliverAs: s.DeliverAs, TriggerTurn: s.TriggerTurn})
		}
	case "ext_error":
		var e struct{ Event, Error string }
		if json.Unmarshal(m.Params, &e) == nil {
			h.logf("[%s] %s: %s", p.Name, e.Event, e.Error)
			if h.OnError != nil {
				h.OnError(ExtError{Extension: p.Name, Event: e.Event, Error: e.Error})
			}
		}
	case "shutdown_request":
		if h.OnShutdown != nil {
			h.OnShutdown()
		}
	case "tool_update":
		var u struct{ CallID, Text string }
		if json.Unmarshal(m.Params, &u) == nil {
			if fn, ok := h.updates.Load(u.CallID); ok {
				fn.(func(string))(u.Text)
			}
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
		p.notifyCancel(id)
		return nil, ctx.Err()
	}
}

// notifyCancel tells the extension that the host gave up on request id, so a tool or handler can stop its work (its AbortSignal fires).
func (p *Proc) notifyCancel(id int64) {
	raw, _ := json.Marshal(map[string]any{"id": id})
	p.write(msg{Method: "cancel", Params: raw})
}

// relay passes a notification of one extension on to all the others.
func (h *Host) relay(method string, from *Proc, params json.RawMessage) {
	h.mu.Lock()
	procs := append([]*Proc(nil), h.procs...)
	h.mu.Unlock()
	for _, p := range procs {
		if p != from {
			p.write(msg{Method: method, Params: params})
		}
	}
}

// Broadcast sends a notification to every extension.
func (h *Host) Broadcast(method string, params any) {
	raw, err := json.Marshal(params)
	if err != nil {
		return
	}
	h.relay(method, nil, raw)
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
			if name == events.ResourcesDiscover && d.Resources != nil {
				p.absolute(d.Resources)
			}
			out.Merge(d)
			payload = chain(name, payload, d)
		}
		if out.Block || out.Cancel {
			break
		}
	}
	return out
}

// absolute makes the paths an extension contributed absolute: a relative path is taken from the extension's working directory, a leading ~
// is the home directory. Blank entries are dropped.
func (p *Proc) absolute(r *events.Resources) {
	fix := func(in []string) []string {
		var out []string
		for _, s := range in {
			s = strings.TrimSpace(s)
			switch {
			case s == "":
				continue
			case s == "~" || strings.HasPrefix(s, "~/"):
				if home, err := os.UserHomeDir(); err == nil {
					s = filepath.Join(home, strings.TrimPrefix(s, "~"))
				}
			case !filepath.IsAbs(s):
				s = filepath.Join(p.dir, s)
			}
			out = append(out, filepath.Clean(s))
		}
		return out
	}
	r.SkillPaths, r.PromptPaths, r.ThemePaths = fix(r.SkillPaths), fix(r.PromptPaths), fix(r.ThemePaths)
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
