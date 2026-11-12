package llm

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEmbeddedCatalogIsValid(t *testing.T) {
	c := DefaultCatalog()
	if len(c.Models) == 0 {
		t.Fatal("no models")
	}
	seen := map[string]bool{}
	for _, m := range c.Models {
		if m.ID == "" || m.ContextWindow <= 0 || m.Cost == nil || m.Cost.Input <= 0 || m.Cost.Output <= 0 || seen[m.ID] {
			t.Errorf("bad or duplicate entry: %+v", m)
		}
		seen[m.ID] = true
	}
}

func TestLookupAcceptsProviderPrefixAndDatedSuffix(t *testing.T) {
	c := DefaultCatalog()
	for _, id := range []string{"claude-sonnet-5-5", "anthropic/claude-sonnet-5-5", "claude-sonnet-5-5-20261001"} {
		if m, ok := c.Lookup(id); !ok || m.ID != "claude-sonnet-5-5" {
			t.Errorf("%s: %+v %v", id, m, ok)
		}
	}
	for _, id := range []string{"", "gpt-9", "claude-sonnet-5-5-latest", "claude-sonnet-5-5-2026"} {
		if _, ok := c.Lookup(id); ok {
			t.Errorf("%q must not match", id)
		}
	}
	var none *Catalog
	if _, ok := none.Lookup("claude-sonnet-5-5"); ok {
		t.Error("nil catalog knows nothing")
	}
}

func TestCostOfUsage(t *testing.T) {
	m := Model{Cost: &Price{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}}
	c, ok := m.Of(Usage{Input: 1_000_000, Output: 500_000, CacheRead: 1_000_000, CacheWrite: 1_000_000})
	if !ok || !near(c.Input, 2) || !near(c.Output, 5) || !near(c.CacheRead, 0.1) || !near(c.CacheWrite, 2.5) || !near(c.Total, 9.6) {
		t.Fatalf("%+v %v", c, ok)
	}
	if _, ok := (Model{}).Of(Usage{Input: 5}); ok {
		t.Error("a model without prices has no cost")
	}
	long := Model{Cost: &Price{Input: 1, Output: 1}, LongContext: &LongContext{Threshold: 100, Multiplier: 5}}
	if c, _ := long.Of(Usage{Input: 100, Output: 100}); !near(c.Total, 200e-6) {
		t.Errorf("at the threshold the normal rate applies: %v", c.Total)
	}
	if c, _ := long.Of(Usage{Input: 101, Output: 100}); !near(c.Total, 201e-6*5) {
		t.Errorf("past the threshold every rate is multiplied: %v", c.Total)
	}
}

func TestUsageArithmetic(t *testing.T) {
	var u Usage
	u.Add(Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4})
	u.Add(Usage{Input: 1})
	if u.Total() != 11 || u.Prompt() != 9 {
		t.Errorf("%+v %d %d", u, u.Total(), u.Prompt())
	}
}

func TestMergeReplacesAddsAndRejects(t *testing.T) {
	dir := t.TempDir()
	c := DefaultCatalog()
	n := len(c.Models)
	if err := c.Merge(filepath.Join(dir, "missing.json")); err != nil || len(c.Models) != n {
		t.Fatalf("a missing file is fine: %v", err)
	}
	f := filepath.Join(dir, "m.json")
	body := `{"models":[{"id":"claude-sonnet-5-5","contextWindow":42,"cost":{"input":1,"output":2}},{"id":"mine","contextWindow":7}]}`
	_ = os.WriteFile(f, []byte(body), 0o600)
	if err := c.Merge(f); err != nil || len(c.Models) != n+1 {
		t.Fatalf("%v %d", err, len(c.Models))
	}
	if m, _ := c.Lookup("claude-sonnet-5-5"); m.ContextWindow != 42 {
		t.Errorf("same id replaces: %+v", m)
	}
	if m, ok := c.Lookup("mine"); !ok || m.ContextWindow != 7 {
		t.Errorf("new id is added: %+v", m)
	}
	_ = os.WriteFile(f, []byte(`{"models":[{"contextWindow":1}]}`), 0o600)
	if c.Merge(f) == nil {
		t.Error("a model without an id must be rejected")
	}
	_ = os.WriteFile(f, []byte(`nope`), 0o600)
	if c.Merge(f) == nil {
		t.Error("bad JSON must be rejected")
	}
	if c.Merge(dir) == nil {
		t.Error("an unreadable path must be an error")
	}
}

func TestProvidersReportUsage(t *testing.T) {
	anth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn",` +
			`"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`))
	}))
	defer anth.Close()
	resp, err := (&Anthropic{APIKey: "k", BaseURL: anth.URL}).Complete(context.Background(),
		Request{Model: "m", Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "q"}}}}})
	if err != nil || resp.Usage != (Usage{Input: 10, Output: 5, CacheRead: 3, CacheWrite: 2}) {
		t.Fatalf("anthropic: %+v %v", resp, err)
	}

	oai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"hi"}}],` +
			`"usage":{"prompt_tokens":100,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":60}}}`))
	}))
	defer oai.Close()
	resp, err = (&OpenAICompat{Name: "t", BaseURL: oai.URL}).Complete(context.Background(),
		Request{Model: "m", Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "q"}}}}})
	if err != nil || resp.Usage != (Usage{Input: 40, Output: 7, CacheRead: 60}) {
		t.Fatalf("openai: cached tokens are counted apart from input: %+v %v", resp, err)
	}
}

// The prices and windows below are the ones Pi's catalog (https://pi.dev/models) lists, so a change here is a deliberate departure from Pi.
func TestClaudeEntriesMatchPisCatalog(t *testing.T) {
	c := DefaultCatalog()
	for id, want := range map[string]struct {
		window int
		price  Price
	}{
		"claude-opus-5-5":   {1_000_000, Price{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5}},
		"claude-sonnet-5-5": {1_000_000, Price{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}},
		"claude-haiku-5-5":  {1_000_000, Price{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}},
		"claude-fable-5-2":  {1_000_000, Price{Input: 8, Output: 40, CacheRead: 0.2, CacheWrite: 10}},
		"claude-haiku-4-5":  {200_000, Price{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}},
		"claude-sonnet-4-5": {200_000, Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}},
	} {
		m, ok := c.Lookup(id)
		if !ok || m.ContextWindow != want.window || m.Cost == nil || *m.Cost != want.price {
			t.Errorf("%s: %+v", id, m)
		}
	}
	if m, ok := c.Lookup("claude-haiku-4-5-20251001"); !ok || m.ID != "claude-haiku-4-5" {
		t.Errorf("a dated id resolves to its alias entry: %+v %v", m, ok)
	}
	if m, _ := c.Lookup("claude-haiku-5-5"); m.LongContext != nil {
		t.Error("Pi's catalog lists a flat price for Haiku 5.5: no long-context multiplier")
	}
}
