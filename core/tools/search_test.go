package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLs(t *testing.T) {
	r, dir := setup(t)
	writeTree(t, dir, map[string]string{"b.txt": "", "A.txt": "", ".hidden": "", "pkg/x.go": "", "Zed/y": ""})
	out, err := run(t, r, "ls", `{}`)
	if err != nil || out != ".hidden\nA.txt\nb.txt\npkg/\nZed/" {
		t.Fatalf("%q %v", out, err)
	}
	out, _ = run(t, r, "ls", `{"path":"pkg"}`)
	if out != "x.go" {
		t.Fatal(out)
	}
	out, _ = run(t, r, "ls", `{"limit":2}`)
	if out != ".hidden\nA.txt\n\n[2 entries limit reached. Use limit=4 for more]" {
		t.Fatalf("%q", out)
	}
	_ = os.Mkdir(filepath.Join(dir, "empty"), 0o755)
	if out, _ := run(t, r, "ls", `{"path":"empty"}`); out != "(empty directory)" {
		t.Fatal(out)
	}
	if _, err := run(t, r, "ls", `{"path":"missing"}`); err == nil || !strings.Contains(err.Error(), "path not found") {
		t.Fatal(err)
	}
	if _, err := run(t, r, "ls", `{"path":"A.txt"}`); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatal(err)
	}
}

func TestLsIsCappedAt500Entries(t *testing.T) {
	r, dir := setup(t)
	for i := 0; i < 520; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), nil, 0o644)
	}
	out, _ := run(t, r, "ls", `{}`)
	if !strings.HasSuffix(out, "[500 entries limit reached. Use limit=1000 for more]") || strings.Contains(out, "f0500") {
		t.Fatal(out[len(out)-80:])
	}
}

func TestFind(t *testing.T) {
	r, dir := setup(t)
	writeTree(t, dir, map[string]string{
		"a.go": "", "pkg/b.go": "", "pkg/c.txt": "", "pkg/deep/d.go": "", ".dot/e.go": "", ".git/config": "", "src/app.test.ts": "", "src/app.ts": "",
	})
	cases := map[string]string{
		`{"pattern":"*.go"}`:              ".dot/e.go\na.go\npkg/b.go\npkg/deep/d.go",
		`{"pattern":"**/*.go"}`:           ".dot/e.go\na.go\npkg/b.go\npkg/deep/d.go",
		`{"pattern":"*.go","path":"pkg"}`: "b.go\ndeep/d.go",
		`{"pattern":"pkg/*.go"}`:          "pkg/b.go",
		`{"pattern":"pkg/**/*.go"}`:       "pkg/b.go\npkg/deep/d.go",
		`{"pattern":"/a.go"}`:             "a.go",
		`{"pattern":"pkg/?.txt"}`:         "pkg/c.txt",
		`{"pattern":"*.{ts,txt}"}`:        "pkg/c.txt\nsrc/app.test.ts\nsrc/app.ts",
		`{"pattern":"deep"}`:              "pkg/deep/",
		`{"pattern":"app.[tj]s"}`:         "src/app.ts",
		`{"pattern":"*.rs"}`:              "No files found matching pattern",
		`{"pattern":"*.go","limit":2}`:    ".dot/e.go\na.go\n\n[2 results limit reached. Use limit=4 for more, or refine pattern]",
	}
	for args, want := range cases {
		if out, err := run(t, r, "find", args); err != nil || out != want {
			t.Errorf("%s:\n got %q (%v)\nwant %q", args, out, err, want)
		}
	}
	for _, args := range []string{`{"pattern":""}`, `{"pattern":"*.go","path":"missing"}`} {
		if _, err := run(t, r, "find", args); err == nil {
			t.Errorf("expected an error for %s", args)
		}
	}
}

func TestFindHonoursGitignoreInNestedAndParentDirectories(t *testing.T) {
	r, dir := setup(t)
	writeTree(t, dir, map[string]string{
		".git/HEAD": "", ".gitignore": "# build output\nbuild/\n*.log\n/secret.txt\n!keep.log\n",
		"build/out.go": "", "app.go": "", "debug.log": "", "keep.log": "", "secret.txt": "", "sub/secret.txt": "",
		"sub/.gitignore": "local.go\n", "sub/local.go": "", "sub/other.go": "", "sub/build/x.go": "",
	})
	out, _ := run(t, r, "find", `{"pattern":"*"}`)
	want := ".gitignore\napp.go\nkeep.log\nsub/\nsub/.gitignore\nsub/other.go\nsub/secret.txt"
	if out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	// Searching from a subdirectory still applies the repository's .gitignore (anchored patterns are relative to their own directory).
	out, _ = run(t, r, "find", `{"pattern":"*.txt","path":"sub"}`)
	if out != "secret.txt" {
		t.Fatalf("%q", out)
	}
	out, _ = run(t, r, "find", `{"pattern":"*.go","path":"sub"}`)
	if out != "other.go" {
		t.Fatalf("%q", out)
	}
}

