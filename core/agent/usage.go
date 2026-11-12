package agent

import (
	"fmt"
	"koine/core/compact"
	"koine/core/llm"
)

// UsageInfo is what a "usage" event carries: one response's tokens and cost, and where the context stands afterwards.
type UsageInfo struct {
	llm.Usage
	Cost          *float64 `json:"cost,omitempty"` // dollars; absent when the model's price is not known
	ContextTokens int      `json:"contextTokens"`
	ContextWindow int      `json:"contextWindow"`
	ContextPct    float64  `json:"contextPercent"`
}

// window is the context window in tokens: the explicit setting, else the catalog's, else the default.
func (a *Agent) window() int {
	if a.ContextWindow > 0 {
		return a.ContextWindow
	}
	if m, ok := a.Catalog.Lookup(a.Model); ok && m.ContextWindow > 0 {
		return m.ContextWindow
	}
	return DefaultContextWindow
}

// contextTokens is how many tokens the next request will carry: the provider's count at the last response plus an estimate for what
// was added since, or an estimate of everything when no count is known.
func (a *Agent) contextTokens(system string) int {
	if a.lastCtx > 0 && a.ctxAt <= len(a.History) {
		return a.lastCtx + compact.EstimateTokens(a.History[a.ctxAt:])
	}
	return compact.EstimateText(system) + compact.EstimateTokens(a.History)
}

// drainWarm adds the usage of cache refreshes (cacheWarming) to the session totals. It never touches the conversation. Refreshes happen on
// timers, so their usage is taken up here, on the agent's own goroutine, at the start and end of a run and after each response.
func (a *Agent) drainWarm() {
	if a.Warm == nil {
		return
	}
	for _, w := range a.Warm.Drain() {
		a.Totals.Add(w.Usage)
		if mod, ok := a.Catalog.Lookup(w.Model); ok {
			if c, priced := mod.Of(w.Usage); priced {
				a.CostTotal += c.Total
				continue
			}
		}
		a.Unpriced = true
	}
}

// DrainWarm is drainWarm for frontends that want fresh totals between runs (a footer, /session).
func (a *Agent) DrainWarm() { a.drainWarm() }

// record turns a response into the assistant message for the history and accounts for its usage. The message is appended by the caller;
// the context count it sets includes that message.
func (a *Agent) record(resp *llm.Response, emit func(Event)) llm.Message {
	m := llm.Message{Role: "assistant", Content: resp.Content, Origin: a.Model}
	u := resp.Usage
	if u.Total() == 0 {
		return m
	}
	m.Usage = &u
	a.Totals.Add(u)
	info := &UsageInfo{Usage: u}
	if mod, ok := a.Catalog.Lookup(a.Model); ok {
		if c, priced := mod.Of(u); priced {
			m.Cost = &c
			a.CostTotal += c.Total
			info.Cost = &c.Total
		} else {
			a.Unpriced = true
		}
	} else {
		a.Unpriced = true
	}
	a.lastCtx, a.ctxAt = u.Total(), len(a.History)+1
	info.ContextTokens, info.ContextWindow = a.lastCtx, a.window()
	info.ContextPct = float64(info.ContextTokens) * 100 / float64(info.ContextWindow)
	emit(Event{Type: "usage", Usage: info})
	return m
}

// UsageSummary is the one-line account /session prints: tokens, cost and how full the context is.
func (a *Agent) UsageSummary() string {
	t := a.Totals
	cost := "cost unknown (no price for this model)"
	switch {
	case a.CostTotal > 0 && a.Unpriced:
		cost = fmt.Sprintf("cost at least $%.4f (some responses had no price)", a.CostTotal)
	case a.CostTotal > 0 || (t.Total() > 0 && !a.Unpriced):
		cost = fmt.Sprintf("cost $%.4f", a.CostTotal)
	}
	w := a.window()
	ctx := a.contextTokens("")
	return fmt.Sprintf("tokens: %d in, %d out, %d cache read, %d cache write · %s · context %d/%d (%.1f%%)",
		t.Input, t.Output, t.CacheRead, t.CacheWrite, cost, ctx, w, float64(ctx)*100/float64(w))
}
