package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolCall is one tool invocation in the transcript: the arguments from tool_start and the result from tool_end.
type ToolCall struct {
	ID, Name string
	Args     json.RawMessage
	Result   string
	Done     bool
	IsError  bool
}

// ToolOptions say how a tool call is drawn.
type ToolOptions struct {
	Width    int
	Theme    *Theme
	Expanded bool
}

const (
	collapsedDiffLines = 14
	collapsedOutLines  = 8
)

func (t *ToolCall) args() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(t.Args, &m)
	return m
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}

// Summary is the one-line description after the tool name: the command for bash, the path for file tools, the pattern for searches.
func (t *ToolCall) Summary() string {
	m := t.args()
	switch t.Name {
	case "bash":
		if c := str(m, "command", "cmd"); c != "" {
			return "$ " + strings.Join(strings.Fields(c), " ")
		}
	case "read", "write", "edit":
		if p := str(m, "path", "file_path", "filePath"); p != "" {
			if n, ok := m["offset"].(float64); ok {
				p += fmt.Sprintf(":%d", int(n))
			}
			return p
		}
	case "grep", "find", "ls":
		return strings.TrimSpace(str(m, "pattern", "query") + " " + str(m, "path"))
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// editPairs extracts (old,new) replacements from the arguments of an edit-like tool.
func (t *ToolCall) editPairs() [][2]string {
	m := t.args()
	one := func(x map[string]any) (p [2]string, ok bool) {
		p[0] = str(x, "old", "oldText", "old_string", "oldString")
		p[1] = str(x, "new", "newText", "new_string", "newString")
		_, hasNew := x["new"]
		for _, k := range []string{"newText", "new_string", "newString"} {
			if _, h := x[k]; h {
				hasNew = true
			}
		}
		return p, p[0] != "" || (hasNew && p[1] != "")
	}
	var out [][2]string
	if list, ok := m["edits"].([]any); ok {
		for _, e := range list {
			if x, ok := e.(map[string]any); ok {
				if p, ok := one(x); ok {
					out = append(out, p)
				}
			}
		}
		return out
	}
	if p, ok := one(m); ok {
		out = append(out, p)
	}
	return out
}

// DiffLine is one line of a diff: Op is ' ', '+' or '-'.
type DiffLine struct {
	Op   byte
	Text string
}

// Diff compares two texts line by line (longest common subsequence) and returns every line with its operation.
func Diff(oldText, newText string) []DiffLine {
	a, b := strings.Split(oldText, "\n"), strings.Split(newText, "\n")
	if oldText == "" {
		a = nil
	}
	if newText == "" {
		b = nil
	}
	var out []DiffLine
	if len(a)*len(b) > 250000 {
		for _, l := range a {
			out = append(out, DiffLine{'-', l})
		}
		for _, l := range b {
			out = append(out, DiffLine{'+', l})
		}
		return out
	}
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, DiffLine{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, DiffLine{'-', a[i]})
			i++
		default:
			out = append(out, DiffLine{'+', b[j]})
			j++
		}
	}
	return out
}

// trimContext keeps changed lines and up to n unchanged lines around them, marking cut regions with an ellipsis line (Op 0).
func trimContext(d []DiffLine, n int) []DiffLine {
	keep := make([]bool, len(d))
	for i, l := range d {
		if l.Op == ' ' {
			continue
		}
		for k := max(0, i-n); k <= min(len(d)-1, i+n); k++ {
			keep[k] = true
		}
	}
	var out []DiffLine
	for i, l := range d {
		switch {
		case keep[i]:
			out = append(out, l)
		case len(out) == 0 || out[len(out)-1].Op != 0:
			out = append(out, DiffLine{0, "…"})
		}
	}
	return out
}

func diffStyled(d []DiffLine, th *Theme) []string {
	out := make([]string, 0, len(d))
	for _, l := range d {
		switch l.Op {
		case '+':
			out = append(out, th.Fg("toolDiffAdded", "+ "+l.Text))
		case '-':
			out = append(out, th.Fg("toolDiffRemoved", "- "+l.Text))
		case ' ':
			out = append(out, th.Fg("toolDiffContext", "  "+l.Text))
		default:
			out = append(out, th.Fg("dim", "  "+l.Text))
		}
	}
	return out
}

// looksLikeUnifiedDiff recognises tool output that already is a diff (edit tools may return one).
func looksLikeUnifiedDiff(s string) bool {
	return strings.Contains(s, "\n@@") || strings.HasPrefix(s, "@@") || strings.HasPrefix(s, "--- ") || strings.HasPrefix(s, "diff --git")
}

// body is the detail lines below the header, before collapsing.
func (t *ToolCall) body(th *Theme) (lines []string, tail bool) {
	if t.IsError {
		return styleLines(strings.Split(strings.TrimRight(t.Result, "\n"), "\n"), th, "error"), true
	}
	if t.Name == "edit" || len(t.editPairs()) > 0 {
		if looksLikeUnifiedDiff(t.Result) {
			return Highlight("diff", strings.Split(strings.TrimRight(t.Result, "\n"), "\n"), th), false
		}
		var all []DiffLine
		for i, p := range t.editPairs() {
			if i > 0 {
				all = append(all, DiffLine{0, "…"})
			}
			all = append(all, trimContext(Diff(p[0], p[1]), 2)...)
		}
		if len(all) > 0 {
			return diffStyled(all, th), false
		}
	}
	if t.Name == "write" {
		if c := str(t.args(), "content"); c != "" {
			return styleLines(strings.Split(strings.TrimRight(c, "\n"), "\n"), th, "toolOutput"), false
		}
	}
	if !t.Done || strings.TrimSpace(t.Result) == "" {
		return nil, false
	}
	return styleLines(strings.Split(strings.TrimRight(t.Result, "\n"), "\n"), th, "toolOutput"), t.Name == "bash"
}

func styleLines(l []string, th *Theme, tok string) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = th.Fg(tok, strings.ReplaceAll(s, "\t", "    "))
	}
	return out
}

// Render draws the call: a title line, then the detail (a diff for edits, the output otherwise), collapsed to a few lines unless
// Expanded. The block is painted with the pending, success or error background, one cell of padding on each side.
func (t *ToolCall) Render(o ToolOptions) []string {
	th := o.Theme
	inner := max(10, o.Width-2)
	bgTok := "toolPendingBg"
	switch {
	case t.IsError:
		bgTok = "toolErrorBg"
	case t.Done:
		bgTok = "toolSuccessBg"
	}
	head := th.Fg("toolTitle", th.Bold(t.Name))
	if s := t.Summary(); s != "" {
		head += " " + th.Fg("toolOutput", s)
	}
	lines := []string{Truncate(head, inner, "…")}
	body, tail := t.body(th)
	limit := collapsedOutLines
	if t.Name == "edit" {
		limit = collapsedDiffLines
	}
	var flat []string
	for _, l := range body {
		flat = append(flat, hardWrap(l, inner)...)
	}
	if !o.Expanded && len(flat) > limit {
		hidden := len(flat) - limit
		hint := th.Fg("dim", fmt.Sprintf("… %d more lines (ctrl+o to expand)", hidden))
		if tail { // keep the end of long command output, which is where results and errors are
			flat = append([]string{th.Fg("dim", fmt.Sprintf("… %d earlier lines (ctrl+o to expand)", hidden))}, flat[hidden:]...)
		} else {
			flat = append(flat[:limit:limit], hint)
		}
	}
	lines = append(lines, flat...)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = th.Bg(bgTok, " "+Pad(l, inner)+" ")
	}
	return out
}
