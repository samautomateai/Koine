package tui

import (
	"fmt"
	"strings"
)

// ColorTokens are the colour roles of a theme: Pi's documented groups (general, messages, tool execution, markdown, diffs, syntax, editor
// modes). The docs list the families with wildcards and point to a JSON schema for the exact names, so this list is Koine's reading of
// them; a theme that names a token outside it loads with a warning instead of failing.
var ColorTokens = []string{
	"accent", "border", "borderAccent", "borderMuted", "success", "error", "warning", "muted", "dim", "text", "thinkingText",
	"selectedBg", "userMessageBg", "userMessageText", "customMessageBg", "customMessageText", "customMessageLabel",
	"toolPendingBg", "toolSuccessBg", "toolErrorBg", "toolTitle", "toolOutput",
	"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder", "mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
	"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
	"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable", "syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator",
	"syntaxPunctuation",
	"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium", "thinkingHigh", "thinkingXhigh", "bashMode",
}

// optionalTokens may be left out of a theme; the value is the token whose colour stands in.
var optionalTokens = map[string]string{
	"searchMatchBg": "selectedBg", "searchMatchText": "text", "scrollbarTrack": "muted", "scrollbarThumb": "text", "thinkingMax": "thinkingXhigh",
}

// Color is one resolved theme value.
type Color struct {
	Set     bool // false: terminal default
	Index   int  // 0-255 when RGB is false
	RGB     bool
	R, G, B uint8
}

// Theme is a loaded colour theme. A nil *Theme is valid and paints nothing, which keeps tests and NO_COLOR output simple.
type Theme struct {
	Name       string
	Appearance string // "dark" or "light"
	Source     string // file the theme came from, "" for built-ins
	colors     map[string]Color
	TrueColor  bool // emit 24-bit colour; otherwise RGB values are mapped onto the 256-colour palette
}

// Color returns the resolved colour of a token.
func (t *Theme) Color(token string) Color {
	if t == nil {
		return Color{}
	}
	if c, ok := t.colors[token]; ok {
		return c
	}
	if fb, ok := optionalTokens[token]; ok {
		return t.colors[fb]
	}
	return Color{}
}

// code is the SGR parameter for c as a foreground (bg false) or background colour.
func (t *Theme) code(c Color, bg bool) string {
	base := 38
	if bg {
		base = 48
	}
	switch {
	case !c.Set:
		return fmt.Sprint(base + 1)
	case c.RGB && t.TrueColor:
		return fmt.Sprintf("%d;2;%d;%d;%d", base, c.R, c.G, c.B)
	case c.RGB:
		return fmt.Sprintf("%d;5;%d", base, nearest256(c.R, c.G, c.B))
	case c.Index < 8:
		return fmt.Sprint(base - 8 + c.Index)
	case c.Index < 16:
		return fmt.Sprint(base + 52 + c.Index - 8)
	}
	return fmt.Sprintf("%d;5;%d", base, c.Index)
}

// Fg paints s with the foreground colour of token and returns to the default foreground afterwards.
func (t *Theme) Fg(token, s string) string {
	if t == nil || s == "" {
		return s
	}
	return "\x1b[" + t.code(t.Color(token), false) + "m" + s + "\x1b[39m"
}

// Bg paints s with the background colour of token.
func (t *Theme) Bg(token, s string) string {
	if t == nil || s == "" {
		return s
	}
	return "\x1b[" + t.code(t.Color(token), true) + "m" + s + "\x1b[49m"
}

// FgBg paints s with a token's foreground over another token's background.
func (t *Theme) FgBg(fg, bg, s string) string { return t.Bg(bg, t.Fg(fg, s)) }

// Bold, Italic, Underline, Strike and Dim style text independent of the theme. They are plain when t is nil, so tests stay readable.
func (t *Theme) Bold(s string) string      { return t.sgr("1", "22", s) }
func (t *Theme) Italic(s string) string    { return t.sgr("3", "23", s) }
func (t *Theme) Underline(s string) string { return t.sgr("4", "24", s) }
func (t *Theme) Strike(s string) string    { return t.sgr("9", "29", s) }
func (t *Theme) Inverse(s string) string   { return t.sgr("7", "27", s) }

func (t *Theme) sgr(on, off, s string) string {
	if t == nil || s == "" {
		return s
	}
	return "\x1b[" + on + "m" + s + "\x1b[" + off + "m"
}

// Level is the colour token of a thinking level ("off" … "xhigh", "max").
func (t *Theme) Level(level string) string {
	tok := "thinkingOff"
	switch level {
	case "minimal", "low", "medium", "high", "xhigh", "max":
		tok = "thinking" + strings.ToUpper(level[:1]) + level[1:]
	}
	return t.Fg(tok, level)
}

// nearest256 maps an RGB value to the closest entry of the 6x6x6 colour cube or the grey ramp of the 256-colour palette.
func nearest256(r, g, b uint8) int {
	cube := func(v uint8) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return (int(v) - 35) / 40
	}
	ci, cj, ck := cube(r), cube(g), cube(b)
	levels := []int{0, 95, 135, 175, 215, 255}
	dist := func(a, b, c, x, y, z int) int { return (a-x)*(a-x) + (b-y)*(b-y) + (c-z)*(c-z) }
	cubeIdx := 16 + 36*ci + 6*cj + ck
	cd := dist(levels[ci], levels[cj], levels[ck], int(r), int(g), int(b))
	avg := (int(r) + int(g) + int(b)) / 3
	gi := (avg - 8) / 10
	gi = max(0, min(23, gi))
	gv := 8 + 10*gi
	if gd := dist(gv, gv, gv, int(r), int(g), int(b)); gd < cd {
		return 232 + gi
	}
	return cubeIdx
}
