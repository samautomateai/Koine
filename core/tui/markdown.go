package tui

import (
	"regexp"
	"strings"
)

// MDOptions control Markdown rendering.
type MDOptions struct {
	Width      int
	Theme      *Theme
	CodeIndent string // prefix of code block lines (markdown.codeBlockIndent); default one space
	Hyperlinks bool   // emit OSC 8 links instead of "text (url)"
}

var (
	fenceRe   = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})\\s*([A-Za-z0-9_+#.-]*)")
	headingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	hrRe      = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	bulletRe  = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	orderedRe = regexp.MustCompile(`^(\s*)(\d{1,9})[.)]\s+(.*)$`)
	quoteRe   = regexp.MustCompile(`^\s{0,3}>\s?(.*)$`)
	taskRe    = regexp.MustCompile(`^\[([ xX])\]\s+`)
)

// Markdown renders CommonMark-ish text for the terminal: headings, paragraphs, lists (nested, ordered, task), block quotes, rules,
// tables, fenced code with simple syntax highlighting, and inline code, emphasis, strike-through and links. It is a hand-written
// renderer over the standard library, not a full CommonMark parser: HTML, reference links and setext headings are shown as written.
func Markdown(src string, o MDOptions) []string {
	if o.Width < 10 {
		o.Width = 10
	}
	if o.CodeIndent == "" {
		o.CodeIndent = " "
	}
	return renderBlocks(strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n"), o)
}

func renderBlocks(lines []string, o MDOptions) []string {
	th := o.Theme
	var out []string
	gap := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "":
			i++
		case fenceRe.MatchString(l):
			m := fenceRe.FindStringSubmatch(l)
			j := i + 1
			var code []string
			for j < len(lines) && !closesFence(lines[j], m[1]) {
				code = append(code, lines[j])
				j++
			}
			gap()
			out = append(out, codeBlock(m[2], code, o)...)
			i = min(j+1, len(lines))
		case headingRe.MatchString(l):
			m := headingRe.FindStringSubmatch(l)
			gap()
			text := inline(m[2], o)
			if len(m[1]) >= 3 {
				text = m[1] + " " + text
			}
			style := func(s string) string { return th.Fg("mdHeading", th.Bold(s)) }
			if len(m[1]) == 1 {
				style = func(s string) string { return th.Fg("mdHeading", th.Bold(th.Underline(s))) }
			}
			for _, w := range Wrap(text, o.Width) {
				out = append(out, style(w))
			}
			out = append(out, "")
			i++
		case hrRe.MatchString(l):
			gap()
			out = append(out, th.Fg("mdHr", strings.Repeat("─", o.Width)), "")
			i++
		case quoteRe.MatchString(l):
			var inner []string
			for i < len(lines) && quoteRe.MatchString(lines[i]) {
				inner = append(inner, quoteRe.FindStringSubmatch(lines[i])[1])
				i++
			}
			gap()
			qo := o
			qo.Width = o.Width - 2
			for _, q := range renderBlocks(inner, qo) {
				out = append(out, th.Fg("mdQuoteBorder", "│ ")+th.Fg("mdQuote", th.Italic(q)))
			}
			out = append(out, "")
		case isTableStart(lines, i):
			j := i + 2
			for j < len(lines) && strings.Contains(lines[j], "|") && strings.TrimSpace(lines[j]) != "" {
				j++
			}
			gap()
			out = append(out, table(lines[i:j], o)...)
			out = append(out, "")
			i = j
		case bulletRe.MatchString(l) || orderedRe.MatchString(l):
			j := i
			for j < len(lines) && (bulletRe.MatchString(lines[j]) || orderedRe.MatchString(lines[j]) || continuesItem(lines, j)) {
				j++
			}
			gap()
			out = append(out, list(lines[i:j], o)...)
			out = append(out, "")
			i = j
		default:
			j := i
			var para []string
			for j < len(lines) && strings.TrimSpace(lines[j]) != "" && (j == i || !startsBlock(lines, j)) {
				para = append(para, lines[j])
				j++
			}
			gap()
			out = append(out, paragraph(para, o)...)
			out = append(out, "")
			i = j
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func closesFence(l, open string) bool {
	t := strings.TrimSpace(l)
	return len(t) >= len(open) && strings.Trim(t, string(open[0])) == ""
}

func startsBlock(lines []string, i int) bool {
	l := lines[i]
	return fenceRe.MatchString(l) || headingRe.MatchString(l) || hrRe.MatchString(l) || quoteRe.MatchString(l) || bulletRe.MatchString(l) ||
		orderedRe.MatchString(l) || isTableStart(lines, i)
}

func continuesItem(lines []string, j int) bool {
	l := lines[j]
	return strings.TrimSpace(l) != "" && (strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "\t")) && !fenceRe.MatchString(l)
}

func paragraph(para []string, o MDOptions) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, Wrap(inline(strings.Join(cur, " "), o), o.Width)...)
			cur = nil
		}
	}
	for _, l := range para {
		hard := strings.HasSuffix(l, "  ") || strings.HasSuffix(l, "\\")
		cur = append(cur, strings.TrimSpace(strings.TrimSuffix(l, "\\")))
		if hard {
			flush()
		}
	}
	flush()
	return out
}

