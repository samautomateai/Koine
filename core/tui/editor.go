package tui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Editor is the multi-line prompt editor: text, cursor, kill ring, undo stack and prompt history. It knows nothing about terminals; the
// view (editor_view.go) lays it out and the UI feeds it keys.
type Editor struct {
	lines    [][]rune
	Row, Col int // cursor, in runes
	want     int // display column Up and Down try to keep
	Width    int // wrap width used by Up and Down; set by the view

	ring      []string
	lastKind  string // what the previous edit was, for merging kills and typing into one undo step
	yankStart [2]int
	yankLen   int
	undo      []snapshot

	history []string
	histPos int // -1 when not browsing
	draft   string

	pastes     map[int]string
	pasteCount int
}

type snapshot struct {
	lines    []string
	row, col int
}

const (
	maxUndo   = 200
	maxRing   = 32
	pasteMax  = 1000 // a paste longer than this many characters, or ...
	pasteRows = 10   // ... with more lines than this, is shown as a marker and expanded when the prompt is sent
)

// NewEditor returns an empty editor.
func NewEditor() *Editor {
	e := &Editor{histPos: -1, Width: 80}
	e.lines = [][]rune{{}}
	return e
}

// Text is the content, lines joined by newlines, with paste markers as shown.
func (e *Editor) Text() string {
	parts := make([]string, len(e.lines))
	for i, l := range e.lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

var pasteMarker = regexp.MustCompile(`\[paste #(\d+) (?:\+\d+ lines|\d+ chars)\]`)

// Submitted is the content with paste markers replaced by what was pasted.
func (e *Editor) Submitted() string {
	return pasteMarker.ReplaceAllStringFunc(e.Text(), func(m string) string {
		var n int
		_, _ = fmt.Sscanf(m, "[paste #%d", &n)
		if p, ok := e.pastes[n]; ok {
			return p
		}
		return m
	})
}

// IsEmpty reports whether there is no text.
func (e *Editor) IsEmpty() bool { return len(e.lines) == 1 && len(e.lines[0]) == 0 }

// SetText replaces the content and puts the cursor at the end. It is not an undoable edit (the undo stack is cleared).
func (e *Editor) SetText(s string) {
	e.lines = nil
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		e.lines = append(e.lines, []rune(l))
	}
	e.Row = len(e.lines) - 1
	e.Col = len(e.lines[e.Row])
	e.want = -1
	e.undo, e.lastKind = nil, ""
}

// Clear empties the editor.
func (e *Editor) Clear() {
	e.SetText("")
	e.pastes = nil
	e.histPos = -1
}

func (e *Editor) snap() snapshot {
	s := snapshot{row: e.Row, col: e.Col}
	for _, l := range e.lines {
		s.lines = append(s.lines, string(l))
	}
	return s
}

// begin records an undo point before an edit of the given kind. Consecutive edits of the same "merging" kind (typing, kills) share one.
func (e *Editor) begin(kind string) {
	merge := kind != "" && kind == e.lastKind
	e.lastKind = kind
	e.want = -1
	if merge {
		return
	}
	e.undo = append(e.undo, e.snap())
	if len(e.undo) > maxUndo {
		e.undo = e.undo[1:]
	}
}

// Undo restores the state before the last edit step. It reports whether there was one.
func (e *Editor) Undo() bool {
	if len(e.undo) == 0 {
		return false
	}
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.lines = nil
	for _, l := range s.lines {
		e.lines = append(e.lines, []rune(l))
	}
	e.Row, e.Col, e.lastKind, e.want = s.row, s.col, "", -1
	return true
}

// Insert types text at the cursor. Newlines in it start new lines.
func (e *Editor) Insert(text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	kind := "type"
	if strings.ContainsAny(text, " \t\n") || len([]rune(text)) > 1 {
		kind = ""
	}
	e.begin(kind)
	e.insert(text)
}

func (e *Editor) insert(text string) {
	for i, part := range strings.Split(text, "\n") {
		if i > 0 {
			e.splitLine()
		}
		cur := e.lines[e.Row]
		r := []rune(part)
		e.lines[e.Row] = append(append(append([]rune{}, cur[:e.Col]...), r...), cur[e.Col:]...)
		e.Col += len(r)
	}
}

func (e *Editor) splitLine() {
	cur := e.lines[e.Row]
	tail := append([]rune{}, cur[e.Col:]...)
	e.lines[e.Row] = append([]rune{}, cur[:e.Col]...)
	e.lines = append(e.lines[:e.Row+1], append([][]rune{tail}, e.lines[e.Row+1:]...)...)
	e.Row++
	e.Col = 0
}

// Newline inserts a line break.
func (e *Editor) Newline() { e.begin(""); e.splitLine() }

