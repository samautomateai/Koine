package llm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func raw(s string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return m
}

func TestCredentialPrecedenceIsRuntimeStoredModelsFileThenEnvironment(t *testing.T) {
	vars := map[string]string{"GROQ_API_KEY": "env-key"}
	stored := func(p string) (string, map[string]string, bool, error) { return "stored-key", nil, p == "groq", nil }
	r := &Router{Getenv: env(vars), Secret: func(v string) (string, error) { return strings.Replace(v, "$MODELS_KEY", vars["MODELS_KEY"], 1), nil }}
	key := func() Credential {
		t.Helper()
		c, err := r.Credential("groq")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := key(); c.Key != "env-key" || c.Source != "env:GROQ_API_KEY" {
		t.Errorf("env: %+v", c)
	}
	r.Custom = map[string]ProviderConfig{"groq": {APIKey: "$MODELS_KEY"}}
	vars["MODELS_KEY"] = "models-key"
	if c := key(); c.Key != "models-key" || c.Source != "models.json" {
		t.Errorf("models.json beats env: %+v", c)
	}
	r.Stored = stored
	if c := key(); c.Key != "stored-key" || c.Source != "auth.json" {
		t.Errorf("auth.json beats models.json: %+v", c)
	}
	r.RuntimeKeys = map[string]string{"groq": "flag-key"}
	if c := key(); c.Key != "flag-key" || c.Source != "--api-key" {
		t.Errorf("--api-key beats auth.json: %+v", c)
	}
	// the flag is for one provider only
	if c, err := r.Credential("openai"); err == nil {
		t.Errorf("openai should have nothing: %+v", c)
	}
	// the key reaches the provider that is built
	p, id, err := r.Resolve("groq/llama")
	if oc, ok := p.(*OpenAICompat); err != nil || !ok || oc.APIKey != "flag-key" || id != "llama" || oc.Name != "groq" {
		t.Errorf("resolve: %T %v", p, err)
	}
}

func TestStoredEntryEnvWinsAndAnEntryWithoutAKeyFallsThrough(t *testing.T) {
	r := &Router{Getenv: env(map[string]string{"AZURE_OPENAI_API_KEY": "env-key", "AZURE_OPENAI_RESOURCE_NAME": "from-process"}),
		Stored: func(string) (string, map[string]string, bool, error) {
			return "", map[string]string{"AZURE_OPENAI_API_KEY": "entry-env-key", "AZURE_OPENAI_RESOURCE_NAME": "from-entry"}, true, nil
		}}
	c, err := r.Credential("azure")
	if err != nil || c.Key != "entry-env-key" || c.Source != "env:AZURE_OPENAI_API_KEY" {
		t.Errorf("entry env should beat the process environment: %+v %v", c, err)
	}
	p, _, _ := r.Resolve("azure/m")
	if rp, ok := p.(*Responses); !ok || rp.BaseURL != "https://from-entry.openai.azure.com/openai/v1" || rp.APIKey != "entry-env-key" {
		t.Errorf("azure from the entry's env: %#v", p)
	}
}

func TestMissingAndUnusableCredentialsExplainThemselves(t *testing.T) {
	r := &Router{Getenv: env(nil)}
	_, err := r.Credential("anthropic")
	var ce *CredentialError
	if !errors.As(err, &ce) || !ce.Missing || ce.Provider != "anthropic" ||
		!strings.Contains(err.Error(), "ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN") || !strings.Contains(err.Error(), "koine auth login anthropic") {
		t.Errorf("missing: %v", err)
	}
	r.Custom = map[string]ProviderConfig{"corp": {BaseURL: "http://corp", APIKeyEnv: "CORP_KEY"}, "nokey": {BaseURL: "http://x", API: APIGemini}}
	if _, err := r.Credential("corp"); err == nil || !strings.Contains(err.Error(), "set CORP_KEY") {
		t.Errorf("custom variable: %v", err)
	}
	if c, err := r.Credential("nokey"); err != nil || c.Key != "" {
		t.Errorf("a custom endpoint declared without a key source needs none: %+v %v", c, err)
	}
	r.Custom["groq"] = ProviderConfig{BaseURL: "http://proxy"} // a built-in with a proxy still needs its key
	if _, err := r.Credential("groq"); err == nil {
		t.Error("groq via a proxy needs a key")
	}
	r.Stored = func(p string) (string, map[string]string, bool, error) {
		return "", nil, true, errors.New("OAuth login")
	}
	_, err = r.Credential("groq")
	if !errors.As(err, &ce) || ce.Missing || !strings.Contains(err.Error(), "configured credential cannot be used: OAuth login") {
		t.Errorf("unusable: %v", err)
	}
	r.Stored = nil
	r.Secret = func(string) (string, error) { return "", errors.New("command failed") }
	r.Custom["groq"] = ProviderConfig{APIKey: "!pass x"}
	want := "configured credential cannot be used: apiKey in models.json cannot be resolved: command failed"
	if _, err := r.Credential("groq"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("secret: %v", err)
	}
	// Resolve hands the reason to the first request instead of failing at startup
	p, _, err := r.Resolve("groq/x")
	if m, ok := p.(Missing); err != nil || !ok || !strings.Contains(m.Reason, "command failed") {
		t.Errorf("Resolve: %T %v", p, err)
	}
	if _, err := r.Credential("nonsense"); err == nil {
		t.Error("an unknown provider")
	}
	if _, err := r.Credential("gemini"); err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("alias: %v", err)
	}
	if _, _, err := r.ProviderOf("nope/x"); err == nil {
		t.Error("ProviderOf")
	}
	if p, id, err := r.ProviderOf("gemini/x"); p != "google" || id != "x" || err != nil {
		t.Errorf("ProviderOf alias: %s %s %v", p, id, err)
	}
}

