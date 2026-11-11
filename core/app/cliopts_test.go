package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/llm"
)

// newApp starts an app in a fresh project with an isolated home, after writing the given files (path relative to the project).
func startApp(t *testing.T, o Options, files map[string]string) *App {
	t.Helper()
	isolate(t)
	t.Setenv("KOINE_MODEL", "")
	proj := t.TempDir()
	for rel, body := range files {
		writeFile(t, filepath.Join(proj, rel), body)
	}
	a, err := NewWithOptions(context.Background(), proj, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func warned(a *App, substr string) bool {
	for _, w := range a.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestResolveModelMatchesIdsNamesAndProviders(t *testing.T) {
	a := startApp(t, Options{}, nil)
	good := map[string]string{
		"claude-sonnet-5-5":         "claude-sonnet-5-5", // an id the catalog lists is kept as written
		"CLAUDE-HAIKU-5-5":          "anthropic/claude-haiku-5-5",
		"sonnet":                    "anthropic/claude-sonnet-5-5",
		"Sonnet 5.5":                "anthropic/claude-sonnet-5-5",
		"anthropic/opus":            "anthropic/claude-opus-5-5",
		"claude-haiku-5-5-20261001": "claude-haiku-5-5-20261001", // not listed, but the router can place it
		"ollama/llama3:8b":          "ollama/llama3:8b",
		"openrouter/vendor/some":    "openrouter/vendor/some",
	}
	for in, want := range good {
		if got, err := a.resolveModel(in, ""); err != nil || got != want {
			t.Errorf("resolveModel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := a.resolveModel("opus", "anthropic"); err != nil || got != "anthropic/claude-opus-5-5" {
		t.Errorf("with provider: %q %v", got, err)
	}
	if got, err := a.resolveModel("llama3", "ollama"); err != nil || got != "ollama/llama3" {
		t.Errorf("a model of a provider with no catalog entries: %q %v", got, err)
	}
	for _, c := range [][2]string{{"nothing-like-it", ""}, {"openai/sonnet", "anthropic"}, {"x", "nosuch"}, {"unknownvendor/x", ""}, {"", ""}} {
		if got, err := a.resolveModel(c[0], c[1]); err == nil {
			t.Errorf("resolveModel(%q, %q) = %q, want an error", c[0], c[1], got)
		}
	}
}

func TestFuzzyMatchPrefersCredentialsThenReportsAmbiguity(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_MODEL", "")
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".koine", "model-catalog.json"), `{"models":[
		{"id":"alpha-fast","provider":"groq","name":"Alpha Fast"},
		{"id":"alpha-big","provider":"openrouter","name":"Alpha Big"},
		{"id":"beta-20260101","provider":"groq"}, {"id":"beta","provider":"groq"}, {"id":"beta-20260202","provider":"groq"},
		{"id":"gpt-zeta","name":"Zeta"}]}`)
	t.Setenv("GROQ_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	a, err := NewWithOptions(context.Background(), t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.resolveModel("alpha", ""); err == nil || !strings.Contains(err.Error(), "--provider") {
		t.Errorf("two providers, no keys: want an ambiguity error, got %v", err)
	}
	t.Setenv("OPENROUTER_API_KEY", "k")
	if got, err := a.resolveModel("alpha", ""); err != nil || got != "openrouter/alpha-big" {
		t.Errorf("the provider with a key wins: %q %v", got, err)
	}
	t.Setenv("GROQ_API_KEY", "k")
	if _, err := a.resolveModel("alpha", ""); err == nil {
		t.Error("two providers with keys are still ambiguous")
	}
	if got, err := a.resolveModel("alpha", "groq"); err != nil || got != "groq/alpha-fast" {
		t.Errorf("--provider settles it: %q %v", got, err)
	}
	// within one provider an undated name beats dated ones, and the exact id is always honoured
	if got, err := a.resolveModel("bet", "groq"); err != nil || got != "groq/beta" {
		t.Errorf("undated first: %q %v", got, err)
	}
	if got, err := a.resolveModel("beta-20260202", ""); err != nil || got != "groq/beta-20260202" {
		t.Errorf("exact dated id: %q %v", got, err)
	}
	if got, err := a.resolveModel("zeta", ""); err != nil || got != "openai/gpt-zeta" {
		t.Errorf("provider inferred from the id: %q %v", got, err)
	}
	if !isDated("x-20260101") || isDated("x-2026") || isDated("plain") || isDated("x-2026ab01") {
		t.Error("isDated")
	}
}

func TestExpandScope(t *testing.T) {
	a := startApp(t, Options{}, nil)
	models, warns := a.expandScope([]string{"haiku", "claude-*-5-5", " ", "anthropic/OPUS", "nothing", "anthropic/claude-sonnet-5-5", "ollama/llama3", "[bad"})
	want := []string{"anthropic/claude-haiku-5-5", "anthropic/claude-opus-5-5", "anthropic/claude-sonnet-5-5", "ollama/llama3"}
	if strings.Join(models, ",") != strings.Join(want, ",") {
		t.Errorf("scope %v, want %v", models, want)
	}
	if len(warns) != 2 || !strings.Contains(warns[0], `"nothing"`) || !strings.Contains(warns[1], `"[bad"`) {
		t.Errorf("warnings %v", warns)
	}
}

func TestScopeComesFromFlagOrSettingAndIsWarnedAbout(t *testing.T) {
	a := startApp(t, Options{}, map[string]string{".koine/settings.json": `{"enabledModels":["opus","zzz"]}`})
	if a.Agent.Model != "anthropic/claude-opus-5-5" || len(a.Scope) != 1 || !warned(a, `nothing matches "zzz"`) {
		t.Errorf("model %q scope %v warnings %v", a.Agent.Model, a.Scope, a.Warnings)
	}
	a = startApp(t, Options{Load: LoadOptions{Models: []string{"haiku"}}}, map[string]string{".koine/settings.json": `{"enabledModels":["opus"]}`})
	if a.Agent.Model != "anthropic/claude-haiku-5-5" {
		t.Errorf("--models must replace the setting: %q", a.Agent.Model)
	}
	a = startApp(t, Options{}, nil)
	if a.Agent.Model != DefaultModel || len(a.Scope) != 0 {
		t.Errorf("no scope, no model: %q %v", a.Agent.Model, a.Scope)
	}
	isolate(t)
	if _, err := NewWithOptions(context.Background(), t.TempDir(), Options{Load: LoadOptions{Provider: "anthropic"}}); err == nil {
		t.Error("--provider without --model must be refused")
	}
	if _, err := NewWithOptions(context.Background(), t.TempDir(), Options{RunOptions: RunOptions{Model: "nonsense-model"}}); err == nil {
		t.Error("an unresolvable --model must be refused")
	}
}

func TestAPIKeyStandsInForTheProvidersVariableOnly(t *testing.T) {
	a := startApp(t, Options{RunOptions: RunOptions{Model: "haiku"}, Load: LoadOptions{APIKey: "secret"}}, nil)
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	t.Setenv("OPENAI_API_KEY", "other")
	if a.apiKeyEnv != "ANTHROPIC_API_KEY" || a.getenv("ANTHROPIC_API_KEY") != "secret" || a.getenv("OPENAI_API_KEY") != "other" {
		t.Errorf("env %q, anthropic %q, openai %q", a.apiKeyEnv, a.getenv("ANTHROPIC_API_KEY"), a.getenv("OPENAI_API_KEY"))
	}
	for model, want := range map[string]string{"openai/gpt-5": "OPENAI_API_KEY", "ollama/x": "", "claude-sonnet-5-5": "ANTHROPIC_API_KEY", "nosuch/x": ""} {
		if got := a.keyEnvOf(model); got != want {
			t.Errorf("keyEnvOf(%q) = %q, want %q", model, got, want)
		}
	}
	a.Cfg.Providers = map[string]llm.ProviderConfig{"mine": {BaseURL: "http://x", APIKeyEnv: "MY_KEY"}}
	if got := a.keyEnvOf("mine/m"); got != "MY_KEY" {
		t.Errorf("custom provider: %q", got)
	}
	if a.hasCredentials("nosuch/x") || !a.hasCredentials("ollama/x") {
		t.Error("hasCredentials")
	}
}

func TestToolSelectionCombinesSettingAndOptions(t *testing.T) {
	names := func(a *App) string { return strings.Join(a.Agent.Tools.Names(), ",") }
	cases := []struct {
		name  string
		files map[string]string
		o     Options
		want  string
	}{
		{"nothing set", nil, Options{}, "bash,edit,glob,grep,ls,read,write"},
		{"exclude by pattern", nil, Options{Load: LoadOptions{ExcludeTools: []string{"w*", "BASH"}}}, "edit,glob,grep,ls,read"},
		{"setting then exclude", map[string]string{".koine/settings.json": `{"defaultTools":["read","grep","bash"]}`},
			Options{Load: LoadOptions{ExcludeTools: []string{"bash"}}}, "grep,read"},
		{"no builtin with a setting that has plain names", map[string]string{".koine/settings.json": `{"defaultTools":["read"]}`},
			Options{Load: LoadOptions{NoBuiltinTools: true}}, ""},
		{"setting adjustments", map[string]string{".koine/settings.json": `{"defaultTools":["-bash","-w*"]}`}, Options{}, "edit,glob,grep,ls,read"},
		{"-t adjustments apply to the setting", map[string]string{".koine/settings.json": `{"defaultTools":["read","grep"]}`},
			Options{RunOptions: RunOptions{Tools: []string{"+ls", "-read"}}}, "grep,ls"},
		{"-t beats -nbt", nil, Options{RunOptions: RunOptions{Tools: []string{"read"}}, Load: LoadOptions{NoBuiltinTools: true}}, "read"},
		{"-nt beats -t", nil, Options{RunOptions: RunOptions{NoTools: true, Tools: []string{"read"}}}, ""},
		{"unmatched entries change nothing", nil, Options{RunOptions: RunOptions{Tools: []string{"+nosuch", "-nosuch*"}}}, "bash,edit,glob,grep,ls,read,write"},
	}
	for _, c := range cases {
		if got := names(startApp(t, c.o, c.files)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExplicitResourcesAndTheirProblems(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_MODEL", "")
	proj := t.TempDir()
	good := filepath.Join(proj, "good.mjs")
	writeFile(t, good, `export default function (pi) {}`)
	writeFile(t, filepath.Join(proj, "dirext", "index.mjs"), `export default function (pi) {}`)
	writeFile(t, filepath.Join(proj, "jsonext", "extension.json"), `{"command":["true"]}`)
	writeFile(t, filepath.Join(proj, "badjson", "extension.json"), `{}`)
	writeFile(t, filepath.Join(proj, "many", "a.mjs"), `export default function (pi) {}`)
	writeFile(t, filepath.Join(proj, "emptydir", "readme.txt"), `x`)
	writeFile(t, filepath.Join(proj, "notes.txt"), `x`)
	a, err := NewWithOptions(context.Background(), proj, Options{Load: LoadOptions{
		Extensions: []string{good, "dirext", "jsonext/extension.json", "badjson/extension.json", "badjson", "many", "emptydir", "notes.txt",
			"missing.mjs", "builtin:mcp"},
		Skills: []string{"no-skill"}, PromptTemplates: []string{"no-prompt.md"}, Themes: []string{"x"}, NoThemes: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, want := range []string{"extension builtin:mcp: Koine has no built-in extensions", "extension missing.mjs", "extension notes.txt: not a script",
		"extension badjson/extension.json: no command", "extension badjson: no extension found there", "extension emptydir: no extension found there",
		"skill " + filepath.Join(proj, "no-skill"), "prompt template " + filepath.Join(proj, "no-prompt.md"), "themes are not supported yet"} {
		if !warned(a, want) {
			t.Errorf("no warning %q in %v", want, a.Warnings)
		}
	}
	targets := a.explicitExtensions()
	var got []string
	for _, tg := range targets {
		got = append(got, tg.name)
	}
	if strings.Join(got, ",") != "good,dirext,jsonext,a" {
		t.Errorf("extension targets %v", got)
	}
}

func TestExtensionFlagsReachTheHostAndSurviveReload(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_MODEL", "")
	proj := t.TempDir()
	ext := filepath.Join(proj, "f.mjs")
	writeFile(t, ext, `export default function (pi) {
  pi.registerFlag("label", { type: "string", default: "d" });
  pi.registerCommand("show", { description: "s", handler: async (a, ctx) => ctx.ui.notify(String(pi.getFlag("label"))) });
}`)
	a, err := NewWithOptions(context.Background(), proj, Options{Load: LoadOptions{Extensions: []string{ext}}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if fl := a.ExtFlags(); len(fl) != 1 || fl[0].Name != "label" || fl[0].Type != "string" {
		t.Fatalf("flags %+v", fl)
	}
	show := func() string {
		out, ok, err := a.Host.RunCommand(context.Background(), "show", "")
		if !ok || err != nil {
			t.Fatalf("%v %v", ok, err)
		}
		return out
	}
	if show() != "d" {
		t.Errorf("default: %q", show())
	}
	a.SetExtFlags(map[string]any{"label": "set"})
	if show() != "set" {
		t.Errorf("after SetExtFlags: %q", show())
	}
	a.Reload(context.Background())
	if show() != "set" {
		t.Errorf("a reload must give the new extension process the same values: %q", show())
	}
}

func TestContextFilesCanBeSwitchedOff(t *testing.T) {
	files := map[string]string{"AGENTS.md": "RULES OF THE HOUSE"}
	if a := startApp(t, Options{}, files); !strings.Contains(strings.Join(a.Agent.ContextFiles, "\n"), "RULES OF THE HOUSE") {
		t.Errorf("context files %v", a.Agent.ContextFiles)
	}
	if a := startApp(t, Options{Load: LoadOptions{NoContextFiles: true}}, files); len(a.Agent.ContextFiles) != 0 {
		t.Errorf("-nc left %v", a.Agent.ContextFiles)
	}
}

func TestShellEnvironmentAndMarkers(t *testing.T) {
	a := startApp(t, Options{RunOptions: RunOptions{Model: "haiku"}}, nil)
	a.persist(llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: "x"}}}) // the session file exists once something is saved
	env := strings.Join(a.shellEnv(), "\n")
	for _, want := range []string{"KOINE_SESSION_ID=" + a.Session.ID, "KOINE_SESSION_FILE=" + a.Session.Path, "KOINE_SESSION_PROVIDER=anthropic",
		"KOINE_SESSION_MODEL=claude-haiku-5-5"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q:\n%s", want, env)
		}
	}
	a.Agent.Model = "openrouter/vendor/m"
	if p, m := a.ProviderAndModel(); p != "openrouter" || m != "vendor/m" {
		t.Errorf("prefixed: %q %q", p, m)
	}
	a.Agent.Model = "gpt-5"
	if p, m := a.ProviderAndModel(); p != "openai" || m != "gpt-5" {
		t.Errorf("inferred: %q %q", p, m)
	}
	a.Agent.Model = "mystery"
	if p, m := a.ProviderAndModel(); p != "" || m != "mystery" {
		t.Errorf("unknown: %q %q", p, m)
	}

	n := startApp(t, Options{RunOptions: RunOptions{NoSession: true}}, nil)
	first := strings.Join(n.shellEnv(), "\n")
	if strings.Contains(first, "KOINE_SESSION_FILE") || !strings.Contains(first, "KOINE_SESSION_ID="+n.ephemeralID) || n.ephemeralID == "" {
		t.Errorf("no session: %s", first)
	}
	if second := strings.Join(n.shellEnv(), "\n"); second != first {
		t.Errorf("the ephemeral id must stay the same:\n%s\n%s", first, second)
	}

	t.Setenv("AI_AGENT", "")
	t.Setenv("KOINE_CODING_AGENT", "")
	SetProcessMarkers()
	if os.Getenv("AI_AGENT") != "koine" || os.Getenv("KOINE_CODING_AGENT") != "true" {
		t.Error("process markers")
	}
}

func TestListModelsAndTokens(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".koine", "model-catalog.json"), `{"models":[{"id":"tiny","provider":"local","name":"Tiny One",
		"contextWindow":8192,"maxOutput":512}]}`)
	all := ListModels(home, "")
	for _, want := range []string{"provider", "claude-opus-5-5", "tiny", "8192", "512", "Tiny One", "1M"} {
		if !strings.Contains(all, want) {
			t.Errorf("listing lacks %q:\n%s", want, all)
		}
	}
	if got := ListModels(home, "tiny one"); strings.Contains(got, "claude") || !strings.Contains(got, "tiny") {
		t.Errorf("filter by name: %s", got)
	}
	if got := ListModels(home, "anthropic haiku"); strings.Contains(got, "opus") || !strings.Contains(got, "haiku") {
		t.Errorf("filter by words: %s", got)
	}
	if got := ListModels(home, "zzz"); got != "no models match" {
		t.Errorf("none: %q", got)
	}
	if tokens(200_000) != "200K" || tokens(1_000_000) != "1M" || tokens(1500) != "1500" || tokens(0) != "0" {
		t.Error("tokens")
	}
}
