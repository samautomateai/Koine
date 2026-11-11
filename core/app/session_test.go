package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/llm"
)

// say runs one prompt through a scripted provider and returns the notices and errors it produced.
func say(t *testing.T, a *App, line string) string {
	t.Helper()
	var out []string
	if _, err := a.HandleInput(context.Background(), line, func(e agent.Event) {
		if e.Type == "notice" || e.Type == "error" {
			out = append(out, e.Text)
		}
	}); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, "|")
}

func newApp(t *testing.T, dir string, o RunOptions) *App {
	t.Helper()
	o.SessionDir = filepath.Join(dir, "sessions")
	a, err := NewWithOptions(context.Background(), dir, Options{RunOptions: o})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Agent.Provider = &scripted{turns: [][]llm.Block{{use("1", "ls", `{}`)}}}
	return a
}

func TestSessionIsSavedContinuedAndResumed(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	a := newApp(t, dir, RunOptions{Name: "work"})
	say(t, a, "first prompt")
	id := a.Session.ID
	if got := len(a.Agent.History); got != 4 { // user, assistant(tool call), user(tool result), assistant(done)
		t.Fatalf("history %d", got)
	}

	// -c picks up the same conversation
	b := newApp(t, dir, RunOptions{Continue: true})
	if b.Session.ID != id || len(b.Agent.History) != 4 || b.Session.Name() != "work" {
		t.Fatalf("continue: id=%s history=%d name=%q", b.Session.ID, len(b.Agent.History), b.Session.Name())
	}
	if !strings.Contains(strings.Join(b.Info, "\n"), "4 messages loaded") {
		t.Errorf("info: %v", b.Info)
	}
	say(t, b, "second prompt")

	// --session by name and by id prefix; history includes both prompts
	for _, ref := range []string{"work", id[:8]} {
		c := newApp(t, dir, RunOptions{SessionRef: ref})
		if c.Session.ID != id || len(c.Agent.History) != 8 {
			t.Errorf("--session %s: id=%s history=%d", ref, c.Session.ID, len(c.Agent.History))
		}
	}
	// a plain run starts a fresh session
	if d := newApp(t, dir, RunOptions{}); d.Session.ID == id || len(d.Agent.History) != 0 {
		t.Error("default run must start a new session")
	}
	// -c with nothing to continue starts a new one instead of failing
	if e := newApp(t, t.TempDir(), RunOptions{Continue: true}); e.Session == nil || len(e.Agent.History) != 0 {
		t.Error("continue without sessions should start a new one")
	}
	bad := Options{RunOptions: RunOptions{SessionDir: filepath.Join(dir, "sessions"), SessionRef: "nope"}}
	if _, err := NewWithOptions(context.Background(), dir, bad); err == nil {
		t.Error("unknown --session must fail")
	}
}

func TestForkAndNoSession(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	a := newApp(t, dir, RunOptions{})
	say(t, a, "base")
	f := newApp(t, dir, RunOptions{Fork: a.Session.ID})
	if f.Session.ID == a.Session.ID || f.Session.Parent != a.Session.Path || len(f.Agent.History) != 4 {
		t.Fatalf("fork: %+v history=%d", f.Session, len(f.Agent.History))
	}
	say(t, f, "diverge")
	if len(a.Session.Messages()) != 4 {
		t.Error("the original session must not change")
	}

	n := newApp(t, t.TempDir(), RunOptions{NoSession: true})
	say(t, n, "secret")
	if n.Session != nil {
		t.Error("--no-session keeps no session")
	}
	if got := say(t, n, "/session"); !strings.Contains(got, "off") {
		t.Errorf("/session: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions")); err != nil {
		t.Error(err)
	}
	if entries, _ := filepath.Glob(filepath.Join(n.Cwd, "sessions", "*")); len(entries) != 0 {
		t.Errorf("--no-session wrote %v", entries)
	}
}

