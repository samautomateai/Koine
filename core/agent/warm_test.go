package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"koine/core/llm"
)

// cacheStub streams one answer per request, like a provider with prompt caching: the real request is answered in full, a refresh (whose
// first token cancels its context) returns the usage seen so far.
type cacheStub struct {
	mu    sync.Mutex
	reqs  []llm.Request
	usage llm.Usage
}

func (c *cacheStub) Complete(ctx context.Context, r llm.Request) (*llm.Response, error) {
	return c.Stream(ctx, r, func(llm.StreamEvent) {})
}

func (c *cacheStub) Stream(ctx context.Context, r llm.Request, fn func(llm.StreamEvent)) (*llm.Response, error) {
	c.mu.Lock()
	c.reqs = append(c.reqs, r)
	u := c.usage
	c.mu.Unlock()
	fn(llm.StreamEvent{Type: llm.EvTextDelta, Delta: "o"})
	return &llm.Response{Content: []llm.Block{txt("ok")}, Usage: u}, ctx.Err()
}

func TestCacheHintsOriginAndWarmingReachTheProvider(t *testing.T) {
	stub := &cacheStub{usage: llm.Usage{CacheRead: 90000, Input: 10000}}
	a := newAgent(stub)
	a.Catalog = &llm.Catalog{Models: []llm.Model{{ID: "m", ContextWindow: 200000, PromptCache: &llm.PromptCache{Short: 300, Long: 3600},
		Cost: &llm.Price{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}}}}
	a.CacheKey, a.CacheLong = "session-9", true
	var armed []func()
	var delay []time.Duration
	a.Warm = &llm.Warmer{Mode: llm.WarmIdle, Long: true, Catalog: a.Catalog, Provider: func() llm.Provider { return stub },
		After: func(d time.Duration, f func()) func() bool {
			delay, armed = append(delay, d), append(armed, f)
			return func() bool { return true }
		}}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if got := stub.reqs[0].Cache; got.Key != "session-9" || !got.Long {
		t.Errorf("cache hints = %+v", got)
	}
	if a.History[len(a.History)-1].Origin != "m" {
		t.Errorf("the assistant message should remember its model: %+v", a.History[len(a.History)-1])
	}
	if len(armed) != 1 || delay[0] != 3570*time.Second {
		t.Fatalf("a refresh should be armed 30 s before the one-hour cache lapses: %v", delay)
	}
	totals, cost := a.Totals, a.CostTotal
	armed[0]() // the timer fires between runs
	if len(stub.reqs) != 2 || stub.reqs[1].Model != "m" || stub.reqs[1].Cache.Key != "session-9" {
		t.Fatalf("the refresh replays the last request: %d requests", len(stub.reqs))
	}
	if len(a.History) != 2 {
		t.Errorf("a refresh must never enter the conversation: %d messages", len(a.History))
	}
	a.DrainWarm()
	if a.Totals.CacheRead != totals.CacheRead+90000 || a.CostTotal <= cost {
		t.Errorf("refresh usage should count toward the totals: %+v cost %v -> %v", a.Totals, cost, a.CostTotal)
	}
	// an unpriced model's refresh is counted in tokens and marks the cost as incomplete
	a.Catalog = &llm.Catalog{}
	armed[len(armed)-1]()
	a.DrainWarm()
	if !a.Unpriced {
		t.Error("a refresh with no known price must set Unpriced")
	}
	// replacing the conversation drops the remembered request
	a.Reset()
	if d := a.Warm.Next(); d.Warm {
		t.Errorf("after a reset there is nothing to warm: %+v", d)
	}
}

func TestRunWithoutAWarmerStillWorks(t *testing.T) {
	a := newAgent(&cacheStub{})
	a.DrainWarm()
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
}
