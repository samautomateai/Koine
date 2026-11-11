package agent

import (
	"context"
	"strings"
	"testing"

	"koine/core/llm"
)

type metered struct {
	script
	usage []llm.Usage
	n     int
}

func (m *metered) Complete(ctx context.Context, r llm.Request) (*llm.Response, error) {
	resp, err := m.script.Complete(ctx, r)
	if err == nil && m.n < len(m.usage) {
		resp.Usage = m.usage[m.n]
	}
	m.n++
	return resp, err
}

func catalogOf(id string, window int, in, out float64) *llm.Catalog {
	return &llm.Catalog{Models: []llm.Model{{ID: id, ContextWindow: window, Cost: &llm.Price{Input: in, Output: out}}}}
}

func TestUsageIsRecordedPricedAndReported(t *testing.T) {
	p := &metered{script: script{turns: [][]llm.Block{{use("1", "echo", `{}`)}}}, usage: []llm.Usage{{Input: 1000, Output: 100}, {Input: 1500, Output: 50}}}
	a := newAgent(p)
	a.Catalog = catalogOf("m", 10000, 1000, 2000) // $ per million tokens
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	var infos []*UsageInfo
	for _, e := range evs {
		if e.Type == "usage" {
			infos = append(infos, e.Usage)
		}
	}
	if len(infos) != 2 || infos[1].ContextTokens != 1550 || infos[1].ContextWindow != 10000 || infos[1].ContextPct != 15.5 || infos[0].Cost == nil {
		t.Fatalf("%+v %+v", infos[0], infos[len(infos)-1])
	}
	if a.Totals != (llm.Usage{Input: 2500, Output: 150}) || a.Unpriced || a.CostTotal < 2.79 || a.CostTotal > 2.81 {
		t.Errorf("totals %+v cost %v", a.Totals, a.CostTotal)
	}
	if a.History[1].Usage == nil || a.History[1].Cost == nil || a.History[1].Usage.Input != 1000 {
		t.Errorf("assistant message must carry its usage: %+v", a.History[1])
	}
	if s := a.UsageSummary(); !strings.Contains(s, "2500 in") || !strings.Contains(s, "cost $2.8") || !strings.Contains(s, "1550/10000") {
		t.Errorf("summary: %s", s)
	}
}

func TestUnknownModelHasNoCostAndDefaultWindow(t *testing.T) {
	p := &metered{usage: []llm.Usage{{Input: 10, Output: 5}}}
	a := newAgent(p)
	a.Catalog = catalogOf("other", 5, 1, 1)
	if _, err := collect(a, "x"); err != nil {
		t.Fatal(err)
	}
	if !a.Unpriced || a.CostTotal != 0 || a.History[1].Cost != nil || a.window() != DefaultContextWindow {
		t.Errorf("unpriced=%v cost=%v window=%d", a.Unpriced, a.CostTotal, a.window())
	}
	if s := a.UsageSummary(); !strings.Contains(s, "cost unknown") {
		t.Errorf("summary: %s", s)
	}
	a.ContextWindow = 123
	if a.window() != 123 {
		t.Error("an explicit window wins over the catalog and the default")
	}
	// a response without usage is not an event and leaves the totals alone
	q := newAgent(&script{})
	evs, _ := collect(q, "x")
	for _, e := range evs {
		if e.Type == "usage" {
			t.Error("no usage event without usage")
		}
	}
	if s := q.UsageSummary(); !strings.Contains(s, "cost unknown") {
		t.Errorf("nothing used yet: %s", s)
	}
}

func TestCompactionUsesTheProvidersCountAndResetClearsIt(t *testing.T) {
	a := newAgent(&script{})
	a.Catalog = catalogOf("m", 1000, 1, 1)
	a.AutoCompact, a.Reserve = true, 100
	a.History = []llm.Message{{Role: "user", Content: []llm.Block{{Type: "text", Text: "hi"}}}}
	if a.needsCompaction("") {
		t.Fatal("a tiny history fits")
	}
	a.lastCtx, a.ctxAt = 950, 1 // the provider said the context is nearly full, although the text looks tiny
	if !a.needsCompaction("") {
		t.Error("the provider's count must decide, not the 4-chars estimate")
	}
	a.History = append(a.History, llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: strings.Repeat("x", 400)}}})
	if got := a.contextTokens(""); got < 1050 || got > 1060 {
		t.Errorf("messages added since the count are estimated on top: %d", got)
	}
	a.SetHistory([]llm.Message{
		{Role: "assistant", Content: []llm.Block{{Type: "text", Text: "a"}}, Usage: &llm.Usage{Input: 5, Output: 1}, Cost: &llm.Cost{Total: 0.5}},
		{Role: "assistant", Content: []llm.Block{{Type: "text", Text: "b"}}, Usage: &llm.Usage{Input: 5}},
	})
	if a.Totals.Input != 10 || a.CostTotal != 0.5 || !a.Unpriced || a.lastCtx != 0 {
		t.Errorf("loading a history rebuilds the totals: %+v %v %v", a.Totals, a.CostTotal, a.Unpriced)
	}
	a.Reset()
	if a.History != nil || a.Totals.Total() != 0 || a.CostTotal != 0 || a.Unpriced {
		t.Error("reset clears usage")
	}
}
