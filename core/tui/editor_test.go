package tui

import (
	"strings"
	"testing"
)

func typed(s string) *Editor {
	e := NewEditor()
	e.Width = 40
	for _, r := range s {
		e.Insert(string(r))
	}
	return e
}

func TestEditorTypingNewlinesAndBackspace(t *testing.T) {
	e := typed("hello")
	e.Newline()
	e.Insert("wörld")
	if e.Text() != "hello\nwörld" || e.Row != 1 || e.Col != 5 {
		t.Fatalf("%q %d %d", e.Text(), e.Row, e.Col)
	}
	e.Backspace()
	if e.Text() != "hello\nwörl" {
		t.Errorf("backspace deletes a rune: %q", e.Text())
	}
	e.LineStart()
	e.Backspace()
	if e.Text() != "hellowörl" || e.Row != 0 || e.Col != 5 {
		t.Errorf("backspace at a line start joins: %q %d %d", e.Text(), e.Row, e.Col)
	}
	e.DeleteForward()
	if e.Text() != "helloörl" {
		t.Errorf("delete: %q", e.Text())
	}
	e.LineEnd()
	e.Newline()
	e.Left()
	e.DeleteForward()
	if e.Text() != "helloörl" {
		t.Errorf("delete at a line end joins the next line: %q", e.Text())
	}
	e.Insert("a\nb\r\nc")
	if strings.Count(e.Text(), "\n") != 2 {
		t.Errorf("multi-line insert: %q", e.Text())
	}
	NewEditor().Backspace()
	NewEditor().DeleteForward()
}

func TestEditorMovement(t *testing.T) {
	e := typed("one two  three")
	e.WordLeft()
	if e.Col != 9 {
		t.Errorf("word left: %d", e.Col)
	}
	e.WordLeft()
	e.WordLeft()
	if e.Col != 0 {
		t.Errorf("word left to the start: %d", e.Col)
	}
	e.WordRight()
	if e.Col != 3 {
		t.Errorf("word right: %d", e.Col)
	}
	e.WordRight()
	e.WordRight()
	if e.Col != 14 {
		t.Errorf("word right to the end: %d", e.Col)
	}
	e.Left()
	e.Right()
	e.Right()
	if e.Col != 14 {
		t.Errorf("left/right: %d", e.Col)
	}
	p := typed("foo.bar(baz)")
	p.WordLeft()
	if p.Col != 11 {
		t.Errorf("punctuation is its own word: %d", p.Col)
	}
	m := typed("ab")
	m.Newline()
	m.Insert("cd")
	m.LineStart()
	m.Left()
	if m.Row != 0 || m.Col != 2 {
		t.Errorf("left wraps to the previous line: %d %d", m.Row, m.Col)
	}
	m.Right()
	if m.Row != 1 || m.Col != 0 {
		t.Errorf("right wraps to the next line: %d %d", m.Row, m.Col)
	}
	m.WordLeft()
	if m.Row != 0 || m.Col != 2 {
		t.Errorf("word left crosses lines: %d %d", m.Row, m.Col)
	}
	m.WordRight()
	if m.Row != 1 || m.Col != 0 {
		t.Errorf("word right crosses lines: %d %d", m.Row, m.Col)
	}
}

func TestEditorVerticalMovementAndWrapping(t *testing.T) {
	e := NewEditor()
	e.Width = 11 // content width 10
	e.SetText("aaaa bbbb cccc dddd\nxy")
	if segs := e.segments(10); len(segs) != 3 || segs[0].end != 10 || segs[1].start != 10 {
		t.Fatalf("segments %v", segs)
	}
	e.Row, e.Col = 1, 2
	if !e.MoveRows(-1) || e.Row != 0 || e.Col != 12 {
		t.Errorf("up keeps the column (second visual row of line 0 starts at 10): %d %d", e.Row, e.Col)
	}
	if !e.MoveRows(-1) || e.Col != 2 {
		t.Errorf("up again: %d %d", e.Row, e.Col)
	}
	e.Row, e.Col = 0, 12
	e.MoveRows(1)
	if e.Row != 1 {
		t.Errorf("down into the next logical line: %d %d", e.Row, e.Col)
	}
	if e.MoveRows(1) {
		t.Error("cannot go below the last row")
	}
	e.Row, e.Col = 0, 0
	if e.MoveRows(-1) {
		t.Error("cannot go above the first row")
	}
	e.MoveRows(5)
	if e.Row != 1 {
		t.Errorf("page down clamps: %d", e.Row)
	}
	w := NewEditor()
	w.Width = 6
	w.SetText("日本語日本語")
	if len(w.segments(5)) != 3 {
		t.Errorf("wide runes: %v", w.segments(5))
	}
	long := NewEditor()
	long.SetText("abcdefghijklmnopqrstuvwxyz")
	if len(long.segments(10)) != 3 {
		t.Error("words longer than the width are split")
	}
	if s := long.segments(0); len(s) < 20 {
		t.Error("zero width still progresses")
	}
}

