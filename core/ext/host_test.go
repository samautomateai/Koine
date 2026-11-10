package ext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"koine/core/tools"
)

// A complete protocol implementation in ~40 lines of Python — also the living
// proof that extensions can be written in any language.
const pyExt = `
import sys, json
def send(**o): sys.stdout.write(json.dumps(dict(jsonrpc="2.0", **o)) + "\n"); sys.stdout.flush()
print("garbage line that is not JSON"); sys.stdout.flush()
sys.stderr.write("stderr line\n"); sys.stderr.flush()
for line in sys.stdin:
    m = json.loads(line); meth, id_, p = m.get("method"), m.get("id"), m.get("params", {})
    if meth == "initialize":
        send(method="register_tool", params={"name": "echo", "description": "d"})   # no schema -> default
        send(method="register_command", params={"name": "hi", "description": "say hi"})
        send(method="log", params={"level": "info", "message": "hello from ext"})
        send(method="send_user_message", params={"text": "injected"})
        send(id=id_, result={"events": ["tool_call"]})
    elif meth == "tool.execute":
        a = p["args"]
        if a.get("fail"): send(id=id_, result={"content": "tool failed", "isError": True})
        elif a.get("crash"): sys.exit(1)
        else: send(id=id_, result={"content": "echo:" + a.get("t", "")})
    elif meth == "command.execute":
        if p["args"] == "err": send(id=id_, error={"code": 1, "message": "cmd failed"})
        else: send(id=id_, result={"output": "hi " + p["args"]})
    elif meth == "event":
        send(id=id_, result={"block": True, "reason": "py says no"} if p["name"] == "tool_call" else {})
    elif meth == "weird":
        send(id=id_, result={})
    elif meth == "shutdown": break
`

