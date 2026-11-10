package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompleteSuccessAndRequestShape(t *testing.T) {
	var got map[string]any
	var key, ver string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ver = r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"},` +
			`{"type":"tool_use","id":"t1","name":"read","input":{"path":"a"}}],"stop_reason":"tool_use"}`))
	}))
	defer srv.Close()
	a := &Anthropic{APIKey: "k", BaseURL: srv.URL}
	resp, err := a.Complete(context.Background(), Request{
		Model: "m", System: "sys",
		Messages: []Message{
			{Role: "user", Content: []Block{{Type: "text", Text: "q"}, {Type: "text", Text: ""}}}, // empty text dropped
			{Role: "assistant", Content: []Block{{Type: "tool_use", ID: "x", Name: "n"}}},         // nil input -> {}
			{Role: "user", Content: []Block{{Type: "text", Text: ""}}},                            // message with no blocks dropped
		},
		Tools: []ToolDef{{Name: "read", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if key != "k" || ver != "2023-06-01" {
		t.Errorf("headers %q %q", key, ver)
	}
	if got["model"] != "m" || got["system"] != "sys" || got["max_tokens"].(float64) != 8192 {
		t.Errorf("body %v", got)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages: %v", msgs)
	}
	if in := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)["input"]; in == nil {
		t.Error("tool_use input must default to {}")
	}
	if len(resp.Content) != 2 || resp.StopReason != "tool_use" || resp.Content[1].Name != "read" {
		t.Errorf("%+v", resp)
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte("slow down"))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()
	a := &Anthropic{APIKey: "k", BaseURL: srv.URL}
	resp, err := a.Complete(context.Background(), Request{Model: "m", MaxTokens: 5})
	if err != nil || resp.Content[0].Text != "ok" || n.Load() != 2 {
		t.Fatalf("%v %v calls=%d", resp, err, n.Load())
	}
}

func TestClientErrorIsNotRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()
	_, err := (&Anthropic{APIKey: "k", BaseURL: srv.URL}).Complete(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || n.Load() != 1 {
		t.Fatalf("%v calls=%d", err, n.Load())
	}
}

func TestBadJSONAndContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer srv.Close()
	if _, err := (&Anthropic{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}).Complete(context.Background(), Request{}); err == nil {
		t.Fatal("expected decode error")
	}
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer fail.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Anthropic{APIKey: "k", BaseURL: fail.URL}).Complete(ctx, Request{}); err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestEnvAndMissing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := NewAnthropicFromEnv(); err == nil {
		t.Fatal("missing key must error")
	}
	t.Setenv("ANTHROPIC_API_KEY", "abc")
	t.Setenv("ANTHROPIC_BASE_URL", "http://example.test/")
	a, err := NewAnthropicFromEnv()
	if err != nil || a.APIKey != "abc" || a.BaseURL != "http://example.test" {
		t.Fatalf("%+v %v", a, err)
	}
	t.Setenv("ANTHROPIC_BASE_URL", "")
	if a, _ = NewAnthropicFromEnv(); a.BaseURL != "https://api.anthropic.com" {
		t.Fatal(a.BaseURL)
	}
	if _, err := (Missing{Reason: "why"}).Complete(context.Background(), Request{}); err == nil || err.Error() != "why" {
		t.Fatal(err)
	}
	if truncate("abcdef", 3) != "abc…" || truncate("ab", 3) != "ab" {
		t.Fatal("truncate")
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRouterResolve(t *testing.T) {
	r := &Router{
		Getenv: env(map[string]string{
			"ANTHROPIC_API_KEY": "a", "OPENAI_API_KEY": "o", "OPENROUTER_API_KEY": "r", "MYKEY": "m", "OPENAI_BASE_URL": "http://proxy/v1",
		}),
		Custom: map[string]ProviderConfig{
			"corp": {BaseURL: "http://corp/v1", APIKeyEnv: "MYKEY"},
			"ant":  {Type: "anthropic", BaseURL: "http://ant", APIKeyEnv: "ANTHROPIC_API_KEY"},
		},
	}
	cases := []struct {
		in, model, kind string
	}{
		{"claude-sonnet-5-5", "claude-sonnet-5-5", "anthropic"},
		{"anthropic/claude-x", "claude-x", "anthropic"},
		{"gpt-5", "gpt-5", "openai"},
		{"o3-mini", "o3-mini", "openai"},
		{"openrouter/anthropic/claude-3.5", "anthropic/claude-3.5", "openai"}, // only the first slash splits
		{"ollama/llama3", "llama3", "openai"},
		{"corp/model-1", "model-1", "openai"},
		{"ant/claude-y", "claude-y", "anthropic"},
	}
	for _, c := range cases {
		p, m, err := r.Resolve(c.in)
		if err != nil || m != c.model {
			t.Errorf("%s: %v model=%q", c.in, err, m)
			continue
		}
		switch p.(type) {
		case *Anthropic:
			if c.kind != "anthropic" {
				t.Errorf("%s -> anthropic", c.in)
			}
		case *OpenAICompat:
			if c.kind != "openai" {
				t.Errorf("%s -> openai", c.in)
			}
		default:
			t.Errorf("%s -> %T", c.in, p)
		}
	}
	if p, _, _ := r.Resolve("gpt-5"); p.(*OpenAICompat).BaseURL != "http://proxy/v1" {
		t.Error("OPENAI_BASE_URL override")
	}
	if p, _, _ := r.Resolve("corp/m"); p.(*OpenAICompat).APIKey != "m" || p.(*OpenAICompat).Name != "corp" {
		t.Error("custom key env")
	}
	// local servers need no key
	if p, _, _ := (&Router{Getenv: env(nil)}).Resolve("ollama/x"); p.(*OpenAICompat).APIKey != "" {
		t.Error("ollama has no key")
	}
}

func TestRouterRejectsInsteadOfGuessing(t *testing.T) {
	r := &Router{Getenv: env(nil)}
	for in, want := range map[string]string{"gemini-2": "cannot infer", "weird/model": "unknown provider prefix"} {
		if _, _, err := r.Resolve(in); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "openrouter") {
			t.Errorf("%s: %v", in, err)
		}
	}
	// a known provider with a missing key fails at call time with the variable name
	for in, want := range map[string]string{"claude-x": "ANTHROPIC_API_KEY", "gpt-5": "OPENAI_API_KEY", "groq/x": "GROQ_API_KEY"} {
		if _, err := r.Complete(context.Background(), Request{Model: in}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
	if _, err := r.Complete(context.Background(), Request{Model: "nope"}); err == nil {
		t.Error("Complete must surface resolve errors")
	}
}

func TestOpenAIRoundTrip(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("authorization")
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":"thinking","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a\"}"}},
			{"id":"c2","type":"function","function":{"name":"bad","arguments":"{oops"}}]}}]}`))
	}))
	defer srv.Close()
	o := &OpenAICompat{Name: "t", APIKey: "k", BaseURL: srv.URL + "/v1/"}
	resp, err := o.Complete(context.Background(), Request{
		Model: "m", System: "sys", MaxTokens: 50,
		Tools: []ToolDef{{Name: "read", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{
			{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []Block{{Type: "text", Text: "ok"}, {Type: "tool_use", ID: "c0", Name: "ls"}}},
			{Role: "user", Content: []Block{
				{Type: "tool_result", ToolUseID: "c0", Content: "files"},
				{Type: "tool_result", ToolUseID: "c9", Content: "boom", IsError: true}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" || got["max_tokens"].(float64) != 50 || got["model"] != "m" {
		t.Errorf("request %v auth=%q", got, auth)
	}
	msgs := got["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,tool" {
		t.Fatalf("roles %v", roles)
	}
	asst := msgs[2].(map[string]any)
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "c0" || tc["function"].(map[string]any)["arguments"] != "{}" { // nil input -> {}
		t.Errorf("assistant tool_call %v", tc)
	}
	if msgs[4].(map[string]any)["content"] != "Error: boom" || msgs[3].(map[string]any)["tool_call_id"] != "c0" {
		t.Errorf("tool messages %v %v", msgs[3], msgs[4])
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	if tool["type"] != "function" || tool["function"].(map[string]any)["name"] != "read" {
		t.Errorf("tools %v", tool)
	}
	if resp.StopReason != "tool_calls" || len(resp.Content) != 3 || resp.Content[0].Text != "thinking" ||
		resp.Content[1].Name != "read" || string(resp.Content[1].Input) != `{"path":"a"}` || string(resp.Content[2].Input) != "{}" {
		t.Errorf("response %+v", resp)
	}
}

func TestOpenAIErrors(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }))
	defer empty.Close()
	if _, err := (&OpenAICompat{BaseURL: empty.URL}).Complete(context.Background(), Request{}); err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Errorf("%v", err)
	}
	notJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer notJSON.Close()
	if _, err := (&OpenAICompat{BaseURL: notJSON.URL}).Complete(context.Background(), Request{}); err == nil {
		t.Error("decode error expected")
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer denied.Close()
	_, err := (&OpenAICompat{Name: "grok", BaseURL: denied.URL}).Complete(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "grok: HTTP 401") {
		t.Errorf("%v", err)
	}
	// plain text reply, no tool calls, nil content tolerated
	txt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":null}}]}`))
	}))
	defer txt.Close()
	if r, err := (&OpenAICompat{BaseURL: txt.URL}).Complete(context.Background(), Request{}); err != nil || len(r.Content) != 0 {
		t.Errorf("%v %v", r, err)
	}
}
