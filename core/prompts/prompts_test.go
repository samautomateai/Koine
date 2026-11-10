package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAndExpand(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	w := func(base, rel, c string) {
		p := filepath.Join(base, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(c), 0o644)
	}
	w(cwd, ".claude/commands/fix.md", "---\ndescription: Fix\n---\nFix: $ARGUMENTS")
	w(cwd, ".opencode/command/pos.md", "first=$1 second=$2 third=$3 all=$@")
	w(cwd, ".pi/prompts/fix.md", "shadowed? no: .pi comes first")
	w(home, ".config/opencode/commands/user.md", "u")
	w(cwd, ".claude/commands/readme.txt", "ignored")
	ps := Discover(cwd, home)
	m := map[string]Prompt{}
	for _, p := range ps {
		m[p.Name] = p
	}
	if len(m) != 3 {
		t.Fatalf("got %d prompts: %v", len(m), m)
	}
	if m["fix"].Body != "shadowed? no: .pi comes first" {
		t.Errorf("precedence: %q", m["fix"].Body)
	}
	if got := m["pos"].Expand("a b"); got != "first=a second=b third= all=a b" {
		t.Errorf("expand: %q", got)
	}
	if got := (Prompt{Body: "Fix: $ARGUMENTS"}).Expand("it"); got != "Fix: it" {
		t.Errorf("args: %q", got)
	}
}
