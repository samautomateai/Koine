package app

import (
	"strings"
	"testing"

	"koine/core/llm"
)

func TestCacheWarmingSettingIsGlobalOnlyAndDefaultsToIdle(t *testing.T) {
	for _, c := range []struct{ name, user, project, want string }{
		{"unset", `{}`, ``, llm.WarmIdle},
		{"user setting", `{"cacheWarming":"streaming"}`, ``, llm.WarmStreaming},
		{"off", `{"cacheWarming":"off"}`, ``, llm.WarmOff},
		{"a project file cannot change it", `{"cacheWarming":"off"}`, `{"cacheWarming":"idle"}`, llm.WarmOff},
		{"a project file cannot turn it on", `{}`, `{"cacheWarming":"streaming"}`, llm.WarmIdle},
		{"an invalid value is ignored", `{"cacheWarming":"always"}`, ``, llm.WarmIdle},
	} {
		a, _ := settingsApp(t, c.user, c.project, Options{})
		if got := a.Agent.Warm.Mode; got != c.want {
			t.Errorf("%s: mode = %q, want %q", c.name, got, c.want)
		}
		if a.Agent.Warm.Catalog != a.Agent.Catalog {
			t.Errorf("%s: the warmer should use the agent's catalog", c.name)
		}
	}
	a, _ := settingsApp(t, `{"cacheWarming":"off"}`, `{"cacheWarming":"idle"}`, Options{})
	if diag := strings.Join(a.Warnings, "\n"); !strings.Contains(diag, "cacheWarming") {
		t.Errorf("a global-only key in a project file is reported: %q", diag)
	}
	a.Reload(t.Context())
	if a.Agent.Warm.Mode != llm.WarmOff {
		t.Error("reloading keeps the setting")
	}
}

func TestSessionIDAndRetentionReachTheRequest(t *testing.T) {
	t.Setenv("KOINE_CACHE_RETENTION", "long")
	a, _ := settingsApp(t, `{}`, "", Options{})
	if a.Session == nil || a.Agent.CacheKey != a.Session.ID || !a.Agent.CacheLong {
		t.Fatalf("session %v key %q long %v", a.Session != nil, a.Agent.CacheKey, a.Agent.CacheLong)
	}
	c, _ := run(t, a, "hi")
	if got := c.reqs[0].Cache; got.Key != a.Session.ID || !got.Long {
		t.Errorf("request cache hints = %+v", got)
	}
	old := a.Agent.CacheKey
	a.NewSession()
	if a.Agent.CacheKey == "" || a.Agent.CacheKey == old || a.Agent.CacheKey != a.Session.ID {
		t.Errorf("a new session is a new cache key: %q -> %q", old, a.Agent.CacheKey)
	}
	b, _ := settingsApp(t, `{}`, "", Options{RunOptions: RunOptions{}})
	b.Agent.CacheLong = false
	t.Setenv("KOINE_CACHE_RETENTION", "short")
	b.applyLLMSettings()
	if b.Agent.CacheLong {
		t.Error("only KOINE_CACHE_RETENTION=long asks for extended retention")
	}
}

func TestBedrockAndVertexAreRegisteredWithTheRouter(t *testing.T) {
	a, _ := settingsApp(t, `{}`, "", Options{})
	have := strings.Join(a.loginProviders(), ",")
	for _, id := range []string{"amazon-bedrock", "google-vertex"} {
		if !strings.Contains(","+have+",", ","+id+",") {
			t.Errorf("%s missing from the provider list: %s", id, have)
		}
	}
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "fake-bearer")
	t.Setenv("AWS_REGION", "us-west-2")
	b, _ := settingsApp(t, `{}`, "", Options{})
	p, id, err := b.router.Resolve("amazon-bedrock/anthropic.claude-sonnet-5-5-v1:0")
	if bd, ok := p.(*llm.Bedrock); err != nil || !ok || bd.Bearer != "fake-bearer" || id != "anthropic.claude-sonnet-5-5-v1:0" {
		t.Errorf("%T %v", p, err)
	}
	// an auth.json entry for a cloud provider carries its settings in env, and needs no key
	writeFile(t, b.Home+"/.koine/auth.json", `{"amazon-bedrock":{"type":"api_key","key":"","env":{"AWS_REGION":"eu-north-1"}}}`)
	b.Reload(t.Context())
	if bd, _, _ := b.router.Resolve("bedrock/m"); bd == nil {
		t.Fatal("no provider")
	} else if p, ok := bd.(*llm.Bedrock); !ok || p.Region != "eu-north-1" {
		t.Errorf("stored env: %#v", bd)
	}
}