// Paste inserts pasted text; a large paste becomes a "[paste #n +m lines]" marker that Submitted expands.
func (e *Editor) Paste(text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	n := strings.Count(text, "\n") + 1
	if n > pasteRows || len([]rune(text)) > pasteMax {
		e.pasteCount++
		if e.pastes == nil {
			e.pastes = map[int]string{}
		}
		e.pastes[e.pasteCount] = text
		if n > pasteRows {
			text = fmt.Sprintf("[paste #%d +%d lines]", e.pasteCount, n)
		} else {
			text = fmt.Sprintf("[paste #%d %d chars]", e.pasteCount, len([]rune(text)))
		}
	}
	e.begin("")
	e.insert(text)
}

// Backspace deletes the character before the cursor, joining lines at a line start.
func (e *Editor) Backspace() {
	if e.Row == 0 && e.Col == 0 {
		return
	}
	e.begin("")
	if e.Col > 0 {
		cur := e.lines[e.Row]
		e.lines[e.Row] = append(append([]rune{}, cur[:e.Col-1]...), cur[e.Col:]...)
		e.Col--
		return
	}
	e.joinWithPrevious()
}

func (e *Editor) joinWithPrevious() {
	prev := e.lines[e.Row-1]
	e.Col = len(prev)
	e.lines[e.Row-1] = append(append([]rune{}, prev...), e.lines[e.Row]...)
	e.lines = append(e.lines[:e.Row], e.lines[e.Row+1:]...)
	e.Row--
}

