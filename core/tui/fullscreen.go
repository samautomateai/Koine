package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// Terminal modes of fullscreen mode (REQ-TUI-002): the alternate screen buffer, and mouse reporting (press and release, SGR encoding) so the
// wheel arrives as keys. Mouse reporting takes selection away from the terminal; most terminals still select with Shift held.
const (
	FullscreenEnter = "\x1b[?1049h\x1b[?1000h\x1b[?1006h\x1b[2J\x1b[H"
	FullscreenLeave = "\x1b[?1000l\x1b[?1006l\x1b[?1049l"
	kittyDeleteAll  = "\x1b_Ga=d,d=A,q=2\x1b\\"
)

// Fullscreen is the transcript viewport of fullscreen mode: a scroll position over the rendered transcript, a layout that puts the editor
// and footer below it, and a painter that rewrites only the rows that changed. Like Screen it never touches a terminal.
type Fullscreen struct {
	off       int // lines the view is scrolled up from the end of the transcript (0 follows new output)
	top       bool
	lastTotal int
	liveStart int // row index where the live area starts
	liveCut   int // live lines cut off its top to leave room for the transcript
	maxOff    int

	prev   []string
	imgSig string
}

// FSOptions describe one layout. Scrollbar is "auto", "always" or "hidden"; Thumb and Track are the styled glyphs.
type FSOptions struct {
	Width, Height int
	Scrollbar     string
	Thumb, Track  string
}

// Scroll moves the view by n lines, up when n is positive. Scrolling down past the end follows the output again.
func (f *Fullscreen) Scroll(n int) {
	f.top = false
	f.off = max(0, f.off+n)
}

// Top scrolls to the beginning of the transcript; Bottom to the end, which follows new output.
func (f *Fullscreen) Top()    { f.top = true }
func (f *Fullscreen) Bottom() { f.top, f.off = false, 0 }

// Off is the number of lines the view is above the end; Following reports that the view sits at the end.
func (f *Fullscreen) Off() int        { return f.off }
func (f *Fullscreen) Following() bool { return f.off == 0 && !f.top }

// CursorRow turns a row inside the live area (as ui.live reports it) into a screen row.
func (f *Fullscreen) CursorRow(r int) int { return f.liveStart + max(0, r-f.liveCut) }

// ViewHeight is the number of rows the transcript has in the last layout.
func (f *Fullscreen) ViewHeight() int { return f.liveStart }

// PageSize is the number of lines a page scroll moves: the view less one line of overlap.
func (f *Fullscreen) PageSize() int { return max(1, f.liveStart-1) }

// Layout returns exactly o.Height rows: the visible part of content (top-aligned when it is short), then the live area. content is
// already wrapped to the width left of the scrollbar column.
func (f *Fullscreen) Layout(content, live []string, o FSOptions) []string {
	if len(live) == 0 {
		live = []string{""}
	}
	f.liveCut = 0
	if len(live) > o.Height-1 && o.Height > 1 {
		f.liveCut = len(live) - (o.Height - 1)
		live = live[f.liveCut:]
	}
	view := max(0, o.Height-len(live))
	total := len(content)
	f.maxOff = max(0, total-view)
	switch {
	case f.top:
		f.off = f.maxOff
	case f.off > 0 && total > f.lastTotal:
		f.off += total - f.lastTotal // keep what the reader is looking at where it is
	}
	f.off = min(f.off, f.maxOff)
	if f.top && f.off < f.maxOff {
		f.top = false
	}
	f.lastTotal = total
	end := total - f.off
	start := max(0, end-view)
	rows := make([]string, 0, o.Height)
	showBar := o.Scrollbar == "always" || o.Scrollbar == "auto" && total > view
	thumb, pos := 0, 0
	if showBar && view > 0 {
		thumb = view
		if total > view {
			thumb = max(1, view*view/total)
			pos = (start*(view-thumb) + f.maxOff/2) / f.maxOff
		}
	}
	for j := 0; j < view; j++ {
		line := ""
		if i := start + j; i < end {
			line = content[i]
			if n := imageRows(line); n > j+1 { // its top rows are above the view: drawing upward from here would land on other rows
				line = ""
			}
		}
		row := "\x1b[2K" + line + "\x1b[0m"
		if showBar {
			glyph := o.Track
			if j >= pos && j < pos+thumb {
				glyph = o.Thumb
			}
			row += fmt.Sprintf("\x1b[%dG", o.Width) + glyph
		}
		rows = append(rows, row)
	}
	f.liveStart = len(rows)
	for _, l := range live {
		rows = append(rows, "\x1b[2K"+l+"\x1b[0m")
	}
	return rows
}

// imageRows is the height of the image a line draws (see RenderImage), or 0 when the line is not an image's last line.
func imageRows(l string) int {
	i := strings.Index(l, "\x1b7")
	if i < 0 || !strings.Contains(l, "\x1b_Ga=T") && !strings.Contains(l, "\x1b]1337;File=") {
		return 0
	}
	rest := l[i+2:]
	if !strings.HasPrefix(rest, "\x1b[") {
		return 1
	}
	if k := strings.IndexByte(rest, 'A'); k > 2 {
		if n, err := strconv.Atoi(rest[2:k]); err == nil {
			return n + 1
		}
	}
	return 1
}

// Paint returns the bytes that bring the terminal from the previous call's rows to rows, and place the cursor at screen row curRow
// (0-based) and column curCol. force clears the screen first. When the set of kitty images on screen changes, all of them are deleted
// and every row is drawn again, because a placement stays where it was drawn while the text under it scrolls.
func (f *Fullscreen) Paint(rows []string, curRow, curCol int, show, force bool) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l")
	sig := imageSignature(rows)
	all := force || len(f.prev) != len(rows) || sig != f.imgSig
	if all && (strings.Contains(sig, "a=T") || strings.Contains(f.imgSig, "a=T")) {
		b.WriteString(kittyDeleteAll)
	}
	if force || len(f.prev) != len(rows) {
		b.WriteString("\x1b[2J")
	}
	for i, r := range rows {
		if all || f.prev[i] != r {
			fmt.Fprintf(&b, "\x1b[%d;1H%s", i+1, r)
		}
	}
	f.prev, f.imgSig = append(f.prev[:0], rows...), sig
	fmt.Fprintf(&b, "\x1b[%d;%dH", curRow+1, curCol+1)
	if show {
		b.WriteString("\x1b[?25h")
	}
	return b.String()
}

// imageSignature identifies the images on screen by row and content.
func imageSignature(rows []string) string {
	var b strings.Builder
	for i, r := range rows {
		if imageRows(r) > 0 {
			fmt.Fprintf(&b, "%d:%s;", i, r)
		}
	}
	return b.String()
}

// wheel turns mouse-wheel events into a number of lines. Pi's "auto" moves one line per event in local macOS terminals (which accelerate
// the wheel themselves) and speeds fast spins up to at most 6 lines elsewhere; a number is used as it is.
type wheel struct {
	last   int64
	streak int
}

func (w *wheel) step(lines int, localMac bool, nowNanos int64) int {
	fast := w.last != 0 && nowNanos-w.last < 80_000_000
	w.last = nowNanos
	if fast {
		w.streak++
	} else {
		w.streak = 0
	}
	switch {
	case lines > 0:
		return lines
	case localMac:
		return 1
	}
	return min(6, 1+w.streak/2)
}
