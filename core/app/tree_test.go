package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koine/core/llm"
	"koine/core/session"
)

// treeApp is an app whose model answers "ok" and summarises with SUMMARY, after three exchanges.
func treeApp(t *testing.T) (*App, *summarising, []string) {
	t.Helper()
	isolate(t)
	a := newApp(t, t.TempDir(), RunOptions{})
	p := &summarising{}
	a.Agent.Provider = p
	for _, in := range []string{"one", "two", "three"} {
		say(t, a, in)
	}
	var users []string
	for _, r := range a.Session.Rows(session.FilterUserOnly) {
		users = append(users, r.Entry.ID)
	}
	if len(users) != 3 || len(a.Agent.History) != 6 {
		t.Fatalf("setup: users=%v history=%d", users, len(a.Agent.History))
	}
	return a, p, users
}

func TestTreeListsAndFilters(t *testing.T) {
	a, _, users := treeApp(t)
	got := say(t, a, "/tree")
	if !strings.Contains(got, users[0]) || !strings.Contains(got, "<- current") || !strings.Contains(got, "user: three") {
		t.Errorf("/tree: %q", got)
	}
	if got := say(t, a, "/tree user-only"); strings.Contains(got, "assistant") || !strings.Contains(got, "user: two") {
		t.Errorf("/tree user-only: %q", got)
	}
	if got := RenderTree(session.Create(t.TempDir(), "/p"), "all"); got != "no entries yet" {
		t.Errorf("empty tree: %q", got)
	}
	if a.TreeFilterMode() != "default" {
		t.Errorf("default filter: %q", a.TreeFilterMode())
	}
}

func TestTreeMoveBranchAndSummary(t *testing.T) {
	a, p, users := treeApp(t)
	// moving to the second user message: context is the first exchange; the text comes back for editing; the hint says nothing was summarised
	got := say(t, a, "/tree "+users[1])
	if len(a.Agent.History) != 2 || !strings.Contains(got, "two") || !strings.Contains(got, "not summarised") {
		t.Errorf("move: %q history=%d", got, len(a.Agent.History))
	}
	say(t, a, "two, edited") // a new branch off the first answer
	if _, leaves, _ := a.Session.Stats(); leaves != 2 {
		t.Errorf("leaves=%d", leaves)
	}
	if len(a.Agent.History) != 4 {
		t.Fatalf("history on the new branch: %d", len(a.Agent.History))
	}
	// back to the old branch's last answer, with a summary of the branch left behind
	rows := a.Session.Rows(session.FilterAll)
	oldLeaf := rows[5].Entry.ID // u1 a1 u2 a2 u3 a3 | u2' a2'
	got = say(t, a, "/tree "+oldLeaf+" --summary keep it short")
	if p.summaries != 1 || !strings.Contains(got, "summary of the old branch added") {
		t.Errorf("summary move: %q summaries=%d", got, p.summaries)
	}
	var bs session.Entry
	for _, r := range a.Session.Rows(session.FilterAll) {
		if r.Entry.Type == "branch_summary" {
			bs = r.Entry
		}
	}
	if bs.Type != "branch_summary" || bs.Summary != "SUMMARY" || bs.FromID == "" || bs.ParentID == nil || *bs.ParentID != oldLeaf {
		t.Errorf("branch_summary entry: %+v", bs)
	}
	if h := a.Agent.History; len(h) != 7 || !strings.Contains(h[len(h)-1].Content[0].Text, "SUMMARY") {
		t.Errorf("history after summary move: %d", len(h))
	}
	if got := say(t, a, "/tree "+bs.ID); !strings.Contains(got, "already there") {
		t.Errorf("same position: %q", got)
	}
	if got := say(t, a, "/tree zz-nothing"); !strings.Contains(got, "no entry matches") {
		t.Errorf("bad ref: %q", got)
	}
}

