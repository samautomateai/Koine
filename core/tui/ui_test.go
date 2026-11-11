package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koine/core/agent"
	"koine/core/app"
)

// newTestApp builds an app on an isolated HOME. KOINE_TRUST is set on purpose: the fixtures hold no project code, and tests that need an
// untrusted folder unset it.
func newTestApp(t *testing.T, cwd string) *app.App {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, "cache"))
	t.Setenv("KOINE_TRUST", "1")
	t.Setenv("KOINE_AGENT_DIR", "")
	if cwd == "" {
		cwd = t.TempDir()
	}
	a, err := app.New(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Agent.Provider = say("model says hi")
	return a
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestUI(t *testing.T, setup func(*app.App)) *UI {
	t.Helper()
	a := newTestApp(t, "")
	if setup != nil {
		setup(a)
	}
	set := ResolveSettings(a, func(string) string { return "" })
	th, keys, dirs, _ := Looks(a, set)
	th.TrueColor = true
	ui := NewUI(a, set, th, keys, 80, 24, (&clock{t: time.Unix(1000, 0)}).now)
	ui.themeDirs = dirs
	return ui
}

func press(ui *UI, names ...string) {
	for _, n := range names {
		ui.Key(Key{Name: n})
	}
}

func typeText(ui *UI, s string) {
	for _, r := range s {
		ui.Key(textKey(r))
	}
}

func frameText(ui *UI) string {
	var out []string
	for _, l := range strings.Split(ui.Frame(), "\r\n") {
		out = append(out, StripANSI(l))
	}
	return strings.Join(out, "\n")
}

func actionKinds(ui *UI) string {
	var k []string
	for _, a := range ui.Take() {
		k = append(k, a.Kind+":"+a.Text)
	}
	return strings.Join(k, ",")
}

func TestUISubmitRunsALineAndShowsIt(t *testing.T) {
	ui := newTestUI(t, nil)
	typeText(ui, "hello world")
	if got := frameText(ui); !strings.Contains(got, "hello world") || !strings.Contains(got, "koine") {
		t.Errorf("frame:\n%s", got)
	}
	press(ui, "enter")
	if got := actionKinds(ui); got != "run:hello world" || !ui.Busy() || !ui.ed.IsEmpty() {
		t.Errorf("submit: %q busy=%v", got, ui.Busy())
	}
	ui.Event(agent.Event{Type: "text", Text: "**hi**"})
	f := frameText(ui)
	if !strings.Contains(f, "hello world") || !strings.Contains(f, "hi") || !strings.Contains(f, "Working…") || !strings.Contains(f, "esc to interrupt") {
		t.Errorf("transcript and loader:\n%s", f)
	}
	typeText(ui, "next")
	press(ui, "enter")
	if actionKinds(ui) != "" || ui.ed.Text() != "next" || !strings.Contains(frameText(ui), "a turn is running") {
		t.Error("Enter while a turn runs keeps the text and says so")
	}
	if !ui.Tick() {
		t.Error("the spinner advances")
	}
	ui.RunDone(false, nil, false)
	if ui.Busy() || strings.Contains(frameText(ui), "Working") {
		t.Error("done")
	}
	ui.RunDone(false, os.ErrNotExist, false)
	ui.RunDone(false, nil, true)
	if f := frameText(ui); !strings.Contains(f, "error: file does not exist") || !strings.Contains(f, "(interrupted)") {
		t.Errorf("errors and interrupts show:\n%s", f)
	}
	ui.RunDone(true, nil, false)
	if !ui.Quit() {
		t.Error("/exit")
	}
}

func TestUIFlashExpires(t *testing.T) {
	ui := newTestUI(t, nil)
	c := &clock{t: time.Unix(1000, 0)}
	ui.now = c.now
	ui.say("hello flash")
	if ui.Tick() {
		t.Error("nothing changed yet")
	}
	c.t = c.t.Add(3 * time.Second)
	if !ui.Tick() || ui.flash != "" {
		t.Error("expired")
	}
}

func TestUIInterruptClearAndExit(t *testing.T) {
	ui := newTestUI(t, nil)
	c := &clock{t: time.Unix(1000, 0)}
	ui.now = c.now
	typeText(ui, "abc")
	press(ui, "ctrl+c")
	if !ui.ed.IsEmpty() || ui.Quit() {
		t.Error("Ctrl+C clears the editor")
	}
	press(ui, "ctrl+c")
	if ui.Quit() || !strings.Contains(ui.flash, "again") {
		t.Error("the first Ctrl+C on an empty editor only warns")
	}
	press(ui, "ctrl+c")
	if !ui.Quit() || actionKinds(ui) != "quit:" {
		t.Error("the second one exits")
	}

	ui = newTestUI(t, nil)
	ui.now = c.now
	typeText(ui, "x")
	press(ui, "ctrl+d")
	if ui.Quit() || ui.ed.Text() != "x" {
		t.Error("Ctrl+D with text deletes forward, it does not exit")
	}
	ui.ed.Clear()
	press(ui, "ctrl+d")
	if !ui.Quit() {
		t.Error("Ctrl+D on an empty editor exits")
	}

	ui = newTestUI(t, nil)
	ui.busy = true
	press(ui, "escape")
	press(ui, "ctrl+c")
	if got := actionKinds(ui); got != "interrupt:,interrupt:" {
		t.Errorf("Esc and Ctrl+C interrupt a running turn: %q", got)
	}
}

func TestUIDoubleEscape(t *testing.T) {
	for mode, want := range map[string]string{"tree": "tree", "fork": "fork", "none": ""} {
		ui := newTestUI(t, func(a *app.App) { a.Cfg.DoubleEscapeAction = mode })
		_ = ui.a.HandleInput // keep the import used in all branches
		if _, err := ui.a.HandleInput(context.Background(), "hello", func(agent.Event) {}); err != nil {
			t.Fatal(err)
		}
		c := &clock{t: time.Unix(1000, 0)}
		ui.now = c.now
		press(ui, "escape")
		if ui.tree != nil {
			t.Fatal("one Escape does nothing")
		}
		c.t = c.t.Add(300 * time.Millisecond)
		press(ui, "escape")
		switch want {
		case "":
			if ui.tree != nil {
				t.Errorf("%s: opened a tree", mode)
			}
		default:
			if ui.tree == nil || ui.treeFor != want {
				t.Errorf("%s: tree=%v for=%q", mode, ui.tree != nil, ui.treeFor)
			}
		}
	}
	ui := newTestUI(t, nil)
	c := &clock{t: time.Unix(1000, 0)}
	ui.now = c.now
	typeText(ui, "text")
	press(ui, "escape")
	c.t = c.t.Add(100 * time.Millisecond)
	press(ui, "escape")
	if ui.tree != nil {
		t.Error("Escape with text in the editor does not open the tree")
	}
}

func TestUIEditingKeys(t *testing.T) {
	ui := newTestUI(t, nil)
	typeText(ui, "one two")
	press(ui, "ctrl+w", "ctrl+y", "ctrl+a", "ctrl+k", "ctrl+y", "ctrl+-")
	if ui.ed.Text() != "" {
		t.Errorf("kill, yank, undo: %q", ui.ed.Text())
	}
	ui.Key(Key{Name: "shift+enter"})
	typeText(ui, "x")
	press(ui, "ctrl+j")
	if ui.ed.Text() != "\nx\n" {
		t.Errorf("newlines: %q", ui.ed.Text())
	}
	ui.ed.Clear()
	ui.Key(Key{Paste: true, Text: "pasted\ntext"})
	press(ui, "left", "right", "home", "end", "alt+left", "alt+right", "delete", "backspace", "alt+d", "alt+backspace", "ctrl+u", "alt+y", "up", "down",
		"pageUp", "pageDown", "f1")
	ui.Key(Key{Name: "x", Text: "\x1b"})
	if strings.Contains(ui.ed.Text(), "\x1b") {
		t.Error("control text is never inserted")
	}
}

func TestUIPromptHistory(t *testing.T) {
	ui := newTestUI(t, nil)
	for _, p := range []string{"first", "second"} {
		typeText(ui, p)
		press(ui, "enter")
		ui.Take()
		ui.RunDone(false, nil, false)
	}
	press(ui, "up")
	if ui.ed.Text() != "second" {
		t.Errorf("up: %q", ui.ed.Text())
	}
	press(ui, "up")
	if ui.ed.Text() != "first" {
		t.Errorf("up again: %q", ui.ed.Text())
	}
	press(ui, "down", "down")
	if ui.ed.Text() != "" {
		t.Errorf("down returns to the empty draft: %q", ui.ed.Text())
	}
	typeText(ui, "abc")
	press(ui, "up")
	if ui.ed.Text() != "abc" {
		t.Error("Up in a non-empty editor on its only row does not start history")
	}
}

func TestUIAutocompleteInTheEditor(t *testing.T) {
	ui := newTestUI(t, nil)
	typeText(ui, "/mod")
	if ui.comp == nil || !strings.Contains(frameText(ui), "/model") {
		t.Fatal("popup opens while typing a slash command")
	}
	press(ui, "tab")
	if ui.ed.Text() != "/model " || ui.comp != nil {
		t.Errorf("Tab accepts: %q", ui.ed.Text())
	}
	ui.ed.Clear()
	typeText(ui, "/")
	press(ui, "down", "up", "escape")
	if ui.comp != nil || ui.busy {
		t.Error("Esc closes the popup")
	}
	typeText(ui, "e")
	if ui.comp == nil {
		t.Error("typing reopens it")
	}
	press(ui, "enter")
	if !strings.HasPrefix(ui.ed.Text(), "/e") || ui.comp != nil || actionKinds(ui) != "" {
		t.Errorf("Enter accepts the suggestion instead of sending: %q", ui.ed.Text())
	}
	ui.ed.Clear()
	ui.afterEdit()
	if err := os.WriteFile(filepath.Join(ui.a.Cwd, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	typeText(ui, "see @note")
	if ui.comp == nil || ui.comp.Kind != "file" {
		t.Fatal("@ opens file search")
	}
	press(ui, "tab")
	if ui.ed.Text() != "see @notes.txt " {
		t.Errorf("file completion: %q", ui.ed.Text())
	}
	ui.ed.Clear()
	typeText(ui, "./no")
	press(ui, "tab") // nothing to complete: no such entry
	typeText(ui, "x")
	ui.ed.Clear()
	typeText(ui, "./not")
	press(ui, "tab")
	if ui.ed.Text() != "./notes.txt" {
		t.Errorf("Tab completes a single path match: %q", ui.ed.Text())
	}
	if err := os.Mkdir(filepath.Join(ui.a.Cwd, "notes2"), 0o755); err != nil {
		t.Fatal(err)
	}
	ui.ed.Clear()
	typeText(ui, "./not")
	press(ui, "tab")
	if ui.comp == nil || len(ui.comp.Items) != 2 {
		t.Errorf("Tab with several matches opens the popup: %+v", ui.comp)
	}
}

func TestUIToggles(t *testing.T) {
	ui := newTestUI(t, nil)
	ui.Event(agent.Event{Type: "thinking", Text: "secret plan"})
	ui.RunDone(false, nil, false)
	ui.Frame()
	press(ui, "ctrl+t")
	f := ui.Frame()
	if !ui.hideThinking || !strings.Contains(f, "\x1b[3J") || !strings.Contains(StripANSI(f), "Thinking…") || strings.Contains(StripANSI(f), "secret plan") {
		t.Errorf("Ctrl+T hides thinking and redraws everything: %q", StripANSI(f))
	}
	press(ui, "ctrl+t")
	if f := StripANSI(ui.Frame()); !strings.Contains(f, "secret plan") {
		t.Errorf("shown again: %q", f)
	}
	ui.Event(agent.Event{Type: "tool_start", Tool: "bash", CallID: "x", Args: []byte(`{"command":"seq"}`)})
	ui.Event(agent.Event{Type: "tool_end", Tool: "bash", CallID: "x", Text: strings.Repeat("l\n", 30)})
	if f := StripANSI(ui.Frame()); !strings.Contains(f, "earlier lines (ctrl+o to expand)") {
		t.Errorf("collapsed:\n%s", f)
	}
	press(ui, "ctrl+o")
	if f := StripANSI(ui.Frame()); strings.Contains(f, "ctrl+o to expand") || !ui.expanded {
		t.Errorf("Ctrl+O expands:\n%s", f)
	}
	ui.Event(agent.Event{Type: "text", Text: "the answer"})
	press(ui, "ctrl+x")
	press(ui, "ctrl+z")
	press(ui, "ctrl+g")
	if got := actionKinds(ui); got != "copy:the answer,suspend:,editor:" {
		t.Errorf("actions: %q", got)
	}
	typeText(ui, "/copy")
	press(ui, "enter")
	if got := actionKinds(ui); got != "copy:the answer" {
		t.Errorf("/copy: %q", got)
	}
	ui.Resize(100, 30)
	if f := ui.Frame(); !strings.Contains(f, "\x1b[3J") || ui.width != 100 {
		t.Error("resize redraws")
	}
	if ui.Frame() == "" || strings.Contains(ui.Frame(), "\x1b[3J") {
		t.Error("only one full redraw per request")
	}
	typeText(ui, "/hotkeys")
	press(ui, "enter")
	if f := StripANSI(ui.Frame()); !strings.Contains(f, "app.model.select") {
		t.Errorf("/hotkeys:\n%s", f)
	}
}

func TestUILayoutStaysInsideTheTerminal(t *testing.T) {
	ui := newTestUI(t, nil)
	typeText(ui, strings.Repeat("wide ", 30))
	ui.Resize(30, 8)
	live, _, _ := ui.live()
	for _, l := range live {
		if Width(l) > 30 {
			t.Errorf("line wider than the terminal: %q", l)
		}
	}
	ui.Frame()
	ui.set.HardwareCursor = true
	if f := ui.Frame(); !strings.Contains(f, "\x1b[?25h") {
		t.Error("a hardware cursor is shown")
	}
}
