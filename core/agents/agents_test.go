package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseTools(t *testing.T) {
	cases := []struct {
		in          string
		allow, deny []string
	}{
		{"", nil, nil},
		{"Read, Grep, MultiEdit", []string{"read", "grep", "edit"}, nil},
		{"[Read, Bash]", []string{"read", "bash"}, nil},
		{"- Read\n- Glob", []string{"read", "glob"}, nil},
		{"write: false\nedit: false\nbash: true", []string{"bash"}, []string{"write", "edit"}},
		{"write: false\nedit: false", nil, []string{"write", "edit"}},
		{"WebSearch", nil, nil}, // unsupported tools map to nothing
	}
	for _, c := range cases {
		a, d := parseTools(c.in)
		if !reflect.DeepEqual(a, c.allow) || !reflect.DeepEqual(d, c.deny) {
			t.Errorf("parseTools(%q) = %v, %v; want %v, %v", c.in, a, d, c.allow, c.deny)
		}
	}
}

func TestDiscover(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	w := func(rel, c string) {
		p := filepath.Join(cwd, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(c), 0o644)
	}
	w(".opencode/agent/rev.md", "---\ndescription: reviews\nmode: subagent\nmodel: anthropic/claude-haiku-5-5\ntools:\n  write: false\n---\nPROMPT")
	w(".claude/agents/plan.md", "---\nname: planner\ndescription: plans\nmodel: inherit\ntools: Read, Grep\n---\nPLAN")
	w(".opencode/agent/main.md", "---\ndescription: primary\nmode: primary\n---\nx")
	w(".claude/agents/note.txt", "ignored")
	ds := Discover(cwd, home)
	if len(ds) != 2 {
		t.Fatalf("%+v", ds)
	}
	rev, ok := Find(ds, "rev")
	if !ok || rev.Model != "claude-haiku-5-5" || !reflect.DeepEqual(rev.Deny, []string{"write"}) || rev.Prompt != "PROMPT" {
		t.Fatalf("%+v", rev)
	}
	pl, ok := Find(ds, "planner")
	if !ok || pl.Model != "" || !reflect.DeepEqual(pl.Allow, []string{"read", "grep"}) {
		t.Fatalf("%+v", pl)
	}
	if _, ok := Find(ds, "main"); ok {
		t.Fatal("primary agents must not be delegates")
	}
}