func TestEditorKillRingYankAndUndo(t *testing.T) {
	e := typed("alpha beta gamma")
	e.KillWordBack()
	if e.Text() != "alpha beta " {
		t.Fatalf("kill word back: %q", e.Text())
	}
	e.KillWordBack()
	if e.Text() != "alpha " {
		t.Fatalf("kill word back again: %q", e.Text())
	}
	e.Yank()
	if e.Text() != "alpha beta gamma" {
		t.Errorf("consecutive backward kills merge into one entry, in order: %q", e.Text())
	}
	e.SetText("one two")
	e.LineStart()
	e.KillWordForward()
	e.KillWordForward()
	e.Yank()
	if e.Text() != "one two" {
		t.Errorf("forward kills append: %q", e.Text())
	}
	e.SetText("keep this")
	e.LineStart()
	e.WordRight()
	e.KillToEnd()
	if e.Text() != "keep" {
		t.Errorf("kill to end: %q", e.Text())
	}
	e.LineStart()
	e.KillToEnd()
	e.Yank()
	if e.Text() != "keep" {
		t.Errorf("kill whole line then yank: %q", e.Text())
	}
	e.KillToStart()
	if e.Text() != "" {
		t.Errorf("kill to start: %q", e.Text())
	}
	e.Yank()
	e.Insert(" x")
	e.KillToStart()
	e.Yank()
	// yank-pop replaces the last yank with the previous ring entry
	e.SetText("")
	e.ring = []string{"first", "second"}
	e.Yank()
	if e.Text() != "second" {
		t.Fatalf("yank: %q", e.Text())
	}
	e.YankPop()
	if e.Text() != "first" {
		t.Errorf("yank-pop: %q", e.Text())
	}
	e.YankPop()
	if e.Text() != "second" {
		t.Errorf("yank-pop rotates: %q", e.Text())
	}
	e.Insert("!")
	e.YankPop() // only right after a yank
	if e.Text() != "second!" {
		t.Errorf("yank-pop elsewhere does nothing: %q", e.Text())
	}
	NewEditor().Yank()

	n := typed("ab")
	n.Newline()
	n.Insert("cd")
	n.LineStart()
	n.KillToStart() // at a line start the line break goes
	if n.Text() != "abcd" {
		t.Errorf("kill to start at a line start: %q", n.Text())
	}
	n.LineEnd()
	n.KillToEnd()
	n.Newline()
	n.Insert("zz")
	n.KillWordBack()
	n.KillWordBack()
	if n.Text() != "abcd" {
		t.Errorf("kill word back over a line break: %q", n.Text())
	}
	j := typed("a")
	j.Newline()
	j.Left()
	j.KillToEnd()
	if j.Text() != "a" {
		t.Errorf("kill to end at a line end joins: %q", j.Text())
	}
	j.KillWordForward()
}

func TestEditorUndo(t *testing.T) {
	e := typed("hello world")
	e.Insert(" ")
	e.Insert("again")
	if e.Text() != "hello world again" {
		t.Fatal(e.Text())
	}
	steps := 0
	for e.Undo() {
		steps++
	}
	if e.Text() != "" || steps < 2 || steps > 6 {
		t.Errorf("undo should remove typing in word-sized steps and stop at empty: %q after %d steps", e.Text(), steps)
	}
	e.SetText("keep")
	e.KillToStart()
	if !e.Undo() || e.Text() != "keep" {
		t.Errorf("undo a kill: %q", e.Text())
	}
	e.Newline()
	e.Undo()
	if e.Text() != "keep" {
		t.Errorf("undo a newline: %q", e.Text())
	}
	big := NewEditor()
	for i := 0; i < maxUndo+50; i++ {
		big.Insert("a b")
	}
	if len(big.undo) > maxUndo {
		t.Error("undo stack is bounded")
	}
}

