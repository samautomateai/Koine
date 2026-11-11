package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/llm"
)

func TestFooter(t *testing.T) {
	cost := 0.1234
	f := FooterState{Cwd: "/home/u/proj", Branch: "main", Session: "refactor", Model: "claude-sonnet-5-5", Thinking: "high"}
	f.AddUsage(&agent.UsageInfo{Usage: llm.Usage{Input: 1200, Output: 340, CacheRead: 5000, CacheWrite: 800}, Cost: &cost, ContextTokens: 7340,
		ContextWindow: 200000, ContextPct: 3.67})
	f.AddUsage(&agent.UsageInfo{Usage: llm.Usage{Input: 100, Output: 10}, ContextTokens: 7500, ContextWindow: 200000, ContextPct: 3.75})
	f.AddUsage(nil)
	lines := Footer(f, "/home/u", 100, nil)
	if lines[0] != "~/proj (main) • refactor" {
		t.Errorf("line 1: %q", lines[0])
	}
	for _, want := range []string{"↑1.3k", "↓350", "R5.0k", "W800", "≥$0.123", "3.8%/200k", "claude-sonnet-5-5 • high"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("line 2 lacks %q: %q", want, lines[1])
		}
	}
	if !strings.HasSuffix(lines[1], "claude-sonnet-5-5 • high") || Width(lines[1]) != 100 {
		t.Errorf("the model is right-aligned: %q (%d)", lines[1], Width(lines[1]))
	}
	f.Unpriced = false
	if l := Footer(f, "", 100, nil); !strings.Contains(l[1], " $0.123") {
		t.Errorf("exact cost: %q", l[1])
	}
	th := Builtin("dark")
	th.TrueColor = true
	f.CtxPct = 75
	warn := Footer(f, "", 100, th)[1]
	f.CtxPct = 95
	crit := Footer(f, "", 100, th)[1]
	if !strings.Contains(warn, th.Fg("warning", "75.0%/200k")) || !strings.Contains(crit, th.Fg("error", "95.0%/200k")) {
		t.Errorf("context colours:\n%q\n%q", warn, crit)
	}
	empty := Footer(FooterState{Cwd: "/x", Model: "m"}, "", 40, nil)
	if strings.TrimSpace(empty[1]) != "m" {
		t.Errorf("no usage yet: %q", empty[1])
	}
	narrow := Footer(f, "", 20, nil)
	for _, l := range narrow {
		if Width(l) > 20 {
			t.Errorf("narrow footer: %q", l)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1.0k", 15400: "15k", 999999: "1000k", 1500000: "1.5M"} {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d)=%q want %q", n, got, want)
		}
	}
	if ShortPath("/home/u", "/home/u") != "~" || ShortPath("/home/ux", "/home/u") != "/home/ux" || ShortPath("/a", "") != "/a" {
		t.Error("ShortPath")
	}
}

func TestGitBranch(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/feat/x\n")
	if got := GitBranch(sub); got != "feat/x" {
		t.Errorf("branch %q", got)
	}
	write(filepath.Join(root, ".git", "HEAD"), "0123456789abcdef\n")
	if got := GitBranch(sub); got != "0123456" {
		t.Errorf("detached %q", got)
	}
	// a worktree has a .git file that points at the real directory
	wt := t.TempDir()
	real := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(real, "HEAD"), "ref: refs/heads/wtbranch\n")
	write(filepath.Join(wt, ".git"), "gitdir: "+real+"\n")
	if got := GitBranch(wt); got != "wtbranch" {
		t.Errorf("worktree %q", got)
	}
	write(filepath.Join(wt, ".git"), "gitdir: rel\n")
	if GitBranch(wt) != "" {
		t.Error("a relative gitdir that does not exist")
	}
	write(filepath.Join(wt, ".git"), "garbage")
	if GitBranch(wt) != "" {
		t.Error("garbage .git file")
	}
	if GitBranch(t.TempDir()) != "" {
		t.Error("outside a repository")
	}
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if GitBranch(bare) != "" {
		t.Error("no HEAD file")
	}
}