func TestTreeSummaryPromptAndSettings(t *testing.T) {
	a, p, users := treeApp(t)
	var asked []string
	a.Choose = func(title string, options []string) int { asked = append(asked, title); return 1 }
	say(t, a, "/tree "+users[1])
	if len(asked) != 1 || p.summaries != 1 {
		t.Errorf("Choose=Summarise: asked=%v summaries=%d", asked, p.summaries)
	}
	a.Choose = func(string, []string) int { return 0 }
	say(t, a, "/tree "+users[2])
	say(t, a, "/tree "+users[0])
	if p.summaries != 1 {
		t.Errorf("Choose=No summary must not summarise (summaries=%d)", p.summaries)
	}
	// skipPrompt: no question, no hint, no summary; the flag still works
	yes := true
	a.Cfg.BranchSummary.SkipPrompt = &yes
	asked = nil
	a.Choose = func(t string, _ []string) int { asked = append(asked, t); return 1 }
	say(t, a, "/tree "+users[2])
	say(t, a, "/tree "+users[1])
	if len(asked) != 0 || p.summaries != 1 {
		t.Errorf("skipPrompt: asked=%v summaries=%d", asked, p.summaries)
	}
	say(t, a, "/tree "+users[0]+" -s")
	if p.summaries != 2 {
		t.Errorf("-s with skipPrompt: summaries=%d", p.summaries)
	}
	if got := say(t, a, "/tree "+users[1]+" --no-summary"); strings.Contains(got, "summary of the old") {
		t.Errorf("--no-summary: %q", got)
	}
}

type failing struct{}

func (failing) Complete(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("boom")
}

func TestTreeSummaryFailureKeepsPosition(t *testing.T) {
	a, _, users := treeApp(t)
	leaf := a.Session.Leaf()
	a.Agent.Provider = failing{}
	if got := say(t, a, "/tree "+users[1]+" --summary"); !strings.Contains(got, "still at the old position") || a.Session.Leaf() != leaf {
		t.Errorf("failure: %q leaf=%s", got, a.Session.Leaf())
	}
}

func TestBranchSummaryBudgetKeepsNewestMessages(t *testing.T) {
	a, _, _ := treeApp(t)
	a.Cfg.BranchSummary.ReserveTokens = 1 << 30 // nothing fits: only the newest message is kept, never zero
	left, _ := a.Session.Abandoned("")
	if s, err := a.summariseBranch(context.Background(), left, ""); err != nil || s != "SUMMARY" {
		t.Errorf("summarise: %q %v", s, err)
	}
	if _, err := a.summariseBranch(context.Background(), nil, ""); err == nil {
		t.Error("nothing to summarise should fail")
	}
}

func TestLabelCommand(t *testing.T) {
	isolate(t)
	a := newApp(t, t.TempDir(), RunOptions{})
	a.Agent.Provider = &summarising{}
	if got := say(t, a, "/label x"); !strings.Contains(got, "nothing to label") {
		t.Errorf("empty session: %q", got)
	}
	if got := say(t, a, "/label"); !strings.Contains(got, "no labels yet") {
		t.Errorf("no labels: %q", got)
	}
	say(t, a, "first")
	say(t, a, "second")
	if got := say(t, a, "/label good point"); !strings.Contains(got, "labelled") {
		t.Errorf("set: %q", got)
	}
	_ = a.Session.RecordModel("p", "m") // bookkeeping at the leaf is skipped when picking a default target
	if got := say(t, a, "/label second mark"); !strings.Contains(got, "labelled") {
		t.Errorf("set again: %q", got)
	}
	rows := a.Session.Rows(session.FilterAll)
	first := rows[0].Entry.ID
	if got := say(t, a, "/label @"+first+" start"); !strings.Contains(got, "start") {
		t.Errorf("@ref: %q", got)
	}
	if got := say(t, a, "/label"); !strings.Contains(got, "[start]") || !strings.Contains(got, "[second mark]") {
		t.Errorf("list: %q", got)
	}
	if got := say(t, a, "/tree labeled-only"); !strings.Contains(got, "[start]") || strings.Contains(got, "user: second") {
		t.Errorf("labeled-only tree: %q", got)
	}
	if got := say(t, a, "/tree start"); !strings.Contains(got, "moved to") { // a label works as a reference
		t.Errorf("by label: %q", got)
	}
	if got := say(t, a, "/label @start -"); !strings.Contains(got, "cleared") {
		t.Errorf("clear: %q", got)
	}
	if got := say(t, a, "/label @"+first); !strings.Contains(got, "usage") {
		t.Errorf("missing text: %q", got)
	}
	if got := say(t, a, "/label @nope x"); !strings.Contains(got, "no entry matches") {
		t.Errorf("bad ref: %q", got)
	}
}

