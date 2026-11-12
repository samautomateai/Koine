package ext

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"koine/core/srcinfo"
	"koine/core/tools"
)

// Speaks the protocol in Python: registers a tool and a command, and answers resources_discover with relative, absolute and ~ paths.
const resourcesExt = `
import sys, json
def send(**o): sys.stdout.write(json.dumps(dict(jsonrpc="2.0", **o)) + "\n"); sys.stdout.flush()
for line in sys.stdin:
    m = json.loads(line); meth, id_, p = m.get("method"), m.get("id"), m.get("params", {})
    if meth == "initialize":
        send(method="register_tool", params={"name": "t1", "description": "d"})
        send(method="register_command", params={"name": "c1", "description": "d"})
        send(id=id_, result={"events": ["resources_discover"]})
    elif meth == "event":
        send(id=id_, result={"resources": {"skillPaths": ["skills", "/abs/skills", " ", "~/mine"], "promptPaths": ["prompts/a.md"],
                                           "themePaths": ["themes"]}, "echo": p["payload"]})
    elif meth == "shutdown": break
`

func TestResourcesDiscoverAndSourceInfo(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	script := filepath.Join(dir, "e.py")
	if err := os.WriteFile(script, []byte(resourcesExt), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	h := NewHost(dir, reg)
	info := srcinfo.Package(script, "npm:acme@1.0.0", "user", dir)
	if err := h.Start(context.Background(), Spec{Name: "r", Command: []string{"python3", script}, Dir: dir, Source: info}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)

	if got, ok := reg.SourceOf("t1"); !ok || got != info {
		t.Errorf("the tool carries the extension's source: %+v", got)
	}
	if cmds := h.Commands(); len(cmds) != 1 || cmds[0].Source != info {
		t.Errorf("the command carries it too: %+v", cmds)
	}
	d := h.Emit(context.Background(), "resources_discover", map[string]any{"cwd": dir, "reason": "startup"})
	want := []string{filepath.Join(dir, "skills"), "/abs/skills", filepath.Join(home, "mine")}
	if d.Resources == nil || !reflect.DeepEqual(d.Resources.SkillPaths, want) {
		t.Fatalf("skill paths: %+v", d.Resources)
	}
	if !reflect.DeepEqual(d.Resources.PromptPaths, []string{filepath.Join(dir, "prompts", "a.md")}) ||
		!reflect.DeepEqual(d.Resources.ThemePaths, []string{filepath.Join(dir, "themes")}) {
		t.Errorf("prompt and theme paths: %+v", d.Resources)
	}
	// other events are not forwarded to an extension that did not ask for them
	if d := h.Emit(context.Background(), "turn_start", nil); d.Resources != nil {
		t.Errorf("%+v", d)
	}
}
