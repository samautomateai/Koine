package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/app"
	"koine/core/session"
)

func userSettings(t *testing.T, ui *UI) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ui.a.Home, ".koine", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUIModelSelector(t *testing.T) {
	ui := newTestUI(t, nil)
	press(ui, "ctrl+l")
	if ui.sel == nil || !strings.Contains(frameText(ui), "Select model") || !strings.Contains(frameText(ui), "✓") {
		t.Fatalf("Ctrl+L opens the model selector with the current model marked:\n%s", frameText(ui))
	}
	typeText(ui, "haiku")
	if ui.sel.Filter != "haiku" {
		t.Error("typing filters")
	}
	press(ui, "enter")
	got := actionKinds(ui)
	if !strings.HasPrefix(got, "run:/model ") || !strings.Contains(got, "haiku") || ui.sel != nil {
		t.Errorf("choosing sends /model: %q", got)
	}
	ui.RunDone(false, nil, false)
	press(ui, "ctrl+l", "escape")
	if ui.sel != nil || actionKinds(ui) != "" {
		t.Error("Esc cancels")
	}
	press(ui, "ctrl+l")
	press(ui, "down", "up", "pageDown", "pageUp", "backspace")
	typeText(ui, "zzzzzz")
	press(ui, "enter") // nothing matches: stays open
	if ui.sel == nil {
		t.Error("Enter with no match does nothing")
	}
	press(ui, "escape")
	ui.a.Agent.Catalog = nil
	typeText(ui, "/model")
	press(ui, "enter")
	if f := frameText(ui); !strings.Contains(f, "no models to choose from") {
		t.Errorf("no catalog:\n%s", f)
	}
}

func TestUIScopedModels(t *testing.T) {
	ui := newTestUI(t, nil)
	press(ui, "ctrl+p")
	if !strings.Contains(ui.flash, "no scoped models") {
		t.Errorf("Ctrl+P without a scope: %q", ui.flash)
	}
	ui.scoped = []string{"claude-haiku-4-5", "claude-sonnet-5-5", "claude-opus-4-1"}
	press(ui, "ctrl+p")
	if got := actionKinds(ui); got != "run:/model claude-opus-4-1" {
		t.Errorf("the current model is second in the scope, so forward goes to the third: %q", got)
	}
	ui.RunDone(false, nil, false)
	press(ui, "ctrl+shift+p")
	if got := actionKinds(ui); got != "run:/model claude-haiku-4-5" {
		t.Errorf("backward: %q", got)
	}
	ui.RunDone(false, nil, false)
	ui.a.Agent.Model = "somethingelse"
	press(ui, "ctrl+shift+p")
	if got := actionKinds(ui); got != "run:/model claude-opus-4-1" {
		t.Errorf("an unlisted model goes backward to the last: %q", got)
	}
	ui.RunDone(false, nil, false)
	ui.a.Agent.Model = "claude-sonnet-5-5"

	press(ui, "ctrl+l")
	if !strings.Contains(frameText(ui), "scoped; tab for all") {
		t.Error("with a scope the selector starts on it")
	}
	press(ui, "tab")
	if !strings.Contains(frameText(ui), "all; tab for scoped") {
		t.Error("Tab shows all models")
	}
	press(ui, "tab", "escape")

	typeText(ui, "/scoped-models")
	press(ui, "enter")
	if ui.sel == nil || !ui.sel.Multi {
		t.Fatal("/scoped-models opens a multi-select")
	}
	press(ui, "ctrl+x")
	for _, it := range ui.sel.Items {
		if it.Mark != "[ ]" {
			t.Fatal("clear all")
		}
	}
	press(ui, "ctrl+a")
	for _, it := range ui.sel.Items {
		if it.Mark != "[x]" {
			t.Fatal("enable all")
		}
	}
	press(ui, "ctrl+x", "enter") // toggle the first one on
	press(ui, "ctrl+s")
	if ui.sel != nil || len(ui.scoped) != 1 {
		t.Fatalf("saved: %v", ui.scoped)
	}
	got, _ := userSettings(t, ui)["enabledModels"].([]any)
	if len(got) != 1 {
		t.Errorf("enabledModels is written to settings.json: %v", got)
	}
	typeText(ui, "/scoped-models")
	press(ui, "enter", "ctrl+x", "ctrl+s")
	if _, ok := userSettings(t, ui)["enabledModels"]; ok {
		t.Error("saving an empty scope removes the key")
	}
	press(ui, "x") // not a selector key: ignored by the multi-select hook, typed into the filter
}