func start(t *testing.T) (*Host, *tools.Registry, *[]string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "e.py")
	if err := os.WriteFile(script, []byte(pyExt), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	h := NewHost(dir, reg)
	var mu sync.Mutex
	var logs, msgs []string
	h.Logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, f); mu.Unlock() }
	h.OnUserMessage = func(s string) { mu.Lock(); msgs = append(msgs, s); mu.Unlock() }
	if err := h.Start(context.Background(), Spec{Name: "py", Command: []string{"python3", script}, Env: map[string]string{"X": "1"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, reg, &logs, &msgs
}

func TestProtocolRoundTrip(t *testing.T) {
	h, reg, logs, msgs := start(t)
	tl, ok := reg.Get("echo")
	if !ok || !strings.Contains(string(tl.Schema()), "object") || tl.Description() != "d" || tl.Name() != "echo" {
		t.Fatal("tool not registered with default schema")
	}
	ctx := context.Background()
	if out, err := tl.Run(ctx, json.RawMessage(`{"t":"x"}`)); err != nil || out != "echo:x" {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := tl.Run(ctx, nil); err != nil || out != "echo:" { // nil args become {}
		t.Fatalf("%q %v", out, err)
	}
	if out, err := tl.Run(ctx, json.RawMessage(`{"fail":1}`)); err == nil || out != "tool failed" {
		t.Fatalf("isError must surface: %q %v", out, err)
	}

	cmds := h.Commands()
	if len(cmds) != 1 || cmds[0].Name != "hi" {
		t.Fatalf("%+v", cmds)
	}
	if out, ok, err := h.RunCommand(ctx, "hi", "bob"); !ok || err != nil || out != "hi bob" {
		t.Fatalf("%q %v %v", out, ok, err)
	}
	if _, ok, err := h.RunCommand(ctx, "hi", "err"); !ok || err == nil || !strings.Contains(err.Error(), "cmd failed") {
		t.Fatalf("%v %v", ok, err)
	}
	if _, ok, _ := h.RunCommand(ctx, "unknown", ""); ok {
		t.Fatal("unknown command must report !ok")
	}

	// event filtering: extension declared only tool_call
	if d := h.Emit(ctx, "tool_call", map[string]any{"toolName": "bash"}); !d.Block || d.Reason != "py says no" {
		t.Fatalf("%+v", d)
	}
	if d := h.Emit(ctx, "turn_start", nil); d.Block {
		t.Fatal("undeclared events must not be forwarded")
	}

	time.Sleep(100 * time.Millisecond) // notifications are async
	if len(*msgs) != 1 || (*msgs)[0] != "injected" {
		t.Errorf("send_user_message: %v", *msgs)
	}
	joined := strings.Join(*logs, "|")
	if !strings.Contains(joined, "non-protocol") || !strings.Contains(joined, "%s") {
		t.Errorf("logs: %v", *logs)
	}
}

func TestExtensionCrashSurfacesAsError(t *testing.T) {
	_, reg, _, _ := start(t)
	tl, _ := reg.Get("echo")
	if _, err := tl.Run(context.Background(), json.RawMessage(`{"crash":1}`)); err == nil {
		t.Fatal("expected error when extension exits mid-call")
	}
	// later calls fail fast instead of hanging
	done := make(chan struct{})
	go func() { _, _ = tl.Run(context.Background(), json.RawMessage(`{}`)); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("call to dead extension hung")
	}
}

func TestContextCancelAbortsCall(t *testing.T) {
	_, reg, _, _ := start(t)
	tl, _ := reg.Get("echo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// may race with a fast reply, but must never hang
	done := make(chan struct{})
	go func() { _, _ = tl.Run(ctx, json.RawMessage(`{}`)); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hung")
	}
}

func TestStartErrors(t *testing.T) {
	h := NewHost(t.TempDir(), tools.NewRegistry())
	if err := h.Start(context.Background(), Spec{}); err == nil {
		t.Fatal("empty command")
	}
	if err := h.Start(context.Background(), Spec{Name: "x", Command: []string{"/nonexistent/binary"}}); err == nil {
		t.Fatal("bad binary")
	}
	// process that exits without answering initialize
	spec := Spec{Name: "x", Command: []string{"sh", "-c", "exit 0"}}
	if err := h.Start(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("%v", err)
	}
	if d := h.Emit(context.Background(), "tool_call", nil); d.Block {
		t.Fatal("no extensions: no-op")
	}
}

func TestSpecDirAndJSSpec(t *testing.T) {
	dir := t.TempDir()
	proj := t.TempDir()
	h := NewHost(proj, tools.NewRegistry())
	var cwd string
	h.Logf = func(f string, a ...any) {}
	_ = os.WriteFile(filepath.Join(dir, "pwd.py"), []byte(`
import sys, json, os
for line in sys.stdin:
    m = json.loads(line)
    if m["method"] == "initialize":
        sys.stdout.write(json.dumps({"jsonrpc":"2.0","id":m["id"],"result":{"name":os.getcwd()}})+"\n"); sys.stdout.flush()
    elif m["method"] == "shutdown": break
`), 0o644)
	if err := h.Start(context.Background(), Spec{Name: "d", Dir: dir, Command: []string{"python3", "pwd.py"}}); err != nil {
		t.Fatalf("relative script must resolve against Spec.Dir: %v", err)
	}
	h.Close()
	_ = cwd

	t.Setenv("KOINE_JS_RUNTIME", "node")
	s, err := JSSpec("n", "x.ts")
	if err != nil || !strings.HasSuffix(s.Command[0], "node") || s.Command[len(s.Command)-1] != "x.ts" {
		t.Fatalf("%+v %v", s, err)
	}
	t.Setenv("KOINE_JS_RUNTIME", "")
	if s, err = JSSpec("b", "x.ts"); err != nil || s.Name != "b" {
		t.Fatalf("%+v %v", s, err)
	}
	t.Setenv("PATH", "")
	if _, err = JSSpec("none", "x.ts"); err == nil {
		t.Fatal("no runtime on PATH must error")
	}
}

func TestExtractSidecarIdempotent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	a, err := ExtractSidecar()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ExtractSidecar()
	if a != b {
		t.Fatal("content-addressed dir must be stable")
	}
	for _, f := range []string{"runner.mjs", "loader.mjs", "register.mjs", "shims/map.json", "shims/typebox.mjs"} {
		if _, err := os.Stat(filepath.Join(a, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}
