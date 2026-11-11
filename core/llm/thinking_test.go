package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// systemText reads the "system" field of an Anthropic request, which is a string or an array of text blocks.
func systemText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var parts []string
	arr, _ := v.([]any)
	for _, b := range arr {
		m, _ := b.(map[string]any)
		parts = append(parts, m["text"].(string))
	}
	return strings.Join(parts, "")
}

// serve runs an Anthropic-style endpoint that records the requests and answers with reply.
func serve(t *testing.T, reply string, seen *[]map[string]any) *Anthropic {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		*seen = append(*seen, m)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return &Anthropic{APIKey: "k", BaseURL: srv.URL}
}

func TestThinkingResolve(t *testing.T) {
	c := DefaultCatalog()
	_ = c.Merge("/nonexistent")
	c.Models = append(c.Models,
		Model{ID: "budget-model", Provider: "anthropic", Reasoning: &Reasoning{Mode: "budget"}},
		Model{ID: "plain-model", Provider: "x"},
		Model{ID: "o-like", Provider: "openai", Reasoning: &Reasoning{Mode: "effort", Max: "xhigh"}})
	cases := []struct {
		name  string
		s     ThinkingSettings
		model string
		want  *Thinking
	}{
		{"default is medium on a reasoning model", ThinkingSettings{}, "claude-sonnet-5-5", &Thinking{Level: "medium", Mode: "adaptive", Off: "between_tools"}},
		{"default setting", ThinkingSettings{Default: "high"}, "claude-sonnet-5-5", &Thinking{Level: "high", Mode: "adaptive", Off: "between_tools"}},
		{"explicit beats settings", ThinkingSettings{Level: "low", Default: "high"}, "claude-haiku-5-5",
			&Thinking{Level: "low", Mode: "adaptive", Off: "disabled"}},
		{"per model by provider/id", ThinkingSettings{Default: "high", PerModel: map[string]string{"anthropic/claude-sonnet-5-5": "minimal"}},
			"claude-sonnet-5-5", &Thinking{Level: "minimal", Mode: "adaptive", Off: "between_tools"}},
		{"per model by exact key", ThinkingSettings{PerModel: map[string]string{"anthropic/claude-sonnet-5-5": "low"}},
			"anthropic/claude-sonnet-5-5", &Thinking{Level: "low", Mode: "adaptive", Off: "between_tools"}},
		{"invalid levels are ignored", ThinkingSettings{Default: "banana", PerModel: map[string]string{"claude-opus-5-5": "x"}},
			"claude-opus-5-5", &Thinking{Level: "medium", Mode: "adaptive", Off: "omit"}},
		{"budget mode uses the budget", ThinkingSettings{Level: "low"}, "budget-model", &Thinking{Level: "low", Mode: "budget", Budget: 2048}},
		{"budget from settings, floor 1024", ThinkingSettings{Level: "high", Budgets: map[string]int{"high": 500}}, "budget-model",
			&Thinking{Level: "high", Mode: "budget", Budget: 1024}},
		{"levels above high lower to high's budget", ThinkingSettings{Level: "max", Budgets: map[string]int{"high": 9000}}, "budget-model",
			&Thinking{Level: "high", Mode: "budget", Budget: 9000}},
		{"off has no budget", ThinkingSettings{Level: "off"}, "budget-model", &Thinking{Level: "off", Mode: "budget"}},
		{"effort model lowers to its maximum", ThinkingSettings{Level: "max"}, "o-like", &Thinking{Level: "xhigh", Mode: "effort"}},
		{"a model that does not think", ThinkingSettings{Level: "high"}, "plain-model", nil},
		{"unknown model, nothing asked", ThinkingSettings{Default: "high"}, "gpt-4o", nil},
		{"unknown model, default does not count", ThinkingSettings{Default: "high"}, "o3", nil},
		{"unknown claude, explicit", ThinkingSettings{Level: "low"}, "claude-future-1", &Thinking{Level: "low", Mode: "budget", Budget: 2048}},
		{"unknown other, explicit lowers to high", ThinkingSettings{Level: "xhigh"}, "o3", &Thinking{Level: "high", Mode: "effort"}},
		{"unknown, per-model setting is explicit", ThinkingSettings{PerModel: map[string]string{"openai/o3": "low"}}, "openai/o3",
			&Thinking{Level: "low", Mode: "effort"}},
	}
	for _, tc := range cases {
		got := tc.s.Resolve(c, tc.model)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
	if lvl, explicit := (ThinkingSettings{}).Effective(nil, "x"); lvl != "medium" || explicit {
		t.Errorf("effective with no catalog: %q %v", lvl, explicit)
	}
}

func TestSplitThinkingSuffix(t *testing.T) {
	cases := [][3]string{{"claude-sonnet-5-5:high", "claude-sonnet-5-5", "high"}, {"openrouter/x/y:off", "openrouter/x/y", "off"},
		{"ollama/llama3:8b", "ollama/llama3:8b", ""}, {"plain", "plain", ""}, {":high", ":high", ""}, {"m:max", "m", "max"}}
	for _, c := range cases {
		if m, l := SplitThinkingSuffix(c[0]); m != c[1] || l != c[2] {
			t.Errorf("%q: got %q %q", c[0], m, l)
		}
	}
	for _, l := range ThinkingLevels {
		if !ValidLevel(l) {
			t.Errorf("%s should be valid", l)
		}
	}
	if ValidLevel("") || ValidLevel("medium ") || ValidLevel("ultra") {
		t.Error("only Pi's level names are valid")
	}
}

func TestReasoningCatalogFlag(t *testing.T) {
	var c Catalog
	js := `{"models":[{"id":"a","reasoning":true},{"id":"b","reasoning":false},{"id":"c","reasoning":{"mode":"effort","max":"xhigh"}},{"id":"d"}]}`
	if err := json.Unmarshal([]byte(js), &c); err != nil {
		t.Fatal(err)
	}
	s := ThinkingSettings{Level: "low"}
	if th := s.Resolve(&c, "a"); th == nil || th.Level != "low" {
		t.Errorf("true should mean the provider's defaults: %+v", th)
	}
	if th := s.Resolve(&c, "b"); th != nil {
		t.Errorf("false means the model does not think: %+v", th)
	}
	if th := s.Resolve(&c, "c"); th == nil || th.Mode != "effort" {
		t.Errorf("object form: %+v", th)
	}
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		if m, _ := DefaultCatalog().Lookup(id); m.Reasoning == nil || m.Reasoning.Mode != ModeAdaptive {
			t.Errorf("%s should be an adaptive reasoning model", id)
		}
	}
	if err := json.Unmarshal([]byte(`{"models":[{"id":"x","reasoning":"yes"}]}`), &c); err == nil {
		t.Error("a string is not a reasoning value")
	}
}

