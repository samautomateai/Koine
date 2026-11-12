package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func cachedRequest(long bool) Request {
	return Request{Model: "claude-sonnet-5-5", System: "sys", Cache: CacheHints{Key: "session-1", Long: long},
		Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "hi"}}}},
		Tools:    []ToolDef{{Name: "read", InputSchema: json.RawMessage(`{}`)}}}
}

func TestAnthropicLongCacheRetentionOnlyOnAnthropicsEndpoint(t *testing.T) {
	body := func(base string, long bool) string {
		_, _, payload, err := (&Anthropic{BaseURL: base}).request(cachedRequest(long), false)
		if err != nil {
			t.Fatal(err)
		}
		return string(payload)
	}
	if got := body("https://api.anthropic.com", true); strings.Count(got, `"cache_control":{"type":"ephemeral","ttl":"1h"}`) != 3 {
		t.Errorf("system, last tool and last user message should carry the one-hour ttl: %s", got)
	}
	for name, got := range map[string]string{"short": body("https://api.anthropic.com", false), "proxy": body("http://proxy.local", true),
		"bad url": body("://", true)} {
		if strings.Contains(got, `"ttl"`) || strings.Count(got, `"cache_control":{"type":"ephemeral"}`) != 3 {
			t.Errorf("%s: default ephemeral caching without a ttl expected: %s", name, got)
		}
	}
}

func TestOpenAIPromptCacheKeyAndRetention(t *testing.T) {
	req := cachedRequest(true)
	chat := func(on bool) map[string]any {
		_, _, payload, _ := (&OpenAICompat{PromptCache: on}).request(req, false)
		var m map[string]any
		_ = json.Unmarshal(payload, &m)
		return m
	}
	if m := chat(true); m["prompt_cache_key"] != "session-1" || m["prompt_cache_retention"] != "24h" {
		t.Errorf("chat completions: %v", m)
	}
	if m := chat(false); m["prompt_cache_key"] != nil || m["prompt_cache_retention"] != nil {
		t.Errorf("a server that is not OpenAI must not get the fields: %v", m)
	}
	resp := func(on bool, r Request) map[string]any {
		_, _, payload, _ := (&Responses{PromptCache: on}).request(r, false)
		var m map[string]any
		_ = json.Unmarshal(payload, &m)
		return m
	}
	if m := resp(true, req); m["prompt_cache_key"] != "session-1" || m["prompt_cache_retention"] != "24h" {
		t.Errorf("responses: %v", m)
	}
	short := req
	short.Cache = CacheHints{Key: "k"}
	if m := resp(true, short); m["prompt_cache_key"] != "k" || m["prompt_cache_retention"] != nil {
		t.Errorf("short retention sends the key only: %v", m)
	}
	short.Cache = CacheHints{}
	if m := resp(true, short); m["prompt_cache_key"] != nil {
		t.Errorf("no session, no key: %v", m)
	}
}

func TestRouterEnablesPromptCacheForOpenAIAndAzureOnly(t *testing.T) {
	env := func(k string) string {
		switch k {
		case "OPENAI_API_KEY", "AZURE_OPENAI_API_KEY":
			return "fake"
		case "AZURE_OPENAI_RESOURCE_NAME":
			return "res"
		}
		return ""
	}
	r := &Router{Getenv: env}
	if o := mustResolve(t, r, "openai/gpt-5").(*OpenAICompat); !o.PromptCache {
		t.Error("api.openai.com takes prompt_cache_key")
	}
	if a := mustResolve(t, r, "azure/gpt-5").(*Responses); !a.PromptCache {
		t.Error("Azure OpenAI takes prompt_cache_key")
	}
	r.Custom = map[string]ProviderConfig{"openai": {BaseURL: "http://proxy.local/v1"}, "mine": {BaseURL: "http://x/v1", API: APIResponses}}
	if o := mustResolve(t, r, "openai/gpt-5").(*OpenAICompat); o.PromptCache {
		t.Error("a proxy in front of OpenAI is not known to accept it")
	}
	if o := mustResolve(t, r, "mine/m").(*Responses); o.PromptCache {
		t.Error("a custom Responses server is not known to accept it")
	}
	viaResponses := &Router{Getenv: env, Custom: map[string]ProviderConfig{"openai": {API: APIResponses}}}
	if p := mustResolve(t, viaResponses, "openai/gpt-5").(*Responses); !p.PromptCache {
		t.Error("OpenAI's Responses API takes it too")
	}
}