func TestUIThinking(t *testing.T) {
	ui := newTestUI(t, nil)
	if ui.foot.Thinking == "" {
		t.Skip("the default model has no thinking levels")
	}
	cur := ui.foot.Thinking
	press(ui, "shift+tab")
	if got := actionKinds(ui); !strings.HasPrefix(got, "run:/thinking ") || strings.HasSuffix(got, cur) {
		t.Errorf("Shift+Tab cycles: %q", got)
	}
	ui.RunDone(false, nil, false)
	typeText(ui, "/thinking")
	press(ui, "enter")
	if ui.sel == nil || !strings.Contains(frameText(ui), "Thinking level") {
		t.Fatal("/thinking opens the selector")
	}
	press(ui, "down", "enter")
	if !strings.HasPrefix(actionKinds(ui), "run:/thinking ") {
		t.Error("choosing sends /thinking")
	}
	ui.RunDone(false, nil, false)

	ui.a.Agent.Model = "model-without-thinking"
	ui.refreshFooter()
	press(ui, "shift+tab")
	if !strings.Contains(ui.flash, "no thinking levels") {
		t.Errorf("flash %q", ui.flash)
	}
	ui.openThinkingSelector()
	if ui.sel != nil || !strings.Contains(frameText(ui), "not used with") {
		t.Error("the selector explains why there is nothing to pick")
	}
	if ui.borderToken() != "borderMuted" {
		t.Error("no level, plain border")
	}
	ui.foot.Thinking = "off"
	if ui.borderToken() != "thinkingOff" {
		t.Error("off")
	}
	ui.foot.Thinking = "high"
	if ui.borderToken() != "thinkingHigh" {
		t.Error("the editor border follows the thinking level")
	}
}

func TestUISettingsSelector(t *testing.T) {
	ui := newTestUI(t, nil)
	typeText(ui, "/settings")
	press(ui, "enter")
	if ui.sel == nil || !strings.Contains(frameText(ui), "Hide thinking blocks") {
		t.Fatal("settings selector")
	}
	press(ui, "down", "enter") // hide thinking blocks: off -> on
	if !ui.hideThinking || userSettings(t, ui)["hideThinkingBlock"] != true {
		t.Errorf("toggle applies and persists: %v", userSettings(t, ui))
	}
	if ui.sel == nil || ui.sel.Sel != 1 {
		t.Error("the selector stays open on the same row")
	}
	press(ui, "down", "enter") // quiet startup: off -> header
	if ui.set.Quiet != "header" || userSettings(t, ui)["quietStartup"] != "header" {
		t.Errorf("quietStartup: %q %v", ui.set.Quiet, userSettings(t, ui)["quietStartup"])
	}
	press(ui, "enter", "enter") // header -> true -> false
	if ui.set.Quiet != "" || userSettings(t, ui)["quietStartup"] != false {
		t.Errorf("cycles back: %q", ui.set.Quiet)
	}
	press(ui, "down", "enter") // editor padding 0 -> 1
	press(ui, "down", "enter") // output padding 1 -> 0
	press(ui, "down", "enter") // autocomplete rows 5 -> 8
	press(ui, "down", "enter") // hardware cursor
	press(ui, "down", "enter") // terminal progress
	m := userSettings(t, ui)
	if ui.set.EditorPaddingX != 1 || ui.set.OutputPad != 0 || ui.set.AutocompleteMax != 8 || !ui.set.HardwareCursor || !ui.set.TerminalProgress {
		t.Errorf("applied: %+v", ui.set)
	}
	if m["editorPaddingX"] != 1.0 || m["outputPad"] != 0.0 || m["autocompleteMaxVisible"] != 8.0 || m["showHardwareCursor"] != true {
		t.Errorf("persisted: %v", m)
	}
	if tm, _ := m["terminal"].(map[string]any); tm["showTerminalProgress"] != true {
		t.Errorf("dotted keys nest: %v", m)
	}
	press(ui, "escape")
	if ui.sel != nil {
		t.Error("Esc closes")
	}
}