func TestAnthropicBearerTokenFromTheEnvironment(t *testing.T) {
	srv, seen := fakeAPI(t, 200, "application/json", `{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`)
	r := &Router{Getenv: env(map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake-token", "ANTHROPIC_BASE_URL": srv.URL}), Retry: noRetry}
	if _, err := r.Complete(context.Background(), Request{Model: "claude-sonnet-5-5"}); err != nil {
		t.Fatal(err)
	}
	h := (*seen)[0].Header
	if h.Get("authorization") != "Bearer fake-token" || h.Get("x-api-key") != "" {
		t.Errorf("headers: %v", h)
	}
	if c, _ := r.Credential("anthropic"); !c.Bearer || c.Source != "env:ANTHROPIC_AUTH_TOKEN" {
		t.Errorf("credential: %+v", c)
	}
}

func TestBuiltinProvidersAreWellFormedAndLocalOnesNeedNoKey(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Providers() {
		if seen[p.ID] || p.ID == "" || p.Name == "" || !knownAPI(p.API) {
			t.Errorf("bad provider entry %+v", p)
		}
		seen[p.ID] = true
		if p.BaseURL == "" && p.ID != "azure" {
			t.Errorf("%s has no base URL", p.ID)
		}
		if p.Local == (len(p.Env) > 0) {
			t.Errorf("%s: a provider has either a key variable or is local: %+v", p.ID, p)
		}
	}
	for _, id := range []string{"ollama", "lmstudio", "vllm", "sglang", "llamacpp"} {
		p, _, err := (&Router{Getenv: env(nil)}).Resolve(id + "/some-model")
		if oc, ok := p.(*OpenAICompat); err != nil || !ok || oc.APIKey != "" || !strings.HasPrefix(oc.BaseURL, "http://localhost:") {
			t.Errorf("%s: %T %v", id, p, err)
		}
	}
	// the environment variables are Pi's names
	piNames := map[string]string{"google": "GEMINI_API_KEY", "huggingface": "HF_TOKEN", "vercel-ai-gateway": "AI_GATEWAY_API_KEY",
		"nvidia": "NVIDIA_API_KEY", "cerebras": "CEREBRAS_API_KEY", "fireworks": "FIREWORKS_API_KEY", "moonshot": "MOONSHOT_API_KEY",
		"zai": "ZAI_API_KEY"}
	for id, v := range piNames {
		if p, _ := BuiltinProvider(id); len(p.Env) != 1 || p.Env[0] != v {
			t.Errorf("%s: %v", id, p.Env)
		}
	}
	if _, ok := BuiltinProvider("nope"); ok {
		t.Error("nope")
	}
	if CanonicalProvider("azure-openai-responses") != "azure" || CanonicalProvider("groq") != "groq" {
		t.Error("CanonicalProvider")
	}
	p, id, err := (&Router{Getenv: env(map[string]string{"GEMINI_API_KEY": "fake-key"})}).Resolve("gemini-2.5-pro")
	if g, ok := p.(*Gemini); err != nil || !ok || g.APIKey != "fake-key" || id != "gemini-2.5-pro" || g.label() != "google" {
		t.Errorf("bare gemini name: %T %v", p, err)
	}
}

func TestModelsJSONProvidersChangeAndAddProviders(t *testing.T) {
	vars := map[string]string{"ANTHROPIC_API_KEY": "a", "CORP_TOKEN": "corp-secret"}
	r := &Router{Getenv: env(vars), Custom: map[string]ProviderConfig{
		"anthropic": {BaseURL: "http://anthropic-proxy/"}, // a built-in with only a baseUrl is a proxy
		"corp": {BaseURL: "http://corp/v1", API: APIResponses, APIKey: "$CORP_TOKEN", Headers: map[string]string{"x-org": "o1", "x-token": "$CORP_TOKEN"},
			Models: []ModelConfig{{ID: "claude-like", API: APIAnthropic, BaseURL: "http://corp-anthropic", Headers: map[string]string{"x-org": "o2"}},
				{ID: "local", API: APICompletions, Compat: raw(`{"maxTokensField":"max_completion_tokens"}`)}}},
		"strange": {BaseURL: "http://s", API: "bedrock-converse-stream"},
		"goog":    {BaseURL: "http://g", API: APIGemini, AuthHeader: true, APIKey: "k"},
		"ant":     {BaseURL: "http://a", API: APIAnthropic, AuthHeader: true, APIKey: "k2"},
	}, Secret: func(v string) (string, error) {
		if strings.HasPrefix(v, "$") {
			return vars[strings.TrimPrefix(v, "$")], nil
		}
		return v, nil
	}}
	p, _, _ := r.Resolve("claude-sonnet-5-5")
	if a, ok := p.(*Anthropic); !ok || a.BaseURL != "http://anthropic-proxy" || a.APIKey != "a" {
		t.Errorf("proxy: %#v", p)
	}
	p, _, _ = r.Resolve("corp/gpt-x")
	rp, ok := p.(*Responses)
	if !ok || rp.BaseURL != "http://corp/v1" || rp.APIKey != "corp-secret" || rp.Headers["x-token"] != "corp-secret" ||
		rp.Headers["x-org"] != "o1" {
		t.Errorf("provider-level api and headers: %#v", p)
	}
	p, _, _ = r.Resolve("corp/claude-like")
	if a, ok := p.(*Anthropic); !ok || a.BaseURL != "http://corp-anthropic" || a.Headers["x-org"] != "o2" ||
		a.Headers["x-token"] != "corp-secret" || a.Name != "corp" {
		t.Errorf("model-level overrides: %#v", p)
	}
	p, _, _ = r.Resolve("corp/local")
	if oc, ok := p.(*OpenAICompat); !ok || oc.Flags.MaxTokensField != "max_completion_tokens" {
		t.Errorf("model-level api and compat: %#v", p)
	}
	if p, _, _ = r.Resolve("strange/m"); !strings.Contains(p.(Missing).Reason, `"bedrock-converse-stream" is not supported`) {
		t.Errorf("unsupported api: %v", p)
	}
	if g, ok := mustResolve(t, r, "goog/m").(*Gemini); !ok || !g.Bearer || g.APIKey != "k" {
		t.Error("gemini authHeader")
	}
	if a, ok := mustResolve(t, r, "ant/m").(*Anthropic); !ok || !a.Bearer {
		t.Error("anthropic authHeader")
	}
	r.Secret = func(string) (string, error) { return "", errors.New("no such variable") }
	if p, _, _ = r.Resolve("corp/gpt-x"); !strings.Contains(p.(Missing).Reason, "cannot be resolved") {
		t.Errorf("secret failure: %v", p)
	}
	delete(r.Custom, "corp")
	r.Custom["hdr"] = ProviderConfig{BaseURL: "http://h", APIKey: "literal", Headers: map[string]string{"x-a": "b"}}
	r.Secret = func(v string) (string, error) {
		if v == "b" {
			return "", errors.New("header boom")
		}
		return v, nil
	}
	if p, _, _ = r.Resolve("hdr/m"); !strings.Contains(p.(Missing).Reason, `header "x-a" cannot be resolved: header boom`) {
		t.Errorf("header failure: %v", p)
	}
}

func mustResolve(t *testing.T, r *Router, model string) Provider {
	t.Helper()
	p, _, err := r.Resolve(model)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpenAICompatFlagsChangeTheRequest(t *testing.T) {
	srv, seen := fakeAPI(t, 200, "application/json", `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	flags, unused := parseOpenAIFlags(raw(`{"supportsReasoningEffort":false,"supportsUsageInStreaming":false,"maxTokensField":"max_tokens",
		"requiresToolResultName":true,"requiresNonNullAssistantContent":true,"requiresToolsWithToolHistory":true,"thinkingFormat":"qwen"}`))
	if !flags.NoReasoningEffort || !flags.NoStreamUsage || flags.MaxTokensField != "max_tokens" || !flags.ToolResultName || !flags.NonNullAssistant ||
		!flags.ToolsWithHistory || len(unused) != 1 || unused[0] != "thinkingFormat" {
		t.Errorf("flags: %+v %v", flags, unused)
	}
	o := &OpenAICompat{Name: "x", BaseURL: srv.URL, Flags: flags, Extra: map[string]any{"top_p": 0.9, "model": "ignored"}, Retry: noRetry}
	msgs := []Message{{Role: "user", Content: []Block{{Type: "text", Text: "go"}}},
		{Role: "assistant", Content: []Block{{Type: "tool_use", ID: "c1", Name: "ls"}}},
		{Role: "user", Content: []Block{{Type: "tool_result", ToolUseID: "c1", Content: "out"}}}}
	if _, err := o.Complete(context.Background(), Request{Model: "m", Messages: msgs, MaxTokens: 50, Thinking: &Thinking{Level: "high"}}); err != nil {
		t.Fatal(err)
	}
	b := (*seen)[0].Body
	if b["reasoning_effort"] != nil || b["max_tokens"] != float64(50) || b["max_completion_tokens"] != nil || b["top_p"] != 0.9 || b["model"] != "m" {
		t.Errorf("body: %v", b)
	}
	if tools, ok := b["tools"].([]any); !ok || len(tools) != 0 {
		t.Errorf("tools: %v", b["tools"])
	}
	ms := b["messages"].([]any)
	if ms[1].(map[string]any)["content"] != "" {
		t.Errorf("assistant content: %v", ms[1])
	}
	if ms[2].(map[string]any)["name"] != "ls" {
		t.Errorf("tool name: %v", ms[2])
	}
	_, _, payload, _ := o.request(Request{Model: "m"}, true)
	if strings.Contains(string(payload), "stream_options") {
		t.Errorf("stream usage: %s", payload)
	}
	// without flags the old behaviour holds: reasoning models get max_completion_tokens and a stream asks for usage
	plain := &OpenAICompat{BaseURL: srv.URL}
	_, _, payload, _ = plain.request(Request{Model: "m", MaxTokens: 5, Thinking: &Thinking{Level: "low"}}, true)
	if !strings.Contains(string(payload), `"max_completion_tokens":5`) || !strings.Contains(string(payload), "include_usage") ||
		strings.Contains(string(payload), `"tools"`) {
		t.Errorf("plain: %s", payload)
	}
	if !strings.Contains(strings.Join(ActedCompatKeys(APICompletions), ","), "maxTokensField") || ActedCompatKeys(APIGemini) != nil {
		t.Error("ActedCompatKeys")
	}
	if f, _ := parseOpenAIFlags(raw(`{"maxTokensField":"nonsense","supportsReasoningEffort":"yes"}`)); f.MaxTokensField != "" || f.NoReasoningEffort {
		t.Errorf("bad values must be ignored: %+v", f)
	}
}

func TestReadModelsFileAndCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if p, w, err := ReadModelsFile(path); p != nil || w != nil || err != nil {
		t.Errorf("missing file: %v %v %v", p, w, err)
	}
	doc := `{
  // a comment line
  "$schema": "https://pi.dev/schemas/models.schema.json",
  "providers": {
    "ollama": {
      "baseUrl": "http://localhost:11434/v1", "api": "openai-completions", "apiKey": "ollama",
      "compat": {"supportsReasoningEffort": false, "thinkingFormat": "qwen"},
      "models": [
        {"id": "qwen2.5-coder:7b", "name": "Qwen Coder", "reasoning": true, "input": ["text", "image"], "contextWindow": 32000,
         "cost": {"input": 1, "output": 2, "cacheRead": 0.5, "cacheWrite": 0, "tiers": []}, "promptCache": {"short": 300}},
        {"id": "plain"}, {"name": "no id"}
      ]
    },
    "openrouter": {"modelOverrides": {"claude-sonnet-5-5": {"contextWindow": 500000, "cost": {"output": 11}, "name": "Sonnet"}, "unknown": {"name": "x"}}},
    "mine": {"models": [{"id": "m", "api": "wire-protocol-x"}], "api": "weird", "oauth": "radius"},
    "empty": {}
  }
}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	ps, warns, err := ReadModelsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(warns, "\n")
	for _, want := range []string{"providers.ollama.compat.thinkingFormat is not acted on", `models["qwen2.5-coder:7b"].promptCache is not acted on`,
		"models[2] has no id", "providers.mine.oauth is not acted on", `api "weird" is not supported`, `models["m"]: api "wire-protocol-x" is not supported`,
		`providers.mine has no baseUrl, so model "m" cannot be reached`, "providers.empty has no baseUrl and no models"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing warning %q in:\n%s", want, all)
		}
	}
	if len(ps["ollama"].Models) != 2 {
		t.Fatalf("the model without an id should be dropped: %+v", ps["ollama"].Models)
	}
	c := DefaultCatalog()
	c.AddProviders(ps)
	m, ok := c.Lookup("qwen2.5-coder:7b")
	if !ok || m.Provider != "ollama" || m.Name != "Qwen Coder" || m.ContextWindow != 32000 || m.MaxOutput != DefaultMaxTokens ||
		m.Cost == nil || m.Cost.Input != 1 || m.Cost.Output != 2 || m.Cost.CacheRead != 0.5 || m.Reasoning == nil || len(m.Input) != 2 {
		t.Errorf("model: %+v", m)
	}
	p, _ := c.Lookup("plain")
	if p.ContextWindow != DefaultContextWindow || p.MaxOutput != DefaultMaxTokens || p.Cost != nil || len(p.Input) != 1 || p.Input[0] != "text" {
		t.Errorf("Pi's defaults: %+v", p)
	}
	s, _ := c.Lookup("claude-sonnet-5-5")
	if s.ContextWindow != 500000 || s.Name != "Sonnet" || s.Cost.Output != 11 || s.Cost.Input != 2 || s.Cost.CacheRead != 0.1 {
		t.Errorf("override should merge the price field by field: %+v", s)
	}
	if _, ok := c.Lookup("unknown"); ok {
		t.Error("an override of an unknown model is ignored")
	}
	// a models entry with an id the catalog has replaces it
	c.AddProviders(map[string]ProviderConfig{"mine": {Models: []ModelConfig{{ID: "claude-haiku-5-5", Name: "Replaced"}}}})
	if h, _ := c.Lookup("claude-haiku-5-5"); h.Name != "Replaced" || h.Cost != nil {
		t.Errorf("replace: %+v", h)
	}
	if err := os.WriteFile(path, []byte(`{"providers": [}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadModelsFile(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("broken file: %v", err)
	}
	_ = os.WriteFile(path, []byte(`{"providers": {"x": {"models": "nope"}}}`), 0o600)
	if _, _, err := ReadModelsFile(path); err == nil || !strings.Contains(err.Error(), "providers.x") {
		t.Errorf("bad provider: %v", err)
	}
	if _, _, err := ReadModelsFile(dir); err == nil {
		t.Error("a directory is not a file")
	}
}

func TestErrorMessagesFromProviderBodies(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":{"message":"Incorrect API key","type":"invalid_request_error","code":"invalid_api_key"}}`: "Incorrect API key (invalid_request_error)",
		`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`:            "invalid x-api-key (authentication_error)",
		`{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT"}}`:                 "API key not valid. (INVALID_ARGUMENT)",
		`[{"error":{"code":401,"message":"bad","status":"UNAUTHENTICATED"}}]`:                               "bad (UNAUTHENTICATED)",
		`{"error":"model 'x' not found"}`: "model 'x' not found",
		`{"message":"Unauthorized"}`:      "Unauthorized",
		`{"detail":"Not Found"}`:          "Not Found",
		`{"error":{"message":"invalid_api_key means what it says","type":"invalid_api_key"}}`: "invalid_api_key means what it says",
		`{"error":{"type":"rate_limit_error"}}`:                                               "rate_limit_error",
		`{"error":{"code":429}}`:                                                              "429",
		`{"error":{}}`:                                                                        "",
		`<html>bad gateway</html>`:                                                            "",
		`[]`:                                                                                  "",
		`[1]`:                                                                                 "",
		`{`:                                                                                   "",
		``:                                                                                    "",
	} {
		if got := ErrorMessage(body); got != want {
			t.Errorf("ErrorMessage(%s) = %q, want %q", body, got, want)
		}
	}
}

func TestHTTPErrorsAddAuthGuidance(t *testing.T) {
	for _, c := range []struct {
		provider string
		status   int
		body     string
		want     []string
		not      string
	}{
		{"anthropic", 401, `{"error":{"message":"invalid x-api-key"}}`,
			[]string{"anthropic: HTTP 401: invalid x-api-key", "set ANTHROPIC_API_KEY, or run `koine auth login anthropic`"}, ""},
		{"google", 403, `{"error":{"message":"denied"}}`, []string{"access denied", "GEMINI_API_KEY"}, ""},
		{"ollama", 401, `{}`, []string{"authentication failed: check the API key for ollama and the server's address"}, "koine auth login"},
		{"corp", 401, `nope`, []string{"corp: HTTP 401: nope", "check the API key for corp"}, ""},
		{"openai", 402, `{"error":"pay"}`, []string{"payment required"}, ""},
		{"openai", 500, `{"error":"down"}`, []string{"openai: HTTP 500: down"}, "("},
	} {
		msg := (&HTTPError{Provider: c.provider, Status: c.status, Body: c.body}).Error()
		for _, w := range c.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%s %d: %q lacks %q", c.provider, c.status, msg, w)
			}
		}
		if c.not != "" && strings.Contains(msg, c.not) {
			t.Errorf("%s %d: %q should not contain %q", c.provider, c.status, msg, c.not)
		}
	}
	// overflow detection still reads the raw body
	if !IsOverflow(&HTTPError{Provider: "openai", Status: 400, Body: `{"error":{"code":"context_length_exceeded"}}`}) {
		t.Error("overflow")
	}
}