func TestEditorPasteMarkers(t *testing.T) {
	e := NewEditor()
	e.Paste("short")
	if e.Text() != "short" {
		t.Errorf("small pastes are inserted: %q", e.Text())
	}
	lines := strings.Repeat("line\n", 14) + "end"
	e.Paste(lines)
	if !strings.Contains(e.Text(), "[paste #1 +15 lines]") {
		t.Errorf("many lines collapse: %q", e.Text())
	}
	e.Paste(strings.Repeat("x", 1500))
	if !strings.Contains(e.Text(), "[paste #2 1500 chars]") {
		t.Errorf("long pastes collapse: %q", e.Text())
	}
	got := e.Submitted()
	if !strings.Contains(got, lines) || !strings.Contains(got, strings.Repeat("x", 1500)) || strings.Contains(got, "[paste") {
		t.Errorf("markers expand on submit")
	}
	e.SetText("[paste #9 +3 lines]")
	if e.Submitted() != "[paste #9 +3 lines]" {
		t.Error("an unknown marker is left alone")
	}
	e.Paste("a\r\nb")
	if strings.Contains(e.Text(), "\r") {
		t.Error("CRLF is normalised")
	}
	e.Clear()
	if !e.IsEmpty() || e.pastes != nil {
		t.Error("Clear")
	}
}

func TestEditorHistory(t *testing.T) {
	e := NewEditor()
	if e.HistoryPrev() || e.HistoryNext() {
		t.Error("no history yet")
	}
	e.AddHistory("first")
	e.AddHistory("  ")
	e.AddHistory("second")
	e.AddHistory("second")
	e.Insert("draft")
	if !e.HistoryPrev() || e.Text() != "second" || !e.Browsing() {
		t.Fatalf("prev: %q", e.Text())
	}
	e.HistoryPrev()
	if e.Text() != "first" {
		t.Errorf("older: %q", e.Text())
	}
	if e.HistoryPrev() {
		t.Error("cannot go past the oldest")
	}
	e.HistoryNext()
	if e.Text() != "second" {
		t.Errorf("newer: %q", e.Text())
	}
	e.HistoryNext()
	if e.Text() != "draft" || e.Browsing() {
		t.Errorf("past the newest brings the draft back: %q", e.Text())
	}
	e.SetHistory([]string{"a"})
	if !e.HistoryPrev() || e.Text() != "a" {
		t.Error("SetHistory")
	}
}

func TestEditorViewDrawsFrameAndCursor(t *testing.T) {
	e := typed("hello")
	lines, r, c := e.View(ViewOptions{Width: 20, PadX: 1})
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "─") || lines[1] != " hello " || r != 1 || c != 6 {
		t.Errorf("%q row %d col %d", lines, r, c)
	}
	th := Builtin("dark")
	lines, _, _ = e.View(ViewOptions{Width: 20, Theme: th, BorderToken: "thinkingHigh"})
	if !strings.Contains(lines[1], "\x1b[7m \x1b[27m") {
		t.Errorf("the cursor is drawn as an inverse block: %q", lines[1])
	}
	lines, _, c = e.View(ViewOptions{Width: 20, Theme: th, HardwareCursor: true})
	if strings.Contains(lines[1], "\x1b[7m") || c != 5 {
		t.Errorf("a hardware cursor is not drawn: %q %d", lines[1], c)
	}
	e.Left()
	lines, _, _ = e.View(ViewOptions{Width: 20, Theme: th})
	if !strings.Contains(lines[1], "\x1b[7mo\x1b[27m") {
		t.Errorf("cursor on a character: %q", lines[1])
	}
	empty := NewEditor()
	lines, _, _ = empty.View(ViewOptions{Width: 30, Placeholder: "Type a message"})
	if !strings.Contains(StripANSI(lines[1]), "Type a message") {
		t.Errorf("placeholder: %q", lines[1])
	}
	lines, _, _ = empty.View(ViewOptions{Width: 30, Placeholder: "Type", HardwareCursor: true})
	if StripANSI(lines[1]) != "Type" {
		t.Errorf("placeholder with a hardware cursor: %q", lines[1])
	}
	wrapped := typed(strings.Repeat("word ", 10))
	lines, _, _ = wrapped.View(ViewOptions{Width: 22, PadX: 9})
	if len(lines) < 4 {
		t.Errorf("padding is clamped to 3 and long text wraps: %d lines", len(lines))
	}
}
