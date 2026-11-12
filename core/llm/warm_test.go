package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// warmModel is priced like Claude Sonnet 5.5: a miss re-writes at $2.50 per million tokens, a refresh reads at $0.10.
func warmModel() Model {
	return Model{ID: "m", Cost: &Price{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}, PromptCache: &PromptCache{Short: 300, Long: 3600}}
}

func TestDecideWarm(t *testing.T) {
	big := Usage{CacheRead: 90000, Input: 10000} // 100k tokens: a miss costs $0.25 against $0.01 for a refresh
	d := DecideWarm(WarmIdle, warmModel(), big, false)
	if !d.Warm || d.Lifetime != 300*time.Second || d.Interval != 270*time.Second || d.Savings < 0.23 || d.Savings > 0.25 {
		t.Errorf("worthwhile: %+v", d)
	}
	if d = DecideWarm(WarmStreaming, warmModel(), big, true); !d.Warm || d.Lifetime != time.Hour || d.Interval != 3570*time.Second {
		t.Errorf("long retention: %+v", d)
	}
	noLong := warmModel()
	noLong.PromptCache = &PromptCache{Short: 300}
	if d = DecideWarm(WarmIdle, noLong, big, true); d.Lifetime != 300*time.Second {
		t.Errorf("a model without a long tier keeps its short one: %+v", d)
	}
	onlyLong := warmModel()
	onlyLong.PromptCache = &PromptCache{Long: 600}
	if d = DecideWarm(WarmIdle, onlyLong, big, false); d.Lifetime != 600*time.Second {
		t.Errorf("only a long tier: %+v", d)
	}
	short := warmModel()
	short.PromptCache = &PromptCache{Short: 40}
	if d = DecideWarm(WarmIdle, short, big, false); d.Interval != 20*time.Second {
		t.Errorf("a very short lifetime refreshes at half of it: %+v", d)
	}
	noPrice := warmModel()
	noPrice.Cost = nil
	noWrite := warmModel()
	noWrite.Cost = &Price{Input: 3, CacheRead: 0.3}
	for name, c := range map[string]struct {
		mode string
		m    Model
		u    Usage
		want string
	}{
		"off":            {WarmOff, warmModel(), big, "off"},
		"unknown mode":   {"sometimes", warmModel(), big, "off"},
		"no lifetime":    {WarmIdle, Model{Cost: warmModel().Cost}, big, "no cache lifetime"},
		"empty lifetime": {WarmIdle, Model{Cost: warmModel().Cost, PromptCache: &PromptCache{}}, big, "no cache lifetime"},
		"no price":       {WarmIdle, noPrice, big, "no price"},
		"no usage":       {WarmIdle, warmModel(), Usage{}, "no price or no prompt size"},
		"too cheap":      {WarmIdle, warmModel(), Usage{CacheRead: 18000}, "under $0.05"},
	} {
		if d := DecideWarm(c.mode, c.m, c.u, false); d.Warm || !strings.Contains(d.Reason, c.want) {
			t.Errorf("%s: %+v", name, d)
		}
	}
	// with no cache-write price the miss is priced at the input rate
	if d = DecideWarm(WarmIdle, noWrite, Usage{Input: 100000}, false); !d.Warm || d.Savings < 0.26 || d.Savings > 0.28 {
		t.Errorf("input-priced miss: %+v", d)
	}
}

// fakeClock collects the timers the warmer arms; the test fires them by hand.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) After(d time.Duration, f func()) func() bool {
	t := &fakeTimer{d: d, f: f}
	c.mu.Lock()
	c.timers = append(c.timers, t)
	c.mu.Unlock()
	return func() bool { t.stopped = true; return true }
}

// last returns the newest timer that has not been stopped.
func (c *fakeClock) last() *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.timers) - 1; i >= 0; i-- {
		if !c.timers[i].stopped {
			return c.timers[i]
		}
	}
	return nil
}

// warmProvider streams like Anthropic does: a first token, then the answer; cancelling after the first token returns the usage so far.
type warmProvider struct {
	mu    sync.Mutex
	calls []Request
	err   error
	usage Usage
}

