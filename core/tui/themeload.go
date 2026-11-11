package tui

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed themes/*.json
var builtinThemes embed.FS

// BuiltinThemes are the theme names that need no file.
var BuiltinThemes = []string{"system", "dark", "light"}

type themeFile struct {
	Schema     string                     `json:"$schema"`
	Name       string                     `json:"name"`
	Appearance string                     `json:"appearance"`
	Vars       map[string]json.RawMessage `json:"vars"`
	Colors     map[string]json.RawMessage `json:"colors"`
	Export     map[string]json.RawMessage `json:"export"`
}

// ParseTheme reads a theme file. Unknown top-level fields are errors; colour keys outside ColorTokens are reported in warnings and
// ignored. A token the file leaves out takes its value from the built-in theme that matches its appearance. A missing variable or a
// reference cycle makes the theme invalid.
func ParseTheme(raw []byte, source string) (*Theme, []string, error) {
	var f themeFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", source, err)
	}
	if f.Name == "" || strings.Contains(f.Name, "/") || f.Name == "system" && !strings.HasPrefix(source, "builtin:") {
		return nil, nil, fmt.Errorf("%s: name is required, cannot contain / and cannot be \"system\"", source)
	}
	if f.Colors == nil {
		return nil, nil, fmt.Errorf("%s: colors is required", source)
	}
	if f.Appearance != "" && f.Appearance != "dark" && f.Appearance != "light" {
		return nil, nil, fmt.Errorf("%s: appearance must be \"dark\" or \"light\"", source)
	}
	t := &Theme{Name: f.Name, Source: source, colors: map[string]Color{}}
	known := map[string]bool{}
	for _, k := range ColorTokens {
		known[k] = true
	}
	for k := range optionalTokens {
		known[k] = true
	}
	var warns []string
	keys := make([]string, 0, len(f.Colors))
	for k := range f.Colors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !known[k] {
			warns = append(warns, fmt.Sprintf("%s: unknown colour token %q ignored", source, k))
			continue
		}
		c, err := resolveColor(f.Colors[k], f.Vars, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: colors.%s: %w", source, k, err)
		}
		t.colors[k] = c
	}
	for k, v := range f.Vars { // variables must resolve even when nothing uses them
		if _, err := resolveColor(v, f.Vars, nil); err != nil {
			return nil, nil, fmt.Errorf("%s: vars.%s: %w", source, k, err)
		}
	}
	t.Appearance = f.Appearance
	if t.Appearance == "" {
		t.Appearance = guessAppearance(t)
	}
	if !strings.HasPrefix(source, "builtin:") {
		base, _, _ := ParseTheme(mustBuiltin(t.Appearance), "builtin:"+t.Appearance)
		for _, k := range ColorTokens {
			if _, ok := t.colors[k]; !ok && base != nil {
				t.colors[k] = base.colors[k]
			}
		}
	}
	return t, warns, nil
}

func mustBuiltin(name string) []byte {
	b, _ := builtinThemes.ReadFile("themes/" + name + ".json")
	return b
}

func guessAppearance(t *Theme) string {
	c := t.colors["text"]
	if !c.Set {
		return "dark"
	}
	if c.RGB && (int(c.R)*299+int(c.G)*587+int(c.B)*114)/1000 < 128 {
		return "light"
	}
	return "dark"
}

// resolveColor turns a JSON value into a Color: a hex string, an oklch() string, a 0-255 index, a variable name, or "" for the default.
func resolveColor(raw json.RawMessage, vars map[string]json.RawMessage, seen []string) (Color, error) {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		if n != math.Trunc(n) || n < 0 || n > 255 {
			return Color{}, fmt.Errorf("colour index %v is not 0-255", n)
		}
		return Color{Set: true, Index: int(n)}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return Color{}, fmt.Errorf("a colour is a string or a number")
	}
	switch {
	case s == "":
		return Color{}, nil
	case strings.HasPrefix(s, "#"):
		return parseHex(s)
	case strings.HasPrefix(s, "oklch("):
		return parseOKLCH(s)
	case strings.HasPrefix(s, "okhsl("):
		return Color{}, fmt.Errorf("okhsl() colours are not supported by Koine; use hex or oklch()")
	}
	for _, v := range seen {
		if v == s {
			return Color{}, fmt.Errorf("variable %q refers to itself", s)
		}
	}
	ref, ok := vars[s]
	if !ok {
		return Color{}, fmt.Errorf("unknown variable %q", s)
	}
	return resolveColor(ref, vars, append(seen, s))
}

func parseHex(s string) (Color, error) {
	h := s[1:]
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return Color{}, fmt.Errorf("%q is not #rgb or #rrggbb", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, fmt.Errorf("%q is not a hex colour", s)
	}
	return Color{Set: true, RGB: true, R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)}, nil
}

func parseOKLCH(s string) (Color, error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(s, "oklch("), ")")
	f := strings.Fields(strings.ReplaceAll(inner, ",", " "))
	if len(f) < 3 {
		return Color{}, fmt.Errorf("%q needs lightness, chroma and hue", s)
	}
	num := func(v string) (float64, bool, error) {
		pct := strings.HasSuffix(v, "%")
		x, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(v, "%"), "deg"), 64)
		return x, pct, err
	}
	l, pct, err1 := num(f[0])
	c, _, err2 := num(f[1])
	h, _, err3 := num(f[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return Color{}, fmt.Errorf("%q is not a valid oklch colour", s)
	}
	if pct {
		l /= 100
	}
	a, b := c*math.Cos(h*math.Pi/180), c*math.Sin(h*math.Pi/180)
	l1, m1, s1 := l+0.3963377774*a+0.2158037573*b, l-0.1055613458*a-0.0638541728*b, l-0.0894841775*a-1.2914855480*b
	l1, m1, s1 = l1*l1*l1, m1*m1*m1, s1*s1*s1
	lin := [3]float64{4.0767416621*l1 - 3.3077115913*m1 + 0.2309699292*s1, -1.2684380046*l1 + 2.6097574011*m1 - 0.3413193965*s1,
		-0.0041960863*l1 - 0.7034186147*m1 + 1.7076147010*s1}
	var out [3]uint8
	for i, v := range lin {
		v = math.Max(0, math.Min(1, v))
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		out[i] = uint8(math.Round(v * 255))
	}
	return Color{Set: true, RGB: true, R: out[0], G: out[1], B: out[2]}, nil
}

// Builtin returns one of the built-in themes: system (the default, 16 terminal colours that follow the terminal's own palette), dark or
// light. It returns nil for any other name.
func Builtin(name string) *Theme {
	b, err := builtinThemes.ReadFile("themes/" + name + ".json")
	if err != nil {
		return nil
	}
	t, _, err := ParseTheme(b, "builtin:"+name)
	if err != nil {
		return nil
	}
	t.Source = ""
	return t
}

// ThemeDirs are the directories searched for <name>.json: the user's <agent dir>/themes, and the project's .koine/themes when trusted.
func ThemeDirs(agentDir, cwd string, trusted bool) []string {
	var d []string
	if agentDir != "" {
		d = append(d, filepath.Join(agentDir, "themes"))
	}
	if trusted && cwd != "" {
		d = append(d, filepath.Join(cwd, ".koine", "themes"))
	}
	return d
}

// ThemeNames lists the themes that can be selected: built-ins first, then the valid files found in dirs (later directories do not
// replace earlier ones; the clash is a warning).
func ThemeNames(dirs []string) (names []string, warns []string) {
	names = append(names, BuiltinThemes...)
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, dir := range dirs {
		for _, f := range themeFiles(dir) {
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			t, w, err := ParseTheme(raw, f)
			warns = append(warns, w...)
			if err != nil {
				warns = append(warns, err.Error())
				continue
			}
			if seen[t.Name] {
				warns = append(warns, fmt.Sprintf("%s: theme name %q is already taken", f, t.Name))
				continue
			}
			seen[t.Name] = true
			names = append(names, t.Name)
		}
	}
	return names, warns
}

// LoadTheme finds the theme called name: a built-in, or a file in dirs. When it cannot be found or is invalid the built-in "system"
// theme is returned with a warning, so a bad setting never stops the UI from starting.
func LoadTheme(name string, dirs []string, trueColor bool) (*Theme, []string) {
	if name == "" {
		name = "system"
	}
	t, warns := loadNamed(name, dirs)
	if t == nil {
		warns = append(warns, fmt.Sprintf("theme %q not found; using system", name))
		t = Builtin("system")
	}
	t.TrueColor = trueColor
	return t, warns
}

func loadNamed(name string, dirs []string) (*Theme, []string) {
	if t := Builtin(name); t != nil {
		return t, nil
	}
	var warns []string
	for _, dir := range dirs {
		for _, f := range themeFiles(dir) {
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			t, w, err := ParseTheme(raw, f)
			if err != nil {
				warns = append(warns, err.Error())
				continue
			}
			if t.Name == name {
				return t, append(warns, w...)
			}
		}
	}
	return nil, warns
}

// themeFiles are the theme files at a location: the *.json files of a directory, or the file itself (as --theme PATH may name one).
func themeFiles(loc string) []string {
	if strings.HasSuffix(loc, ".json") {
		return []string{loc}
	}
	files, _ := filepath.Glob(filepath.Join(loc, "*.json"))
	sort.Strings(files)
	return files
}
