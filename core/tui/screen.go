package tui

import (
	"fmt"
	"strings"
)

// Screen turns "lines to add to the scrollback" plus "the live area at the bottom" into the bytes that update a terminal. Finished
// transcript lines are written once and left to scroll away; the live area (status, editor, popup, footer) is erased and drawn again on
// every frame. All of it is a pure function of its arguments and the previous frame's cursor row, so tests can check the exact output.
type Screen struct {
	prevRow int // row of the cursor inside the live area after the last frame
}

// Draw returns the output for one frame. print are finished lines to write above the live area. live is the live area, curRow/curCol the
// cursor inside it. full clears the screen and scrollback first (the caller then passes the whole transcript in print). The live area is
// cut from the top to fit in height-1 rows, which keeps the cursor arithmetic valid.
func (s *Screen) Draw(print, live []string, curRow, curCol int, showCursor, full bool, height int) string {
	if len(live) == 0 {
		live = []string{""}
	}
	if height > 1 && len(live) > height-1 {
		cut := len(live) - (height - 1)
		live = live[cut:]
		curRow = max(0, curRow-cut)
	}
	curRow = max(0, min(curRow, len(live)-1))
	var b strings.Builder
	b.WriteString("\x1b[?25l")
	switch {
	case full:
		b.WriteString("\x1b[3J\x1b[2J\x1b[H")
	default:
		if s.prevRow > 0 {
			fmt.Fprintf(&b, "\x1b[%dA", s.prevRow)
		}
		b.WriteString("\r\x1b[J")
	}
	for _, l := range print {
		b.WriteString(l + "\x1b[0m\r\n")
	}
	for i, l := range live {
		b.WriteString(l + "\x1b[0m")
		if i < len(live)-1 {
			b.WriteString("\r\n")
		}
	}
	if up := len(live) - 1 - curRow; up > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", up)
	}
	b.WriteString("\r")
	if curCol > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", curCol)
	}
	if showCursor {
		b.WriteString("\x1b[?25h")
	}
	s.prevRow = curRow
	return b.String()
}

// Clear returns the sequence that erases the live area and leaves the cursor at its top, for a clean exit or before running another
// program on the terminal.
func (s *Screen) Clear() string {
	out := ""
	if s.prevRow > 0 {
		out = fmt.Sprintf("\x1b[%dA", s.prevRow)
	}
	s.prevRow = 0
	return out + "\r\x1b[J\x1b[?25h"
}

// ProgressSeq is the OSC 9;4 sequence that shows (on) or clears an indeterminate progress indicator in the terminal's tab.
func ProgressSeq(on bool) string {
	if on {
		return "\x1b]9;4;3;\x1b\\"
	}
	return "\x1b]9;4;0;\x1b\\"
}

// Copy returns the OSC 52 sequence that puts text on the system clipboard (terminals that allow it).
func Copy(text string) string {
	return "\x1b]52;c;" + base64Std(text) + "\x1b\\"
}
