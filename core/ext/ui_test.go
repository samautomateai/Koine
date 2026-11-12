package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"koine/core/events"
	"koine/core/tools"
)

// A second protocol extension, for what extensions ask of the host: dialogs, nested tools, the bus, messages. Its tool_call handler does
// what payload.input.act says and answers with the outcome in the reason of a block.
const pyActor = `
import sys, json, os
WHO = os.environ.get("WHO", "?")
def send(**o): sys.stdout.write(json.dumps(dict(jsonrpc="2.0", **o)) + "\n"); sys.stdout.flush()
def ask(method, params):
    send(id=7, method=method, params=params)
    while True:
        m = json.loads(sys.stdin.readline())
        if m.get("method") is None and m.get("id") == 7: return m
for line in sys.stdin:
    m = json.loads(line); meth, id_, p = m.get("method"), m.get("id"), m.get("params", {})
    if meth == "initialize":
        send(method="log", params={"message": "init %s %s %s" % (WHO, p.get("mode"), p.get("hasUI"))})
        send(method="register_command", params={"name": "dup", "description": "d " + WHO, "hasCompletions": WHO == "a"})
        send(method="register_command", params={"name": "plain"})
        send(id=id_, result={"events": ["tool_call", "before_agent_start"]})
    elif meth == "command.execute": send(id=id_, result={"output": "ran %s by %s" % (p["name"], WHO)})
    elif meth == "command.complete": send(id=id_, result={"items": [{"value": p["prefix"] + "1", "label": "L"}]})
    elif meth == "bus_event": send(method="log", params={"message": "bus %s %s" % (WHO, json.dumps(p))})
    elif meth == "event" and p["name"] == "before_agent_start":
        send(id=id_, result={"systemPrompt": p["payload"]["systemPrompt"] + "+" + WHO})
    elif meth == "event":
        inp = p["payload"]["input"]; act = inp["act"]
        if act == "dialog":
            r = ask("ui.dialog", inp["req"])
            send(id=id_, result={"block": True, "reason": json.dumps(r.get("result", r.get("error")))})
        elif act == "nested":
            r = ask("tools.execute", {"name": inp["name"], "args": {"t": "n"}})
            send(id=id_, result={"block": True, "reason": json.dumps(r.get("result", r.get("error")))})
        elif act == "unknown":
            r = ask("nope", {})
            send(id=id_, result={"block": True, "reason": r["error"]["message"]})
        elif act == "bus":
            if inp.get("who", WHO) == WHO: send(method="bus_emit", params={"channel": "c", "data": {"n": 1}})
            send(id=id_, result={})
        elif act == "msg":
            send(method="send_message", params={"customType": "t", "content": "hello", "display": True, "deliverAs": "followUp", "triggerTurn": True})
            send(method="send_message", params={"customType": "t", "content": ""})
            send(method="ui", params={"method": "setTitle", "text": "T"})
            send(method="ui", params={"method": ""})
            send(method="ext_error", params={"event": "tool_call", "error": "boom"})
            send(method="shutdown_request", params={}); send(id=id_, result={})
        elif act == "slow":
            send(method="log", params={"message": "slow started"})
            while True:
                n = json.loads(sys.stdin.readline())
                if n.get("method") == "cancel":
                    send(method="log", params={"message": "cancel %s" % n["params"]["id"]}); break
            send(id=id_, result={})
    elif meth == "shutdown": break
`

type actor struct {
	h    *Host
	mu   sync.Mutex
	logs []string
}

func (a *actor) log() string { a.mu.Lock(); defer a.mu.Unlock(); return strings.Join(a.logs, "\n") }

