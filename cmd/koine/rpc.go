package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"koine/core/agent"
	"koine/core/app"
	"koine/core/llm"
)

// rpcServer is `--mode rpc` (REQ-RPC-001): Pi's RPC protocol (https://pi.dev/docs/latest/rpc). Commands arrive as JSON lines on stdin;
// stdout carries one response per command, the session events of the JSON event stream (jsonmode.go) and the extension UI requests of
// the sub-protocol (rpc_ui.go). Diagnostics go to stderr. Commands run on goroutines of their own, because a prompt may wait for the
// answer to a dialog that arrives on the same stream.
type rpcServer struct {
	a    *app.App
	ctx  context.Context
	errW io.Writer

	wmu sync.Mutex
	w   io.Writer

	emitMu sync.Mutex // serialises the events of a run with the messages added to the conversation
	misc   *jsonStream

	mu         sync.Mutex
	running    bool
	runDone    chan struct{}
	cancel     context.CancelFunc
	bashCancel context.CancelFunc
	compacting bool
	quitting   chan struct{}
	quitOnce   sync.Once
	wg         sync.WaitGroup

	ui rpcUI
}

// rpcCmd holds the fields any command may carry. Fields a command does not use stay empty.
type rpcCmd struct {
	Type               string          `json:"type"`
	ID                 json.RawMessage `json:"id"`
	Message            string          `json:"message"`
	Images             []rpcImage      `json:"images"`
	StreamingBehavior  string          `json:"streamingBehavior"`
	ParentSession      string          `json:"parentSession"`
	SessionPath        string          `json:"sessionPath"`
	EntryID            string          `json:"entryId"`
	Since              string          `json:"since"`
	OutputPath         string          `json:"outputPath"`
	Name               string          `json:"name"`
	Command            string          `json:"command"`
	ExcludeFromContext bool            `json:"excludeFromContext"`
	Provider           string          `json:"provider"`
	ModelID            string          `json:"modelId"`
	Level              string          `json:"level"`
	Mode               string          `json:"mode"`
	Enabled            *bool           `json:"enabled"`
	CustomInstructions string          `json:"customInstructions"`
}

type rpcImage struct{ Type, Data, MimeType string }

func (s *rpcServer) blocks(in []rpcImage) []llm.Block {
	var out []llm.Block
	for _, i := range in {
		if i.Data != "" && i.MimeType != "" {
			out = append(out, llm.ImageBlock(i.MimeType, i.Data))
		}
	}
	return out
}

// Write makes the server the single, locked writer of stdout.
func (s *rpcServer) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.w.Write(p)
}

func (s *rpcServer) put(r record) {
	b, err := json.Marshal(r)
	if err == nil {
		_, _ = s.Write(append(b, '\n'))
	}
}

func (s *rpcServer) reply(c rpcCmd, data any) {
	r := record{{"type", "response"}}
	if len(c.ID) > 0 {
		r = append(r, kv{"id", c.ID})
	}
	r = append(r, kv{"command", c.Type}, kv{"success", true})
	if data != nil {
		r = append(r, kv{"data", data})
	}
	s.put(r)
}

func (s *rpcServer) fail(c rpcCmd, msg string) {
	r := record{{"type", "response"}}
	if len(c.ID) > 0 {
		r = append(r, kv{"id", c.ID})
	}
	s.put(append(r, kv{"command", c.Type}, kv{"success", false}, kv{"error", msg}))
}

// runRPC serves the protocol on st until stdin ends, an extension asks to quit or ctx is cancelled. It returns the exit code.
func runRPC(ctx context.Context, a *app.App, st streams) int {
	s := &rpcServer{a: a, ctx: ctx, errW: st.Err, w: st.Out, quitting: make(chan struct{})}
	s.ui.init(s)
	s.misc = newJSONStream(s, a)
	a.NoUserBash = true
	a.SetInputSource(agent.SourceRPC)
	a.OnDialog, a.OnSurface = s.ui.dialog, s.ui.surface
	a.SetEventSink(s.sinkEvent)
	a.OnShutdown = func() { s.shutdown() }
	defer func() { a.OnDialog, a.OnSurface, a.OnShutdown = nil, nil, nil; a.SetEventSink(nil) }()

	lines := make(chan []byte)
	go func() {
		defer close(lines)
		br := bufio.NewReaderSize(st.In, 1<<20)
		for {
			line, err := br.ReadBytes('\n') // strict JSONL: split on LF only
			if len(bytes.TrimSpace(line)) > 0 {
				lines <- bytes.TrimRight(line, "\r\n")
			}
			if err != nil {
				return
			}
		}
	}()
	for open := true; open; {
		select {
		case line, ok := <-lines:
			if !ok {
				open = false
				break
			}
			s.handleLine(line)
		case <-s.quitting:
			open = false
		case <-ctx.Done():
			open = false
		}
	}
	// orderly shutdown: finish the work in progress, then let the extensions know
	s.finish()
	return 0
}

// finish waits for the commands in flight (a run ends when its work is done), then returns.
func (s *rpcServer) finish() {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		fmt.Fprintln(s.errW, "koine: rpc: gave up waiting for the running work")
	}
}

// shutdown asks the server to stop reading and exit. If the agent is busy it first finishes the run, as Pi does.
func (s *rpcServer) shutdown() { s.quitOnce.Do(func() { close(s.quitting) }) }

