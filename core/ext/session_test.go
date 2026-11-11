package ext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"koine/core/tools"
)

// An extension that stores session data, queues messages with and without a delivery mode, and streams partial output from its tool.
const sessionExt = `
import sys, json
def send(**o): sys.stdout.write(json.dumps(dict(jsonrpc="2.0", **o)) + "\n"); sys.stdout.flush()
for line in sys.stdin:
    m = json.loads(line); meth, id_, p = m.get("method"), m.get("id"), m.get("params", {})
    if meth == "initialize":
        send(method="register_tool", params={"name": "stream", "description": "d"})
        send(method="register_command", params={"name": "go"})
        send(id=id_, result={})
    elif meth == "command.execute":
        send(method="append_entry", params={"customType": "my-ext", "data": {"n": 1}})
        send(method="append_entry", params={"customType": "", "data": 1})
        send(method="send_user_message", params={"text": "steer me", "deliverAs": "steer"})
        send(method="send_user_message", params={"text": "later"})
        send(id=id_, result={"output": "ok"})
    elif meth == "tool.execute":
        cid = p["name"] + "/" + p["callId"]
        send(method="tool_update", params={"callId": cid, "text": "part 1"})
        send(method="tool_update", params={"callId": "other/ext-9", "text": "nobody listens"})
        send(method="tool_update", params={"callId": cid, "text": "part 2"})
        send(id=id_, result={"content": "done"})
    elif meth == "shutdown": break
`

func TestExtensionsStoreSessionDataQueueMessagesAndStreamToolOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "s.py")
	if err := os.WriteFile(script, []byte(sessionExt), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	h := NewHost(dir, reg)
	t.Cleanup(h.Close)
	var mu sync.Mutex
	var entries, msgs []string
	h.OnAppendEntry = func(ct string, data json.RawMessage) {
		mu.Lock()
		entries = append(entries, ct+"="+string(data))
		mu.Unlock()
	}
	h.OnUserMessage = func(text, mode string) { mu.Lock(); msgs = append(msgs, text+"|"+mode); mu.Unlock() }
	if err := h.Start(context.Background(), Spec{Name: "sess", Command: []string{"python3", script}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := h.RunCommand(context.Background(), "go", ""); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	mu.Lock()
	if len(entries) != 1 || entries[0] != `my-ext={"n": 1}` || len(msgs) != 2 || msgs[0] != "steer me|steer" || msgs[1] != "later|" {
		t.Errorf("entries %q messages %q", entries, msgs)
	}
	mu.Unlock()

	tool, _ := reg.Get("stream")
	var parts []string
	ctx := tools.WithUpdates(context.Background(), func(p string) { parts = append(parts, p) })
	out, err := tool.Run(ctx, nil)
	if err != nil || out != "done" || len(parts) != 2 || parts[0] != "part 1" || parts[1] != "part 2" {
		t.Errorf("%q %v %v", out, err, parts)
	}
	// without a listener the updates are dropped, and the update table does not grow
	if out, err = tool.Run(context.Background(), nil); err != nil || out != "done" {
		t.Errorf("%q %v", out, err)
	}
	n := 0
	h.updates.Range(func(_, _ any) bool { n++; return true })
	if n != 0 {
		t.Errorf("%d stale update listeners", n)
	}
}
