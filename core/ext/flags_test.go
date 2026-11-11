package ext

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"koine/core/tools"
)

// An extension in Python that registers two flags, keeps the values the host sends, and reports them from a command.
const flagExt = `
import sys, json
def send(**o): sys.stdout.write(json.dumps(dict(jsonrpc="2.0", **o)) + "\n"); sys.stdout.flush()
values = {}
for line in sys.stdin:
    m = json.loads(line); meth, id_, p = m.get("method"), m.get("id"), m.get("params", {})
    if meth == "initialize":
        send(method="register_flag", params={"name": "plan", "description": "Plan first", "type": "boolean", "default": False})
        send(method="register_flag", params={"name": "label", "description": "A label", "type": "string"})
        send(method="register_flag", params={"name": "odd", "type": "weird"})
        send(method="register_flag", params={"description": "no name"})
        send(method="register_command", params={"name": "show"})
        send(id=id_, result={})
    elif meth == "flags":
        values.update(p["values"])
    elif meth == "command.execute":
        send(id=id_, result={"output": json.dumps(values, sort_keys=True)})
    elif meth == "shutdown": break
`

func TestExtensionFlagsAreRegisteredAndReceiveTheirValues(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "f.py")
	if err := os.WriteFile(script, []byte(flagExt), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewHost(dir, tools.NewRegistry())
	t.Cleanup(h.Close)
	if err := h.Start(context.Background(), Spec{Name: "flagger", Command: []string{"python3", script}}); err != nil {
		t.Fatal(err)
	}
	fl := h.Flags()
	if len(fl) != 3 {
		t.Fatalf("want 3 flags (the nameless one is dropped): %+v", fl)
	}
	if fl[0].Name != "plan" || fl[0].Type != "boolean" || fl[0].Default != false || fl[0].Extension != "flagger" || fl[0].Description != "Plan first" {
		t.Errorf("%+v", fl[0])
	}
	if fl[1].Type != "string" || fl[2].Type != "boolean" {
		t.Errorf("types: %+v %+v", fl[1], fl[2])
	}

	h.SetFlags(nil) // nothing to send
	h.SetFlags(map[string]any{"plan": true, "label": "x"})
	out, ok, err := h.RunCommand(context.Background(), "show", "")
	if !ok || err != nil || out != `{"label": "x", "plan": true}` {
		t.Fatalf("%q %v %v", out, ok, err)
	}
}