func TestCloneAndForkFromMessage(t *testing.T) {
	a, _, users := treeApp(t)
	id, path := a.Session.ID, a.Session.Path
	if got := say(t, a, "/clone"); !strings.Contains(got, "cloned") || a.Session.ID == id || a.Session.Parent != path || len(a.Agent.History) != 6 {
		t.Errorf("/clone: %q id=%s history=%d", got, a.Session.ID, len(a.Agent.History))
	}
	if got := say(t, a, "/fork "+users[2]); !strings.Contains(got, "three") || len(a.Agent.History) != 4 || a.Session.Parent == "" {
		t.Errorf("/fork <user>: %q history=%d", got, len(a.Agent.History))
	}
	rows := a.Session.Rows(session.FilterAll)
	if got := say(t, a, "/fork "+rows[1].Entry.ID); !strings.Contains(got, "not a user message") {
		t.Errorf("/fork <assistant>: %q", got)
	}
	if got := say(t, a, "/fork zz-nothing"); !strings.Contains(got, "no entry matches") {
		t.Errorf("/fork bad: %q", got)
	}
	if got := say(t, a, "/fork"); !strings.Contains(got, "forked") { // no argument: the whole branch, as before
		t.Errorf("/fork: %q", got)
	}
}

func TestExportCommand(t *testing.T) {
	a, _, _ := treeApp(t)
	dir := t.TempDir()
	html := filepath.Join(dir, "out", "s.html")
	if got := say(t, a, "/export "+html); !strings.Contains(got, "exported to") {
		t.Errorf("html: %q", got)
	}
	b, _ := os.ReadFile(html)
	if !strings.Contains(string(b), "<!doctype html>") || !strings.Contains(string(b), "three") {
		t.Errorf("html content: %.200s", b)
	}
	jl := filepath.Join(dir, "s.JSONL")
	say(t, a, "/export "+jl)
	if raw, _ := os.ReadFile(jl); !strings.Contains(string(raw), `"type":"session"`) || strings.Contains(string(raw), "<html") {
		t.Errorf("jsonl content: %.200s", raw)
	}
	if got := say(t, a, "/export "+html); !strings.Contains(got, "exists") {
		t.Errorf("must not overwrite: %q", got)
	}
	if got := say(t, a, "/export"); !strings.Contains(got, "koine-session-") { // default name, in the project
		t.Errorf("default name: %q", got)
	}
	// ExportFile converts a session file on disk (the --export path)
	out := filepath.Join(dir, "conv.html")
	if err := ExportFile(a.Session.Path, out); err != nil {
		t.Fatal(err)
	}
	if err := ExportFile(filepath.Join(dir, "missing.jsonl"), filepath.Join(dir, "x.html")); err == nil {
		t.Error("missing input")
	}
	// a session that was never written cannot be exported, and leaves no file
	empty := session.Create(dir, "/p")
	bad := filepath.Join(dir, "empty.jsonl")
	if err := ExportSession(empty, bad); err == nil {
		t.Error("expected an error for an empty session")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("failed export must not leave a file")
	}
}

