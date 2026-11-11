package session

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"koine/core/llm"
)

// Tree filter modes, as Pi's treeFilterMode setting names them.
const (
	FilterDefault     = "default"      // conversation and summaries; hides bookkeeping entries (labels, model changes, names, custom data)
	FilterNoTools     = "no-tools"     // as default, without tool results
	FilterUserOnly    = "user-only"    // user messages only
	FilterLabeledOnly = "labeled-only" // entries that carry a label
	FilterAll         = "all"          // every entry
)

// ValidFilter reports whether mode is one of the filter modes.
func ValidFilter(mode string) bool {
	switch mode {
	case FilterDefault, FilterNoTools, FilterUserOnly, FilterLabeledOnly, FilterAll:
		return true
	}
	return false
}

// Row is one line of the flattened entry tree.
type Row struct {
	Entry    Entry
	Depth    int    // indentation level; grows only where the tree forks
	OnPath   bool   // on the active branch (root to leaf)
	IsLeaf   bool   // the entry new messages attach to
	Label    string // the entry's current label
	Text     string // one-line description
	Children int
}

// Leaf is the id of the entry new entries attach to ("" before the first entry, or after moving to before the first one).
func (s *Session) Leaf() string { return s.leaf }

// Entry returns the entry with the given id.
func (s *Session) Entry(id string) (Entry, bool) {
	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Labels maps entry ids to their current label (the latest label entry in the file wins; an empty label clears).
func (s *Session) Labels() map[string]string {
	out := map[string]string{}
	for _, e := range s.entries {
		if e.Type != "label" || e.TargetID == "" {
			continue
		}
		if e.Label == "" {
			delete(out, e.TargetID)
		} else {
			out[e.TargetID] = e.Label
		}
	}
	return out
}

// SetLabel labels an entry (a bookmark shown in /tree); an empty label clears it.
func (s *Session) SetLabel(target, label string) error {
	if _, ok := s.Entry(target); !ok {
		return fmt.Errorf("no entry %q in this session", target)
	}
	return s.appendEntry("label", func(e *Entry) { e.TargetID, e.Label = target, label })
}

// Find resolves ref to an entry: an exact id, a unique id prefix of at least 2 characters, or an exact label.
func (s *Session) Find(ref string) (Entry, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Entry{}, fmt.Errorf("no entry given")
	}
	var hits []Entry
	for _, e := range s.entries {
		if e.ID == ref {
			return e, nil
		}
		if len(ref) >= 2 && strings.HasPrefix(e.ID, ref) {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		for id, l := range s.Labels() {
			if l == ref {
				e, _ := s.Entry(id)
				hits = append(hits, e)
			}
		}
	}
	switch len(hits) {
	case 0:
		return Entry{}, fmt.Errorf("no entry matches %q (use an id or label from /tree)", ref)
	case 1:
		return hits[0], nil
	}
	return Entry{}, fmt.Errorf("%q matches %d entries; use more of the id", ref, len(hits))
}

// Rows flattens the entry tree depth first, in file order, for display under the given filter mode.
func (s *Session) Rows(mode string) []Row {
	kids := map[string][]Entry{}
	var roots []Entry
	known := map[string]bool{}
	for _, e := range s.entries {
		known[e.ID] = true
	}
	for _, e := range s.entries {
		if e.ParentID == nil || !known[*e.ParentID] {
			roots = append(roots, e)
		} else {
			kids[*e.ParentID] = append(kids[*e.ParentID], e)
		}
	}
	onPath := map[string]bool{}
	for _, e := range s.path() {
		onPath[e.ID] = true
	}
	labels := s.Labels()
	var rows []Row
	var walk func(e Entry, depth int)
	walk = func(e Entry, depth int) {
		c := kids[e.ID]
		if keep(e, mode, labels) {
			rows = append(rows, Row{Entry: e, Depth: depth, OnPath: onPath[e.ID], IsLeaf: e.ID == s.leaf, Label: labels[e.ID], Text: describe(e),
				Children: len(c)})
		}
		next := depth
		if len(c) > 1 {
			next++
		}
		for _, k := range c {
			walk(k, next)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return rows
}

func keep(e Entry, mode string, labels map[string]string) bool {
	role := ""
	if e.Type == "message" {
		var pm piMessage
		_ = json.Unmarshal(e.Message, &pm)
		role = pm.Role
	}
	switch mode {
	case FilterAll:
		return true
	case FilterUserOnly:
		return role == "user"
	case FilterLabeledOnly:
		return labels[e.ID] != ""
	}
	switch e.Type {
	case "label", "custom", "model_change", "thinking_level_change", "session_info":
		return false
	}
	return mode != FilterNoTools || role != "toolResult"
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// describe is a one-line description of an entry for /tree and exports.
func describe(e Entry) string {
	switch e.Type {
	case "message":
		var pm piMessage
		if json.Unmarshal(e.Message, &pm) != nil {
			return "message"
		}
		t := oneLine(textOf(pm.Content), 70)
		switch pm.Role {
		case "toolResult":
			return "tool result: " + pm.ToolName
		case "assistant":
			var blocks []piBlock
			_ = json.Unmarshal(pm.Content, &blocks)
			for _, b := range blocks {
				if b.Type == "toolCall" {
					t += " [tool: " + b.Name + "]"
				}
			}
		}
		return pm.Role + ": " + strings.TrimSpace(t)
	case "compaction":
		return "compaction: " + oneLine(e.Summary, 60)
	case "branch_summary":
		return "branch summary: " + oneLine(e.Summary, 60)
	case "label":
		return fmt.Sprintf("label %q on %s", e.Label, e.TargetID)
	case "session_info":
		return fmt.Sprintf("name: %s", e.Name)
	case "model_change":
		return "model: " + e.ModelID
	case "custom_message":
		return "custom message (" + e.CustomType + "): " + oneLine(textOf(e.Content), 50)
	}
	return e.Type
}

// Destination is where the leaf goes when the conversation is moved to e: the entry itself, except for a user message, whose parent is
// used so the message can be edited and sent again as a new branch (its text is returned for that). "" means before the first entry.
func Destination(e Entry) (leaf, userText string) {
	if e.Type == "message" {
		var pm piMessage
		if json.Unmarshal(e.Message, &pm) == nil && pm.Role == "user" {
			return deref(e.ParentID), textOf(pm.Content)
		}
	}
	return e.ID, ""
}

// SetLeaf moves the point new entries continue from. Nothing is written until the next entry is added, which is also how Pi treats a
// /tree move, so a session reopened without one starts at the last entry of the file again.
func (s *Session) SetLeaf(id string) { s.leaf = id }

// Abandoned returns the entries between the current leaf and its closest common ancestor with target (oldest first), which is what a
// branch summary describes when moving the leaf to target, and the id of that ancestor ("" when the branches share no entry).
func (s *Session) Abandoned(target string) ([]Entry, string) {
	want := map[string]bool{}
	for _, e := range pathTo(s.entries, target) {
		want[e.ID] = true
	}
	cur := s.path()
	for i := len(cur) - 1; i >= 0; i-- {
		if want[cur[i].ID] {
			return cur[i+1:], cur[i].ID
		}
	}
	return cur, ""
}

func pathTo(entries []Entry, id string) []Entry {
	t := &Session{entries: entries, leaf: id}
	return t.path()
}

// MessagesOf rebuilds conversation messages from a run of entries (for example the ones a branch summary will describe).
func MessagesOf(entries []Entry) []llm.Message {
	if len(entries) == 0 {
		return nil
	}
	t := &Session{entries: entries, leaf: entries[len(entries)-1].ID}
	return t.Messages()
}

// AddBranchSummary records that the conversation left the branch ending at fromID for the current leaf, with a summary of that branch.
// It becomes part of the context of the branch being entered.
func (s *Session) AddBranchSummary(fromID, summary string) error {
	return s.appendEntry("branch_summary", func(e *Entry) { e.FromID, e.Summary = fromID, summary })
}

const branchPrefix = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"

// entryMessage turns the entries that carry context but are not messages (branch summaries, custom messages) into user messages.
func entryMessage(e Entry) (llm.Message, bool) {
	var t string
	switch e.Type {
	case "branch_summary":
		t = branchPrefix + e.Summary + "\n</summary>"
	case "custom_message":
		t = textOf(e.Content)
	}
	if t == "" {
		return llm.Message{}, false
	}
	return llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: t}}}, true
}

// ForkAt starts a new session in dir holding the branch that ends at leafID ("" gives an empty one), with this session as its parent.
func (s *Session) ForkAt(dir, cwd, leafID string) (*Session, error) {
	return s.copyBranch(dir, cwd, leafID)
}

func (s *Session) copyBranch(dir, cwd, leafID string) (*Session, error) {
	n := Create(dir, cwd)
	n.Parent = s.Path
	var lines []any
	skipped := map[string]*string{} // id of a skipped entry -> the parent its children should take
	var last string
	for _, e := range pathTo(s.entries, leafID) {
		parent := e.ParentID
		if p, ok := skipped[deref(parent)]; ok {
			parent = p
		}
		if e.Type == "session_info" {
			skipped[e.ID] = parent
			continue
		}
		e2 := e
		if parent != e.ParentID {
			e2.ParentID = parent
			e2.raw = reparent(e.raw, parent)
		}
		lines = append(lines, entryLine(e2))
		last = e.ID
	}
	if len(lines) == 0 {
		return n, nil
	}
	head := header{Type: "session", Version: Version, ID: n.ID, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Cwd: cwd, ParentSession: s.Path}
	if err := n.write(append([]any{head}, lines...)...); err != nil {
		return nil, err
	}
	n.started, n.leaf = true, last
	for _, l := range lines {
		var e Entry
		_ = json.Unmarshal([]byte(l.(rawLine)), &e)
		e.raw = []byte(l.(rawLine))
		n.entries = append(n.entries, e)
	}
	return n, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// entryLine is the line to write for e: the bytes it was read from when it has them.
func entryLine(e Entry) rawLine {
	if len(e.raw) > 0 {
		return rawLine(e.raw)
	}
	b, _ := json.Marshal(e)
	return rawLine(b)
}

// reparent rewrites the parentId of a stored line, keeping every other field.
func reparent(raw []byte, parent *string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	m["parentId"] = json.RawMessage("null")
	if parent != nil {
		m["parentId"] = quote(*parent)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}

// Stats counts what the file holds, for /session.
func (s *Session) Stats() (entries, leaves, labels int) {
	isParent := map[string]bool{}
	for _, e := range s.entries {
		if e.ParentID != nil {
			isParent[*e.ParentID] = true
		}
	}
	for _, e := range s.entries {
		if !isParent[e.ID] {
			leaves++
		}
	}
	return len(s.entries), leaves, len(s.Labels())
}

// Raw returns the session file's bytes, every line as stored (branches, labels and unknown entries included).
func (s *Session) Raw() ([]byte, error) { return os.ReadFile(s.Path) }
