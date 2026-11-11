package tui

import (
	"strconv"
	"strings"

	"koine/core/session"
)

// TreeSource is what the navigator needs from a session.
type TreeSource interface {
	Rows(mode string) []session.Row
	Entry(id string) (session.Entry, bool)
}

// TreeNav is the state of the interactive /tree navigator: the rows under a filter, which are folded, the selection and a search text.
type TreeNav struct {
	src        TreeSource
	Mode       string
	Sel        int // index into Visible
	Search     string
	Timestamps bool // show each labelled entry's timestamp
	Max        int
	rows       []session.Row
	parent     map[string]string // entry id -> nearest ancestor that is in rows
	kids       map[string]int
	folded     map[string]bool
	vis        []int
}

// NewTreeNav opens the navigator on src with a filter mode, the selection on the current position.
func NewTreeNav(src TreeSource, mode string) *TreeNav {
	t := &TreeNav{src: src, folded: map[string]bool{}, Max: 12}
	t.SetMode(mode, "")
	return t
}

// SetMode switches the filter. The selection stays on the same entry if it is still listed, else it goes to want (an entry id), else to
// the current position.
func (t *TreeNav) SetMode(mode, want string) {
	if cur := t.CurrentID(); want == "" {
		want = cur
	}
	t.Mode = mode
	t.rows = t.src.Rows(mode)
	t.parent, t.kids = map[string]string{}, map[string]int{}
	listed := map[string]bool{}
	for _, r := range t.rows {
		listed[r.Entry.ID] = true
	}
	for _, r := range t.rows {
		for p := r.Entry.ParentID; p != nil; {
			if listed[*p] {
				t.parent[r.Entry.ID] = *p
				t.kids[*p]++
				break
			}
			e, ok := t.src.Entry(*p)
			if !ok {
				break
			}
			p = e.ParentID
		}
	}
	t.vis = nil
	t.refresh(want)
	if t.CurrentID() != want {
		for i, r := range t.rows {
			if r.IsLeaf {
				t.selectID(t.rows[i].Entry.ID)
			}
		}
	}
}

func (t *TreeNav) hidden(id string) bool {
	for p := t.parent[id]; p != ""; p = t.parent[p] {
		if t.folded[p] {
			return true
		}
	}
	return false
}

// refresh recomputes the visible rows and keeps the selection on entry keep when it is still visible.
func (t *TreeNav) refresh(keep string) {
	t.vis = t.vis[:0]
	for i, r := range t.rows {
		if t.hidden(r.Entry.ID) {
			continue
		}
		if t.Search != "" {
			if _, ok := FuzzyScore(t.Search, r.Text+" "+r.Label+" "+r.Entry.ID); !ok {
				continue
			}
		}
		t.vis = append(t.vis, i)
	}
	t.Sel = max(0, min(t.Sel, len(t.vis)-1))
	if keep != "" {
		t.selectID(keep)
	}
}

func (t *TreeNav) selectID(id string) {
	for k, i := range t.vis {
		if t.rows[i].Entry.ID == id {
			t.Sel = k
			return
		}
	}
}

// CurrentID is the selected entry's id, "" when nothing is listed.
func (t *TreeNav) CurrentID() string {
	if t.Sel < 0 || t.Sel >= len(t.vis) {
		return ""
	}
	return t.rows[t.vis[t.Sel]].Entry.ID
}

// Count is the number of visible rows.
func (t *TreeNav) Count() int { return len(t.vis) }

// Move changes the selection by d rows (clamped).
func (t *TreeNav) Move(d int) { t.Sel = max(0, min(len(t.vis)-1, t.Sel+d)) }

// FoldOrUp folds the selected entry if it has children and is open; otherwise it moves to the parent.
func (t *TreeNav) FoldOrUp() {
	id := t.CurrentID()
	if id == "" {
		return
	}
	if t.kids[id] > 0 && !t.folded[id] {
		t.folded[id] = true
		t.refresh(id)
		return
	}
	if p := t.parent[id]; p != "" {
		t.selectID(p)
	}
}

// UnfoldOrDown unfolds the selected entry if it is folded; otherwise it moves to its first child (the next row).
func (t *TreeNav) UnfoldOrDown() {
	id := t.CurrentID()
	if id == "" {
		return
	}
	if t.folded[id] {
		delete(t.folded, id)
		t.refresh(id)
		return
	}
	if t.kids[id] > 0 {
		t.Move(1)
	}
}

// Type adds to the search text, Backspace removes from it.
func (t *TreeNav) Type(s string) { t.Search += s; t.Sel = 0; t.refresh("") }
func (t *TreeNav) Backspace() {
	if r := []rune(t.Search); len(r) > 0 {
		t.Search = string(r[:len(r)-1])
		t.refresh(t.CurrentID())
	}
}

// View draws the rows around the selection, the filter in effect and the key hints.
func (t *TreeNav) View(width int, th *Theme, hint string) []string {
	out := []string{th.Fg("borderAccent", strings.Repeat("─", max(1, width))),
		" " + th.Fg("accent", th.Bold("Session tree")) + th.Fg("muted", "  filter: "+t.Mode)}
	if t.Search != "" {
		out = append(out, " search: "+t.Search)
	}
	if len(t.vis) == 0 {
		out = append(out, th.Fg("muted", "  nothing to show under this filter"))
	}
	first := max(0, min(t.Sel-t.Max/2, len(t.vis)-t.Max))
	last := min(len(t.vis), first+t.Max)
	for k := first; k < last; k++ {
		r := t.rows[t.vis[k]]
		mark := "·"
		if r.OnPath {
			mark = "•"
		}
		fold := " "
		if t.folded[r.Entry.ID] {
			fold = "▸"
		} else if t.kids[r.Entry.ID] > 0 && r.Children > 1 {
			fold = "▾"
		}
		line := " " + strings.Repeat("  ", r.Depth) + fold + th.Fg("accent", mark) + " " + r.Text
		if r.Label != "" {
			line += "  " + th.Fg("warning", "["+r.Label+"]")
			if t.Timestamps {
				line += " " + th.Fg("dim", r.Entry.Timestamp)
			}
		}
		if r.IsLeaf {
			line += th.Fg("accent", "  ← current")
		}
		line = Truncate(line, width, "…")
		if k == t.Sel {
			line = th.Bg("selectedBg", Pad(line, width))
		}
		out = append(out, line)
	}
	if len(t.vis) > t.Max {
		out = append(out, th.Fg("dim", " ("+strconv.Itoa(t.Sel+1)+"/"+strconv.Itoa(len(t.vis))+")"))
	}
	return append(out, th.Fg("dim", " "+Truncate(hint, width-2, "…")))
}