func TestResumePickerNumbersAndChoose(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	a := newApp(t, dir, RunOptions{Name: "alpha"})
	say(t, a, "hello alpha")
	b := newApp(t, dir, RunOptions{Name: "beta"})
	say(t, b, "hello beta")
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(a.Session.Path, old, old)
	c := newApp(t, dir, RunOptions{})
	list := say(t, c, "/resume")
	if !strings.Contains(list, " 1  ") || !strings.Contains(list, "alpha") || !strings.Contains(list, "<number|id|name>") {
		t.Errorf("list without Choose: %q", list)
	}
	if got := say(t, c, "/resume 1"); !strings.Contains(got, "resumed") || c.Session.ID != b.Session.ID { // newest first: beta
		t.Errorf("/resume 1: %q", got)
	}
	c.Choose = func(title string, opts []string) int {
		if len(opts) != 2 {
			t.Errorf("options: %v", opts)
		}
		return 1
	}
	if got := say(t, c, "/resume"); !strings.Contains(got, "resumed") || c.Session.ID != a.Session.ID {
		t.Errorf("/resume via Choose: %q", got)
	}
	c.Choose = func(string, []string) int { return -1 }
	if got := say(t, c, "/resume"); got != "" {
		t.Errorf("cancelled picker should say nothing: %q", got)
	}
	if got := say(t, c, "/resume 9"); !strings.Contains(got, "no session matches") {
		t.Errorf("number out of range: %q", got)
	}

	// the -r picker reads a number from a stream
	d := newApp(t, dir, RunOptions{})
	var out strings.Builder
	if ok, err := d.PickResume(strings.NewReader("2\n"), &out); err != nil || !ok || d.Session.ID != a.Session.ID {
		t.Errorf("PickResume: ok=%v err=%v out=%q", ok, err, out.String())
	}
	for _, in := range []string{"q\n", "", "99\n"} {
		if ok, _ := d.PickResume(strings.NewReader(in), &out); ok {
			t.Errorf("input %q should cancel", in)
		}
	}
	if ok, _ := newApp(t, t.TempDir(), RunOptions{}).PickResume(strings.NewReader("1\n"), &out); ok || !strings.Contains(out.String(), "no sessions yet") {
		t.Error("no sessions")
	}
}

func TestTreeCommandsWithoutSessionAndSettings(t *testing.T) {
	isolate(t)
	a := newApp(t, t.TempDir(), RunOptions{NoSession: true})
	for _, c := range []string{"/tree", "/label x", "/clone", "/export", "/fork abc"} {
		if got := say(t, a, c); !strings.Contains(got, "sessions are off") {
			t.Errorf("%s: %q", c, got)
		}
	}
	if a.DoubleEscapeAction() != "tree" {
		t.Errorf("default double escape: %q", a.DoubleEscapeAction())
	}
	home, proj := os.Getenv("HOME"), t.TempDir()
	writeFile(t, filepath.Join(home, ".koine", "settings.json"),
		`{"treeFilterMode":"user-only","doubleEscapeAction":"fork","branchSummary":{"reserveTokens":5000,"skipPrompt":true}}`)
	b := startIn(t, proj, Options{})
	if b.TreeFilterMode() != "user-only" || b.DoubleEscapeAction() != "fork" || b.Cfg.BranchSummary.ReserveTokens != 5000 || !b.skipSummaryPrompt() {
		t.Errorf("settings: %+v", b.Cfg)
	}
	b.Cfg.TreeFilterMode, b.Cfg.DoubleEscapeAction = "bogus", "bogus"
	if b.TreeFilterMode() != "default" || b.DoubleEscapeAction() != "tree" {
		t.Error("invalid values must fall back to the defaults")
	}
	// the project file overrides the user file in a trusted project
	writeFile(t, filepath.Join(proj, ".koine", "settings.json"), `{"treeFilterMode":"all","doubleEscapeAction":"none","branchSummary":{"reserveTokens":7}}`)
	c := startIn(t, proj, Options{Trust: true})
	if c.TreeFilterMode() != "all" || c.DoubleEscapeAction() != "none" || c.Cfg.BranchSummary.ReserveTokens != 7 || !c.skipSummaryPrompt() {
		t.Errorf("project settings: %+v", c.Cfg)
	}
}
