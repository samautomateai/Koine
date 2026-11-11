package tui

import (
	"strconv"
	"strings"
)

// SelItem is one choice in a Selector.
type SelItem struct {
	Label, Desc string
	Mark        string // "✓" for the current value, "[x]" for multi-select state
	Value       string
}

// Selector is a filterable list that replaces the editor while it is open: model, thinking level, settings, sessions, scoped models and
// the questions the app asks through App.Choose. Typing filters (fuzzy), the select.* bindings move and confirm.
type Selector struct {
	Title, Hint string
	Items       []SelItem
	Filter      string
	Sel         int
	Max         int  // visible rows
	NoSearch    bool // typing does not filter (for short yes/no questions)
	Multi       bool // Enter toggles instead of confirming
	view        []int
}

// NewSelector builds a selector with all items visible.
func NewSelector(title string, items []SelItem) *Selector {
	s := &Selector{Title: title, Items: items, Max: 10}
	s.refilter()
	return s
}

func (s *Selector) refilter() {
	s.view = s.view[:0]
	type hit struct{ i, score int }
	var hits []hit
	for i, it := range s.Items {
		sc, ok := FuzzyScore(s.Filter, it.Label+" "+it.Desc)
		if ok {
			hits = append(hits, hit{i, sc})
		}
	}
	if s.Filter != "" { // best match first; with no filter the order is the caller's
		for i := 1; i < len(hits); i++ {
			for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
				hits[j], hits[j-1] = hits[j-1], hits[j]
			}
		}
	}
	for _, h := range hits {
		s.view = append(s.view, h.i)
	}
	s.Sel = max(0, min(s.Sel, len(s.view)-1))
}

// Move changes the selection by d rows (clamped).
func (s *Selector) Move(d int) {
	if len(s.view) == 0 {
		return
	}
	s.Sel = max(0, min(len(s.view)-1, s.Sel+d))
}

// Type adds text to the filter. It does nothing when searching is off.
func (s *Selector) Type(text string) {
	if s.NoSearch {
		return
	}
	s.Filter += text
	s.Sel = 0
	s.refilter()
}

// Backspace removes the last filter character.
func (s *Selector) Backspace() {
	if r := []rune(s.Filter); len(r) > 0 {
		s.Filter = string(r[:len(r)-1])
		s.Sel = 0
		s.refilter()
	}
}

// Current returns the highlighted item's index in Items.
func (s *Selector) Current() (int, bool) {
	if len(s.view) == 0 {
		return -1, false
	}
	return s.view[s.Sel], true
}

// View draws the selector.
func (s *Selector) View(width int, th *Theme) []string {
	out := []string{th.Fg("borderAccent", strings.Repeat("─", max(1, width))), " " + th.Fg("accent", th.Bold(s.Title))}
	if !s.NoSearch {
		f := s.Filter
		if f == "" {
			f = th.Fg("dim", "type to filter")
		}
		out = append(out, " "+f)
	}
	if len(s.view) == 0 {
		out = append(out, th.Fg("muted", "  no matches"))
	}
	first := max(0, min(s.Sel-s.Max/2, len(s.view)-s.Max))
	last := min(len(s.view), first+s.Max)
	lw := 0
	for _, i := range s.view[first:last] {
		lw = max(lw, Width(s.Items[i].Label))
	}
	for k := first; k < last; k++ {
		it := s.Items[s.view[k]]
		mark := "  "
		if it.Mark != "" {
			mark = it.Mark + " "
		}
		line := " " + mark + Pad(it.Label, lw)
		if it.Desc != "" {
			line += "  " + th.Fg("muted", it.Desc)
		}
		line = Truncate(line, width, "…")
		if k == s.Sel {
			line = th.Bg("selectedBg", Pad(line, width))
		}
		out = append(out, line)
	}
	if len(s.view) > s.Max {
		out = append(out, th.Fg("dim", " ("+strconv.Itoa(s.Sel+1)+"/"+strconv.Itoa(len(s.view))+")"))
	}
	if s.Hint != "" {
		out = append(out, th.Fg("dim", " "+Truncate(s.Hint, width-2, "…")))
	}
	return out
}