func TestGrepGoFallback(t *testing.T) {
	r, dir := setup(t)
	writeTree(t, dir, map[string]string{
		"a.go": "package a\nfunc Foo() {}\n", "pkg/b.go": "package b\nfunc Bar() {}\nfunc foo() {}\n", "pkg/c.txt": "Foo here\n",
		".gitignore": "ignored/\n", "ignored/x.go": "func Foo() {}", "bin.dat": "Foo\x00bin", ".git/objects/x": "Foo",
	})
	out, err := run(t, r, "grep", `{"pattern":"Foo"}`)
	if err != nil || out != "a.go:2: func Foo() {}\npkg/c.txt:1: Foo here" {
		t.Fatalf("%q %v", out, err)
	}
	out, _ = run(t, r, "grep", `{"pattern":"foo","ignoreCase":true,"glob":"*.go"}`)
	if out != "a.go:2: func Foo() {}\npkg/b.go:3: func foo() {}" {
		t.Fatalf("%q", out)
	}
	out, _ = run(t, r, "grep", `{"pattern":"Foo","glob":"*.txt","path":"pkg"}`)
	if out != "c.txt:1: Foo here" {
		t.Fatalf("%q", out)
	}
	out, _ = run(t, r, "grep", `{"pattern":"Foo","path":"a.go"}`)
	if out != "a.go:2: func Foo() {}" {
		t.Fatalf("file search: %q", out)
	}
	// literal: the regexp metacharacters are plain text.
	out, _ = run(t, r, "grep", `{"pattern":"Foo() {","literal":true}`)
	if out != "a.go:2: func Foo() {}" {
		t.Fatalf("literal: %q", out)
	}
	if out, _ := run(t, r, "grep", `{"pattern":"nothing-matches"}`); out != "No matches found" {
		t.Fatal(out)
	}
	for _, args := range []string{`{"pattern":"("}`, `{"pattern":""}`, `{"pattern":"x","path":"missing"}`} {
		if _, err := run(t, r, "grep", args); err == nil {
			t.Errorf("expected an error for %s", args)
		}
	}
}

func TestGrepContextAndSeparators(t *testing.T) {
	r, dir := setup(t)
	writeTree(t, dir, map[string]string{"f.txt": "l1\nl2\nHIT one\nl4\nHIT two\nl6\nl7\nl8\n"})
	out, _ := run(t, r, "grep", `{"pattern":"HIT","context":1}`)
	want := "f.txt-2- l2\nf.txt:3: HIT one\nf.txt-4- l4\nf.txt:5: HIT two\nf.txt-6- l6"
	if out != want {
		t.Fatalf("%q", out)
	}
	out, _ = run(t, r, "grep", `{"pattern":"l1","context":2}`)
	if out != "f.txt:1: l1\nf.txt-2- l2\nf.txt-3- HIT one" {
		t.Fatalf("%q", out)
	}
}

func TestGrepLimitAndLineCap(t *testing.T) {
	r, dir := setup(t)
	var b strings.Builder
	for i := 0; i < 150; i++ {
		b.WriteString("match\n")
	}
	writeTree(t, dir, map[string]string{"many.txt": b.String(), "long.txt": "needle " + strings.Repeat("z", 800) + "\n"})
	out, _ := run(t, r, "grep", `{"pattern":"match"}`)
	if strings.Count(out, "many.txt:") != 100 || !strings.HasSuffix(out, "[100 matches limit reached. Use limit=200 for more, or refine pattern]") {
		t.Fatal(out[len(out)-120:])
	}
	out, _ = run(t, r, "grep", `{"pattern":"match","limit":5}`)
	if strings.Count(out, "many.txt:") != 5 || !strings.Contains(out, "Use limit=10") {
		t.Fatal(out)
	}
	out, _ = run(t, r, "grep", `{"pattern":"needle"}`)
	if !strings.Contains(out, "... [truncated]") || !strings.HasSuffix(out, "[Some lines truncated to 500 chars. Use read tool to see full lines]") ||
		len(out) > 700 {
		t.Fatalf("%d bytes: %q", len(out), out[len(out)-120:])
	}
}

func TestGrepByteLimit(t *testing.T) {
	r, dir := setup(t)
	var b strings.Builder
	for i := 0; i < 120; i++ {
		b.WriteString("hit " + strings.Repeat("w", 600) + "\n")
	}
	writeTree(t, dir, map[string]string{"wide.txt": b.String()})
	out, _ := run(t, r, "grep", `{"pattern":"hit"}`)
	if !strings.Contains(out, "50.0KB limit reached") {
		t.Fatal(out[len(out)-120:])
	}
}