func TestScreenDraw(t *testing.T) {
	var s Screen
	first := s.Draw([]string{"one", "two"}, []string{"live1", "live2", "live3"}, 1, 4, false, false, 24)
	want := "\x1b[?25l\r\x1b[J" + "one\x1b[0m\r\ntwo\x1b[0m\r\n" + "live1\x1b[0m\r\nlive2\x1b[0m\r\nlive3\x1b[0m" + "\x1b[1A\r\x1b[4C"
	if first != want {
		t.Errorf("first frame:\n%q\nwant\n%q", first, want)
	}
	next := s.Draw(nil, []string{"a"}, 0, 0, true, false, 24)
	if !strings.HasPrefix(next, "\x1b[?25l\x1b[1A\r\x1b[J") || !strings.HasSuffix(next, "\r\x1b[?25h") {
		t.Errorf("the next frame moves up to the top of the previous live area and shows the cursor: %q", next)
	}
	full := s.Draw([]string{"x"}, nil, 0, 0, false, true, 24)
	if !strings.Contains(full, "\x1b[3J\x1b[2J\x1b[H") || strings.Contains(full, "\x1b[J\r") {
		t.Errorf("full redraw: %q", full)
	}
	tall := make([]string, 10)
	for i := range tall {
		tall[i] = "l"
	}
	out := s.Draw(nil, tall, 9, 0, false, false, 5)
	if strings.Count(out, "\r\n") != 3 || s.prevRow != 3 {
		t.Errorf("a live area taller than the screen is cut from the top (4 rows): %q prev=%d", out, s.prevRow)
	}
	if got := s.Clear(); got != "\x1b[3A\r\x1b[J\x1b[?25h" || s.Clear() != "\r\x1b[J\x1b[?25h" {
		t.Errorf("clear: %q", got)
	}
	if ProgressSeq(true) != "\x1b]9;4;3;\x1b\\" || ProgressSeq(false) != "\x1b]9;4;0;\x1b\\" {
		t.Error("progress")
	}
	if Copy("hi") != "\x1b]52;c;aGk=\x1b\\" {
		t.Errorf("copy %q", Copy("hi"))
	}
}