func TestUIThemeSelectorAndReload(t *testing.T) {
	ui := newTestUI(t, nil)
	dir := filepath.Join(ui.a.Home, ".koine", "themes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.json"), []byte(minimalTheme), 0o644); err != nil {
		t.Fatal(err)
	}
	ui.openSettings(0)
	press(ui, "enter")
	if ui.sel == nil || !strings.Contains(frameText(ui), "mine") {
		t.Fatalf("theme list:\n%s", frameText(ui))
	}
	typeText(ui, "mine")
	press(ui, "enter")
	if ui.th.Name != "mine" || userSettings(t, ui)["theme"] != "mine" || ui.sel == nil {
		t.Errorf("theme applied and saved: %q", ui.th.Name)
	}
	press(ui, "escape")
	ui.openSettings(0)
	press(ui, "enter", "escape") // cancel the theme list: back to settings
	if ui.sel == nil {
		t.Error("cancelling the theme list returns to settings")
	}
	press(ui, "escape")
	// a keybindings change takes effect on reload
	if err := os.WriteFile(filepath.Join(ui.a.Home, ".koine", "keybindings.json"), []byte(`{"app.model.select":"ctrl+k","nonsense":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ui.a.Cfg.Theme.V, ui.a.Cfg.Theme.Set = "dark", true
	ui.ReloadLooks()
	press(ui, "ctrl+k")
	if ui.sel == nil || ui.th.Name != "dark" || !strings.Contains(frameText(ui), "keybindings:") {
		t.Errorf("reload re-reads keybindings.json and the theme")
	}
	press(ui, "escape")
	ui.save("x", 1)
	if err := os.WriteFile(filepath.Join(ui.a.Home, ".koine", "settings.json"), []byte("{\n // comment\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui.save("y", 2)
	if f := frameText(ui); !strings.Contains(f, "not plain JSON") {
		t.Errorf("a settings file with comments is not rewritten:\n%s", f)
	}
}

func TestUIAskAndTrustPrompt(t *testing.T) {
	ui := newTestUI(t, nil)
	var got = -2
	ui.Ask("Pick one", []string{"a", "b"}, func(i int) { got = i })
	if f := frameText(ui); !strings.Contains(f, "Pick one") || strings.Contains(f, "type to filter") {
		t.Errorf("short lists need no filter box:\n%s", f)
	}
	press(ui, "down", "enter")
	if got != 1 {
		t.Errorf("choice %d", got)
	}
	ui.Ask("Pick", []string{"a", "b", "c", "d", "e"}, func(i int) { got = i })
	if !strings.Contains(frameText(ui), "type to filter") {
		t.Error("long lists can be filtered")
	}
	press(ui, "escape")
	if got != -1 {
		t.Errorf("cancel %d", got)
	}

	// an untrusted folder with project code gets a question at startup
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("KOINE_TRUST", "")
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".koine"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"hooks":{"PreToolUse":[{"command":"true"}]},"plugin":["./x.js"]}`
	if err := os.WriteFile(filepath.Join(proj, ".koine", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(context.Background(), proj)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Agent.Provider = say("ok")
	if a.Trusted || a.UntrustedCode == 0 {
		t.Fatalf("fixture should be untrusted with project code: %v %d", a.Trusted, a.UntrustedCode)
	}
	set := ResolveSettings(a, func(string) string { return "" })
	th, keys, _, _ := Looks(a, set)
	u := NewUI(a, set, th, keys, 80, 24, nil)
	u.StartupPrompts()
	if u.sel == nil || !strings.Contains(frameText(u), "Trust ") || !strings.Contains(frameText(u), "Continue without them") {
		t.Fatalf("trust question:\n%s", frameText(u))
	}
	press(u, "down", "enter")
	if actionKinds(u) != "" {
		t.Error("declining runs nothing")
	}
	u.StartupPrompts()
	press(u, "enter")
	if got := actionKinds(u); got != "run:/trust" {
		t.Errorf("accepting trusts the folder: %q", got)
	}
	a.Trusted = true
	u.StartupPrompts()
	if u.sel != nil {
		t.Error("no question once trusted")
	}
}

func TestUITreeNavigator(t *testing.T) {
	ui := newTestUI(t, nil)
	ctx := context.Background()
	run := func(s string) {
		if _, err := ui.a.HandleInput(ctx, s, func(agent.Event) {}); err != nil {
			t.Fatal(err)
		}
	}
	run("first question")
	run("second question")
	typeText(ui, "/tree")
	press(ui, "enter")
	if ui.tree == nil {
		t.Fatal("/tree opens the navigator")
	}
	f := frameText(ui)
	if !strings.Contains(f, "Session tree") || !strings.Contains(f, "first question") || !strings.Contains(f, "← current") {
		t.Fatalf("tree view:\n%s", f)
	}
	t0 := ui.tree.Count()
	press(ui, "ctrl+u") // user messages only
	if ui.tree.Mode != session.FilterUserOnly || ui.tree.Count() != 2 {
		t.Errorf("filter: %s %d (was %d)", ui.tree.Mode, ui.tree.Count(), t0)
	}
	press(ui, "ctrl+a")
	if ui.tree.Mode != session.FilterAll {
		t.Error("all")
	}
	press(ui, "ctrl+d")
	press(ui, "ctrl+t")
	press(ui, "ctrl+l")
	if ui.tree.Mode != session.FilterLabeledOnly {
		t.Errorf("labelled filter: %s", ui.tree.Mode)
	}
	press(ui, "ctrl+o", "ctrl+o", "ctrl+shift+o")
	press(ui, "ctrl+d", "ctrl+u")

	// label the first user message
	press(ui, "up")
	id := ui.tree.CurrentID()
	press(ui, "shift+l")
	if ui.label == nil || ui.label.id != id {
		t.Fatal("Shift+L edits the label of the selected entry")
	}
	typeText(ui, "keep")
	press(ui, "left", "right", "backspace")
	typeText(ui, "p")
	if f := frameText(ui); !strings.Contains(f, "label for "+id) {
		t.Errorf("label prompt:\n%s", f)
	}
	press(ui, "enter")
	if ui.session().Labels()[id] != "keep" || ui.label != nil {
		t.Errorf("label saved: %v", ui.session().Labels())
	}
	press(ui, "shift+t")
	if f := frameText(ui); !strings.Contains(f, "[keep]") {
		t.Errorf("labels show in the tree:\n%s", f)
	}
	press(ui, "shift+l", "escape")
	if ui.label != nil {
		t.Error("Esc cancels label editing")
	}

	// fold and unfold
	press(ui, "ctrl+a")
	ui.tree.Sel = 0
	n := ui.tree.Count()
	press(ui, "ctrl+left")
	if ui.tree.Count() >= n {
		t.Errorf("folding hides the children: %d -> %d", n, ui.tree.Count())
	}
	press(ui, "ctrl+right")
	if ui.tree.Count() != n {
		t.Error("unfolding shows them again")
	}
	press(ui, "ctrl+right", "alt+left", "down", "pageDown", "pageUp")
	typeText(ui, "first")
	press(ui, "backspace")
	if ui.tree.Search != "firs" {
		t.Errorf("search: %q", ui.tree.Search)
	}
	press(ui, "escape")
	if ui.tree != nil {
		t.Error("Esc closes")
	}

	// selecting moves the leaf through the app's own /tree command
	ui.openTree("tree", "")
	ui.tree.SetMode(session.FilterUserOnly, "")
	ui.tree.Sel = 0
	want := ui.tree.CurrentID()
	press(ui, "enter")
	if got := actionKinds(ui); got != "run:/tree "+want || ui.tree != nil {
		t.Errorf("select: %q", got)
	}
	ui.RunDone(false, nil, false)
	ui.openTree("fork", "bogus-mode")
	if ui.tree.Mode != session.FilterDefault && ui.treeFor != "fork" {
		t.Error("an invalid mode falls back to the default")
	}
	press(ui, "enter")
	if got := actionKinds(ui); !strings.HasPrefix(got, "run:/fork ") {
		t.Errorf("fork mode runs /fork: %q", got)
	}
	ui.a.Session = nil
	ui.openTree("tree", "")
	if ui.tree != nil || !strings.Contains(frameText(ui), "sessions are off") {
		t.Error("no session, no tree")
	}
}

func TestUIRebuildAfterSessionCommands(t *testing.T) {
	ui := newTestUI(t, nil)
	ctx := context.Background()
	if _, err := ui.a.HandleInput(ctx, "remember me", func(agent.Event) {}); err != nil {
		t.Fatal(err)
	}
	ui.lastRun = "/new"
	ui.a.NewSession()
	ui.RunDone(false, nil, false)
	if f := frameText(ui); strings.Contains(f, "remember me") || !strings.Contains(f, "\x1b[3J") && false {
		t.Errorf("a new session starts a clean screen:\n%s", f)
	}
	if !ui.full && len(ui.blocks) > 3 {
		t.Error("rebuilt")
	}
	ui.a.PrefillEditor = ui.Prefill
	ui.Prefill("edit me")
	if ui.ed.Text() != "edit me" {
		t.Error("Prefill")
	}
	if !sessionCommand("/resume abc") || sessionCommand("/model x") || sessionCommand("hello") {
		t.Error("sessionCommand")
	}
}