func list(lines []string, o MDOptions) []string {
	th := o.Theme
	type item struct {
		indent  int
		marker  string
		text    []string
		ordered bool
	}
	var items []item
	for _, l := range lines {
		if m := bulletRe.FindStringSubmatch(l); m != nil {
			items = append(items, item{indent: len(strings.ReplaceAll(m[1], "\t", "    ")), marker: "•", text: []string{m[3]}})
		} else if m := orderedRe.FindStringSubmatch(l); m != nil {
			items = append(items, item{indent: len(strings.ReplaceAll(m[1], "\t", "    ")), marker: m[2] + ".", text: []string{m[3]}, ordered: true})
		} else if len(items) > 0 {
			items[len(items)-1].text = append(items[len(items)-1].text, strings.TrimSpace(l))
		}
	}
	var out []string
	var levels []int // indents of the open nesting levels
	for _, it := range items {
		for len(levels) > 0 && it.indent < levels[len(levels)-1] {
			levels = levels[:len(levels)-1]
		}
		if len(levels) == 0 || it.indent > levels[len(levels)-1] {
			levels = append(levels, it.indent)
		}
		pad := strings.Repeat("  ", len(levels)-1)
		text := strings.Join(it.text, " ")
		marker := it.marker
		if m := taskRe.FindStringSubmatch(text); m != nil {
			text = text[len(m[0]):]
			marker = map[bool]string{true: "☐", false: "☑"}[m[1] == " "]
		}
		mw := Width(marker)
		first := pad + th.Fg("mdListBullet", marker) + " "
		rest := strings.Repeat(" ", Width(pad)+mw+1)
		for k, w := range Wrap(inline(text, o), max(1, o.Width-Width(rest))) {
			if k == 0 {
				out = append(out, first+w)
			} else {
				out = append(out, rest+w)
			}
		}
	}
	return out
}

func codeBlock(lang string, code []string, o MDOptions) []string {
	th := o.Theme
	out := []string{th.Fg("mdCodeBlockBorder", "```"+lang)}
	hl := Highlight(lang, code, th)
	room := max(1, o.Width-Width(o.CodeIndent))
	for _, l := range hl {
		for _, w := range hardWrap(l, room) {
			out = append(out, o.CodeIndent+th.Fg("mdCodeBlock", w))
		}
	}
	return append(out, th.Fg("mdCodeBlockBorder", "```"))
}

// hardWrap splits s into pieces of at most w cells without regard to spaces (code keeps its shape), carrying styles across the cut.
func hardWrap(s string, w int) []string {
	if Width(s) <= w {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	var st sgrState
	n := 0
	for _, t := range tokens(s) {
		if t.ansi {
			st.note(t.s)
			cur.WriteString(t.s)
			continue
		}
		if n+t.w > w {
			cur.WriteString(st.close())
			out = append(out, cur.String())
			cur.Reset()
			cur.WriteString(st.open())
			n = 0
		}
		cur.WriteString(t.s)
		n += t.w
	}
	return append(out, cur.String()+st.close())
}
