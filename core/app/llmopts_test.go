package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koine/core/agent"
	"koine/core/llm"
)

// capture answers every request with "ok" and keeps the requests.
type capture struct{ reqs []llm.Request }

func (c *capture) Complete(_ context.Context, r llm.Request) (*llm.Response, error) {
	c.reqs = append(c.reqs, r)
	return &llm.Response{Content: []llm.Block{{Type: "thinking", Thinking: "pondering", Signature: "s"}, {Type: "text", Text: "ok"}}}, nil
}

func settingsApp(t *testing.T, user, project string, opts Options) (*App, string) {
	t.Helper()
	isolate(t)
	home, _ := os.UserHomeDir()
	proj := t.TempDir()
	if user != "" {
		writeFile(t, filepath.Join(home, ".koine", "settings.json"), user)
	}
	if project != "" {
		writeFile(t, filepath.Join(proj, ".koine", "settings.json"), project)
	}
	a, err := NewWithOptions(context.Background(), proj, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, proj
}

func run(t *testing.T, a *App, line string) (*capture, []agent.Event) {
	t.Helper()
	c := &capture{}
	a.Agent.Provider = c
	var evs []agent.Event
	if _, err := a.HandleInput(context.Background(), line, func(e agent.Event) { evs = append(evs, e) }); err != nil {
		t.Fatal(err)
	}
	return c, evs
}

func notices(evs []agent.Event) string {
	var out []string
	for _, e := range evs {
		if e.Type == "notice" || e.Type == "error" {
			out = append(out, e.Text)
		}
	}
	return strings.Join(out, "\n")
}

func TestThinkingSettingsDecideTheLevelPerModel(t *testing.T) {
	user := `{"defaultModel":"claude-sonnet-5-5","defaultThinkingLevel":"low","modelThinkingLevels":{"anthropic/claude-haiku-5-5":"max"},` +
		`"thinkingBudgets":{"low":3000}}`
	a, _ := settingsApp(t, user, "", Options{})
	c, evs := run(t, a, "hi")
	if th := c.reqs[0].Thinking; th == nil || th.Level != "low" || th.Mode != llm.ModeAdaptive {
		t.Errorf("default level: %+v", th)
	}
	var shown bool
	for _, e := range evs {
		shown = shown || (e.Type == "thinking" && e.Text == "pondering")
	}
	if !shown {
		t.Error("thinking should be shown by default")
	}
	// the per-model level applies when that model is selected
	run(t, a, "/model claude-haiku-5-5")
	if c, _ = run(t, a, "hi"); c.reqs[0].Thinking.Level != "max" {
		t.Errorf("per-model level: %+v", c.reqs[0].Thinking)
	}
	if a.Agent.Thinking.Budgets["low"] != 3000 {
		t.Errorf("budgets: %+v", a.Agent.Thinking)
	}
}

func TestHideThinkingBlockSetting(t *testing.T) {
	a, _ := settingsApp(t, `{"defaultModel":"claude-sonnet-5-5","hideThinkingBlock":true}`, "", Options{})
	_, evs := run(t, a, "hi")
	for _, e := range evs {
		if e.Type == "thinking" {
			t.Errorf("thinking shown despite hideThinkingBlock: %+v", e)
		}
	}
	if a.Agent.History[1].Content[0].Type != "thinking" {
		t.Error("hidden blocks stay in the history")
	}
}

func TestThinkingFlagSuffixAndSlashCommand(t *testing.T) {
	user := `{"defaultModel":"claude-sonnet-5-5","defaultThinkingLevel":"low","modelThinkingLevels":{"anthropic/claude-sonnet-5-5":"minimal"}}`
	// the flag beats the suffix, and both beat the settings
	a, _ := settingsApp(t, user, "", Options{RunOptions: RunOptions{Model: "claude-sonnet-5-5:high", Thinking: "xhigh"}})
	if c, _ := run(t, a, "hi"); c.reqs[0].Thinking.Level != "xhigh" || c.reqs[0].Model != "claude-sonnet-5-5" {
		t.Errorf("flag: %+v model %q", c.reqs[0].Thinking, c.reqs[0].Model)
	}
	a, _ = settingsApp(t, user, "", Options{RunOptions: RunOptions{Model: "claude-sonnet-5-5:high"}})
	if c, _ := run(t, a, "hi"); c.reqs[0].Thinking.Level != "high" || a.Agent.Model != "claude-sonnet-5-5" {
		t.Errorf("suffix: %+v model %q", c.reqs[0].Thinking, a.Agent.Model)
	}
	// /thinking shows and sets; it survives /reload
	_, evs := run(t, a, "/thinking")
	if n := notices(evs); n != "thinking: high (claude-sonnet-5-5)" {
		t.Errorf("show: %q", n)
	}
	_, evs = run(t, a, "/thinking off")
	if n := notices(evs); n != "thinking: off (claude-sonnet-5-5)" {
		t.Errorf("set: %q", n)
	}
	a.Reload(context.Background())
	if c, _ := run(t, a, "hi"); c.reqs[0].Thinking.Level != "off" {
		t.Errorf("after reload: %+v", c.reqs[0].Thinking)
	}
	if _, evs = run(t, a, "/thinking sideways"); !strings.Contains(notices(evs), "must be one of off, minimal") {
		t.Errorf("bad level: %q", notices(evs))
	}
	// /model id:level sets the level too, and a model id with another colon is left whole
	run(t, a, "/model claude-haiku-5-5:medium")
	if a.Agent.Model != "claude-haiku-5-5" || a.Agent.ThinkingLevel() != "medium" {
		t.Errorf("/model suffix: %q %q", a.Agent.Model, a.Agent.ThinkingLevel())
	}
	if _, evs = run(t, a, "/model no-such-vendor/x:high"); !strings.Contains(notices(evs), "unknown provider prefix") {
		t.Errorf("unknown model: %q", notices(evs))
	}
	// a model that gets no thinking says so
	b, _ := settingsApp(t, `{"defaultModel":"ollama/llama3"}`, "", Options{})
	if _, evs = run(t, b, "/thinking"); !strings.Contains(notices(evs), "thinking: not used with ollama/llama3") {
		t.Errorf("non-thinking model: %q", notices(evs))
	}
	if c, _ := run(t, b, "hi"); c.reqs[0].Thinking != nil {
		t.Errorf("unknown models are sent no thinking parameters unless asked: %+v", c.reqs[0].Thinking)
	}
	// an invalid --thinking is ignored with a warning
	d, _ := settingsApp(t, `{"defaultModel":"claude-sonnet-5-5"}`, "", Options{RunOptions: RunOptions{Thinking: "sideways"}})
	if len(d.Warnings) == 0 || !strings.Contains(strings.Join(d.Warnings, "\n"), `--thinking "sideways"`) {
		t.Errorf("warnings: %v", d.Warnings)
	}
}

func TestRetryAndTimeoutSettingsMapToThePolicy(t *testing.T) {
	cases := []struct {
		name, json string
		want       llm.RetryPolicy
	}{
		{"defaults", `{}`, llm.RetryPolicy{}},
		{"all of them", `{"retry":{"enabled":true,"maxRetries":5,"baseDelayMs":100,"maxAgentDelayMs":900,` +
			`"provider":{"timeoutMs":2000,"maxRetries":9,"maxRetryDelayMs":30000}},"httpIdleTimeoutMs":777}`,
			llm.RetryPolicy{Attempts: 6, BaseDelay: 100 * time.Millisecond, MaxDelay: 900 * time.Millisecond, Timeout: 2 * time.Second,
				MaxServerDelay: 30 * time.Second}},
		{"disabled", `{"retry":{"enabled":false}}`, llm.RetryPolicy{Disabled: true}},
		{"no retries", `{"retry":{"maxRetries":0}}`, llm.RetryPolicy{Disabled: true}},
		{"no limit on Retry-After", `{"retry":{"provider":{"maxRetryDelayMs":0}}}`, llm.RetryPolicy{MaxServerDelay: -1}},
		{"idle timeout is the request timeout", `{"httpIdleTimeoutMs":1500}`, llm.RetryPolicy{Timeout: 1500 * time.Millisecond}},
		{"disabled idle timeout", `{"httpIdleTimeoutMs":"disabled"}`, llm.RetryPolicy{}},
		{"provider timeout beats idle timeout", `{"httpIdleTimeoutMs":1500,"retry":{"provider":{"timeoutMs":0}}}`, llm.RetryPolicy{}},
		{"nonsense is ignored", `{"retry":{"maxRetries":"many","enabled":"yes"},"httpIdleTimeoutMs":"soon"}`, llm.RetryPolicy{}},
	}
	for _, tc := range cases {
		a, _ := settingsApp(t, tc.json, "", Options{})
		if got := a.router.Retry; got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestLaterSettingsFilesOverrideAndUntrustedProjectsKeepHarmlessOnes(t *testing.T) {
	user := `{"defaultThinkingLevel":"low","modelThinkingLevels":{"a/b":"low","c/d":"low"},"thinkingBudgets":{"low":2000,"high":9000},` +
		`"retry":{"maxRetries":2,"baseDelayMs":50}}`
	proj := `{"defaultThinkingLevel":"high","modelThinkingLevels":{"a/b":"max"},"thinkingBudgets":{"low":2500},"hideThinkingBlock":true,` +
		`"retry":{"baseDelayMs":75,"enabled":true},"httpIdleTimeoutMs":1000}`
	isolate(t)
	home, _ := os.UserHomeDir()
	dir := t.TempDir()
	writeFile(t, filepath.Join(home, ".koine", "settings.json"), user)
	writeFile(t, filepath.Join(dir, ".koine", "settings.json"), proj)
	cfg, _ := LoadConfig(dir, home)
	if cfg.DefaultThinkingLevel.V != "high" || cfg.ModelThinkingLevels.V["a/b"] != "max" || cfg.ModelThinkingLevels.V["c/d"] != "low" ||
		cfg.ThinkingBudgets.V["low"] != 2500 || cfg.ThinkingBudgets.V["high"] != 9000 || !cfg.HideThinkingBlock.V {
		t.Errorf("merged thinking settings: %+v", cfg.LLMSettings)
	}
	if p := cfg.retryPolicy(); p.Attempts != 3 || p.BaseDelay != 75*time.Millisecond || p.Timeout != time.Second {
		t.Errorf("merged retry: %+v", p)
	}
	t.Setenv("KOINE_TRUST", "")
	a, err := NewWithOptions(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Trusted || a.Agent.Thinking.Default != "high" || !a.Agent.HideThinking || a.router.Retry.BaseDelay != 75*time.Millisecond {
		t.Errorf("an untrusted project still sets harmless scalars: trusted=%v %+v hide=%v retry=%+v", a.Trusted, a.Agent.Thinking, a.Agent.HideThinking,
			a.router.Retry)
	}
}
