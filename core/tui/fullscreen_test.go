package tui

import (
	"fmt"
	"strings"
	"testing"
)

func numbered(n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("line %02d", i))
	}
	return out
}

// plainRows drops positioning, styles and the scrollbar glyphs so a test can read what each row says.
func plainRows(rows []string) []string {
	var out []string
	for _, r := range rows {
		r = StripANSI(strings.ReplaceAll(r, "\x1b[2K", ""))
		r = strings.TrimRight(strings.Map(func(c rune) rune {
			if c == '┃' || c == '│' {
				return -1
			}
			return c
		}, r), " ")
		out = append(out, r)
	}
	return out
}

func layout(f *Fullscreen, content []string, live []string, w, h int, bar string) []string {
	return f.Layout(content, live, FSOptions{Width: w, Height: h, Scrollbar: bar, Thumb: "┃", Track: "│"})
}

func TestFullscreenFollowsTheTailAndScrolls(t *testing.T) {
	f := &Fullscreen{}
	rows := plainRows(layout(f, numbered(10), []string{"> editor", "footer"}, 20, 8, "auto"))
	want := []string{"line 05", "line 06", "line 07", "line 08", "line 09", "line 10", "> editor", "footer"}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("tail:\n%q", rows)
	}
	f.Scroll(2)
	if rows = plainRows(layout(f, numbered(10), []string{"> editor", "footer"}, 20, 8, "auto")); rows[0] != "line 03" || rows[5] != "line 08" {
		t.Errorf("scrolled two lines up: %q", rows)
	}
	f.Scroll(100) // past the top is clamped
	if rows = plainRows(layout(f, numbered(10), []string{"> editor", "footer"}, 20, 8, "auto")); rows[0] != "line 01" || f.Off() != 4 {
		t.Errorf("top: %q off=%d", rows, f.Off())
	}
	f.Scroll(-1)
	if f.Off() != 3 {
		t.Errorf("down one: %d", f.Off())
	}
	f.Bottom()
	if layout(f, numbered(10), nil, 20, 8, "auto"); f.Off() != 0 {
		t.Errorf("bottom: %d", f.Off())
	}
	f.Top()
	layout(f, numbered(10), nil, 20, 8, "auto")
	if f.Off() != 2 || f.Following() {
		t.Errorf("top again: off=%d following=%v", f.Off(), f.Following())
	}
	f.Scroll(-50)
	if f.Off() != 0 || !f.Following() {
		t.Errorf("scrolling back past the end follows again: %d", f.Off())
	}
}

func TestFullscreenKeepsPositionWhenOutputGrows(t *testing.T) {
	f := &Fullscreen{}
	layout(f, numbered(10), []string{"ed"}, 20, 6, "auto")
	f.Scroll(3)
	before := plainRows(layout(f, numbered(10), []string{"ed"}, 20, 6, "auto"))
	after := plainRows(layout(f, numbered(14), []string{"ed"}, 20, 6, "auto"))
	if strings.Join(before, "|") != strings.Join(after, "|") {
		t.Errorf("what the reader sees moved:\n%q\n%q", before, after)
	}
	f.Bottom()
	if rows := plainRows(layout(f, numbered(20), []string{"ed"}, 20, 6, "auto")); rows[4] != "line 20" {
		t.Errorf("a followed tail moves with new output: %q", rows)
	}
}

func TestFullscreenShortTranscriptIsTopAligned(t *testing.T) {
	rows := plainRows(layout(&Fullscreen{}, numbered(2), []string{"ed"}, 20, 6, "auto"))
	if strings.Join(rows, "|") != "line 01|line 02||||ed" {
		t.Errorf("rows: %q", rows)
	}
	if rows = plainRows(layout(&Fullscreen{}, nil, []string{"a", "b", "c", "d"}, 20, 3, "auto")); strings.Join(rows, "|") != "|c|d" {
		t.Errorf("a tall live area is cut from the top, leaving one transcript row: %q", rows)
	}
}

func TestFullscreenScrollbar(t *testing.T) {
	bars := func(f *Fullscreen, content []string, mode string) string {
		var b strings.Builder
		for _, r := range layout(f, content, []string{"ed"}, 20, 7, mode)[:6] {
			switch {
			case strings.Contains(r, "┃"):
				b.WriteString("#")
			case strings.Contains(r, "│"):
				b.WriteString(".")
			default:
				b.WriteString(" ")
			}
		}
		return b.String()
	}
	f := &Fullscreen{}
	if got := bars(f, numbered(12), "auto"); got != "...###" { // 12 lines in a 6-row view: the thumb is half the track, at the bottom
		t.Errorf("auto, following: %q", got)
	}
	f.Top()
	if got := bars(f, numbered(12), "auto"); got != "###..." {
		t.Errorf("at the top: %q", got)
	}
	if got := bars(&Fullscreen{}, numbered(3), "auto"); got != "      " {
		t.Errorf("auto hides the bar when everything fits: %q", got)
	}
	if got := bars(&Fullscreen{}, numbered(3), "always"); got != "######" {
		t.Errorf("always shows a full thumb: %q", got)
	}
	if got := bars(&Fullscreen{}, numbered(30), "hidden"); got != "      " {
		t.Errorf("hidden: %q", got)
	}
	// the bar sits in the last column, set with an absolute column move so image rows and wide text cannot push it
	if r := layout(&Fullscreen{}, numbered(30), nil, 20, 5, "always")[0]; !strings.Contains(r, "\x1b[20G") {
		t.Errorf("bar column: %q", r)
	}
}