func (a *actor) waitLog(t *testing.T, want string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if strings.Contains(a.log(), want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("log never had %q:\n%s", want, a.log())
}

// startActors starts one actor extension per name in who (each told WHO), with the host fields set by setup.
func startActors(t *testing.T, setup func(h *Host), who ...string) *actor {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "actor.py")
	if err := os.WriteFile(script, []byte(pyActor), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &actor{h: NewHost(dir, tools.NewRegistry())}
	a.h.Logf = func(f string, args ...any) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.logs = append(a.logs, strings.TrimSpace(fmt.Sprintf(f, args...)))
	}
	if setup != nil {
		setup(a.h)
	}
	for _, w := range who {
		spec := Spec{Name: "ext-" + w, Command: []string{"python3", script}, Env: map[string]string{"WHO": w}}
		if err := a.h.Start(context.Background(), spec); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(a.h.Close)
	return a
}

func act(a *actor, input map[string]any) events.Decision {
	return a.h.Emit(context.Background(), events.ToolCall, map[string]any{"toolName": "x", "input": input})
}

func str(s string) *string { return &s }

func TestUIDialogsAreAnsweredByTheHost(t *testing.T) {
	var got []UIRequest
	var mu sync.Mutex
	a := startActors(t, func(h *Host) {
		h.Mode, h.HasUI = "rpc", true
		h.Dialog = func(ctx context.Context, r UIRequest) UIResponse {
			mu.Lock()
			got = append(got, r)
			mu.Unlock()
			switch r.Method {
			case "confirm":
				yes := true
				return UIResponse{Confirmed: &yes}
			case "input":
				<-ctx.Done() // never answered: the timeout resolves it
				return UIResponse{}
			}
			return UIResponse{Value: str("picked " + r.Options[0])}
		}
	}, "a")
	a.waitLog(t, "init a rpc True")

	d := act(a, map[string]any{"act": "dialog", "req": map[string]any{"method": "select", "title": "T", "options": []string{"x", "y"}}})
	if !d.Block || d.Reason != `{"value": "picked x"}` {
		t.Fatalf("select: %+v", d)
	}
	d = act(a, map[string]any{"act": "dialog", "req": map[string]any{"method": "confirm", "title": "T", "message": "ok?"}})
	if d.Reason != `{"confirmed": true}` {
		t.Fatalf("confirm: %+v", d)
	}
	d = act(a, map[string]any{"act": "dialog", "req": map[string]any{"method": "input", "title": "T", "timeout": 50}})
	if d.Reason != `{"cancelled": true}` {
		t.Fatalf("a dialog that times out resolves as cancelled: %+v", d)
	}
	mu.Lock()
	if len(got) != 3 || got[0].Extension != "ext-a" || got[0].Options[1] != "y" || got[1].Message != "ok?" {
		t.Errorf("requests: %+v", got)
	}
	mu.Unlock()
	if !(UIRequest{Method: "editor"}).IsDialog() || (UIRequest{Method: "notify"}).IsDialog() {
		t.Error("IsDialog")
	}
}

func TestDialogWithoutAFrontendIsCancelled(t *testing.T) {
	a := startActors(t, nil, "a")
	d := act(a, map[string]any{"act": "dialog", "req": map[string]any{"method": "select", "options": []string{"x"}}})
	if d.Reason != `{"cancelled": true}` {
		t.Fatalf("%+v", d)
	}
	a.waitLog(t, "init a print False") // the default mode
}

func TestExtensionsCanRunToolsThroughTheHost(t *testing.T) {
	a := startActors(t, func(h *Host) {
		h.RunTool = func(ctx context.Context, name string, args json.RawMessage) (string, bool) {
			return name + string(args), name == "bad"
		}
	}, "a")
	if d := act(a, map[string]any{"act": "nested", "name": "ok"}); d.Reason != `{"content": "ok{\"t\": \"n\"}", "isError": false}` {
		t.Fatalf("%+v", d)
	}
	if d := act(a, map[string]any{"act": "nested", "name": "bad"}); !strings.Contains(d.Reason, `"isError": true`) {
		t.Fatalf("%+v", d)
	}
	if d := act(a, map[string]any{"act": "unknown"}); d.Reason != "method not found: nope" {
		t.Fatalf("unknown requests are refused: %+v", d)
	}
	a.h.RunTool = nil
	if d := act(a, map[string]any{"act": "nested", "name": "ok"}); !strings.Contains(d.Reason, "cannot run tools") {
		t.Fatalf("%+v", d)
	}
}

func TestEventBusReachesOtherExtensions(t *testing.T) {
	a := startActors(t, nil, "a", "b")
	d := a.h.Emit(context.Background(), events.ToolCall, map[string]any{"input": map[string]any{"act": "bus", "who": "a"}})
	_ = d
	a.waitLog(t, `bus b {"channel": "c", "data": {"n": 1}}`)
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(a.log(), "bus a") {
		t.Error("the sender must not hear its own event from the host")
	}
	a.h.Broadcast("bus_event", map[string]any{"channel": "h", "data": 2})
	a.waitLog(t, "bus a")
	a.h.Broadcast("bus_event", make(chan int)) // unmarshalable: ignored
}

func TestHostMessagesReachTheApp(t *testing.T) {
	var mu sync.Mutex
	var msgs []events.CustomMessage
	var opts []SendOptions
	var errs []ExtError
	var ui []UIRequest
	quit := make(chan struct{}, 1)
	a := startActors(t, func(h *Host) {
		h.OnSendMessage = func(m events.CustomMessage, o SendOptions) {
			mu.Lock()
			msgs, opts = append(msgs, m), append(opts, o)
			mu.Unlock()
		}
		h.OnError = func(e ExtError) { mu.Lock(); errs = append(errs, e); mu.Unlock() }
		h.Surface = func(r UIRequest) { mu.Lock(); ui = append(ui, r); mu.Unlock() }
		h.OnShutdown = func() { quit <- struct{}{} }
	}, "a")
	act(a, map[string]any{"act": "msg"})
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown_request never arrived")
	}
	a.waitLog(t, "tool_call: boom")
	mu.Lock()
	defer mu.Unlock()
	if len(msgs) != 1 || msgs[0].CustomType != "t" || msgs[0].Content != "hello" || !msgs[0].Display {
		t.Errorf("send_message: %+v", msgs)
	} else if opts[0].DeliverAs != "followUp" || !opts[0].TriggerTurn {
		t.Errorf("send_message: %+v %+v", msgs, opts)
	}
	if len(errs) != 1 || errs[0] != (ExtError{Extension: "ext-a", Event: "tool_call", Error: "boom"}) {
		t.Errorf("ext_error: %+v", errs)
	}
	if len(ui) != 1 || ui[0].Method != "setTitle" || ui[0].Text != "T" || ui[0].Extension != "ext-a" {
		t.Errorf("ui: %+v", ui)
	}
}

