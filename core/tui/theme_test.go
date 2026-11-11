package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinThemesAreCompleteAndValid(t *testing.T) {
	for _, n := range BuiltinThemes {
		th := Builtin(n)
		if th == nil {
			t.Fatalf("%s did not load", n)
		}
		if th.Name != n {
			t.Errorf("name %q", th.Name)
		}
		for _, tok := range ColorTokens {
			if _, ok := th.colors[tok]; !ok {
				t.Errorf("%s lacks %s", n, tok)
			}
		}
	}
	if Builtin("nope") != nil {
		t.Error("unknown built-in")
	}
	if Builtin("light").Appearance != "light" || Builtin("dark").Appearance != "dark" {
		t.Error("appearance")
	}
}

func TestColorCodes(t *testing.T) {
	th := &Theme{TrueColor: true, colors: map[string]Color{
		"a": {Set: true, RGB: true, R: 1, G: 2, B: 3}, "b": {Set: true, Index: 3}, "c": {Set: true, Index: 12}, "d": {Set: true, Index: 200}, "e": {},
	}}
	cases := []struct{ got, want string }{
		{th.Fg("a", "x"), "\x1b[38;2;1;2;3mx\x1b[39m"}, {th.Fg("b", "x"), "\x1b[33mx\x1b[39m"}, {th.Fg("c", "x"), "\x1b[94mx\x1b[39m"},
		{th.Fg("d", "x"), "\x1b[38;5;200mx\x1b[39m"}, {th.Fg("e", "x"), "\x1b[39mx\x1b[39m"}, {th.Bg("a", "x"), "\x1b[48;2;1;2;3mx\x1b[49m"},
		{th.Bg("b", "x"), "\x1b[43mx\x1b[49m"}, {th.Bg("c", "x"), "\x1b[104mx\x1b[49m"}, {th.FgBg("b", "c", "x"), "\x1b[104m\x1b[33mx\x1b[39m\x1b[49m"},
		{th.Bold("x"), "\x1b[1mx\x1b[22m"}, {th.Italic("x"), "\x1b[3mx\x1b[23m"}, {th.Underline("x"), "\x1b[4mx\x1b[24m"},
		{th.Strike("x"), "\x1b[9mx\x1b[29m"}, {th.Inverse("x"), "\x1b[7mx\x1b[27m"}, {th.Fg("a", ""), ""},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
	th.TrueColor = false
	if got := th.Fg("a", "x"); !strings.HasPrefix(got, "\x1b[38;5;") {
		t.Errorf("without true colour RGB maps to the 256 palette: %q", got)
	}
	var none *Theme
	if none.Fg("a", "x") != "x" || none.Bold("x") != "x" || none.Bg("a", "x") != "x" || none.Color("a").Set {
		t.Error("a nil theme paints nothing")
	}
	if th.Color("searchMatchBg") != th.Color("selectedBg") {
		t.Error("optional tokens fall back")
	}
	if !strings.Contains(Builtin("dark").Level("high"), "high") || !strings.Contains(Builtin("dark").Level("bogus"), "bogus") {
		t.Error("Level")
	}
}

func TestNearest256(t *testing.T) {
	cases := map[[3]uint8]int{{0, 0, 0}: 16, {255, 255, 255}: 231, {255, 0, 0}: 196, {128, 128, 128}: 244, {10, 10, 10}: 232, {0, 0, 135}: 18}
	for rgb, want := range cases {
		if got := nearest256(rgb[0], rgb[1], rgb[2]); got != want {
			t.Errorf("%v: got %d want %d", rgb, got, want)
		}
	}
}

const minimalTheme = `{"name":"mine","vars":{"primary":"#0af","alias":"primary","grey":240},
"colors":{"accent":"alias","text":"","muted":"grey","error":"oklch(62% 0.2 25)"}}`

func TestParseTheme(t *testing.T) {
	th, warns, err := ParseTheme([]byte(minimalTheme), "mine.json")
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if c := th.Color("accent"); !c.RGB || c.R != 0 || c.G != 0xaa || c.B != 0xff {
		t.Errorf("variables resolve through other variables: %+v", c)
	}
	if c := th.Color("muted"); c.RGB || c.Index != 240 || !c.Set {
		t.Errorf("256-colour index: %+v", c)
	}
	if c := th.Color("text"); c.Set {
		t.Errorf("empty string is the terminal default: %+v", c)
	}
	if c := th.Color("error"); !c.RGB || c.R < 200 || c.G > 120 {
		t.Errorf("oklch: %+v", c)
	}
	if th.Color("success").Set != Builtin("dark").Color("success").Set || th.Color("toolDiffAdded") != Builtin("dark").Color("toolDiffAdded") {
		t.Error("missing tokens come from the built-in theme of the same appearance")
	}
	if th.Appearance != "dark" {
		t.Errorf("appearance %q", th.Appearance)
	}
}

func TestParseThemeErrors(t *testing.T) {
	bad := map[string]string{
		"not json":      `{`,
		"no name":       `{"colors":{}}`,
		"slash":         `{"name":"a/b","colors":{}}`,
		"system":        `{"name":"system","colors":{}}`,
		"no colors":     `{"name":"x"}`,
		"unknown field": `{"name":"x","colors":{},"extra":1}`,
		"appearance":    `{"name":"x","appearance":"grey","colors":{}}`,
		"missing var":   `{"name":"x","colors":{"accent":"nope"}}`,
		"cycle":         `{"name":"x","vars":{"a":"b","b":"a"},"colors":{"accent":"a"}}`,
		"bad var":       `{"name":"x","vars":{"a":"zzz"},"colors":{}}`,
		"bad hex":       `{"name":"x","colors":{"accent":"#12"}}`,
		"hex digits":    `{"name":"x","colors":{"accent":"#gggggg"}}`,
		"index":         `{"name":"x","colors":{"accent":300}}`,
		"fraction":      `{"name":"x","colors":{"accent":1.5}}`,
		"okhsl":         `{"name":"x","colors":{"accent":"okhsl(250 60% 55%)"}}`,
		"oklch args":    `{"name":"x","colors":{"accent":"oklch(1)"}}`,
		"oklch number":  `{"name":"x","colors":{"accent":"oklch(a b c)"}}`,
		"value type":    `{"name":"x","colors":{"accent":[1]}}`,
	}
	for name, doc := range bad {
		if _, _, err := ParseTheme([]byte(doc), "x.json"); err == nil {
			t.Errorf("%s should fail", name)
		}
	}
	th, warns, err := ParseTheme([]byte(`{"name":"x","appearance":"light","colors":{"customThing":"#fff","accent":"#000"}}`), "x.json")
	if err != nil || len(warns) != 1 || !strings.Contains(warns[0], "customThing") || th.Appearance != "light" {
		t.Errorf("unknown tokens warn and are ignored: %v %v", warns, err)
	}
	th, _, _ = ParseTheme([]byte(`{"name":"x","colors":{"text":"#101010"}}`), "x.json")
	if th.Appearance != "light" {
		t.Errorf("a dark text colour means a light theme, got %q", th.Appearance)
	}
	if c, _ := parseHex("#abc"); c.R != 0xaa || c.G != 0xbb || c.B != 0xcc {
		t.Errorf("short hex: %+v", c)
	}
}

func TestThemeDiscovery(t *testing.T) {
	agent, proj := t.TempDir(), t.TempDir()
	write := func(dir, file, doc string) string {
		d := filepath.Join(dir, "themes")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(d, file)
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write(agent, "mine.json", minimalTheme)
	write(agent, "broken.json", `{`)
	write(agent, "clash.json", `{"name":"dark","colors":{}}`)
	write(agent, "readme.txt", `ignored`)
	write(filepath.Join(proj, ".koine"), "proj.json", strings.Replace(minimalTheme, "mine", "proj", 1))

	if d := ThemeDirs(agent, proj, false); len(d) != 1 {
		t.Errorf("project themes need trust: %v", d)
	}
	dirs := ThemeDirs(agent, proj, true)
	if len(dirs) != 2 {
		t.Fatalf("dirs %v", dirs)
	}
	names, warns := ThemeNames(dirs)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "system,dark,light") || !strings.Contains(joined, "mine") || !strings.Contains(joined, "proj") {
		t.Errorf("names %v", names)
	}
	if len(warns) != 2 {
		t.Errorf("a broken file and a name clash are reported: %v", warns)
	}
	if th, w := LoadTheme("mine", dirs, true); th.Name != "mine" || !th.TrueColor || len(w) != 1 {
		t.Errorf("custom theme: %v %v", th.Name, w)
	}
	if th, w := LoadTheme("dark", dirs, false); th.Name != "dark" || th.TrueColor || len(w) != 0 {
		t.Errorf("built-in wins: %v %v", th.Name, w)
	}
	if th, w := LoadTheme("missing", dirs, false); th.Name != "system" || len(w) == 0 || !strings.Contains(w[len(w)-1], "not found") {
		t.Errorf("fallback: %v %v", th.Name, w)
	}
	if th, _ := LoadTheme("", nil, false); th.Name != "system" {
		t.Error("empty name means system")
	}
	one := write(agent, "single.json", strings.Replace(minimalTheme, "mine", "single", 1))
	if th, _ := LoadTheme("single", []string{one}, false); th.Name != "single" {
		t.Error("--theme may name a single file")
	}
	if ThemeDirsFor("/home/x", "/p", true, true, []string{"/extra"})[0] != "/extra" {
		t.Error("--no-themes drops discovery but keeps --theme paths")
	}
}
