package tui

import "strings"

// seg is one visual row of the editor: runes [start,end) of logical line row.
type seg struct{ row, start, end int }

func cellWidth(r []rune) int {
	n := 0
	for _, c := range r {
		n += runeWidth(c)
	}
	return n
}

// contentWidth is the room for text inside the editor frame; one column is kept free for the cursor at the end of a full row.
func contentWidth(width, padX int) int { return max(1, width-2*padX-1) }

// segments wraps every logical line to cw cells, breaking after the last space that fits, or in the middle of a word that is too long.
func (e *Editor) segments(cw int) []seg {
	var out []seg
	for row, l := range e.lines {
		start := 0
		for {
			w, end := 0, start
			for end < len(l) && w+runeWidth(l[end]) <= cw {
				w += runeWidth(l[end])
				end++
			}
			if end >= len(l) {
				out = append(out, seg{row, start, len(l)})
				break
			}
			if end == start {
				end = start + 1
			}
			for i := end; i > start; i-- { // prefer to break after a space
				if l[i-1] == ' ' {
					end = i
					break
				}
			}
			out = append(out, seg{row, start, end})
			start = end
		}
	}
	return out
}

func (e *Editor) cursorSeg(segs []seg) int {
	for i, s := range segs {
		if s.row != e.Row {
			continue
		}
		last := s.end == len(e.lines[s.row])
		if e.Col >= s.start && (e.Col < s.end || (last && e.Col == s.end)) {
			return i
		}
	}
	return 0
}

// MoveRows moves the cursor n visual rows (negative: up), keeping its display column. It reports whether it moved at all.
func (e *Editor) MoveRows(n int) bool {
	cw := contentWidth(max(e.Width, 2), 0)
	segs := e.segments(cw)
	ci := e.cursorSeg(segs)
	ti := max(0, min(len(segs)-1, ci+n))
	if ti == ci {
		return false
	}
	if e.want < 0 {
		s := segs[ci]
		e.want = cellWidth(e.lines[s.row][s.start:e.Col])
	}
	t := segs[ti]
	line := e.lines[t.row]
	col, w := t.start, 0
	for col < t.end && w+runeWidth(line[col]) <= e.want {
		w += runeWidth(line[col])
		col++
	}
	if col >= t.end && t.end < len(line) {
		col = t.end - 1
	}
	e.Row, e.Col = t.row, max(col, t.start)
	e.lastKind = ""
	return true
}

// ViewOptions say how the editor is drawn.
type ViewOptions struct {
	Width, PadX    int
	Theme          *Theme
	BorderToken    string // colour token of the frame; Pi colours it by thinking level
	HardwareCursor bool   // leave the cursor to the terminal instead of drawing a block
	Placeholder    string
}

// View draws the editor as a frame with the text inside and returns the lines plus where the cursor sits inside them (row, column).
func (e *Editor) View(o ViewOptions) (lines []string, curRow, curCol int) {
	o.PadX = max(0, min(3, o.PadX))
	cw := contentWidth(o.Width, o.PadX)
	e.Width = o.Width - 2*o.PadX
	th := o.Theme
	border := func() string { return th.Fg(o.BorderToken, strings.Repeat("─", max(1, o.Width))) }
	lines = append(lines, border())
	segs := e.segments(cw)
	ci := e.cursorSeg(segs)
	pad := strings.Repeat(" ", o.PadX)
	for i, s := range segs {
		l := e.lines[s.row]
		text := string(l[s.start:s.end])
		if i == ci {
			before := string(l[s.start:e.Col])
			curRow, curCol = len(lines), o.PadX+Width(before)
			if !o.HardwareCursor {
				at, after := " ", ""
				if e.Col < s.end {
					at, after = string(l[e.Col]), string(l[e.Col+1:s.end])
				}
				text = before + th.Inverse(at) + after
			}
		}
		if e.IsEmpty() && o.Placeholder != "" && i == 0 {
			if o.HardwareCursor {
				text = th.Fg("dim", Truncate(o.Placeholder, cw, "…"))
			} else {
				text = th.Inverse(" ") + th.Fg("dim", Truncate(o.Placeholder, cw-1, "…"))
			}
		}
		lines = append(lines, pad+text)
	}
	return append(lines, border()), curRow, curCol
}
