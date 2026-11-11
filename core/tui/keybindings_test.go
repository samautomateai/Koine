package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonKey(t *testing.T) {
	good := map[string]string{"Ctrl+A": "ctrl+a", "shift+ctrl+p": "ctrl+shift+p", "esc": "escape", "Return": "enter", "pageUp": "pageup",
		"alt+ctrl+shift+super+x": "ctrl+shift+alt+super+x", "ctrl+-": "ctrl+-", "ctrl++": "ctrl++", "+": "+", "f12": "f12", "ctrl+]": "ctrl+]"}
	for in, want := range good {
		if got, ok := CanonKey(in); !ok || got != want {
			t.Errorf("CanonKey(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "meta+x", "ctrl+", "f13", "ctrl+ab", "hyper+a", "a b"} {
		if _, ok := CanonKey(bad); ok {
			t.Errorf("CanonKey(%q) should fail", bad)
		}
	}
}

func TestDefaultBindingsMatchPi(t *testing.T) {
	b := DefaultBindings()
	cases := map[string][]string{
		"tui.input.newLine": {"shift+enter", "ctrl+j"}, "tui.input.submit": {"enter"}, "app.model.select": {"ctrl+l"},
		"app.thinking.cycle": {"shift+tab"}, "app.thinking.toggle": {"ctrl+t"}, "app.tools.expand": {"ctrl+o"},
		"app.editor.external": {"ctrl+g"}, "tui.editor.undo": {"ctrl+-"}, "app.exit": {"ctrl+d"}, "app.clear": {"ctrl+c"},
		"tui.editor.cursorWordLeft": {"alt+left", "ctrl+left", "alt+b"}, "app.model.cycleBackward": {"ctrl+shift+p"},
		"app.tree.foldOrUp": {"ctrl+left", "alt+left"}, "app.interrupt": {"escape"}, "tui.editor.historyPrevious": nil,
	}
	for id, want := range cases {
		got := b.Keys(id)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v want %v", id, got, want)
		}
	}
	seen := map[string]bool{}
	for _, a := range Actions {
		if seen[a.ID] {
			t.Errorf("duplicate id %s", a.ID)
		}
		seen[a.ID] = true
		for _, k := range a.Keys {
			if _, ok := CanonKey(k); !ok {
				t.Errorf("%s: bad default key %q", a.ID, k)
			}
		}
	}
}

func TestMatchesAndAny(t *testing.T) {
	b := DefaultBindings()
	if !b.Matches("tui.input.submit", Key{Name: "enter"}) || b.Matches("tui.input.submit", Key{Name: "shift+enter"}) {
		t.Error("Matches")
	}
	if b.Matches("tui.input.submit", Key{Name: "enter", Paste: true}) || b.Matches("tui.input.submit", Key{}) {
		t.Error("pastes and empty keys match nothing")
	}
	if !b.Matches("tui.editor.pageUp", Key{Name: "pageUp"}) {
		t.Error("key names are compared case-insensitively")
	}
	if !b.Any(Key{Name: "escape"}, "app.clear", "app.interrupt") || b.Any(Key{Name: "x"}, "app.clear") {
		t.Error("Any")
	}
}

func TestLoadBindings(t *testing.T) {
	dir := t.TempDir()
	if b, w := LoadBindings(dir); len(w) != 0 || b.Keys("app.exit")[0] != "ctrl+d" {
		t.Errorf("no file means defaults: %v", w)
	}
	if b, w := LoadBindings(""); len(w) != 0 || b == nil {
		t.Error("no directory")
	}
	doc := `{"$schema":"https://pi.dev/schemas/keybindings.schema.json",
	  "app.model.select":"ctrl+k", "tui.input.newLine":["alt+enter","ctrl+j"], "app.exit":[], "tui.editor.undo":"Ctrl+Z",
	  "app.bogus":"x", "app.clear":["ctrl+c","hyper+a"], "app.interrupt": 5, "app.session.rename":"ctrl+r"}`
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	b, warns := LoadBindings(dir)
	if k := b.Keys("app.model.select"); len(k) != 1 || k[0] != "ctrl+k" {
		t.Errorf("override: %v", k)
	}
	if k := b.Keys("tui.input.newLine"); len(k) != 2 || k[0] != "alt+enter" {
		t.Errorf("array override replaces the defaults: %v", k)
	}
	if k := b.Keys("app.exit"); len(k) != 0 {
		t.Errorf("an empty array disables the action: %v", k)
	}
	if b.Matches("app.exit", Key{Name: "ctrl+d"}) {
		t.Error("disabled action still matches")
	}
	if k := b.Keys("tui.editor.undo"); k[0] != "ctrl+z" {
		t.Errorf("keys are normalised: %v", k)
	}
	if k := b.Keys("app.clear"); len(k) != 1 {
		t.Errorf("the valid key of a mixed list stays: %v", k)
	}
	if k := b.Keys("app.interrupt"); k[0] != "escape" {
		t.Errorf("a bad value keeps the default: %v", k)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{`unknown keybinding "app.bogus"`, `"hyper+a" is not a key`, `"app.interrupt" needs a key`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "app.session.rename") || strings.Contains(joined, "$schema") {
		t.Errorf("known-but-unused ids and $schema are not warned about:\n%s", joined)
	}
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{nope`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, w := LoadBindings(dir); len(w) != 1 {
		t.Errorf("bad JSON: %v", w)
	}
}

func TestHotkeys(t *testing.T) {
	h := DefaultBindings().Hotkeys()
	if !strings.Contains(h, "app.model.select") || !strings.Contains(h, "ctrl+l") || strings.Contains(h, "tui.editor.historyPrevious") {
		t.Errorf("hotkeys:\n%s", h)
	}
}
