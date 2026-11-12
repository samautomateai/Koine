package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- harness: a running `--mode rpc` driven line by line ----

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type rpcSess struct {
	t         *testing.T
	in        *io.PipeWriter
	out, errb *syncBuf
	done      chan int
	seq       int
}

func startRPC(t *testing.T, args ...string) *rpcSess {
	t.Helper()
	pr, pw := io.Pipe()
	s := &rpcSess{t: t, in: pw, out: &syncBuf{}, errb: &syncBuf{}, done: make(chan int, 1)}
	args = append(args, "--mode", "rpc")
	go func() { s.done <- run(args, streams{In: pr, Out: s.out, Err: s.errb}) }()
	t.Cleanup(func() { _ = pw.Close() })
	return s
}

func (s *rpcSess) raw(line string) { s.t.Helper(); _, _ = io.WriteString(s.in, line+"\n") }

func (s *rpcSess) send(v map[string]any) {
	s.t.Helper()
	b, _ := json.Marshal(v)
	s.raw(string(b))
}

func (s *rpcSess) records() []map[string]any {
	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.out.String()), "\n") {
		var r map[string]any
		if json.Unmarshal([]byte(line), &r) == nil {
			recs = append(recs, r)
		}
	}
	return recs
}

// wait returns the first record satisfying pred, polling until it appears.
func (s *rpcSess) wait(what string, pred func(map[string]any) bool) map[string]any {
	s.t.Helper()
	for i := 0; i < 1000; i++ {
		for _, r := range s.records() {
			if pred(r) {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.t.Fatalf("never saw %s\nstdout:\n%s\nstderr:\n%s", what, s.out.String(), s.errb.String())
	return nil
}

func ofType(typ string) func(map[string]any) bool {
	return func(r map[string]any) bool { return r["type"] == typ }
}

// cmd sends a command with a fresh id and returns its response.
func (s *rpcSess) cmd(typ string, fields map[string]any) map[string]any {
	s.t.Helper()
	s.seq++
	id := fmt.Sprintf("c%d", s.seq)
	c := map[string]any{"type": typ, "id": id}
	for k, v := range fields {
		c[k] = v
	}
	s.send(c)
	return s.wait("response to "+typ, func(r map[string]any) bool { return r["type"] == "response" && r["id"] == id })
}

func (s *rpcSess) ok(typ string, fields map[string]any) map[string]any {
	s.t.Helper()
	r := s.cmd(typ, fields)
	if r["success"] != true || r["command"] != typ {
		s.t.Fatalf("%s failed: %v", typ, r)
	}
	d, _ := r["data"].(map[string]any)
	return d
}

func (s *rpcSess) fails(typ string, fields map[string]any, want string) {
	s.t.Helper()
	r := s.cmd(typ, fields)
	if r["success"] != false || !strings.Contains(fmt.Sprint(r["error"]), want) {
		s.t.Fatalf("%s should fail with %q: %v", typ, want, r)
	}
}

func (s *rpcSess) stop() int {
	s.t.Helper()
	_ = s.in.Close()
	select {
	case code := <-s.done:
		return code
	case <-time.After(20 * time.Second):
		s.t.Fatal("rpc server did not stop at the end of stdin")
	}
	return -1
}

// gateAPI is a provider that answers "ok", and holds its answer back while blocked.
type gateAPI struct {
	*httptest.Server
	mu   sync.Mutex
	hold chan struct{}
}

func newGateAPI(t *testing.T) *gateAPI {
	g := &gateAPI{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		g.mu.Lock()
		h := g.hold
		g.mu.Unlock()
		if h != nil {
			select {
			case <-h:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *gateAPI) block() { g.mu.Lock(); g.hold = make(chan struct{}); g.mu.Unlock() }
func (g *gateAPI) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hold != nil {
		close(g.hold)
		g.hold = nil
	}
}

func data(r map[string]any) map[string]any { d, _ := r["data"].(map[string]any); return d }

// ---- tests ----

func TestRPCAnswersEveryCommandWithOneResponse(t *testing.T) {
	api := newGateAPI(t)
	isolate(t, api.URL)
	s := startRPC(t, "-C", t.TempDir(), "--no-session", "--trust")

	s.raw("not json")
	if r := s.wait("parse error", func(r map[string]any) bool { return r["command"] == "parse" }); r["success"] != false || r["type"] != "response" {
		t.Fatalf("%v", r)
	}
	s.raw(`{"type":`)
	s.fails("nope", nil, "Unknown command: nope")

	st := s.ok("get_state", nil)
	if st["isStreaming"] != false || st["isCompacting"] != false || st["steeringMode"] != "one-at-a-time" || st["followUpMode"] != "one-at-a-time" ||
		st["sessionId"] != "" || st["messageCount"] != float64(0) || st["autoCompactionEnabled"] == nil {
		t.Errorf("get_state: %v", st)
	}
	if _, has := st["model"]; !has {
		t.Errorf("get_state has no model: %v", st)
	}
	if c := s.ok("get_commands", nil)["commands"]; c == nil {
		t.Error("get_commands")
	}
	if m := s.ok("get_available_models", nil)["models"].([]any); len(m) == 0 {
		t.Error("no models")
	}
	levels := s.ok("get_available_thinking_levels", nil)["levels"].([]any)
	s.ok("set_thinking_level", map[string]any{"level": "low"})
	s.fails("set_thinking_level", map[string]any{"level": "ludicrous"}, "")
	_ = levels
	s.cmd("cycle_thinking_level", nil)
	s.fails("set_model", map[string]any{"provider": "nobody", "modelId": "nothing"}, "")
	s.cmd("cycle_model", nil)

	s.ok("set_steering_mode", map[string]any{"mode": "all"})
	s.ok("set_follow_up_mode", map[string]any{"mode": "all"})
	s.fails("set_steering_mode", map[string]any{"mode": "some"}, "all")
	if st := s.ok("get_state", nil); st["steeringMode"] != "all" || st["followUpMode"] != "all" {
		t.Errorf("modes: %v", st)
	}
	s.fails("set_auto_compaction", nil, "enabled is required")
	s.ok("set_auto_compaction", map[string]any{"enabled": false})
	if s.ok("get_state", nil)["autoCompactionEnabled"] != false {
		t.Error("auto compaction not switched off")
	}
	s.fails("set_auto_retry", map[string]any{"enabled": true}, "not supported")
	s.fails("abort_retry", nil, "not supported")

	q := s.ok("clear_queue", nil)
	if len(q["steering"].([]any)) != 0 || len(q["followUp"].([]any)) != 0 {
		t.Errorf("clear_queue: %v", q)
	}
	s.ok("abort", nil)
	s.ok("abort_bash", nil)
	if len(s.ok("get_messages", nil)["messages"].([]any)) != 0 {
		t.Error("messages")
	}
	if s.ok("get_session_stats", nil)["totalMessages"] != float64(0) {
		t.Error("stats")
	}
	if e := s.ok("get_entries", nil); len(e["entries"].([]any)) != 0 || e["leafId"] != nil {
		t.Errorf("entries: %v", e)
	}
	if len(s.ok("get_tree", nil)["tree"].([]any)) != 0 || len(s.ok("get_fork_messages", nil)["messages"].([]any)) != 0 {
		t.Error("tree or fork messages")
	}
	if s.ok("get_last_assistant_text", nil)["text"] != nil {
		t.Error("last text")
	}
	s.fails("export_html", nil, "sessions are off")
	s.fails("fork", map[string]any{"entryId": "x"}, "sessions are off")
	s.fails("set_session_name", map[string]any{"name": " "}, "name is required")
	s.fails("switch_session", nil, "sessionPath is required")
	s.fails("bash", map[string]any{"command": " "}, "command is required")
	if code := s.stop(); code != 0 {
		t.Fatalf("exit %d: %s", code, s.errb.String())
	}
}

func TestRPCPromptRunsTheAgentAndSessionCommandsReadTheResult(t *testing.T) {
	api := newGateAPI(t)
	isolate(t, api.URL)
	dir, sdir := t.TempDir(), t.TempDir()
	s := startRPC(t, "-C", dir, "--session-dir", sdir, "--trust")

	d := s.ok("prompt", map[string]any{"message": "hello there"})
	if d["disposition"] != "started" {
		t.Fatalf("%v", d)
	}
	s.wait("agent_settled", ofType("agent_settled"))
	var types []string
	for _, r := range s.records() {
		types = append(types, r["type"].(string))
	}
	for _, want := range []string{"agent_start", "turn_start", "message_start", "message_end", "agent_end"} {
		if !has(types, want) {
			t.Errorf("no %s in %v", want, types)
		}
	}

	if got := s.ok("get_last_assistant_text", nil)["text"]; got != "ok" {
		t.Errorf("last text %v", got)
	}
	if len(s.ok("get_messages", nil)["messages"].([]any)) != 2 {
		t.Error("messages")
	}
	stats := s.ok("get_session_stats", nil)
	if stats["userMessages"] != float64(1) || stats["assistantMessages"] != float64(1) || stats["sessionId"] == "" {
		t.Errorf("stats: %v", stats)
	}
	state := s.ok("get_state", nil)
	file, _ := state["sessionFile"].(string)
	if file == "" || state["messageCount"] != float64(2) {
		t.Fatalf("state: %v", state)
	}

	entries := s.ok("get_entries", nil)
	list := entries["entries"].([]any)
	if len(list) < 2 || entries["leafId"] == nil {
		t.Fatalf("entries: %v", entries)
	}
	first := list[0].(map[string]any)["id"].(string)
	if after := s.ok("get_entries", map[string]any{"since": first})["entries"].([]any); len(after) != len(list)-1 {
		t.Errorf("since: %d of %d", len(after), len(list))
	}
	s.fails("get_entries", map[string]any{"since": "missing"}, "not found")
	tree := s.ok("get_tree", nil)["tree"].([]any)
	if len(tree) != 1 || len(tree[0].(map[string]any)["children"].([]any)) != 1 {
		t.Errorf("tree: %v", tree)
	}
	fm := s.ok("get_fork_messages", nil)["messages"].([]any)
	if len(fm) != 1 || fm[0].(map[string]any)["text"] != "hello there" {
		t.Fatalf("fork messages: %v", fm)
	}

	s.ok("set_session_name", map[string]any{"name": "my session"})
	if s.ok("get_state", nil)["sessionName"] != "my session" {
		t.Error("name not set")
	}
	html := filepath.Join(dir, "out.html")
	if p := s.ok("export_html", map[string]any{"outputPath": html})["path"]; p != html {
		t.Errorf("export path %v", p)
	}
	if _, err := os.Stat(html); err != nil {
		t.Error(err)
	}
	s.ok("export_html", nil) // default path

	b := s.ok("bash", map[string]any{"command": "echo from-rpc"})
	if !strings.Contains(fmt.Sprint(b["output"]), "from-rpc") || b["exitCode"] != float64(0) {
		t.Errorf("bash: %v", b)
	}
	if r := s.cmd("compact", map[string]any{"customInstructions": "short"}); r["command"] != "compact" {
		t.Errorf("compact: %v", r)
	}

	f := s.ok("fork", map[string]any{"entryId": fm[0].(map[string]any)["entryId"]})
	if f["text"] != "hello there" || f["cancelled"] != false {
		t.Errorf("fork: %v", f)
	}
	s.fails("fork", map[string]any{"entryId": "missing"}, "")
	if c := s.ok("clone", nil); c["cancelled"] != false {
		t.Errorf("clone: %v", c)
	}
	if n := s.ok("new_session", nil); n["cancelled"] != false {
		t.Errorf("new_session: %v", n)
	}
	if st := s.ok("get_state", nil); st["messageCount"] != float64(0) {
		t.Errorf("a new session starts empty: %v", st)
	}
	if sw := s.ok("switch_session", map[string]any{"sessionPath": file}); sw["cancelled"] != false {
		t.Errorf("switch: %v", sw)
	}
	if st := s.ok("get_state", nil); st["messageCount"].(float64) < 2 {
		t.Errorf("the resumed session has its messages: %v", st)
	}
	if code := s.stop(); code != 0 {
		t.Fatalf("exit %d: %s", code, s.errb.String())
	}
}

func TestRPCQueuesMessagesWhileTheAgentRuns(t *testing.T) {
	api := newGateAPI(t)
	isolate(t, api.URL)
	api.block()
	defer api.release()
	s := startRPC(t, "-C", t.TempDir(), "--no-session", "--trust")

	if d := s.ok("prompt", map[string]any{"message": "slow one"}); d["disposition"] != "started" {
		t.Fatalf("%v", d)
	}
	s.fails("prompt", map[string]any{"message": "second"}, "streamingBehavior")
	s.fails("prompt", map[string]any{"message": "second", "streamingBehavior": "later"}, "streamingBehavior must be")
	if d := s.ok("prompt", map[string]any{"message": "second", "streamingBehavior": "steer"}); d["disposition"] != "queued" {
		t.Errorf("%v", d)
	}
	if d := s.ok("prompt", map[string]any{"message": "third", "streamingBehavior": "followUp"}); d["disposition"] != "queued" {
		t.Errorf("%v", d)
	}
	if d := s.ok("steer", map[string]any{"message": "steered"}); d["disposition"] != "queued" {
		t.Errorf("%v", d)
	}
	if d := s.ok("follow_up", map[string]any{"message": "followed"}); d["disposition"] != "queued" {
		t.Errorf("%v", d)
	}
	st := s.ok("get_state", nil)
	if st["isStreaming"] != true || st["pendingMessageCount"] != float64(4) {
		t.Errorf("state while running: %v", st)
	}
	q := s.ok("clear_queue", nil)
	if len(q["steering"].([]any)) != 2 || len(q["followUp"].([]any)) != 2 {
		t.Errorf("queues: %v", q)
	}
	s.ok("abort", nil)
	s.wait("agent_settled", ofType("agent_settled"))
	if s.ok("get_state", nil)["isStreaming"] != false {
		t.Error("still streaming after abort")
	}
	if code := s.stop(); code != 0 {
		t.Fatalf("exit %d: %s", code, s.errb.String())
	}
}

const rpcExt = `
export default function (pi) {
  pi.registerCommand("ask", { description: "asks", handler: async (args, ctx) => {
    const ok = await ctx.ui.confirm("Sure?", "really");
    ctx.ui.notify("answer " + ok, "info");
    ctx.ui.setStatus("k", "v");
    ctx.ui.setWidget("w", ["l1"]);
    ctx.ui.setTitle("title");
    ctx.ui.setEditorText("draft");
    ctx.ui.setWorkingMessage("ignored in rpc");
  } });
  pi.registerCommand("pick", { handler: async (args, ctx) => { ctx.ui.notify("picked " + await ctx.ui.select("which", ["a", "b"])); } });
  pi.registerCommand("quit", { handler: async (args, ctx) => ctx.shutdown() });
  pi.on("tool_call", async () => { throw new Error("handler bug"); });
  pi.on("session_shutdown", async () => {});
}`

func TestRPCExtensionDialogsAndShutdown(t *testing.T) {
	api := newGateAPI(t)
	isolate(t, api.URL)
	dir := t.TempDir()
	extFile := filepath.Join(dir, "ext.mjs")
	if err := os.WriteFile(extFile, []byte(rpcExt), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startRPC(t, "-C", dir, "--no-session", "--trust", "-e", extFile)

	s.send(map[string]any{"type": "prompt", "id": "p1", "message": "/ask"})
	req := s.wait("confirm request", func(r map[string]any) bool { return r["type"] == "extension_ui_request" && r["method"] == "confirm" })
	if req["title"] != "Sure?" || req["message"] != "really" {
		t.Errorf("%v", req)
	}
	s.send(map[string]any{"type": "extension_ui_response", "id": "stray", "confirmed": true}) // nobody waits for it
	s.send(map[string]any{"type": "extension_ui_response", "id": req["id"], "confirmed": true})
	pr := s.wait("prompt response", func(r map[string]any) bool { return r["type"] == "response" && r["id"] == "p1" })
	if pr["success"] != true || data(pr)["disposition"] != "handled" {
		t.Errorf("%v", pr)
	}
	methods := map[string]map[string]any{}
	for _, r := range s.records() {
		if r["type"] == "extension_ui_request" {
			methods[r["method"].(string)] = r
		}
	}
	if methods["notify"]["message"] != "answer true" || methods["setStatus"]["statusKey"] != "k" || methods["setStatus"]["statusText"] != "v" {
		t.Errorf("notify/status: %v", methods)
	}
	if methods["setWidget"]["widgetKey"] != "w" || methods["setTitle"]["title"] != "title" || methods["set_editor_text"]["text"] != "draft" {
		t.Errorf("widget/title/editor: %v", methods)
	}
	if _, sent := methods["setWorkingMessage"]; sent {
		t.Error("setWorkingMessage is a no-op in RPC mode")
	}

	s.fails("steer", map[string]any{"message": "/ask"}, "Extension commands are not allowed")
	s.fails("follow_up", map[string]any{"message": "/ask"}, "Extension commands are not allowed")
	if cmds := s.ok("get_commands", nil)["commands"].([]any); len(cmds) < 3 {
		t.Errorf("commands: %v", cmds)
	}

	s.send(map[string]any{"type": "prompt", "id": "p2", "message": "/pick"})
	pick := s.wait("select request", func(r map[string]any) bool { return r["type"] == "extension_ui_request" && r["method"] == "select" })
	s.send(map[string]any{"type": "extension_ui_response", "id": pick["id"], "value": "b"})
	s.wait("picked b", func(r map[string]any) bool { return r["method"] == "notify" && r["message"] == "picked b" })

	s.send(map[string]any{"type": "prompt", "id": "p3", "message": "/ask"})
	req2 := s.wait("second confirm", func(r map[string]any) bool { return r["method"] == "confirm" && r["id"] != req["id"] })
	s.send(map[string]any{"type": "extension_ui_response", "id": req2["id"], "cancelled": true})
	s.wait("cancelled answer", func(r map[string]any) bool { return r["method"] == "notify" && r["message"] == "answer false" })

	s.send(map[string]any{"type": "prompt", "id": "p4", "message": "/quit"})
	select { // ctx.shutdown() ends the server although stdin stays open
	case code := <-s.done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("ctx.shutdown() did not stop the server")
	}
}

func TestRPCModeRefusesFileArguments(t *testing.T) {
	isolate(t, "http://127.0.0.1:1")
	code, _, errs := cli("", "--mode", "rpc", "@notes.md")
	if code != 2 || !strings.Contains(errs, "does not accept @file") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestRunModeTellsExtensionsHowKoineRuns(t *testing.T) {
	tty := streams{InTTY: true}
	cases := []struct {
		c    cliArgs
		st   streams
		want string
	}{
		{cliArgs{Mode: "rpc"}, tty, "rpc"},
		{cliArgs{Mode: "json"}, tty, "json"},
		{cliArgs{Print: true}, tty, "print"},
		{cliArgs{Message: []string{"hi"}}, tty, "print"},
		{cliArgs{Serve: ":1"}, tty, "print"},
		{cliArgs{}, streams{}, "print"},
		{cliArgs{}, tty, "tui"},
	}
	for _, c := range cases {
		if got := c.c.runMode(c.st); got != c.want {
			t.Errorf("%+v: %s, want %s", c.c, got, c.want)
		}
	}
}