func (p *warmProvider) Complete(context.Context, Request) (*Response, error) {
	return nil, errors.New("Complete must not be used to warm a cache")
}

func (p *warmProvider) Stream(ctx context.Context, req Request, fn func(StreamEvent)) (*Response, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req)
	err, u := p.err, p.usage
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	fn(StreamEvent{Type: EvTextDelta, Delta: "x"})
	return &Response{Usage: u}, ctx.Err()
}

func (p *warmProvider) n() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func newWarmer(mode string, p Provider) (*Warmer, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return &Warmer{Mode: mode, Catalog: &Catalog{Models: []Model{warmModel()}}, Provider: func() Provider { return p },
		Now: clock.Now, After: clock.After}, clock
}

var warmReq = Request{Model: "m", System: "sys", Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}}}
var warmUse = Usage{CacheRead: 90000, Input: 10000}

func TestWarmerRefreshesDuringARunAndCountsTheUsage(t *testing.T) {
	p := &warmProvider{usage: Usage{Input: 5, CacheRead: 100000}}
	w, clock := newWarmer(WarmStreaming, p)
	if d := w.Next(); d.Warm || d.Reason != "no request has been made yet" {
		t.Errorf("before any request: %+v", d)
	}
	w.Begin()
	w.Observe(warmReq, warmUse)
	tm := clock.last()
	if tm == nil || tm.d != 270*time.Second {
		t.Fatalf("a refresh should be armed 30 s before the cache lapses: %+v", tm)
	}
	if d := w.Next(); !d.Warm {
		t.Errorf("next: %+v", d)
	}
	tm.f()
	if p.n() != 1 || p.calls[0].System != "sys" || p.calls[0].Model != "m" {
		t.Fatalf("the refresh should replay the last request: %+v", p.calls)
	}
	if got := w.Drain(); len(got) != 1 || got[0].Usage.CacheRead != 100000 || got[0].Model != "m" {
		t.Errorf("drained usage: %+v", got)
	}
	if len(w.Drain()) != 0 {
		t.Error("Drain should empty the list")
	}
	if next := clock.last(); next == nil || next == tm || next.d != 270*time.Second {
		t.Fatalf("the next refresh should be armed: %+v", next)
	}
	// a real request makes the armed refresh stale
	stale := clock.last()
	w.Busy()
	stale.f()
	if p.n() != 1 {
		t.Error("a refresh armed before a request must not fire after it")
	}
	// after the run, streaming mode stops; the pending timer does nothing
	w.Observe(warmReq, warmUse)
	armed := clock.last()
	w.End()
	armed.f()
	if p.n() != 1 {
		t.Error("streaming mode must not warm between runs")
	}
	w.Stop()
}

func TestWarmerIdleModeKeepsWarmForThirtyMinutes(t *testing.T) {
	p := &warmProvider{usage: Usage{CacheRead: 100000}}
	w, clock := newWarmer(WarmIdle, p)
	w.Begin()
	w.Observe(warmReq, warmUse)
	w.End()
	for i := 1; i <= 6; i++ { // refreshes at 4.5 minute steps stay inside the window
		clock.advance(270 * time.Second)
		if i*270 >= 30*60 {
			break
		}
		clock.last().f()
		if p.n() != i {
			t.Fatalf("refresh %d did not happen (%d calls)", i, p.n())
		}
	}
	clock.advance(10 * time.Minute) // now more than 30 minutes since the run ended
	before := p.n()
	clock.last().f()
	if p.n() != before {
		t.Error("the idle window is 30 minutes")
	}
}

