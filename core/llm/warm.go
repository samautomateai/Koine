package llm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Values of the `cacheWarming` setting (Pi, https://pi.dev/docs/latest/settings).
const (
	WarmOff       = "off"       // never refresh a prompt cache
	WarmStreaming = "streaming" // refresh only while a run is active
	WarmIdle      = "idle"      // refresh during a run and for WarmIdleWindow after it (Pi's default)
)

const (
	// WarmIdleWindow is how long after a run the cache is kept warm in WarmIdle mode ("up to 30 minutes between runs").
	WarmIdleWindow = 30 * time.Minute
	// WarmMinSavings is the least avoided cache-miss cost, in dollars, that makes a refresh worth sending ("at least $0.05").
	WarmMinSavings = 0.05
	// warmMargin is how long before the cache would lapse the refresh is sent.
	warmMargin = 30 * time.Second
)

// PromptCache is how long a provider keeps a cached prompt prefix after its last use, in seconds (Pi's `promptCache.short` and
// `promptCache.long` of a model). Zero means the provider has no such tier; a model with neither is never warmed.
type PromptCache struct {
	Short int `json:"short,omitempty"`
	Long  int `json:"long,omitempty"`
}

// WarmDecision says whether and when to refresh the prompt cache, and why not when it should not.
type WarmDecision struct {
	Warm     bool
	Reason   string        // why not, when Warm is false
	Savings  float64       // dollars a miss would cost more than the refresh
	Lifetime time.Duration // the cache lifetime in force
	Interval time.Duration // how long after the last request to send the refresh
}

// DecideWarm decides whether refreshing the cache of model m is worthwhile, given the last request's usage u. Pi documents the rule only
// as "the model declares a cache lifetime and Pi estimates at least $0.05 in avoided cache-miss cost"; this is Koine's estimate of it: a
// miss re-writes the whole prompt at the cache-write price (the input price when none is set), the refresh reads it at the cache-read
// price, and the difference is the saving. long selects the model's long lifetime when it has one.
func DecideWarm(mode string, m Model, u Usage, long bool) WarmDecision {
	no := func(format string, a ...any) WarmDecision { return WarmDecision{Reason: fmt.Sprintf(format, a...)} }
	if mode != WarmStreaming && mode != WarmIdle {
		return no("cacheWarming is off")
	}
	if m.PromptCache == nil || (m.PromptCache.Short <= 0 && m.PromptCache.Long <= 0) {
		return no("the model declares no cache lifetime")
	}
	secs := m.PromptCache.Short
	if (long && m.PromptCache.Long > 0) || secs <= 0 {
		secs = m.PromptCache.Long
	}
	if m.Cost == nil || u.Prompt() <= 0 {
		return no("no price or no prompt size is known")
	}
	cold := Usage{CacheWrite: u.Prompt()}
	if m.Cost.CacheWrite <= 0 {
		cold = Usage{Input: u.Prompt()}
	}
	miss, _ := m.Of(cold)
	refresh, _ := m.Of(Usage{CacheRead: u.Prompt()})
	saving := miss.Total - refresh.Total
	life := time.Duration(secs) * time.Second
	if saving < WarmMinSavings {
		return WarmDecision{Reason: fmt.Sprintf("a cache miss would cost only $%.3f more than a refresh (under $%.2f)", saving, WarmMinSavings),
			Savings: saving, Lifetime: life}
	}
	interval := life - warmMargin
	if interval < life/2 {
		interval = life / 2
	}
	return WarmDecision{Warm: true, Savings: saving, Lifetime: life, Interval: interval}
}

// WarmUsage is the usage of one refresh request, for the session totals. It never enters the conversation.
type WarmUsage struct {
	Model string
	Usage Usage
}

// Warmer keeps a provider's prompt cache from lapsing by replaying the last request when the cache is about to expire and cancelling it
// at the first token, so that a refresh costs a cache read and a few output tokens. It is driven by the agent: Begin and End bracket a run,
// Busy is called before each request and Observe after each response. Refreshes run on timer goroutines; their usage is collected and
// handed over by Drain.
type Warmer struct {
	Mode     string
	Long     bool // KOINE_CACHE_RETENTION=long (Pi: PI_CACHE_RETENTION): use the model's long lifetime
	Catalog  *Catalog
	Provider func() Provider // the provider to replay requests through; it must be a Streamer, or nothing is sent

	// Now and After are replaceable for tests.
	Now   func() time.Time
	After func(d time.Duration, f func()) (stop func() bool)

	mu        sync.Mutex
	req       Request
	usage     Usage
	have      bool
	running   bool
	idleSince time.Time
	gen       int // bumped whenever pending work is invalidated
	stop      func() bool
	cancel    context.CancelFunc
	pending   []WarmUsage
}

func (w *Warmer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Warmer) after(d time.Duration, f func()) func() bool {
	if w.After != nil {
		return w.After(d, f)
	}
	return time.AfterFunc(d, f).Stop
}

// cancelPending drops the armed timer and any refresh in flight. The caller holds w.mu.
func (w *Warmer) cancelPending() {
	w.gen++
	if w.stop != nil {
		w.stop()
		w.stop = nil
	}
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
}

// Begin marks the start of a run.
func (w *Warmer) Begin() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running = true
}