func TestShutdownRequestReachesTheHost(t *testing.T) {
	asked := make(chan struct{}, 1)
	a := startActors(t, func(h *Host) { h.OnShutdown = func() { asked <- struct{}{} } }, "a")
	act(a, map[string]any{"act": "msg"})
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("no shutdown request")
	}
}

func TestToolAbortSignalFiresWhenTheCallIsCancelled(t *testing.T) {
	a := startActors(t, nil, "a")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { a.waitLog(t, "slow started"); cancel() }()
	a.h.Emit(ctx, events.ToolCall, map[string]any{"input": map[string]any{"act": "slow"}})
	a.waitLog(t, "cancel ") // the extension was told which request to abort
}

func TestCommandCollisionsAreNumberedAndCompleted(t *testing.T) {
	a := startActors(t, nil, "a", "b")
	var inv []string
	for _, c := range a.h.Commands() {
		inv = append(inv, c.Invocation)
	}
	if strings.Join(inv, ",") != "dup:1,plain:1,dup:2,plain:2" {
		t.Fatalf("%v", inv)
	}
	ctx := context.Background()
	if out, ok, err := a.h.RunCommand(ctx, "dup:2", ""); !ok || err != nil || out != "ran dup by b" {
		t.Fatalf("%q %v %v", out, ok, err)
	}
	if _, ok, _ := a.h.RunCommand(ctx, "dup", ""); ok {
		t.Fatal("a colliding name must be used with its number")
	}
	if items := a.h.CompleteCommand(ctx, "dup:1", "ab"); len(items) != 1 || items[0].Value != "ab1" {
		t.Fatalf("%+v", items)
	}
	if a.h.CompleteCommand(ctx, "dup:2", "ab") != nil || a.h.CompleteCommand(ctx, "nope", "") != nil {
		t.Fatal("no completions without hasCompletions or for unknown commands")
	}
}

func TestBeforeAgentStartPromptsChainBetweenExtensions(t *testing.T) {
	a := startActors(t, nil, "a", "b")
	d := a.h.Emit(context.Background(), events.BeforeAgentStart, map[string]any{"prompt": "p", "systemPrompt": "BASE"})
	if d.SystemPrompt == nil || *d.SystemPrompt != "BASE+a+b" {
		t.Fatalf("%+v", d)
	}
}

func TestChainCarriesChangesToTheNextHandler(t *testing.T) {
	out, bad, txt := "o", true, "t"
	pl := map[string]any{"input": 1, "output": "x"}
	cases := []struct {
		name string
		d    events.Decision
		key  string
		want any
	}{
		{events.ToolCall, events.Decision{Args: json.RawMessage(`{"a":1}`)}, "input", json.RawMessage(`{"a":1}`)},
		{events.ToolResult, events.Decision{Output: &out}, "output", "o"},
		{events.ToolResult, events.Decision{IsError: &bad}, "isError", true},
		{events.ToolResult, events.Decision{Content: json.RawMessage(`[1]`)}, "content", json.RawMessage(`[1]`)},
		{events.ToolResult, events.Decision{Details: json.RawMessage(`{"d":1}`)}, "details", json.RawMessage(`{"d":1}`)},
		{events.Input, events.Decision{Text: &txt}, "text", "t"},
		{events.Input, events.Decision{Images: json.RawMessage(`[2]`)}, "images", json.RawMessage(`[2]`)},
	}
	for _, c := range cases {
		next := chain(c.name, pl, c.d).(map[string]any)
		v, ok := next[c.key]
		if !ok {
			t.Errorf("%s %s missing", c.name, c.key)
		} else if c.want != nil && string(mustJSON(v)) != string(mustJSON(c.want)) {
			t.Errorf("%s %s = %v", c.name, c.key, v)
		}
	}
	if _, touched := pl["text"]; touched {
		t.Error("the original payload must not change")
	}
	if got := chain(events.ToolCall, "not a map", events.Decision{}); got != "not a map" {
		t.Error("other payloads pass through")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