func TestSelector(t *testing.T) {
	items := []SelItem{{Label: "alpha", Desc: "first"}, {Label: "beta"}, {Label: "gamma", Mark: "✓"}, {Label: "delta"}}
	s := NewSelector("Pick", items)
	s.Max = 3
	if i, ok := s.Current(); !ok || i != 0 {
		t.Error("starts on the first item")
	}
	s.Move(10)
	if i, _ := s.Current(); i != 3 {
		t.Errorf("Move clamps: %d", i)
	}
	s.Move(-10)
	s.Type("ta")
	if i, ok := s.Current(); !ok || items[i].Label != "beta" {
		t.Errorf("filter keeps the best fuzzy match first: %d", i)
	}
	s.Backspace()
	s.Backspace()
	s.Backspace()
	if s.Filter != "" || len(s.view) != 4 {
		t.Errorf("filter cleared: %q %d", s.Filter, len(s.view))
	}
	s.Type("zzz")
	if _, ok := s.Current(); ok {
		t.Error("no match")
	}
	s.Move(1)
	if v := strings.Join(s.View(40, nil), "\n"); !strings.Contains(v, "no matches") {
		t.Errorf("view: %s", v)
	}
	s.Filter = ""
	s.refilter()
	s.Hint = "enter picks"
	s.Sel = 3
	view := s.View(40, Builtin("dark"))
	text := StripANSI(strings.Join(view, "\n"))
	for _, want := range []string{"Pick", "(4/4)", "enter picks", "✓ gamma", "delta"} {
		if !strings.Contains(text, want) {
			t.Errorf("view lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "alpha") {
		t.Error("the window scrolls with the selection")
	}
	s.NoSearch = true
	s.Type("x")
	if s.Filter != "" {
		t.Error("typing is ignored without search")
	}
	if strings.Contains(strings.Join(s.View(40, nil), "\n"), "type to filter") {
		t.Error("no filter box without search")
	}
	NewSelector("empty", nil).Move(1)
}

func TestExternalEditorCommand(t *testing.T) {
	env := func(m map[string]string) Env { return func(k string) string { return m[k] } }
	cases := []struct {
		setting string
		env     map[string]string
		want    string
	}{
		{"code --wait", map[string]string{"VISUAL": "v", "EDITOR": "e"}, "code --wait"},
		{"", map[string]string{"VISUAL": "nvim", "EDITOR": "e"}, "nvim"},
		{"", map[string]string{"EDITOR": `"/opt/my editor/ed" -w 'a b'`}, "/opt/my editor/ed|-w|a b"},
		{"  ", nil, "vi"},
	}
	for _, c := range cases {
		got := strings.Join(EditorCommand(c.setting, env(c.env)), "|")
		if got != strings.ReplaceAll(c.want, " --wait", "|--wait") {
			t.Errorf("%+v: got %q", c, got)
		}
	}
	if got := splitCommand(`a "" b`); len(got) != 3 || got[1] != "" {
		t.Errorf("empty quoted argument: %q", got)
	}
}

func TestEditExternally(t *testing.T) {
	var gotPath string
	out, err := EditExternally("start", []string{"ed", "-x"}, func(c []string) error {
		gotPath = c[len(c)-1]
		b, _ := os.ReadFile(gotPath)
		if string(b) != "start" || c[0] != "ed" || c[1] != "-x" {
			t.Errorf("the file holds the text and the path is the last argument: %q %v", b, c)
		}
		return os.WriteFile(gotPath, []byte("line1\r\nline2\n"), 0o600)
	})
	if err != nil || out != "line1\nline2" {
		t.Errorf("%q %v", out, err)
	}
	if _, err := os.Stat(gotPath); err == nil {
		t.Error("the temporary file is removed")
	}
	if _, err := EditExternally("x", []string{"ed"}, func([]string) error { return exec.ErrNotFound }); err == nil || !strings.Contains(err.Error(), "ed:") {
		t.Errorf("errors name the editor: %v", err)
	}
	if out, err := EditExternally("x", []string{"ed"}, func(c []string) error { return os.Remove(c[len(c)-1]) }); err == nil {
		t.Errorf("an editor that deletes the file: %q", out)
	}
}

func TestResolveSettingsAndSave(t *testing.T) {
	a := newTestApp(t, "")
	env := map[string]string{"COLORTERM": "24bit", "TERM_PROGRAM": "WezTerm"}
	set := ResolveSettings(a, func(k string) string { return env[k] })
	if !set.TrueColor || !set.Links || set.Theme != "system" || set.OutputPad != 1 || set.AutocompleteMax != 5 ||
		set.CodeIndent != " " || set.EditorPaddingX != 0 {
		t.Errorf("defaults: %+v", set)
	}
	if set.DoubleEscape != "tree" || set.TreeFilter != "default" {
		t.Errorf("tree settings: %+v", set)
	}
	none := ResolveSettings(a, func(string) string { return "" })
	if none.TrueColor || none.Links {
		t.Error("auto detection finds nothing in an empty environment")
	}
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{{map[string]string{"KITTY_WINDOW_ID": "1"}, true}, {map[string]string{"WT_SESSION": "x"}, true}, {map[string]string{"VTE_VERSION": "6003"}, true},
		{map[string]string{"VTE_VERSION": "4000"}, false}, {map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false}} {
		if supportsLinks(func(k string) string { return c.env[k] }) != c.want {
			t.Errorf("supportsLinks(%v) != %v", c.env, c.want)
		}
	}

	// explicit settings win over detection
	cfg := `{"terminal":{"hyperlinks":false,"trueColor":true,"showTerminalProgress":true},"theme":"dark","editorPaddingX":9,"outputPad":0,` +
		`"autocompleteMaxVisible":1,"quietStartup":"header","showHardwareCursor":true,"markdown":{"codeBlockIndent":"  "},"externalEditor":"hx"}`
	dir := filepath.Join(a.Home, ".koine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Reload(t.Context())
	set = ResolveSettings(a, func(k string) string { return env[k] })
	if set.Links || !set.TrueColor || !set.TerminalProgress || set.Theme != "dark" ||
		set.EditorPaddingX != 3 || set.OutputPad != 0 || set.AutocompleteMax != 3 ||
		set.Quiet != "header" || !set.HardwareCursor || set.CodeIndent != "  " || set.ExternalEditor != "hx" {
		t.Errorf("configured: %+v", set)
	}

	if err := SaveSetting(a.Home, "a.b.c", 1); err != nil {
		t.Fatal(err)
	}
	if err := SaveSetting(a.Home, "theme", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["theme"]; ok || m["externalEditor"] != "hx" ||
		m["a"].(map[string]any)["b"].(map[string]any)["c"] != 1.0 || !strings.HasSuffix(string(b), "}\n") {
		t.Errorf("other keys are kept, nil removes a key: %s", b)
	}
	t.Setenv("KOINE_AGENT_DIR", "")
	if err := SaveSetting("", "x", 1); err == nil {
		t.Error("no agent directory")
	}
}
