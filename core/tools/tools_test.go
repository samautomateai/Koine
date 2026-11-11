package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func run(t *testing.T, r *Registry, name, args string) (string, error) {
	t.Helper()
	tl, ok := r.Get(name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	return tl.Run(context.Background(), json.RawMessage(args))
}

// setup registers the built-ins in a temp directory and keeps ripgrep out of it, so every test runs the same Go code on every machine.
func setup(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r := NewRegistry()
	RegisterWith(r, Options{Cwd: dir, Home: dir, Ripgrep: "-"})
	return r, dir
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register(&Func{N: "b"})
	r.Register(&Func{N: "a"})
	if got := strings.Join(r.Names(), ","); got != "a,b" {
		t.Fatal(got)
	}
	r.Remove("a")
	if _, ok := r.Get("a"); ok {
		t.Fatal("not removed")
	}
	f := &Func{N: "x", D: "d", S: json.RawMessage(`{}`), F: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}
	if f.Name() != "x" || f.Description() != "d" || string(f.Schema()) != "{}" {
		t.Fatal("accessors")
	}
}

func TestFilteredAndCanon(t *testing.T) {
	r, _ := setup(t)
	allow := r.Filtered([]string{"Read", "Grep"}, nil)
	if got := strings.Join(allow.Names(), ","); got != "grep,read" {
		t.Fatal(got)
	}
	deny := r.Filtered(nil, []string{"Bash", "write"})
	for _, n := range deny.Names() {
		if n == "bash" || n == "write" {
			t.Fatalf("%s should be denied", n)
		}
	}
	for in, want := range map[string]string{"Bash": "bash", "MultiEdit": "edit", " READ ": "read", "WebFetch": "", "Glob": "find"} {
		if got := Canon(in); got != want {
			t.Errorf("Canon(%q)=%q want %q", in, got, want)
		}
	}
}

func TestBuiltinsAreTheSevenPiTools(t *testing.T) {
	r, _ := setup(t)
	if got := strings.Join(r.Names(), ","); got != "bash,edit,find,grep,ls,read,write" {
		t.Fatal(got)
	}
	for _, n := range DefaultTools {
		if _, ok := r.Get(n); !ok {
			t.Errorf("default tool %s is not registered", n)
		}
	}
}

func TestBadJSONArgs(t *testing.T) {
	r, _ := setup(t)
	for _, n := range []string{"read", "write", "edit", "bash", "find", "grep"} {
		if _, err := run(t, r, n, `not json`); err == nil {
			t.Errorf("%s accepted invalid args", n)
		}
	}
}

func TestPromptPartsListSnippetsAndDedupeGuidelines(t *testing.T) {
	r, _ := setup(t)
	r.Register(&Func{N: "zeta", Snippet: "Does zeta things", Guidelines: []string{"Use zeta when asked.", "Use read to examine files instead of cat or sed."}})
	r.Register(&Func{N: "alpha", Snippet: "Does alpha things"})
	r.Register(&Func{N: "quiet", Guidelines: []string{"Use quiet sparingly."}}) // no snippet: not listed, rules still count
	r.Register(&plain{})                                                        // not Prompted at all
	lines, rules := r.PromptParts()
	want := "- read: Read file contents,- bash: Execute bash commands (ls, grep, find, etc.),- edit: Make precise file edits with exact text replacement, " +
		"including multiple disjoint edits in one call,- write: Create or overwrite files,- grep: Search file contents for patterns (respects .gitignore)," +
		"- find: Find files by glob pattern (respects .gitignore),- ls: List directory contents,- alpha: Does alpha things,- zeta: Does zeta things"
	if got := strings.Join(lines, ","); got != want {
		t.Fatalf("tools:\n%s\nwant:\n%s", got, want)
	}
	n := 0
	for _, g := range rules {
		if strings.Contains(g, "Use read to examine files") {
			n++
		}
	}
	all := strings.Join(rules, "\n")
	if n != 1 || !strings.Contains(all, "- Use quiet sparingly.") || !strings.Contains(all, "- Use zeta when asked.") {
		t.Fatalf("rules: %q", rules)
	}
}

type plain struct{}

func (plain) Name() string                                         { return "plain" }
func (plain) Description() string                                  { return "" }
func (plain) Schema() json.RawMessage                              { return nil }
func (plain) Run(context.Context, json.RawMessage) (string, error) { return "", nil }