func TestFullscreenPaintDrawsOnlyWhatChanged(t *testing.T) {
	f := &Fullscreen{}
	rows := layout(f, numbered(4), []string{"ed"}, 20, 5, "hidden")
	first := f.Paint(rows, 4, 2, true, false)
	if !strings.HasPrefix(first, "\x1b[?25l") || !strings.Contains(first, "\x1b[1;1H") || !strings.Contains(first, "line 04") ||
		!strings.HasSuffix(first, "\x1b[5;3H\x1b[?25h") {
		t.Errorf("first paint: %q", first)
	}
	if again := f.Paint(rows, 4, 2, false, false); strings.Contains(again, "line 0") || !strings.HasSuffix(again, "\x1b[5;3H") {
		t.Errorf("an unchanged frame repaints nothing: %q", again)
	}
	rows2 := append([]string(nil), rows...)
	rows2[1] = "changed"
	if p := f.Paint(rows2, 4, 2, false, false); !strings.Contains(p, "\x1b[2;1H") || !strings.Contains(p, "changed") || strings.Contains(p, "line 01") {
		t.Errorf("only row 2 repaints: %q", p)
	}
	if p := f.Paint(rows2, 4, 2, false, true); !strings.Contains(p, "line 01") || !strings.Contains(p, "\x1b[2J") {
		t.Errorf("forced paint clears and draws everything: %q", p)
	}
}

func TestFullscreenPaintClearsKittyImagesWhenTheyMove(t *testing.T) {
	img := RenderImage(pngData(t, 80, 32), ImageOptions{Proto: "kitty", Cells: 10, Avail: 40})
	f := &Fullscreen{}
	content := append(numbered(6), img...)
	rows := layout(f, content, []string{"ed"}, 40, 8, "hidden")
	if p := f.Paint(rows, 7, 0, false, false); !strings.Contains(p, "\x1b_Ga=d,d=A,q=2\x1b\\") || !strings.Contains(p, "a=T") {
		t.Errorf("first paint deletes stale images, then draws: %q", p[:min(len(p), 120)])
	}
	if p := f.Paint(rows, 7, 0, false, false); strings.Contains(p, "a=T") || strings.Contains(p, "a=d") {
		t.Errorf("an unchanged image is not sent again")
	}
	f.Scroll(1)
	rows = layout(f, content, []string{"ed"}, 40, 8, "hidden")
	if p := f.Paint(rows, 7, 0, false, false); !strings.Contains(p, "a=d") {
		t.Errorf("a moved image deletes the old placement: %q", p[:min(len(p), 80)])
	}
}

func TestFullscreenDoesNotDrawAnImageCutOffAtTheTop(t *testing.T) {
	img := RenderImage(pngData(t, 80, 64), ImageOptions{Proto: "iterm2", Cells: 10, Avail: 40}) // 4 rows
	if len(img) != 4 {
		t.Fatalf("rows: %d", len(img))
	}
	content := append(append([]string{}, img...), numbered(3)...)
	f := &Fullscreen{}
	f.Top()
	if rows := layout(f, content, []string{"ed"}, 40, 5, "hidden"); !strings.Contains(strings.Join(rows[:4], ""), "1337;File=") {
		t.Errorf("a whole image is drawn")
	}
	f = &Fullscreen{}
	f.Scroll(1) // the first image rows are above the window: only part of it would show
	rows := layout(f, content, []string{"ed"}, 40, 5, "hidden")
	if strings.Contains(strings.Join(rows, ""), "1337;File=") {
		t.Errorf("a clipped image must not be drawn:\n%q", rows)
	}
}

func TestMouseWheelKeys(t *testing.T) {
	keys, used := Decode([]byte("\x1b[<64;10;5M\x1b[<65;10;5M\x1b[<72;1;1M\x1b[<73;1;1M\x1b[<0;3;3M\x1b[<0;3;3mx"), false)
	var names []string
	for _, k := range keys {
		names = append(names, k.Name)
	}
	if got := strings.Join(names, ","); got != "wheelUp,wheelDown,alt+wheelUp,alt+wheelDown,x" || used == 0 {
		t.Errorf("keys: %s (used %d)", got, used)
	}
	if _, n := Decode([]byte("\x1b[<64;1"), false); n != 0 {
		t.Errorf("an incomplete mouse report waits for the rest")
	}
}

func TestWheelStep(t *testing.T) {
	w := &wheel{}
	t0 := int64(1_000_000_000)
	if got := w.step(0, false, t0); got != 1 {
		t.Errorf("first event: %d", got)
	}
	for i, want := range []int{1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 6} { // fast spins speed up to at most 6 lines
		t0 += 10_000_000
		if got := w.step(0, false, t0); got != want {
			t.Errorf("fast event %d: got %d, want %d", i, got, want)
		}
	}
	if got := w.step(0, false, t0+int64(500e6)); got != 1 {
		t.Errorf("after a pause: %d", got)
	}
	if got := (&wheel{}).step(0, true, t0); got != 1 {
		t.Errorf("local macOS terminals already accelerate: %d", got)
	}
	m := &wheel{}
	m.step(0, true, t0)
	if got := m.step(0, true, t0+1); got != 1 {
		t.Errorf("macOS stays at one line: %d", got)
	}
	if got := (&wheel{}).step(7, false, t0); got != 7 {
		t.Errorf("fixed: %d", got)
	}
}