// DeleteForward deletes the character under the cursor, joining the next line at a line end.
func (e *Editor) DeleteForward() {
	cur := e.lines[e.Row]
	switch {
	case e.Col < len(cur):
		e.begin("")
		e.lines[e.Row] = append(append([]rune{}, cur[:e.Col]...), cur[e.Col+1:]...)
	case e.Row < len(e.lines)-1:
		e.begin("")
		e.Row++
		e.Col = 0
		e.joinWithPrevious()
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// wordLeft is the column of the start of the word before col: spaces are skipped, then one run of word characters or of punctuation.
func wordLeft(l []rune, col int) int {
	for col > 0 && unicode.IsSpace(l[col-1]) {
		col--
	}
	if col > 0 {
		w := isWordRune(l[col-1])
		for col > 0 && !unicode.IsSpace(l[col-1]) && isWordRune(l[col-1]) == w {
			col--
		}
	}
	return col
}

func wordRight(l []rune, col int) int {
	for col < len(l) && unicode.IsSpace(l[col]) {
		col++
	}
	if col < len(l) {
		w := isWordRune(l[col])
		for col < len(l) && !unicode.IsSpace(l[col]) && isWordRune(l[col]) == w {
			col++
		}
	}
	return col
}

// Left, Right, WordLeft, WordRight, LineStart and LineEnd move the cursor, crossing line boundaries where that makes sense.
func (e *Editor) Left() {
	e.moved()
	switch {
	case e.Col > 0:
		e.Col--
	case e.Row > 0:
		e.Row--
		e.Col = len(e.lines[e.Row])
	}
}

func (e *Editor) Right() {
	e.moved()
	switch {
	case e.Col < len(e.lines[e.Row]):
		e.Col++
	case e.Row < len(e.lines)-1:
		e.Row++
		e.Col = 0
	}
}

func (e *Editor) WordLeft() {
	e.moved()
	if e.Col == 0 && e.Row > 0 {
		e.Row--
		e.Col = len(e.lines[e.Row])
		return
	}
	e.Col = wordLeft(e.lines[e.Row], e.Col)
}

func (e *Editor) WordRight() {
	e.moved()
	if e.Col == len(e.lines[e.Row]) && e.Row < len(e.lines)-1 {
		e.Row++
		e.Col = 0
		return
	}
	e.Col = wordRight(e.lines[e.Row], e.Col)
}

func (e *Editor) LineStart() { e.moved(); e.Col = 0 }
func (e *Editor) LineEnd()   { e.moved(); e.Col = len(e.lines[e.Row]) }

// moved ends merging of typing and kills, as any cursor movement does.
func (e *Editor) moved() { e.lastKind, e.want = "", -1 }

// KillToEnd, KillToStart, KillWordBack and KillWordForward delete text into the kill ring. Consecutive kills in the same direction
// build up one ring entry. At a line boundary they delete the line break.
func (e *Editor) KillToEnd() {
	cur := e.lines[e.Row]
	if e.Col == len(cur) {
		if e.Row < len(e.lines)-1 {
			e.kill("\n", false, func() { e.Row++; e.Col = 0; e.joinWithPrevious() })
		}
		return
	}
	text := string(cur[e.Col:])
	e.kill(text, false, func() { e.lines[e.Row] = append([]rune{}, cur[:e.Col]...) })
}

func (e *Editor) KillToStart() {
	cur := e.lines[e.Row]
	if e.Col == 0 {
		if e.Row > 0 {
			e.kill("\n", true, e.Backspace)
		}
		return
	}
	text := string(cur[:e.Col])
	e.kill(text, true, func() { e.lines[e.Row] = append([]rune{}, cur[e.Col:]...); e.Col = 0 })
}

func (e *Editor) KillWordBack() {
	if e.Col == 0 {
		if e.Row > 0 {
			e.kill("\n", true, e.Backspace)
		}
		return
	}
	cur, to := e.lines[e.Row], wordLeft(e.lines[e.Row], e.Col)
	from := e.Col
	e.kill(string(cur[to:from]), true, func() { e.lines[e.Row] = append(append([]rune{}, cur[:to]...), cur[from:]...); e.Col = to })
}

func (e *Editor) KillWordForward() {
	cur := e.lines[e.Row]
	if e.Col == len(cur) {
		e.KillToEnd()
		return
	}
	from, to := e.Col, wordRight(cur, e.Col)
	e.kill(string(cur[from:to]), false, func() { e.lines[e.Row] = append(append([]rune{}, cur[:from]...), cur[to:]...) })
}

// kill removes text through do and records it. Several kills in a row merge: backward kills prepend, forward kills append.
func (e *Editor) kill(text string, backward bool, do func()) {
	merge := e.lastKind == "kill"
	if !merge {
		e.undo = append(e.undo, e.snap())
	}
	do()
	e.lastKind, e.want = "kill", -1
	switch {
	case merge && len(e.ring) > 0 && backward:
		e.ring[len(e.ring)-1] = text + e.ring[len(e.ring)-1]
	case merge && len(e.ring) > 0:
		e.ring[len(e.ring)-1] += text
	default:
		e.ring = append(e.ring, text)
		if len(e.ring) > maxRing {
			e.ring = e.ring[1:]
		}
	}
}

// Yank inserts the most recent kill at the cursor.
func (e *Editor) Yank() {
	if len(e.ring) == 0 {
		return
	}
	e.begin("")
	e.yankStart = [2]int{e.Row, e.Col}
	text := e.ring[len(e.ring)-1]
	e.insert(text)
	e.yankLen = len([]rune(text))
	e.lastKind = "yank"
}

// YankPop replaces the text just yanked with the previous kill ring entry, rotating the ring. It only acts straight after a yank.
func (e *Editor) YankPop() {
	if e.lastKind != "yank" || len(e.ring) < 2 {
		return
	}
	e.undo = append(e.undo, e.snap())
	// delete the yanked text: it starts at yankStart and the cursor is at its end
	e.Row, e.Col = e.yankStart[0], e.yankStart[1]
	for i := 0; i < e.yankLen; i++ {
		e.DeleteForward()
	}
	last := e.ring[len(e.ring)-1]
	e.ring = append([]string{last}, e.ring[:len(e.ring)-1]...)
	text := e.ring[len(e.ring)-1]
	e.insert(text)
	e.yankLen = len([]rune(text))
	e.lastKind = "yank"
}

// AddHistory remembers a sent prompt (consecutive duplicates are skipped) and ends history browsing.
func (e *Editor) AddHistory(s string) {
	e.histPos = -1
	if s = strings.TrimSpace(s); s != "" && (len(e.history) == 0 || e.history[len(e.history)-1] != s) {
		e.history = append(e.history, s)
	}
}

// SetHistory replaces the history (oldest first), for a resumed session.
func (e *Editor) SetHistory(h []string) { e.history, e.histPos = h, -1 }

// HistoryPrev shows the previous prompt, keeping what was being typed so HistoryNext can bring it back. It reports whether it moved.
func (e *Editor) HistoryPrev() bool {
	if len(e.history) == 0 || e.histPos == 0 {
		return false
	}
	if e.histPos < 0 {
		e.draft = e.Text()
		e.histPos = len(e.history)
	}
	e.histPos--
	e.SetText(e.history[e.histPos])
	return true
}

// HistoryNext moves towards newer prompts and finally back to the draft. It reports whether it moved.
func (e *Editor) HistoryNext() bool {
	if e.histPos < 0 {
		return false
	}
	e.histPos++
	if e.histPos >= len(e.history) {
		e.histPos = -1
		e.SetText(e.draft)
		return true
	}
	e.SetText(e.history[e.histPos])
	return true
}

// Browsing reports whether the editor is showing a history entry.
func (e *Editor) Browsing() bool { return e.histPos >= 0 }
