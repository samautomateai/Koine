package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzy(t *testing.T) {
	if _, ok := FuzzyScore("xyz", "abc"); ok {
		t.Error("not a subsequence")
	}
	if s, ok := FuzzyScore("", "abc"); !ok || s != 0 {
		t.Error("empty query matches everything")
	}
	exact, _ := FuzzyScore("main", "main.go")
	scattered, _ := FuzzyScore("main", "my_amazing_index.go")
	if exact <= scattered {
		t.Errorf("prefix and consecutive matches score higher: %d vs %d", exact, scattered)
	}
	boundary, _ := FuzzyScore("fb", "foo/bar")
	inner, _ := FuzzyScore("fb", "fxxbxx")
	if boundary <= inner {
		t.Errorf("word-boundary matches score higher: %d vs %d", boundary, inner)
	}
	camel, _ := FuzzyScore("gc", "getCount")
	flat, _ := FuzzyScore("gc", "gxxcxxxx")
	if camel <= flat {
		t.Errorf("camel-case humps count as boundaries: %d vs %d", camel, flat)
	}
	got := FuzzyFilter("rd", []string{"README.md", "docs/design.md", "src/render.go", "zzz"})
	if len(got) != 2 || got[0] != "README.md" {
		t.Errorf("filter: %v", got)
	}
}

func TestCompleteSlashCommands(t *testing.T) {
	src := Sources{Commands: []Command{{"model", "choose the model"}, {"new", "new session"}, {"skill:review", "skill"}}}
	c := Complete([]rune("/mo"), 0, 3, src, false)
	if c == nil || c.Kind != "command" || c.Items[0].Value != "/model " || c.Items[0].Desc == "" || c.Start != 0 || c.End != 3 {
		t.Fatalf("%+v", c)
	}
	if c := Complete([]rune("/"), 0, 1, src, false); c == nil || len(c.Items) != 3 {
		t.Errorf("a bare slash lists every command: %+v", c)
	}
	if Complete([]rune("/zzz"), 0, 4, src, false) != nil {
		t.Error("no match, no popup")
	}
	if Complete([]rune("hello /mo"), 0, 9, src, false) != nil {
		t.Error("only at the start of the line")
	}
	if Complete([]rune("/mo"), 1, 3, src, false) != nil {
		t.Error("only on the first editor row")
	}
	if Complete([]rune("/model x"), 0, 8, src, false) != nil {
		t.Error("not after a space")
	}
	e := typed("/mo")
	c = Complete(e.lines[0], 0, 3, src, false)
	c.Apply(e)
	if e.Text() != "/model " || e.Col != 7 {
		t.Errorf("apply: %q", e.Text())
	}
	c = &Completion{Items: []Suggestion{{Value: "a"}, {Value: "b"}, {Value: "c"}}}
	c.Move(-1)
	if c.Sel != 2 {
		t.Error("Move wraps backwards")
	}
	c.Move(1)
	if c.Sel != 0 {
		t.Error("Move wraps forwards")
	}
	(*Completion)(nil).Apply(e)
	(&Completion{Start: 5, End: 2, Items: []Suggestion{{Value: "x"}}}).Apply(e)
}

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"README.md", "src/main.go", "src/render/markdown.go", "docs/design notes.md",
		"node_modules/x/index.js", ".git/config", "vendor/v.go"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("# c\nvendor/\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCompleteFileReferences(t *testing.T) {
	root := tree(t)
	src := Sources{Cwd: root, Files: &FileIndex{}}
	list := src.Files.List(root)
	joined := strings.Join(list, " ")
	if strings.Contains(joined, "node_modules") || strings.Contains(joined, ".git/") || strings.Contains(joined, "vendor") {
		t.Errorf("ignored directories are skipped: %v", list)
	}
	if !strings.Contains(joined, "src/main.go") || !strings.Contains(joined, "src/") {
		t.Errorf("files and directories: %v", list)
	}
	c := Complete([]rune("see @mdn"), 0, 8, src, false)
	if c == nil || c.Kind != "file" || c.Items[0].Label != "src/render/markdown.go" || c.Start != 4 {
		t.Fatalf("fuzzy file search: %+v", c)
	}
	e := typed("see @mdn")
	c.Apply(e)
	if e.Text() != "see @src/render/markdown.go " {
		t.Errorf("apply: %q", e.Text())
	}
	c = Complete([]rune("@design"), 0, 7, src, false)
	if c == nil || c.Items[0].Value != `@"docs/design notes.md"` {
		t.Errorf("paths with spaces are quoted: %+v", c)
	}
	c = Complete([]rune("@"), 0, 1, src, false)
	if c == nil || len(c.Items) != 4 {
		t.Errorf("a bare @ lists the top level: %+v", c)
	}
	c = Complete([]rune(`@"docs/des`), 0, 10, src, false)
	if c == nil || c.Items[0].Label != "docs/design notes.md" {
		t.Errorf("a quoted query: %+v", c)
	}
	if Complete([]rune("@qqqq"), 0, 5, src, false) != nil {
		t.Error("no match")
	}
	if Complete([]rune("@x"), 0, 2, Sources{}, false) != nil {
		t.Error("no index, no completion")
	}
	e = typed("@src")
	c = Complete(e.lines[0], 0, 4, src, false)
	for i, it := range c.Items {
		if it.Label == "src/" {
			c.Sel = i
		}
	}
	c.Apply(e)
	if e.Text() != "@src/" {
		t.Errorf("directories keep the cursor in the path: %q", e.Text())
	}
	small := &FileIndex{Limit: 2}
	if len(small.List(root)) != 2 {
		t.Error("the walk is bounded")
	}
	if got := src.Files.List(root); len(got) != len(list) {
		t.Error("cached")
	}
}