func TestSessionSlashCommands(t *testing.T) {
	isolate(t)
	a := newApp(t, t.TempDir(), RunOptions{})
	say(t, a, "hello there")
	id := a.Session.ID
	if got := say(t, a, "/session"); !strings.Contains(got, id) || !strings.Contains(got, "4 messages") {
		t.Errorf("/session: %q", got)
	}
	if got := say(t, a, "/name"); !strings.Contains(got, `""`) {
		t.Errorf("/name: %q", got)
	}
	say(t, a, "/name Big refactor")
	if a.Session.Name() != "Big refactor" {
		t.Error("name not set")
	}
	if got := say(t, a, "/resume"); !strings.Contains(got, "Big refactor") || !strings.Contains(got, id[:8]) {
		t.Errorf("/resume list: %q", got)
	}
	if got := say(t, a, "/new"); !strings.Contains(got, "new session") || a.Session.ID == id || len(a.Agent.History) != 0 {
		t.Errorf("/new: %q", got)
	}
	if got := say(t, a, "/resume Big refactor"); !strings.Contains(got, "resumed") || a.Session.ID != id || len(a.Agent.History) != 4 {
		t.Errorf("/resume ref: %q id=%s", got, a.Session.ID)
	}
	if got := say(t, a, "/resume nothing-like-this"); !strings.Contains(got, "no session matches") {
		t.Errorf("/resume bad: %q", got)
	}
	if got := say(t, a, "/fork"); !strings.Contains(got, "forked") || a.Session.ID == id || len(a.Session.Messages()) != 4 {
		t.Errorf("/fork: %q", got)
	}
	if got := ListSessions(t.TempDir()); got != "no sessions yet" {
		t.Errorf("empty list: %q", got)
	}
}

// summarising answers a summary request with SUMMARY and anything else with "ok".
type summarising struct{ summaries int }

func (s *summarising) Complete(_ context.Context, r llm.Request) (*llm.Response, error) {
	if strings.HasPrefix(r.System, "You summarise") {
		s.summaries++
		return &llm.Response{Content: []llm.Block{{Type: "text", Text: "SUMMARY"}}}, nil
	}
	return &llm.Response{Content: []llm.Block{{Type: "text", Text: "ok"}}}, nil
}

func TestCompactCommandIsSavedAndSurvivesResume(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".koine"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"compaction":{"keepRecentTokens":300,"contextWindow":1000000}}`
	if err := os.WriteFile(filepath.Join(dir, ".koine", "settings.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newApp(t, dir, RunOptions{})
	p := &summarising{}
	a.Agent.Provider = p
	if got := say(t, a, "/compact"); !strings.Contains(got, "nothing to compact") {
		t.Errorf("empty: %q", got)
	}
	for i := 0; i < 8; i++ {
		say(t, a, strings.Repeat("question ", 60))
	}
	if a.Agent.KeepRecent != 300 || a.Agent.ContextWindow != 1000000 {
		t.Fatalf("config not applied: keep=%d window=%d", a.Agent.KeepRecent, a.Agent.ContextWindow)
	}
	got := say(t, a, "/compact focus on the tests")
	if !strings.Contains(got, "compacted:") || p.summaries != 1 {
		t.Fatalf("/compact: %q summaries=%d", got, p.summaries)
	}
	n := len(a.Agent.History)
	b := newApp(t, dir, RunOptions{Continue: true})
	if len(b.Agent.History) != n || !strings.Contains(b.Agent.History[0].Content[0].Text, "SUMMARY") {
		t.Errorf("resumed %d messages, want %d starting with the summary: %+v", len(b.Agent.History), n, b.Agent.History[0])
	}
	if len(a.Warnings) != 0 {
		t.Errorf("warnings: %v", a.Warnings)
	}
}

func TestCompactionNotSavedWhenFileAndConversationDisagree(t *testing.T) {
	isolate(t)
	a := newApp(t, t.TempDir(), RunOptions{})
	a.Agent.Provider = &summarising{}
	a.Agent.KeepRecent = 100
	for i := 0; i < 6; i++ {
		say(t, a, strings.Repeat("question ", 60))
	}
	a.Agent.History = append(a.Agent.History, llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: "not in the file"}}})
	say(t, a, "/compact")
	if len(a.Warnings) == 0 || !strings.Contains(a.Warnings[len(a.Warnings)-1], "disagree") {
		t.Errorf("warnings: %v", a.Warnings)
	}
	off := false
	a.configureCompaction(CompactionConfig{Enabled: &off})
	if a.Agent.AutoCompact {
		t.Error("enabled:false must switch auto-compaction off")
	}
}