func TestAnthropicThinkingRequests(t *testing.T) {
	var seen []map[string]any
	a := serve(t, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`, &seen)
	user := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "q"}}}}
	call := func(th *Thinking, max int) map[string]any {
		if _, err := a.Complete(context.Background(), Request{Model: "m", Messages: user, Thinking: th, MaxTokens: max}); err != nil {
			t.Fatal(err)
		}
		return seen[len(seen)-1]
	}
	// adaptive: thinking type plus effort; minimal is Anthropic's low
	b := call(&Thinking{Level: "xhigh", Mode: ModeAdaptive}, 0)
	if b["thinking"].(map[string]any)["type"] != "adaptive" || b["output_config"].(map[string]any)["effort"] != "xhigh" {
		t.Errorf("adaptive: %v", b)
	}
	if b = call(&Thinking{Level: "minimal", Mode: ModeAdaptive}, 0); b["output_config"].(map[string]any)["effort"] != "low" {
		t.Errorf("minimal should be low: %v", b["output_config"])
	}
	// off: disabled by default, omitted, or another type
	if b = call(&Thinking{Level: "off", Mode: ModeAdaptive}, 0); b["thinking"].(map[string]any)["type"] != "disabled" || b["output_config"] != nil {
		t.Errorf("off: %v", b)
	}
	if b = call(&Thinking{Level: "off", Mode: ModeAdaptive, Off: "omit"}, 0); b["thinking"] != nil {
		t.Errorf("off+omit: %v", b)
	}
	if b = call(&Thinking{Level: "off", Mode: ModeAdaptive, Off: "between_tools"}, 0); b["thinking"].(map[string]any)["type"] != "between_tools" {
		t.Errorf("off+between_tools: %v", b)
	}
	// nil sends nothing
	if b = call(nil, 0); b["thinking"] != nil || b["output_config"] != nil {
		t.Errorf("nil thinking: %v", b)
	}
	// budget: enabled with budget_tokens, and max_tokens makes room
	b = call(&Thinking{Level: "medium", Mode: ModeBudget, Budget: 8192}, 0)
	th := b["thinking"].(map[string]any)
	if th["type"] != "enabled" || th["budget_tokens"].(float64) != 8192 || b["max_tokens"].(float64) != 8192+8192 {
		t.Errorf("budget, default max: %v", b)
	}
	b = call(&Thinking{Level: "high", Mode: ModeBudget, Budget: 16384}, 20000)
	if b["thinking"].(map[string]any)["budget_tokens"].(float64) != 16384 || b["max_tokens"].(float64) != 20000 {
		t.Errorf("budget fits: %v", b)
	}
	b = call(&Thinking{Level: "high", Mode: ModeBudget, Budget: 16384}, 4000)
	if b["thinking"].(map[string]any)["budget_tokens"].(float64) != 4000-1024 || b["max_tokens"].(float64) != 4000 {
		t.Errorf("an explicit max_tokens is kept and the budget shrinks: %v", b)
	}
	if b = call(&Thinking{Level: "high", Mode: ModeBudget, Budget: 16384}, 1500); b["thinking"] != nil {
		t.Errorf("no room to think: %v", b)
	}
	if b = call(&Thinking{Mode: ModeBudget}, 0); b["thinking"] != nil {
		t.Errorf("no level, no thinking: %v", b)
	}
}

func TestAnthropicThinkingBlocksAreKeptAndReplayedWithSignatures(t *testing.T) {
	var seen []map[string]any
	reply := `{"content":[{"type":"thinking","thinking":"hmm","signature":"sig1"},{"type":"redacted_thinking","data":"enc"},` +
		`{"type":"tool_use","id":"t1","name":"ls","input":{}}],"stop_reason":"tool_use"}`
	a := serve(t, reply, &seen)
	user := Message{Role: "user", Content: []Block{{Type: "text", Text: "q"}}}
	resp, err := a.Complete(context.Background(), Request{Model: "m", Messages: []Message{user}, Thinking: &Thinking{Level: "low", Mode: ModeAdaptive}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 3 || resp.Content[0].Thinking != "hmm" || resp.Content[0].Signature != "sig1" || resp.Content[0].Type != "thinking" {
		t.Fatalf("thinking block: %+v", resp.Content)
	}
	if r := resp.Content[1]; r.Type != "thinking" || !r.Redacted || r.Signature != "enc" || r.Thinking != "" {
		t.Fatalf("redacted block: %+v", r)
	}
	hist := []Message{user, {Role: "assistant", Content: append(resp.Content, Block{Type: "thinking", Thinking: "unsigned"})},
		{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "t1", Content: "files"}}}}
	if _, err := a.Complete(context.Background(), Request{Model: "m", Messages: hist, Thinking: &Thinking{Level: "low", Mode: ModeAdaptive}}); err != nil {
		t.Fatal(err)
	}
	asst := seen[1]["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if len(asst) != 3 {
		t.Fatalf("the unsigned block must be dropped: %v", asst)
	}
	first, second := asst[0].(map[string]any), asst[1].(map[string]any)
	if first["type"] != "thinking" || first["thinking"] != "hmm" || first["signature"] != "sig1" || first["redacted"] != nil {
		t.Errorf("replayed thinking: %v", first)
	}
	if second["type"] != "redacted_thinking" || second["data"] != "enc" || second["signature"] != nil || second["thinking"] != nil {
		t.Errorf("replayed redacted thinking: %v", second)
	}
	if asst[2].(map[string]any)["type"] != "tool_use" {
		t.Errorf("order must be kept: %v", asst)
	}
	// disabled thinking: earlier blocks are not sent back
	if _, err := a.Complete(context.Background(), Request{Model: "m", Messages: hist, Thinking: &Thinking{Level: "off", Mode: ModeAdaptive}}); err != nil {
		t.Fatal(err)
	}
	if asst = seen[2]["messages"].([]any)[1].(map[string]any)["content"].([]any); len(asst) != 1 {
		t.Errorf("thinking disabled should drop the blocks: %v", asst)
	}
}

func TestAnthropicPromptCachingBreakpoints(t *testing.T) {
	var seen []map[string]any
	a := serve(t, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":5,"output_tokens":7,`+
		`"cache_read_input_tokens":1000,"cache_creation_input_tokens":200}}`, &seen)
	tools := []ToolDef{{Name: "a", InputSchema: json.RawMessage(`{}`)}, {Name: "b", InputSchema: json.RawMessage(`{}`)}}
	msgs := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "one"}}}, {Role: "assistant", Content: []Block{{Type: "text", Text: "two"}}},
		{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "x", Content: "r1"}, {Type: "tool_result", ToolUseID: "y", Content: "r2"}}}}
	resp, err := a.Complete(context.Background(), Request{Model: "m", System: "sys", Messages: msgs, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	b := seen[0]
	marked := func(m any) bool { return m.(map[string]any)["cache_control"].(map[string]any)["type"] == "ephemeral" }
	sys := b["system"].([]any)
	if len(sys) != 1 || !marked(sys[0]) || sys[0].(map[string]any)["text"] != "sys" {
		t.Errorf("system: %v", sys)
	}
	ts := b["tools"].([]any)
	if ts[0].(map[string]any)["cache_control"] != nil || !marked(ts[1]) {
		t.Errorf("only the last tool is marked: %v", ts)
	}
	ms := b["messages"].([]any)
	for i, want := range []bool{false, false, true} {
		cs := ms[i].(map[string]any)["content"].([]any)
		for j, c := range cs {
			isLast := i == len(ms)-1 && j == len(cs)-1
			if has := c.(map[string]any)["cache_control"] != nil; has != (want && isLast) {
				t.Errorf("message %d block %d: cache_control=%v", i, j, has)
			}
		}
	}
	if msgs[2].Content[1].CacheControl != nil || tools[1].CacheControl != nil {
		t.Error("the caller's request must not be changed")
	}
	if u := resp.Usage; u.CacheRead != 1000 || u.CacheWrite != 200 || u.Input != 5 || u.Output != 7 {
		t.Errorf("cache usage: %+v", u)
	}
	// no system prompt, no tools, last message from the assistant: nothing to mark
	if _, err := a.Complete(context.Background(), Request{Model: "m", Messages: msgs[:2]}); err != nil {
		t.Fatal(err)
	}
	lastBlock := seen[1]["messages"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if seen[1]["system"] != nil || seen[1]["tools"] != nil || lastBlock["cache_control"] != nil {
		t.Errorf("bare request: %v", seen[1])
	}
}

func TestOpenAIReasoningEffortAndReasoningContent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = nil
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer","reasoning_content":"because"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	o := &OpenAICompat{Name: "x", BaseURL: srv.URL}
	msgs := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "q"}}}}
	resp, err := o.Complete(context.Background(), Request{Model: "o3", Messages: msgs, MaxTokens: 100, Thinking: &Thinking{Level: "max", Mode: ModeEffort}})
	if err != nil {
		t.Fatal(err)
	}
	if got["reasoning_effort"] != "xhigh" || got["max_completion_tokens"].(float64) != 100 || got["max_tokens"] != nil {
		t.Errorf("effort request: %v", got)
	}
	if len(resp.Content) != 2 || resp.Content[0].Type != "thinking" || resp.Content[0].Thinking != "because" || resp.Content[0].Signature != "" ||
		resp.Content[1].Text != "answer" {
		t.Errorf("response: %+v", resp.Content)
	}
	for _, tc := range []struct {
		th   *Thinking
		want any
	}{{&Thinking{Level: "minimal"}, "minimal"}, {&Thinking{Level: "high"}, "high"}, {&Thinking{Level: "off"}, nil}, {nil, nil}} {
		if _, err := o.Complete(context.Background(), Request{Model: "o3", Messages: msgs, MaxTokens: 100, Thinking: tc.th}); err != nil {
			t.Fatal(err)
		}
		if got["reasoning_effort"] != tc.want {
			t.Errorf("%+v: reasoning_effort %v, want %v", tc.th, got["reasoning_effort"], tc.want)
		}
		if tc.want == nil && (got["max_tokens"] == nil || got["max_completion_tokens"] != nil) {
			t.Errorf("without reasoning max_tokens stays: %v", got)
		}
	}
	// thinking blocks in the history are not sent to an OpenAI-compatible server
	hist := append(msgs, Message{Role: "assistant", Content: []Block{{Type: "thinking", Thinking: "secret"}, {Type: "text", Text: "a"}}})
	if _, err := o.Complete(context.Background(), Request{Model: "o3", Messages: hist}); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(got["messages"]); strings.Contains(string(b), "secret") {
		t.Errorf("thinking leaked into the request: %s", b)
	}
}
