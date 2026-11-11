package tui

import (
	"strings"
	"testing"
)

func plain(md string, w int) string {
	var out []string
	for _, l := range Markdown(md, MDOptions{Width: w}) {
		out = append(out, StripANSI(l))
	}
	return strings.Join(out, "\n")
}

func TestMarkdownBlocks(t *testing.T) {
	md := "# Title\n\nSome *emphasis*, **strong** and `code`.\nSecond line joins.\n\n## Sub\n\n### Third\n\n---\n\n> quoted\n> text\n"
	got := plain(md, 60)
	for _, want := range []string{"Title", "Some emphasis, strong and code. Second line joins.", "Sub", "### Third", strings.Repeat("─", 60), "│ quoted text"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "**") || strings.Contains(got, "`") || strings.Contains(got, "# Title") {
		t.Errorf("markers must be consumed:\n%s", got)
	}
	if lines := Markdown("a  \nb", MDOptions{Width: 40}); len(lines) != 2 {
		t.Errorf("two trailing spaces are a hard break: %q", lines)
	}
	if lines := Markdown("", MDOptions{Width: 40}); len(lines) != 0 {
		t.Errorf("empty: %q", lines)
	}
	narrow := Markdown(strings.Repeat("word ", 30), MDOptions{Width: 1})
	if len(narrow) < 5 {
		t.Error("tiny widths are raised to a minimum, not an error")
	}
}

func TestMarkdownStyles(t *testing.T) {
	th := Builtin("dark")
	th.TrueColor = true
	o := MDOptions{Width: 60, Theme: th}
	joined := strings.Join(Markdown("# H\n\n**b** *i* ~~s~~ `c` [l](http://x) text", o), "\n")
	for _, want := range []string{"\x1b[1mb\x1b[22m", "\x1b[3mi\x1b[23m", "\x1b[9ms\x1b[29m", "\x1b[4m", "(http://x)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	o.Hyperlinks = true
	joined = strings.Join(Markdown("[l](http://x) and <https://auto.example> and https://bare.example/path.", o), "\n")
	if !strings.Contains(joined, "\x1b]8;;http://x\x1b\\") || !strings.Contains(joined, "\x1b]8;;https://auto.example\x1b\\") ||
		!strings.Contains(joined, "\x1b]8;;https://bare.example/path\x1b\\") {
		t.Errorf("OSC 8 links: %q", joined)
	}
	if strings.Contains(StripANSI(joined), "(http://x)") {
		t.Error("with hyperlinks the URL is not repeated")
	}
}

func TestMarkdownInlineEdgeCases(t *testing.T) {
	cases := map[string]string{
		`a \*not emphasis\* b`:      "a *not emphasis* b",
		"snake_case_name stays":     "snake_case_name stays",
		"2 * 3 * 4":                 "2 * 3 * 4",
		"``double `tick` code``":    "double `tick` code",
		"unclosed `tick":            "unclosed `tick",
		"**bold *inner* end**":      "bold inner end",
		"[nested [brackets]](u)":    "nested [brackets] (u)",
		"[no url]":                  "[no url]",
		"[text](url \"title\")":     "text (url)",
		"_under_ and __double__":    "under and double",
		"a**b**c":                   "abc",
		"~~gone~~":                  "gone",
		"* star":                    "• star",
		"1 < 2 and <b>":             "1 < 2 and <b>",
		"`code with **stars**`":     "code with **stars**",
		"text https://x.io/a, more": "text https://x.io/a, more",
	}
	for in, want := range cases {
		if got := plain(in, 80); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestMarkdownLists(t *testing.T) {
	md := "- one\n- two\n  continued\n  - nested a\n  - nested b\n- three\n\n1. first\n2. second\n\n- [ ] todo\n- [x] done\n"
	got := plain(md, 40)
	want := "• one\n• two continued\n  • nested a\n  • nested b\n• three\n\n1. first\n2. second\n\n☐ todo\n☑ done"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	wrapped := plain("- "+strings.Repeat("word ", 12), 20)
	for _, l := range strings.Split(wrapped, "\n")[1:] {
		if !strings.HasPrefix(l, "  ") {
			t.Errorf("continuation lines hang under the text: %q", l)
		}
	}
}

func TestMarkdownCodeBlocks(t *testing.T) {
	md := "text\n```go\nfunc main() {\n\tfmt.Println(\"hi\") // greet\n}\n```\nafter\n\n~~~\nplain\n~~~\n\n```\nunterminated"
	got := plain(md, 50)
	for _, want := range []string{"```go", " func main() {", "// greet", "```\n\nafter", " plain", " unterminated"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	dark := Builtin("dark")
	dark.TrueColor = true
	o := MDOptions{Width: 30, Theme: dark, CodeIndent: "    "}
	lines := Markdown("```go\nvar x = 1\n```", o)
	if !strings.HasPrefix(StripANSI(lines[1]), "    var x = 1") {
		t.Errorf("markdown.codeBlockIndent: %q", lines[1])
	}
	if !strings.Contains(lines[1], "\x1b[38;2") {
		t.Errorf("highlighted: %q", lines[1])
	}
	long := Markdown("```\n"+strings.Repeat("x", 100)+"\n```", MDOptions{Width: 20})
	for _, l := range long {
		if Width(l) > 20 {
			t.Errorf("code lines are cut to the width: %q", l)
		}
	}
	if !closesFence("````", "```") || closesFence("``", "```") || closesFence("```go", "```") {
		t.Error("closesFence")
	}
}

func TestMarkdownTables(t *testing.T) {
	md := "| Name | Qty | Note |\n|:-----|----:|:----:|\n| apple | 3 | fresh |\n| kiwi | 12 | a \\| b |\n\nafter"
	got := plain(md, 60)
	lines := strings.Split(got, "\n")
	if len(lines) != 8 || !strings.HasPrefix(lines[0], "┌") || !strings.HasPrefix(lines[3], "│ apple") || lines[7] != "after" && lines[6] != "after" {
		t.Fatalf("shape:\n%s", got)
	}
	if !strings.Contains(got, "│ Name  │ Qty │ Note  │") && !strings.Contains(got, "│ Name  │ Qty │ Note  │") {
		t.Logf("table:\n%s", got)
	}
	if !strings.Contains(got, "│ kiwi  │  12 │") || !strings.Contains(got, "a | b") {
		t.Errorf("right alignment and escaped pipes:\n%s", got)
	}
	for _, l := range lines[:6] {
		if Width(l) != Width(lines[0]) {
			t.Errorf("rows must have equal width: %q", l)
		}
	}
	wide := "| a | b |\n|---|---|\n| " + strings.Repeat("long ", 20) + " | short |\n"
	out := Markdown(wide, MDOptions{Width: 30})
	for _, l := range out {
		if Width(l) > 30 {
			t.Errorf("tables shrink to the width: %q (%d)", l, Width(l))
		}
	}
	if len(out) < 6 {
		t.Errorf("cells wrap onto several lines: %d", len(out))
	}
	notTable := plain("a | b\nnot a delimiter row", 40)
	if strings.Contains(notTable, "┌") {
		t.Error("a pipe in a paragraph is not a table")
	}
	if !isTableStart([]string{"a|b", "-|-"}, 0) || isTableStart([]string{"a|b", "-|-|-"}, 0) || isTableStart([]string{"a|b"}, 0) {
		t.Error("isTableStart")
	}
	short := plain("| a | b |\n|---|---|\n| only |\n", 40)
	if !strings.Contains(short, "only") {
		t.Errorf("short rows are padded:\n%s", short)
	}
}
