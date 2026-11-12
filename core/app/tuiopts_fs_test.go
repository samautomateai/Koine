package app

import (
	"path/filepath"
	"testing"
)

func TestFullscreenAndImageSettings(t *testing.T) {
	d := Config{}.TUI()
	if d.Fullscreen || d.ExitOutput != "transcript" || d.Scrollbar != "auto" || d.WheelLines != 0 || !d.ShowImages || d.ImageWidth != 60 ||
		d.ImageProtoIn != "auto" {
		t.Errorf("defaults: %+v", d)
	}
	home := t.TempDir()
	cases := []struct {
		json string
		want TUIConfig
	}{
		{`{"tuiMode":"fullscreen","fullscreenExitOutput":"resume-hint","fullscreenScrollbar":"hidden","fullscreenWheelScrollLines":500,
			"terminal":{"showImages":false,"imageWidthCells":9999,"images":"kitty"}}`,
			TUIConfig{Fullscreen: true, ExitOutput: "resume-hint", Scrollbar: "hidden", WheelLines: 100, ImageWidth: 400, ImageProtoIn: "kitty"}},
		{`{"tuiMode":"bogus","fullscreenExitOutput":"x","fullscreenScrollbar":"always","fullscreenWheelScrollLines":0,
			"terminal":{"showImages":true,"imageWidthCells":0,"images":false}}`,
			TUIConfig{ExitOutput: "transcript", Scrollbar: "always", WheelLines: 1, ShowImages: true, ImageWidth: 1, ImageProtoIn: "false"}},
		{`{"fullscreenWheelScrollLines":"auto","terminal":{"images":"iterm2"}}`,
			TUIConfig{ExitOutput: "transcript", Scrollbar: "auto", ShowImages: true, ImageWidth: 60, ImageProtoIn: "iterm2"}},
		{`{"terminal":{"images":"sixel"}}`, TUIConfig{ExitOutput: "transcript", Scrollbar: "auto", ShowImages: true, ImageWidth: 60, ImageProtoIn: "auto"}},
	}
	for _, c := range cases {
		writeFile(t, filepath.Join(home, ".koine", "settings.json"), c.json)
		cfg, _ := LoadConfig(t.TempDir(), home)
		g, w := cfg.TUI(), c.want
		if g.Fullscreen != w.Fullscreen || g.ExitOutput != w.ExitOutput || g.Scrollbar != w.Scrollbar || g.WheelLines != w.WheelLines ||
			g.ShowImages != w.ShowImages || g.ImageWidth != w.ImageWidth || g.ImageProtoIn != w.ImageProtoIn {
			t.Errorf("%s:\n got %+v\nwant %+v", c.json, g, w)
		}
	}
}

func TestFullscreenSettingsProjectOverridesUser(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(home, ".koine", "settings.json"), `{"tuiMode":"regular","terminal":{"showImages":true,"imageWidthCells":30,"images":"auto"}}`)
	writeFile(t, filepath.Join(proj, ".koine", "settings.json"),
		`{"tuiMode":"fullscreen","fullscreenExitOutput":"resume-hint","fullscreenScrollbar":"always","fullscreenWheelScrollLines":3,`+
			`"terminal":{"showImages":false,"imageWidthCells":40,"images":"kitty"}}`)
	u, p, _ := LoadConfigScoped(proj, home)
	if g := u.TUI(); g.Fullscreen || g.ImageWidth != 30 {
		t.Errorf("user only: %+v", g)
	}
	mergeConfig(&u, p)
	g := u.TUI()
	if !g.Fullscreen || g.ExitOutput != "resume-hint" || g.Scrollbar != "always" || g.WheelLines != 3 || g.ShowImages || g.ImageWidth != 40 ||
		g.ImageProtoIn != "kitty" {
		t.Errorf("merged: %+v", g)
	}
}
