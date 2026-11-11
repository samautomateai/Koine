package tui

import (
	"regexp"
	"strings"
)

var delimRe = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)

func isTableStart(lines []string, i int) bool {
	if i+1 >= len(lines) || !strings.Contains(lines[i], "|") || !delimRe.MatchString(lines[i+1]) {
		return false
	}
	return len(splitRow(lines[i])) == len(splitRow(lines[i+1]))
}

// splitRow splits a table row into trimmed cells; "\|" stays inside a cell.
func splitRow(l string) []string {
	l = strings.TrimSpace(l)
	l = strings.TrimPrefix(l, "|")
	if strings.HasSuffix(l, "|") && !strings.HasSuffix(l, `\|`) {
		l = strings.TrimSuffix(l, "|")
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(l); i++ {
		if l[i] == '\\' && i+1 < len(l) && l[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if l[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(l[i])
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// table draws a GitHub-style table with box characters. Columns shrink (and cells wrap) to fit the width.
func table(lines []string, o MDOptions) []string {
	th := o.Theme
	head := splitRow(lines[0])
	n := len(head)
	align := make([]byte, n)
	for i, d := range splitRow(lines[1]) {
		switch {
		case strings.HasPrefix(d, ":") && strings.HasSuffix(d, ":"):
			align[i] = 'c'
		case strings.HasSuffix(d, ":"):
			align[i] = 'r'
		}
	}
	rows := [][]string{head}
	for _, l := range lines[2:] {
		r := splitRow(l)
		for len(r) < n {
			r = append(r, "")
		}
		rows = append(rows, r[:n])
	}
	rendered := make([][]string, len(rows))
	widths := make([]int, n)
	for i, r := range rows {
		rendered[i] = make([]string, n)
		for j, c := range r {
			rendered[i][j] = inline(c, o)
			widths[j] = max(widths[j], Width(rendered[i][j]), 1)
		}
	}
	for sum(widths)+3*n+1 > o.Width {
		w := 0
		for j := range widths {
			if widths[j] > widths[w] {
				w = j
			}
		}
		if widths[w] <= 3 {
			break
		}
		widths[w]--
	}
	bar := func(l, m, r string) string {
		parts := make([]string, n)
		for j, w := range widths {
			parts[j] = strings.Repeat("─", w+2)
		}
		return th.Fg("border", l+strings.Join(parts, m)+r)
	}
	v := th.Fg("border", "│")
	out := []string{bar("┌", "┬", "┐")}
	for i := range rows {
		cells := make([][]string, n)
		height := 1
		for j := range cells {
			cells[j] = Wrap(rendered[i][j], widths[j])
			height = max(height, len(cells[j]))
		}
		for k := 0; k < height; k++ {
			var b strings.Builder
			b.WriteString(v)
			for j := range cells {
				cell := ""
				if k < len(cells[j]) {
					cell = cells[j][k]
					if i == 0 {
						cell = th.Bold(cell)
					}
				}
				b.WriteString(" " + alignCell(cell, widths[j], align[j]) + " " + v)
			}
			out = append(out, b.String())
		}
		if i == 0 {
			out = append(out, bar("├", "┼", "┤"))
		}
	}
	return append(out, bar("└", "┴", "┘"))
}

func sum(v []int) int {
	t := 0
	for _, x := range v {
		t += x
	}
	return t
}

func alignCell(s string, w int, a byte) string {
	gap := max(0, w-Width(s))
	switch a {
	case 'r':
		return strings.Repeat(" ", gap) + s
	case 'c':
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	return s + strings.Repeat(" ", gap)
}
