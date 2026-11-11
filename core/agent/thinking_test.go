package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"koine/core/agents"
	"koine/core/llm"
)

func thinkingCatalog() *llm.Catalog {
	return &llm.Catalog{Models: []llm.Model{{ID: "m", Provider: "anthropic", MaxOutput: 20000, Reasoning: &llm.Reasoning{Mode: llm.ModeAdaptive}}}}
}

func think(text, sig string) llm.Block {
	return llm.Block{Type: "thinking", Thinking: text, Signature: sig}
}

func TestThinkingLevelReachesTheRequestAndTheMessage(t *testing.T) {
	p := &script{turns: [][]llm.Block{{think("let me see", "sig"), llm.Block{Type: "thinking", Redacted: true, Signature: "enc"}, use("1", "echo", `{}`)}}}
	a := newAgent(p)
	a.Catalog = thinkingCatalog()
	a.Thinking = llm.ThinkingSettings{Default: "high"}
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 2 {
		t.Fatalf("requests: %d", len(p.reqs))
	}
	th := p.reqs[0].Thinking
	if th == nil || th.Level != "high" || th.Mode != llm.ModeAdaptive {
		t.Errorf("thinking in the request: %+v", th)
	}
	if p.reqs[0].MaxTokens != 20000 {
		t.Errorf("thinking with no limit set asks for the model's output limit (capped at 32000): %d", p.reqs[0].MaxTokens)
	}
	// the blocks are in the history, in order, and the second request carries them back
	h := a.History[1]
	if h.ThinkingLevel != "high" || len(h.Content) != 3 || h.Content[0].Signature != "sig" || !h.Content[1].Redacted || h.Content[2].Type != "tool_use" {
		t.Errorf("assistant message: %+v", h)
	}
	if got := p.reqs[1].Messages[1].Content; len(got) != 3 || got[0].Thinking != "let me see" {
		t.Errorf("replayed history: %+v", got)
	}
	// the readable block is an event; the redacted one is not
	var shown []string
	for _, e := range evs {
		if e.Type == "thinking" {
			shown = append(shown, e.Text)
		}
	}
	if len(shown) != 1 || shown[0] != "let me see" {
		t.Errorf("thinking events: %v", shown)
	}
	if a.ThinkingLevel() != "high" {
		t.Errorf("ThinkingLevel: %q", a.ThinkingLevel())
	}
}

func TestHideThinkingKeepsTheBlocksButNotTheEvents(t *testing.T) {
	p := &script{turns: [][]llm.Block{{think("secret plan", "sig"), txt("answer")}}}
	a := newAgent(p)
	a.Catalog = thinkingCatalog()
	a.HideThinking = true
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == "thinking" || strings.Contains(e.Text, "secret") {
			t.Errorf("hidden thinking leaked: %+v", e)
		}
	}
	if a.History[1].Content[0].Thinking != "secret plan" {
		t.Errorf("the block must stay in the history: %+v", a.History[1])
	}
}

func TestNoThinkingForModelsThatDoNotThink(t *testing.T) {
	p := &script{}
	a := newAgent(p)
	a.MaxTokens = 123
	a.Thinking = llm.ThinkingSettings{Default: "high"}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if p.reqs[0].Thinking != nil || p.reqs[0].MaxTokens != 123 || a.ThinkingLevel() != "" {
		t.Errorf("an unknown model with no explicit level gets no thinking: %+v", p.reqs[0])
	}
	// off sends a request with level off and leaves max tokens alone
	a = newAgent(&script{})
	a.Catalog = thinkingCatalog()
	a.Thinking = llm.ThinkingSettings{Level: "off"}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	s := a.Provider.(*script)
	if s.reqs[0].Thinking == nil || s.reqs[0].Thinking.Level != "off" || s.reqs[0].MaxTokens != 0 {
		t.Errorf("off: %+v", s.reqs[0])
	}
	// no catalog entry for max output: the default cap applies
	a = newAgent(&script{})
	a.Thinking = llm.ThinkingSettings{Level: "low"}
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if got := a.Provider.(*script).reqs[0].MaxTokens; got != maxThinkingOutput {
		t.Errorf("max tokens %d", got)
	}
}

func TestSubAgentsInheritThinkingSettings(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("t", "task", `{"agent":"reviewer","prompt":"check it"}`)}, {txt("sub answer")}}}
	a := newAgent(s)
	a.Catalog = thinkingCatalog()
	a.Thinking = llm.ThinkingSettings{Level: "low"}
	a.Defs = []agents.Def{{Name: "reviewer", Description: "r", Prompt: "YOU REVIEW"}}
	a.Tools.Register(a.TaskTool())
	if _, err := collect(a, "go"); err != nil {
		t.Fatal(err)
	}
	if th := s.reqs[1].Thinking; th == nil || th.Level != "low" {
		t.Errorf("the sub-agent's request: %+v", th)
	}
}

func TestRetriesAreReportedAsNotices(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("down"))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()
	a := newAgent(&llm.Anthropic{APIKey: "k", BaseURL: srv.URL, Retry: llm.RetryPolicy{BaseDelay: time.Millisecond}})
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, e := range evs {
		if e.Type == "notice" {
			notes = append(notes, e.Text)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "retrying in 0s (1 of 3)") || !strings.Contains(notes[0], "HTTP 503: down") {
		t.Errorf("notices: %v", notes)
	}
}