func (s *rpcServer) handleLine(line []byte) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		s.put(record{{"type", "response"}, {"command", "parse"}, {"success", false}, {"error", "Failed to parse command: " + err.Error()}})
		return
	}
	if probe.Type == "extension_ui_response" {
		s.ui.answer(line)
		return
	}
	var c rpcCmd
	if err := json.Unmarshal(line, &c); err != nil {
		s.put(record{{"type", "response"}, {"command", "parse"}, {"success", false}, {"error", "Failed to parse command: " + err.Error()}})
		return
	}
	if c.Type == "prompt" { // claims the run before the next line is read, so a following steer sees the agent busy
		s.prompt(c)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.command(c)
	}()
}

// sinkEvent receives events raised while no input is being handled.
func (s *rpcServer) sinkEvent(e agent.Event) { s.event(nil, e) }

// event writes what the JSON stream js does not know about, and passes the rest to js (which may be nil outside a run).
func (s *rpcServer) event(js *jsonStream, e agent.Event) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	switch e.Type {
	case "extension_error":
		s.put(rec("extension_error", "extensionPath", e.Data["extensionPath"], "event", e.Data["event"], "error", e.Data["error"]))
	case "custom_message":
		msg := map[string]any{"role": "custom", "customType": e.Data["customType"], "content": e.Data["content"], "display": e.Data["display"],
			"timestamp": time.Now().UnixMilli()}
		s.put(rec("message_start", "message", msg))
		s.put(rec("message_end", "message", msg))
	case "notice":
		fmt.Fprintln(s.errW, e.Text)
	case "error":
		fmt.Fprintln(s.errW, "koine: error:", e.Text)
	default:
		if js == nil {
			js = s.misc
		}
		js.Event(e)
	}
}

// ---- prompt ----

// prompt handles the prompt command: it starts a run, queues the message behind the run in progress, or lets an extension take it.
func (s *rpcServer) prompt(c rpcCmd) {
	text := strings.TrimSpace(c.Message)
	s.mu.Lock()
	busy := s.running
	if busy && !s.a.IsExtensionCommand(text) {
		s.mu.Unlock()
		s.queue(c, text)
		return
	}
	var runDone chan struct{}
	if !busy {
		s.running, s.runDone = true, make(chan struct{})
		runDone = s.runDone
	}
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if busy { // an extension command runs at once, even while the agent streams
			name, args, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
			_, _, err := s.a.Host.RunCommand(s.ctx, name, strings.TrimSpace(args))
			s.reply(c, map[string]any{"disposition": "handled"})
			if err != nil {
				fmt.Fprintln(s.errW, "koine: error:", err)
			}
			return
		}
		s.run(c, text, runDone)
	}()
}

// queue handles a prompt sent while the agent runs: it needs a streamingBehavior to say where the message goes.
func (s *rpcServer) queue(c rpcCmd, text string) {
	switch c.StreamingBehavior {
	case "steer", "followUp":
	case "":
		s.fail(c, "Agent is streaming: set streamingBehavior to \"steer\" or \"followUp\" to queue the message")
		return
	default:
		s.fail(c, fmt.Sprintf("streamingBehavior must be \"steer\" or \"followUp\" (got %q)", c.StreamingBehavior))
		return
	}
	if t, ok := s.a.ExpandTemplate(text); ok {
		text = t
	}
	if c.StreamingBehavior == "steer" {
		s.a.Agent.Steer(text)
	} else {
		s.a.Agent.FollowUp(text)
	}
	s.reply(c, map[string]any{"disposition": "queued"})
}

// run is one prompt from the input event to agent_settled. The response goes out as soon as the input is settled.
func (s *rpcServer) run(c rpcCmd, text string, runDone chan struct{}) {
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	js := newJSONStream(s, s.a)
	started := false
	settled := make(chan bool, 1)
	s.a.SetInputHook(func(handled bool) {
		if !handled {
			started = true
			s.emitMu.Lock()
			js.StartRun()
			s.emitMu.Unlock()
		}
		settled <- handled
	})
	s.a.OnMessage = func(m llm.Message) {
		s.emitMu.Lock()
		defer s.emitMu.Unlock()
		js.Message(m)
	}
	done := make(chan error, 1)
	go func() {
		quit, err := s.a.HandleInputImages(ctx, text, s.blocks(c.Images), func(e agent.Event) { s.event(js, e) })
		if quit {
			s.shutdown()
		}
		done <- err
	}()
	var err error
	select {
	case handled := <-settled:
		s.reply(c, map[string]any{"disposition": map[bool]string{true: "handled", false: "started"}[handled]})
		err = <-done
	case err = <-done: // a command that was not a prompt: nothing was started
		if err != nil {
			s.fail(c, err.Error())
		} else {
			s.reply(c, map[string]any{"disposition": "handled"})
		}
		err = nil
	}
	s.a.SetInputHook(nil)
	s.a.OnMessage = nil
	if started {
		s.emitMu.Lock()
		js.Finish(ctx.Err() != nil)
		s.emitMu.Unlock()
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(s.errW, "koine:", err)
	}
	cancel()
	s.mu.Lock()
	s.running, s.cancel = false, nil
	s.mu.Unlock()
	close(runDone)
}

// idle waits until no run is in progress.
func (s *rpcServer) idle() {
	s.mu.Lock()
	d := s.runDone
	busy := s.running
	s.mu.Unlock()
	if busy && d != nil {
		select {
		case <-d:
		case <-time.After(30 * time.Second):
		}
	}
}