func TestWarmerStopsWhenItCannotHelp(t *testing.T) {
	// a provider that cannot stream is never used: a refresh would produce a whole answer
	w, clock := newWarmer(WarmIdle, Missing{Reason: "no"})
	w.Begin()
	w.Observe(warmReq, warmUse)
	clock.last().f()
	if len(w.Drain()) != 0 {
		t.Error("nothing to drain")
	}
	w.Provider = nil
	w.Observe(warmReq, warmUse)
	clock.last().f() // no provider at all
	// an error before the first token ends warming until the next response
	p := &warmProvider{err: errors.New("HTTP 500")}
	w, clock = newWarmer(WarmIdle, p)
	w.Begin()
	w.Observe(warmReq, warmUse)
	tm := clock.last()
	tm.f()
	if p.n() != 1 || clock.last() != tm {
		t.Errorf("after an error no new refresh is armed: calls %d", p.n())
	}
	// unknown models, small prompts and mode off arm nothing
	for name, w := range map[string]*Warmer{"off": {Mode: WarmOff}, "unknown model": {Mode: WarmIdle, Catalog: &Catalog{}}} {
		clock := &fakeClock{}
		w.After, w.Now = clock.After, clock.Now
		w.Observe(warmReq, warmUse)
		if clock.last() != nil {
			t.Errorf("%s: a timer was armed", name)
		}
		if d := w.Next(); d.Warm {
			t.Errorf("%s: %+v", name, d)
		}
	}
	w, clock = newWarmer(WarmIdle, &warmProvider{})
	w.Observe(warmReq, Usage{Input: 1000})
	if clock.last() != nil {
		t.Error("a small prompt is not worth a refresh")
	}
	// the real clock and timers work too
	real := &Warmer{Mode: WarmIdle, Catalog: &Catalog{Models: []Model{warmModel()}}}
	real.Observe(warmReq, warmUse)
	real.Busy()
	real.End()
	_ = real.now()
}

// TestWarmerOverlapWithARequestKeepsItsUsage covers a refresh that is still running when the next real request starts.
func TestWarmerOverlapWithARequestKeepsItsUsage(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p := &blockingProvider{started: started, release: release, usage: Usage{CacheRead: 100000}}
	w, clock := newWarmer(WarmIdle, p)
	w.Begin()
	w.Observe(warmReq, warmUse)
	tm := clock.last()
	done := make(chan struct{})
	go func() { tm.f(); close(done) }()
	<-started
	w.Busy() // the agent is about to send a real request; the refresh is cancelled
	close(release)
	<-done
	if got := w.Drain(); len(got) != 1 {
		t.Errorf("the refresh was billed and must be counted: %+v", got)
	}
	if clock.last() != nil && clock.last() != tm {
		t.Error("a cancelled refresh must not arm another one")
	}
}

type blockingProvider struct {
	started, release chan struct{}
	usage            Usage
}

func (p *blockingProvider) Complete(context.Context, Request) (*Response, error) { return nil, nil }
func (p *blockingProvider) Stream(ctx context.Context, _ Request, _ func(StreamEvent)) (*Response, error) {
	close(p.started)
	<-p.release
	return &Response{Usage: p.usage}, ctx.Err()
}

func TestCatalogDeclaresPromptCacheLifetimesForClaude(t *testing.T) {
	c := DefaultCatalog()
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		m, ok := c.Lookup(id)
		if !ok || m.PromptCache == nil || m.PromptCache.Short != 300 || m.PromptCache.Long != 3600 {
			t.Errorf("%s: %+v", id, m.PromptCache)
		}
	}
	// models.json can declare it, and modelOverrides can change a built-in's
	c.AddProviders(map[string]ProviderConfig{"mine": {Models: []ModelConfig{{ID: "x", PromptCache: &PromptCache{Short: 60}}}},
		"anthropic": {ModelOverrides: map[string]ModelConfig{"claude-haiku-5-5": {PromptCache: &PromptCache{Short: 120}}}}})
	if m, _ := c.Lookup("x"); m.PromptCache == nil || m.PromptCache.Short != 60 {
		t.Errorf("models.json: %+v", m.PromptCache)
	}
	if m, _ := c.Lookup("claude-haiku-5-5"); m.PromptCache.Short != 120 || m.PromptCache.Long != 0 {
		t.Errorf("override: %+v", m.PromptCache)
	}
}