func TestGrepWithRipgrep(t *testing.T) {
	// A stand-in for rg that prints ripgrep's JSON events, so the parsing is tested without needing ripgrep installed.
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	writeTree(t, root, map[string]string{"a.txt": "zero\nfind me\nlast\n", "b.txt": "x\nfind me too\n"})
	fake := filepath.Join(dir, "rg")
	events := `{"type":"begin","data":{"path":{"text":"%[1]s/a.txt"}}}
{"type":"match","data":{"path":{"text":"%[1]s/a.txt"},"line_number":2,"lines":{"text":"find me\n"}}}
{"type":"context","data":{"path":{"text":"%[1]s/a.txt"},"line_number":3}}
{"type":"match","data":{"path":{"text":"%[1]s/b.txt"},"line_number":2,"lines":{"text":"find me too\n"}}}
{"type":"summary","data":{}}
`
	script := "#!/bin/sh\necho \"$@\" > " + filepath.Join(dir, "args") + "\ncat <<'EOF'\n" + fmt.Sprintf(events, root) + "EOF\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	RegisterWith(r, Options{Cwd: root, Home: dir, Ripgrep: fake})
	out, err := run(t, r, "grep", `{"pattern":"find me","ignoreCase":true,"literal":true,"glob":"*.txt","context":1}`)
	want := "a.txt-1- zero\na.txt:2: find me\na.txt-3- last\nb.txt-1- x\nb.txt:2: find me too"
	if err != nil || out != want {
		t.Fatalf("%q %v", out, err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	for _, want := range []string{"--json", "--hidden", "-i", "-F", "-g *.txt", "-e find me", "-- " + root} {
		if !strings.Contains(string(args), want) {
			t.Errorf("rg was called without %q: %s", want, args)
		}
	}
	// The limit stops reading rg's output.
	out, _ = run(t, r, "grep", `{"pattern":"find me","limit":1}`)
	if strings.Contains(out, "b.txt") || !strings.Contains(out, "limit reached") {
		t.Fatalf("%q", out)
	}
}

func TestGrepWithRipgrepErrors(t *testing.T) {
	dir := t.TempDir()
	failing := filepath.Join(dir, "rg")
	_ = os.WriteFile(failing, []byte("#!/bin/sh\necho 'regex parse error' >&2\nexit 2\n"), 0o755)
	r := NewRegistry()
	RegisterWith(r, Options{Cwd: dir, Home: dir, Ripgrep: failing})
	if _, err := run(t, r, "grep", `{"pattern":"("}`); err == nil || !strings.Contains(err.Error(), "regex parse error") {
		t.Fatal(err)
	}
	// Exit 1 with no output is just "no matches".
	quiet := filepath.Join(dir, "rg-quiet")
	_ = os.WriteFile(quiet, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	RegisterWith(r, Options{Cwd: dir, Home: dir, Ripgrep: quiet})
	if out, err := run(t, r, "grep", `{"pattern":"x"}`); err != nil || out != "No matches found" {
		t.Fatal(out, err)
	}
	RegisterWith(r, Options{Cwd: dir, Home: dir, Ripgrep: filepath.Join(dir, "does-not-exist")})
	if _, err := run(t, r, "grep", `{"pattern":"x"}`); err == nil {
		t.Fatal("expected a start error")
	}
}

func TestGrepUsesTheRealRipgrepWhenInstalled(t *testing.T) {
	probe := Options{Cwd: t.TempDir(), Ripgrep: ""}
	if probe.ripgrep() == "" {
		t.Skip("ripgrep is not installed")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "package a\nfunc Foo() {}\n", "b.txt": "Foo\n", ".gitignore": "b.txt\n"})
	r := NewRegistry()
	RegisterWith(r, Options{Cwd: dir, Home: dir})
	out, err := run(t, r, "grep", `{"pattern":"Foo"}`)
	if err != nil || out != "a.go:2: func Foo() {}" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestIgnorePatterns(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{".gitignore": "a/**/z\n*.[ch]\n!important.c\n\\#hash\n  \nlogs/\n/top\nno?.txt\nfoo/**\n"})
	set := parseIgnore(filepath.Join(dir, ".gitignore"), "")
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"a/b/c/z", false, true}, {"a/z", false, true}, {"x.c", false, true}, {"deep/x.h", false, true}, {"important.c", false, false},
		{"#hash", false, true}, {"logs", true, true}, {"logs", false, false}, {"top", false, true}, {"sub/top", false, false},
		{"nox.txt", false, true}, {"foo/bar/baz", false, true}, {"foo", true, false}, {"keep.go", false, false},
	}
	for _, c := range cases {
		if got, _ := set.match(c.path, c.isDir); got != c.want {
			t.Errorf("match(%q, dir=%v)=%v want %v", c.path, c.isDir, got, c.want)
		}
	}
	if parseIgnore(filepath.Join(dir, "missing"), "") != nil {
		t.Fatal("missing file should give nil")
	}
	nested := &ignoreSet{base: "sub", rules: set.rules}
	if _, decided := nested.match("other/x.c", false); decided {
		t.Fatal("a nested ignore file must not decide for other directories")
	}
	if got, _ := nested.match("sub/x.c", false); !got {
		t.Fatal("nested rule should apply below its directory")
	}
}

func TestBraceExpansion(t *testing.T) {
	got := expandBraces("src/*.{ts,tsx}")
	if strings.Join(got, "|") != "src/*.ts|src/*.tsx" {
		t.Fatal(got)
	}
	if got := expandBraces("no-braces"); len(got) != 1 {
		t.Fatal(got)
	}
	if got := expandBraces("open{only"); len(got) != 1 || got[0] != "open{only" {
		t.Fatal(got)
	}
}
