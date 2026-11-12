package tui

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;:?<>=]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b_[^\x1b]*\x1b\\\\|\x1b[78]")

// StripANSI removes colour codes and hyperlinks, leaving the text a terminal would show.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// runeWidth is the number of terminal cells r takes: 0 for combining and control characters, 2 for East Asian wide ones and emoji.
func runeWidth(r rune) int {
	switch {
	case r == 0 || r == '​' || r == '‍' || r == '️':
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
		return 0
	case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || (r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1f64f) || (r >= 0x1f900 && r <= 0x1f9ff) || (r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}

// Width is the display width of s in terminal cells, ignoring ANSI sequences.
func Width(s string) int {
	n := 0
	for _, r := range StripANSI(s) {
		n += runeWidth(r)
	}
	return n
}

// token is an ANSI sequence or one rune of text.
type token struct {
	s    string
	ansi bool
	w    int
}

func tokens(s string) []token {
	var out []token
	for len(s) > 0 {
		if s[0] == 0x1b {
			if loc := ansiRe.FindStringIndex(s); loc != nil && loc[0] == 0 {
				out = append(out, token{s: s[:loc[1]], ansi: true})
				s = s[loc[1]:]
				continue
			}
		}
		r, n := utf8.DecodeRuneInString(s)
		out = append(out, token{s: s[:n], w: runeWidth(r)})
		s = s[n:]
	}
	return out
}

// sgrState tracks the styles in effect so a cut line can close them and the next line can reopen them.
type sgrState []string

func (st *sgrState) note(seq string) {
	if !strings.HasSuffix(seq, "m") || !strings.HasPrefix(seq, "\x1b[") {
		return
	}
	if seq == "\x1b[0m" || seq == "\x1b[m" {
		*st = nil
		return
	}
	*st = append(*st, seq)
}

func (st sgrState) open() string { return strings.Join(st, "") }
func (st sgrState) close() string {
	if len(st) == 0 {
		return ""
	}
	return "\x1b[0m"
}

// Truncate cuts s to at most w cells, appending tail (counted in w) when it had to cut. Styles are closed.
func Truncate(s string, w int, tail string) string {
	if Width(s) <= w {
		return s
	}
	tw := Width(tail)
	if w <= tw {
		tail, tw = "", 0
	}
	var b strings.Builder
	var st sgrState
	n := 0
	for _, t := range tokens(s) {
		if t.ansi {
			st.note(t.s)
			b.WriteString(t.s)
			continue
		}
		if n+t.w > w-tw {
			break
		}
		b.WriteString(t.s)
		n += t.w
	}
	b.WriteString(st.close())
	b.WriteString(tail)
	return b.String()
}

// Pad right-pads s with spaces to w cells (it never cuts).
func Pad(s string, w int) string {
	if d := w - Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// Wrap breaks s into lines of at most w cells at spaces, splitting words longer than a line, and carries styles over each break.
// Newlines in s are kept.
func Wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, wrapLine(para, w)...)
	}
	return out
}

func wrapLine(s string, w int) []string {
	if Width(s) <= w {
		return []string{s}
	}
	var lines []string
	var cur, word strings.Builder
	var st sgrState
	cw, ww, sp := 0, 0, 0 // sp: spaces seen since the last word; written only if the next word stays on the line
	flush := func() {
		cur.WriteString(st.close())
		lines = append(lines, cur.String())
		cur.Reset()
		cw = 0
		cur.WriteString(st.open())
	}
	emitWord := func() {
		if word.Len() == 0 {
			return
		}
		if cw+sp+ww > w && cw > 0 {
			flush()
		} else if cw > 0 {
			cur.WriteString(strings.Repeat(" ", sp))
			cw += sp
		}
		sp = 0
		cur.WriteString(word.String())
		cw += ww
		word.Reset()
		ww = 0
	}
	for _, t := range tokens(s) {
		switch {
		case t.ansi:
			st.note(t.s)
			word.WriteString(t.s)
		case t.s == " ":
			emitWord()
			if cw > 0 {
				sp++
			}
		default:
			if ww+t.w > w { // a single word longer than a line
				emitWord()
				flush()
			}
			word.WriteString(t.s)
			ww += t.w
		}
	}
	emitWord()
	if cur.Len() > 0 {
		cur.WriteString(st.close())
		lines = append(lines, cur.String())
	}
	return lines
}
