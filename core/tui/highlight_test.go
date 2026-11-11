package tui

import (
	"strings"
	"testing"
)

func hl(lang string, code ...string) []string {
	th := Builtin("dark")
	th.TrueColor = true
	return Highlight(lang, code, th)
}

func hasTok(line, tok string) bool { // true when the colour of tok appears in the line
	th := Builtin("dark")
	th.TrueColor = true
	s := th.Fg(tok, "x")
	return strings.Contains(line, s[:strings.Index(s, "x")])
}

func TestHighlightGo(t *testing.T) {
	out := hl("go", `func (s *Server) Run(n int) error { // start`, `x := "str" + `+"`raw`"+` + 0x1F`)
	if !hasTok(out[0], "syntaxKeyword") || !hasTok(out[0], "syntaxType") || !hasTok(out[0], "syntaxFunction") || !hasTok(out[0], "syntaxComment") ||
		!hasTok(out[0], "syntaxPunctuation") {
		t.Errorf("line 0: %q", out[0])
	}
	if !hasTok(out[1], "syntaxString") || !hasTok(out[1], "syntaxNumber") || !hasTok(out[1], "syntaxOperator") {
		t.Errorf("line 1: %q", out[1])
	}
	for i, l := range out {
		if StripANSI(l) != []string{`func (s *Server) Run(n int) error { // start`, `x := "str" + ` + "`raw`" + ` + 0x1F`}[i] {
			t.Errorf("highlighting must not change the text: %q", StripANSI(l))
		}
	}
}

func TestHighlightMultilineStates(t *testing.T) {
	out := hl("js", "/* start", "still comment", "end */ let a = 1", `const s = "a\"b"`)
	if !hasTok(out[0], "syntaxComment") || !hasTok(out[1], "syntaxComment") || !hasTok(out[2], "syntaxComment") || !hasTok(out[2], "syntaxKeyword") {
		t.Errorf("block comments span lines: %q", out)
	}
	if !strings.Contains(StripANSI(out[3]), `"a\"b"`) {
		t.Errorf("escaped quotes: %q", out[3])
	}
	py := hl("python", `x = """doc`, "more", `end"""`, "# comment", "def f(self): return None")
	if !hasTok(py[0], "syntaxString") || !hasTok(py[1], "syntaxString") || !hasTok(py[2], "syntaxString") || !hasTok(py[3], "syntaxComment") ||
		!hasTok(py[4], "syntaxKeyword") {
		t.Errorf("python: %q", py)
	}
	if one := hl("py", `y = '''one line'''`); !hasTok(one[0], "syntaxString") {
		t.Errorf("single-line triple quotes: %q", one)
	}
	if c := hl("c", "/* all in one */ int x;"); !hasTok(c[0], "syntaxComment") || !hasTok(c[0], "syntaxType") {
		t.Errorf("c: %q", c)
	}
}

func TestHighlightOtherLanguages(t *testing.T) {
	j := hl("json", `{"key": "value", "n": 12, "ok": true}`)
	if !hasTok(j[0], "syntaxVariable") || !hasTok(j[0], "syntaxString") || !hasTok(j[0], "syntaxNumber") || !hasTok(j[0], "syntaxKeyword") {
		t.Errorf("json keys differ from values: %q", j[0])
	}
	if s := hl("bash", "if [ -f x ]; then echo $HOME; fi # done"); !hasTok(s[0], "syntaxKeyword") || !hasTok(s[0], "syntaxComment") {
		t.Errorf("shell: %q", s)
	}
	if q := hl("sql", "SELECT id FROM t WHERE x = 'a' -- note"); !hasTok(q[0], "syntaxKeyword") || !hasTok(q[0], "syntaxComment") {
		t.Errorf("sql keywords are case-insensitive: %q", q)
	}
	for _, lang := range []string{"yaml", "toml", "html", "css", "ruby", "rust", "ts", "tsx", "java"} {
		if out := hl(lang, "x"); len(out) != 1 {
			t.Errorf("%s", lang)
		}
	}
	if out := hl("brainfuck", "+++[-]"); out[0] != "+++[-]" {
		t.Errorf("unknown languages are left alone: %q", out)
	}
	if out := Highlight("go", []string{"func"}, nil); out[0] != "func" {
		t.Error("no theme, no colour")
	}
	if out := hl("go", "", ".5 + a.b 3.14"); !hasTok(out[1], "syntaxNumber") {
		t.Errorf("numbers: %q", out)
	}
	if out := hl("go", "é := ü + Ünï"); StripANSI(out[0]) != "é := ü + Ünï" {
		t.Errorf("unicode identifiers: %q", out)
	}
	if out := hl("go", "a ` b"); StripANSI(out[0]) != "a ` b" {
		t.Errorf("unterminated string: %q", out)
	}
}

func TestHighlightDiff(t *testing.T) {
	out := hl("diff", "--- a/x", "+++ b/x", "@@ -1 +1 @@", "-old", "+new", " same", "index 1..2")
	if !strings.Contains(out[0], "\x1b[1m") || !hasTok(out[2], "accent") || !hasTok(out[3], "toolDiffRemoved") || !hasTok(out[4], "toolDiffAdded") ||
		!hasTok(out[5], "toolDiffContext") {
		t.Errorf("diff: %q", out)
	}
	if o := Highlight("patch", []string{"+x"}, Builtin("dark")); !strings.Contains(o[0], "+x") {
		t.Error("patch alias")
	}
}