func TestCompleteTabPaths(t *testing.T) {
	root := tree(t)
	src := Sources{Cwd: root}
	if Complete([]rune("src/m"), 0, 5, src, false) != nil {
		t.Error("paths complete on Tab only")
	}
	c := Complete([]rune("src/m"), 0, 5, src, true)
	if c == nil || c.Kind != "path" || len(c.Items) != 1 || c.Items[0].Value != "src/main.go" {
		t.Fatalf("%+v", c)
	}
	c = Complete([]rune("./s"), 0, 3, src, true)
	if c == nil || c.Items[0].Value != "./src/" {
		t.Errorf("directories end in a slash: %+v", c)
	}
	c = Complete([]rune("src/"), 0, 4, src, true)
	if c == nil || c.Items[0].Value != "src/render/" {
		t.Errorf("directories sort first: %+v", c)
	}
	if c := Complete([]rune("./"), 0, 2, src, true); c == nil {
		t.Error("dot-slash")
	} else {
		for _, it := range c.Items {
			if strings.HasPrefix(it.Label, ".") {
				t.Errorf("hidden entries need a dot in the prefix: %v", it)
			}
		}
	}
	if Complete([]rune("nope/"), 0, 5, src, true) != nil || Complete([]rune("src/zz"), 0, 6, src, true) != nil {
		t.Error("missing directory or no match")
	}
	if Complete([]rune("plain"), 0, 5, src, true) != nil {
		t.Error("a plain word is not a path")
	}
	t.Setenv("HOME", root)
	if c := Complete([]rune("~/src/"), 0, 6, src, true); c == nil || c.Items[0].Value != "~/src/render/" {
		t.Errorf("home: %+v", c)
	}
	if c := Complete([]rune(root+"/sr"), 0, len(root)+3, src, true); c == nil || !strings.HasSuffix(c.Items[0].Value, "/src/") {
		t.Errorf("absolute: %+v", c)
	}
}

func TestCompletionView(t *testing.T) {
	var items []Suggestion
	for i := 0; i < 12; i++ {
		items = append(items, Suggestion{Label: strings.Repeat("x", i+1), Desc: "d"})
	}
	c := &Completion{Items: items, Sel: 7}
	lines := c.View(30, 5, Builtin("dark"))
	if len(lines) != 6 {
		t.Fatalf("5 rows and a position line: %d", len(lines))
	}
	if !strings.Contains(lines[5], "8/12") {
		t.Errorf("position: %q", lines[5])
	}
	sel := 0
	for _, l := range lines[:5] {
		if strings.Contains(l, "\x1b[48") {
			sel++
		}
	}
	if sel != 1 {
		t.Errorf("exactly one highlighted row, got %d", sel)
	}
	if got := c.View(30, 1, nil); len(got) != 4 {
		t.Errorf("the minimum is 3 rows: %d", len(got))
	}
	if got := c.View(30, 99, nil); len(got) != 12 {
		t.Errorf("all rows fit when the maximum is high: %d", len(got))
	}
	short := &Completion{Items: items[:2]}
	if got := short.View(30, 5, nil); len(got) != 2 {
		t.Errorf("no position line for short lists: %d", len(got))
	}
}
