package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func setup(t *testing.T) (*Registry, string) {
	dir := t.TempDir()
	r := NewRegistry()
	RegisterBuiltins(r, dir)
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
	for in, want := range map[string]string{"Bash": "bash", "MultiEdit": "edit", " READ ": "read", "WebFetch": ""} {
		if got := Canon(in); got != want {
			t.Errorf("Canon(%q)=%q want %q", in, got, want)
		}
	}
}

func TestWriteReadEdit(t *testing.T) {
	r, dir := setup(t)
	if _, err := run(t, r, "write", `{"path":"sub/a.txt","content":"one\ntwo\nthree\ntwo\n"}`); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, r, "read", `{"path":"sub/a.txt","offset":2,"limit":1}`)
	if err != nil || !strings.Contains(out, "two") || strings.Contains(out, "one") || !strings.Contains(out, "more lines") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := run(t, r, "read", `{"path":"nope"}`); err == nil {
		t.Fatal("expected error")
	}
	// ambiguous edit
	if _, err := run(t, r, "edit", `{"path":"sub/a.txt","old":"two","new":"2"}`); err == nil || !strings.Contains(err.Error(), "2 places") {
		t.Fatalf("%v", err)
	}
	if _, err := run(t, r, "edit", `{"path":"sub/a.txt","old":"zzz","new":"2"}`); err == nil {
		t.Fatal("not found should error")
	}
	if _, err := run(t, r, "edit", `{"path":"sub/a.txt","old":"","new":"2"}`); err == nil {
		t.Fatal("empty old should error")
	}
	if _, err := run(t, r, "edit", `{"path":"sub/a.txt","old":"two","new":"2","replaceAll":true}`); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "sub/a.txt"))
	if string(b) != "one\n2\nthree\n2\n" {
		t.Fatalf("%q", b)
	}
	// absolute paths work
	if _, err := run(t, r, "write", `{"path":"`+filepath.Join(dir, "abs.txt")+`","content":"x"}`); err != nil {
		t.Fatal(err)
	}
}

func TestBadJSONArgs(t *testing.T) {
	r, _ := setup(t)
	for _, n := range []string{"read", "write", "edit", "bash", "glob", "grep"} {
		if _, err := run(t, r, n, `not json`); err == nil {
			t.Errorf("%s accepted invalid args", n)
		}
	}
}

func TestBash(t *testing.T) {
	r, dir := setup(t)
	out, err := run(t, r, "bash", `{"command":"pwd"}`)
	if err != nil || !strings.Contains(out, filepath.Base(dir)) {
		t.Fatalf("%q %v", out, err)
	}
	out, err = run(t, r, "bash", `{"command":"echo boom; exit 3"}`)
	if err == nil || !strings.Contains(out, "boom") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err = run(t, r, "bash", `{"command":"sleep 5","timeout":1}`); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("%v", err)
	}
}

func TestLsGlobGrep(t *testing.T) {
	r, dir := setup(t)
	for p, c := range map[string]string{"a.go": "package a\nfunc Foo() {}\n", "pkg/b.go": "package b\nfunc Bar() {}\n", "pkg/c.txt": "Foo here\n", "node_modules/x.go": "func Foo() {}", "bin.dat": "Foo\x00bin"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		_ = os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644)
	}
	out, _ := run(t, r, "ls", `{}`)
	if !strings.Contains(out, "pkg/") || !strings.Contains(out, "a.go") {
		t.Fatal(out)
	}
	if _, err := run(t, r, "ls", `{"path":"missing"}`); err == nil {
		t.Fatal("ls missing")
	}
	out, _ = run(t, r, "glob", `{"pattern":"**/*.go"}`)
	if out != "a.go\npkg/b.go" {
		t.Fatalf("glob: %q", out)
	}
	out, _ = run(t, r, "glob", `{"pattern":"*.go"}`) // no slash: matches basenames anywhere
	if out != "a.go\npkg/b.go" {
		t.Fatalf("glob base: %q", out)
	}
	out, _ = run(t, r, "glob", `{"pattern":"pkg/?.txt","path":"."}`)
	if out != "pkg/c.txt" {
		t.Fatalf("glob ?: %q", out)
	}
	out, _ = run(t, r, "grep", `{"pattern":"Foo"}`)
	if !strings.Contains(out, "a.go:2:func Foo() {}") || !strings.Contains(out, "pkg/c.txt:1:Foo here") || strings.Contains(out, "node_modules") || strings.Contains(out, "bin.dat") {
		t.Fatalf("grep: %q", out)
	}
	out, _ = run(t, r, "grep", `{"pattern":"Foo","glob":"*.txt"}`)
	if strings.Contains(out, "a.go") || !strings.Contains(out, "c.txt") {
		t.Fatalf("grep glob: %q", out)
	}
	if _, err := run(t, r, "grep", `{"pattern":"("}`); err == nil {
		t.Fatal("bad regexp")
	}
}

func TestClip(t *testing.T) {
	long := strings.Repeat("x", maxOut*2)
	if got := clip(long); len(got) >= len(long) || !strings.Contains(got, "truncated") {
		t.Fatal("clip")
	}
	if clip("short") != "short" {
		t.Fatal("clip short")
	}
}
