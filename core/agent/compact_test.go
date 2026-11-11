package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"koine/core/compact"
	"koine/core/llm"
)

// summariser answers summary requests and otherwise behaves like script.
type summariser struct {
	script
	summaries int
	fail      error
}

func (s *summariser) Complete(ctx context.Context, r llm.Request) (*llm.Response, error) {
	if strings.HasPrefix(r.System, "You summarise") {
		s.summaries++
		if s.fail != nil {
			return nil, s.fail
		}
		return &llm.Response{Content: []llm.Block{txt("SUMMARY")}}, nil
	}
	return s.script.Complete(ctx, r)
}

func filled(a *Agent, n int) {
	for i := 0; i < n; i++ {
		a.History = append(a.History, llm.Message{Role: "user", Content: []llm.Block{txt(strings.Repeat("q", 400))}},
			llm.Message{Role: "assistant", Content: []llm.Block{txt(strings.Repeat("a", 400))}})
	}
}

func TestManualCompactReplacesOldHistoryAndReportsTheCut(t *testing.T) {
	p := &summariser{}
	a := newAgent(p)
	a.KeepRecent = 250
	filled(a, 10)
	n := len(a.History)
	var gotCut int
	var gotSummary string
	a.OnCompact = func(cut int, summary string, _ int) { gotCut, gotSummary = cut, summary }
	r, err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Dropped != gotCut || gotSummary != "SUMMARY" || len(a.History) != n-gotCut+1 || r.TokensAfter >= r.TokensBefore {
		t.Fatalf("dropped=%d cut=%d len=%d/%d result=%+v", r.Dropped, gotCut, len(a.History), n, r)
	}
	if s, ok := compact.SummaryText(a.History[0]); !ok || s != "SUMMARY" {
		t.Errorf("first message: %+v", a.History[0])
	}
	// compacting again updates the summary instead of nesting it
	filled(a, 10)
	if _, err := a.Compact(context.Background(), "tests"); err != nil {
		t.Fatal(err)
	}
	if s, _ := compact.SummaryText(a.History[0]); strings.Count(s, "Summary of the earlier") != 0 {
		t.Errorf("summary nested: %q", s)
	}
}

func TestCompactErrors(t *testing.T) {
	a := newAgent(&summariser{})
	if _, err := a.Compact(context.Background(), ""); !errors.Is(err, ErrNothingToCompact) {
		t.Errorf("empty history: %v", err)
	}
	b := newAgent(&summariser{fail: errors.New("boom")})
	b.KeepRecent = 100
	filled(b, 6)
	n := len(b.History)
	if _, err := b.Compact(context.Background(), ""); err == nil || len(b.History) != n {
		t.Errorf("a failed summary must leave the history alone: %v len=%d/%d", err, len(b.History), n)
	}
}

func TestAutoCompactsBeforeTheWindowFillsUp(t *testing.T) {
	p := &summariser{}
	a := newAgent(p)
	a.ContextWindow, a.Reserve, a.KeepRecent, a.AutoCompact = 2000, 500, 300, true
	filled(a, 12) // ~2400 tokens
	evs, err := collect(a, "next question")
	if err != nil {
		t.Fatal(err)
	}
	if p.summaries != 1 {
		t.Fatalf("summaries: %d", p.summaries)
	}
	var notice bool
	for _, e := range evs {
		notice = notice || (e.Type == "notice" && strings.HasPrefix(e.Text, "compacted:"))
	}
	if !notice {
		t.Errorf("no compaction notice: %+v", evs)
	}
	sent := p.reqs[len(p.reqs)-1].Messages
	if _, ok := compact.SummaryText(sent[0]); !ok || compact.EstimateTokens(sent) > 2000 {
		t.Errorf("the model must get the summary and a smaller history, got %d messages / %d tokens", len(sent), compact.EstimateTokens(sent))
	}
	// switched off or without a window, nothing happens
	for _, set := range []func(*Agent){func(x *Agent) { x.AutoCompact = false }, func(x *Agent) { x.ContextWindow = 0 }} {
		q := &summariser{}
		b := newAgent(q)
		b.ContextWindow, b.Reserve, b.KeepRecent, b.AutoCompact = 2000, 500, 300, true
		set(b)
		filled(b, 12)
		if _, err := collect(b, "hi"); err != nil || q.summaries != 0 {
			t.Errorf("unexpected compaction: %v %d", err, q.summaries)
		}
	}
}

// overflowOnce fails the first non-summary request as a context overflow.
type overflowOnce struct {
	summariser
	failed bool
	always bool
}

func (o *overflowOnce) Complete(ctx context.Context, r llm.Request) (*llm.Response, error) {
	if !strings.HasPrefix(r.System, "You summarise") && (!o.failed || o.always) {
		o.failed = true
		o.reqs = append(o.reqs, r)
		return nil, errors.New("anthropic: HTTP 400: prompt is too long: 300000 tokens > 200000 maximum")
	}
	return o.summariser.Complete(ctx, r)
}

func TestOverflowCompactsAndRetriesOnce(t *testing.T) {
	p := &overflowOnce{}
	a := newAgent(p)
	a.KeepRecent = 300
	filled(a, 12)
	if _, err := collect(a, "go"); err != nil {
		t.Fatalf("recovered run should succeed: %v", err)
	}
	if p.summaries != 1 {
		t.Errorf("summaries: %d", p.summaries)
	}
	// a second overflow in the same prompt is reported, not retried forever
	q := &overflowOnce{always: true}
	b := newAgent(q)
	b.KeepRecent = 300
	filled(b, 12)
	if _, err := collect(b, "go"); err == nil || q.summaries != 1 {
		t.Errorf("want an error after one recovery, got %v with %d summaries", err, q.summaries)
	}
	// nothing to compact: the overflow is simply reported
	c := newAgent(&overflowOnce{})
	if _, err := collect(c, "go"); err == nil {
		t.Error("overflow with an empty history must fail")
	}
}