// Busy is called just before a request is made: whatever refresh was pending is pointless now.
func (w *Warmer) Busy() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancelPending()
}

// End marks the end of a run; in idle mode the cache is then kept warm for WarmIdleWindow.
func (w *Warmer) End() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running = false
	w.idleSince = w.now()
}

// Stop cancels everything; the warmer can be used again.
func (w *Warmer) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancelPending()
}

// Forget stops warming and drops the remembered request, as when the conversation is replaced.
func (w *Warmer) Forget() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancelPending()
	w.have = false
}

// Configure sets the mode, the retention and the catalog; it is safe while refreshes are armed.
func (w *Warmer) Configure(mode string, long bool, c *Catalog) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Mode, w.Long, w.Catalog = mode, long, c
}

// Observe is called after a response: req is the request that was answered and u its usage. It remembers them and arms the next refresh.
func (w *Warmer) Observe(req Request, u Usage) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.req, w.usage, w.have = req, u, true
	w.cancelPending()
	w.arm()
}

// Next reports what the warmer would do now, for display.
func (w *Warmer) Next() WarmDecision {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.have {
		return WarmDecision{Reason: "no request has been made yet"}
	}
	return w.decide()
}

func (w *Warmer) decide() WarmDecision {
	m, ok := w.Catalog.Lookup(w.req.Model)
	if !ok {
		return WarmDecision{Reason: "the model is not in the catalog"}
	}
	return DecideWarm(w.Mode, m, w.usage, w.Long)
}

// arm schedules the next refresh if one is worthwhile. The caller holds w.mu.
func (w *Warmer) arm() {
	if !w.have {
		return
	}
	d := w.decide()
	if !d.Warm {
		return
	}
	gen := w.gen
	w.stop = w.after(d.Interval, func() { w.fire(gen) })
}

// eligible reports whether the cache should still be kept warm at this moment. The caller holds w.mu.
func (w *Warmer) eligible() bool {
	switch {
	case w.running:
		return true
	case w.Mode == WarmIdle:
		return w.now().Sub(w.idleSince) < WarmIdleWindow
	}
	return false
}

// fire sends one refresh, if nothing has changed since it was armed, and arms the next.
func (w *Warmer) fire(gen int) {
	w.mu.Lock()
	if gen != w.gen || !w.eligible() {
		w.mu.Unlock()
		return
	}
	var p Provider
	if w.Provider != nil {
		p = w.Provider()
	}
	s, ok := p.(Streamer)
	if !ok {
		w.mu.Unlock()
		return // a provider that cannot stream cannot be cancelled early, so a refresh would cost a whole answer
	}
	req := w.req
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.mu.Unlock()

	early := false
	resp, err := s.Stream(ctx, req, func(StreamEvent) { early = true; cancel() }) // the first token proves the prompt was read
	cancel()

	w.mu.Lock()
	defer w.mu.Unlock()
	if resp != nil && resp.Usage.Total() > 0 {
		w.pending = append(w.pending, WarmUsage{Model: req.Model, Usage: resp.Usage})
	}
	if gen != w.gen {
		return // a real request began meanwhile and has taken over
	}
	w.cancel = nil
	if err != nil && !early {
		return // the provider refused or failed: stop rather than keep trying
	}
	w.arm()
}

// Drain returns the usage of the refreshes since the last call.
func (w *Warmer) Drain() []WarmUsage {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.pending
	w.pending = nil
	return out
}
