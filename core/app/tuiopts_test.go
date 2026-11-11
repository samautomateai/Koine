package app

import (
	"path/filepath"
	"testing"
)

func TestTUIDefaultsAndClamps(t *testing.T) {
	d := Config{}.TUI()
	if d.Theme != "system" || d.EditorPaddingX != 0 || d.OutputPad != 1 || d.AutocompleteMax != 5 || d.CodeIndent != " " ||
		d.Hyperlinks != "auto" || d.TrueColor != "auto" || d.Quiet != "" || d.HardwareCursor || d.TerminalProgress {
		t.Errorf("defaults: %+v", d)
	}
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".koine", "settings.json"), `{"theme":"dark","quietStartup":"header","editorPaddingX":9,"outputPad":-4,
		"autocompleteMaxVisible":1,"showHardwareCursor":true,"externalEditor":"vi","terminal":{"showTerminalProgress":true,"hyperlinks":false,
		"trueColor":"true"},"markdown":{"codeBlockIndent":"    "}}`)
	cfg, _ := LoadConfig(t.TempDir(), home)
	c := cfg.TUI()
	if c.Theme != "dark" || c.Quiet != "header" || c.EditorPaddingX != 3 || c.OutputPad != 0 || c.AutocompleteMax != 3 || !c.HardwareCursor ||
		c.ExternalEditor != "vi" || !c.TerminalProgress || c.Hyperlinks != "false" || c.TrueColor != "true" || c.CodeIndent != "    " {
		t.Errorf("configured: %+v", c)
	}
	for in, want := range map[string]string{`true`: "true", `false`: "", `"header"`: "header", `"x"`: "", `3`: ""} {
		writeFile(t, filepath.Join(home, ".koine", "settings.json"),
			`{"quietStartup":`+in+`,"editorPaddingX":99,"autocompleteMaxVisible":99,"terminal":{"hyperlinks":"x"}}`)
		cfg, _ = LoadConfig(t.TempDir(), home)
		if got := cfg.TUI(); got.Quiet != want || got.AutocompleteMax != 20 || got.EditorPaddingX != 3 || got.Hyperlinks != "auto" {
			t.Errorf("quietStartup %s: %+v", in, got)
		}
	}
}

func TestTUIProjectSettingsNeedTrust(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(home, ".koine", "settings.json"), `{"theme":"dark","editorPaddingX":1}`)
	writeFile(t, filepath.Join(proj, ".koine", "settings.json"), `{"theme":"light","externalEditor":"evil","markdown":{"codeBlockIndent":"\t"}}`)
	u, p, _ := LoadConfigScoped(proj, home)
	if c := u.TUI(); c.Theme != "dark" || c.ExternalEditor != "" || c.EditorPaddingX != 1 {
		t.Errorf("user only: %+v", c)
	}
	mergeConfig(&u, p)
	if c := u.TUI(); c.Theme != "light" || c.ExternalEditor != "evil" || c.EditorPaddingX != 1 || c.CodeIndent != "\t" {
		t.Errorf("merged: %+v", c)
	}
}

func TestThemeArgs(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "mine.json"), `{}`)
	a := startIn(t, proj, Options{Load: LoadOptions{Themes: []string{"mine.json", "dark"}, NoThemes: true}})
	paths, use, no := a.ThemeArgs()
	if len(paths) != 1 || filepath.Base(paths[0]) != "mine.json" || use != "dark" || !no {
		t.Errorf("paths=%v use=%q no=%v", paths, use, no)
	}
	b := startIn(t, proj, Options{})
	if p, u, n := b.ThemeArgs(); len(p) != 0 || u != "" || n {
		t.Errorf("none given: %v %q %v", p, u, n)
	}
}

func TestTreeMovePrefillsTheEditorWhenOneIsAttached(t *testing.T) {
	a, _, users := treeApp(t)
	var got string
	a.PrefillEditor = func(s string) { got = s }
	say(t, a, "/tree "+users[1]+" no-summary")
	if got != "two" {
		t.Errorf("prefill %q", got)
	}
	a.PrefillEditor = nil
	if out := say(t, a, "/tree "+users[0]+" no-summary"); out == "" {
		t.Error("without an editor the message is shown in a notice")
	}
}

func TestUntrustedCodeIsCounted(t *testing.T) {
	isolate(t)
	proj := t.TempDir()
	t.Setenv("KOINE_TRUST", "") // nothing in this folder was approved
	writeFile(t, filepath.Join(proj, ".koine", "settings.json"), `{"plugin":["./p.mjs"]}`)
	writeFile(t, filepath.Join(proj, "p.mjs"), `export default function (pi) {}`)
	if a := startIn(t, proj, Options{Trust: false}); a.UntrustedCode == 0 {
		t.Errorf("UntrustedCode = %d", a.UntrustedCode)
	}
	t.Setenv("KOINE_TRUST", "1")
	if a := startIn(t, proj, Options{Trust: true}); a.UntrustedCode != 0 {
		t.Errorf("trusted folder: UntrustedCode = %d", a.UntrustedCode)
	}
}
