package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsPiKeysMapToKoine(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	u := filepath.Join(home, ".koine")
	cases := []struct {
		name, user, project, want string
	}{
		{"provider and model", `{"defaultProvider":"openrouter","defaultModel":"x/y"}`, ``, "openrouter/x/y"},
		{"model alone", `{"defaultModel":"claude-haiku-5-5"}`, ``, "claude-haiku-5-5"},
		{"the older model key", `{"model":"claude-sonnet-5-5"}`, ``, "claude-sonnet-5-5"},
		{"defaultModel beats model in the same file", `{"model":"old","defaultModel":"new"}`, ``, "new"},
		{"project beats user", `{"defaultModel":"u"}`, `{"defaultModel":"p"}`, "p"},
		{"settings.json beats config.json", ``, ``, ""},
	}
	for _, c := range cases {
		_ = os.RemoveAll(u)
		_ = os.RemoveAll(filepath.Join(proj, ".koine"))
		if c.user != "" {
			writeFile(t, filepath.Join(u, "settings.json"), c.user)
		}
		if c.project != "" {
			writeFile(t, filepath.Join(proj, ".koine", "settings.json"), c.project)
		}
		if cfg, _ := LoadConfig(proj, home); cfg.Model != c.want {
			t.Errorf("%s: model %q, want %q", c.name, cfg.Model, c.want)
		}
	}
	// the older file name still works and loses to settings.json
	writeFile(t, filepath.Join(u, "config.json"), `{"defaultModel":"legacy","maxTurns":7}`)
	writeFile(t, filepath.Join(u, "settings.json"), `{"defaultModel":"current"}`)
	cfg, _ := LoadConfig(proj, home)
	if cfg.Model != "current" || cfg.MaxTurns != 7 {
		t.Errorf("legacy merge: %+v", cfg)
	}
	// compaction uses Pi's names
	writeFile(t, filepath.Join(u, "settings.json"), `{"compaction":{"enabled":false,"reserveTokens":1000,"keepRecentTokens":2000}}`)
	cfg, _ = LoadConfig(proj, home)
	if c := cfg.Compaction; c.Enabled == nil || *c.Enabled || c.ReserveTokens != 1000 || c.KeepRecentTokens != 2000 {
		t.Errorf("compaction: %+v", c)
	}
}

func TestSettingsExtensionsMixedStringsAndObjects(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".koine", "settings.json"), `{"plugin":["./p.ts"],"extensions":["./a.ts",{"name":"x","command":["true"]},"./b.ts"]}`)
	cfg, _ := LoadConfig(t.TempDir(), home)
	if strings.Join(cfg.Plugin, ",") != "./p.ts,./a.ts,./b.ts" || len(cfg.Extensions) != 1 || cfg.Extensions[0].Name != "x" {
		t.Errorf("plugin=%v extensions=%+v", cfg.Plugin, cfg.Extensions)
	}
}

func startIn(t *testing.T, proj string, o Options) *App {
	t.Helper()
	a, err := NewWithOptions(context.Background(), proj, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func toolNames(a *App) string {
	return strings.Join(a.Agent.Tools.Names(), ",")
}

func TestSettingsSessionDirAndDefaultTools(t *testing.T) {
	isolate(t)
	home := os.Getenv("HOME")
	proj := t.TempDir()
	trusted := Options{Trust: true}
	settings := func(s string) { writeFile(t, filepath.Join(proj, ".koine", "settings.json"), s) }

	// sessionDir: relative to the project, ~ expands, and flag > env > setting
	settings(`{"sessionDir":"my-sessions"}`)
	if got := startIn(t, proj, trusted).SessionDir(); got != filepath.Join(proj, "my-sessions") {
		t.Errorf("relative: %s", got)
	}
	settings(`{"sessionDir":"~/s"}`)
	if got := startIn(t, proj, trusted).SessionDir(); got != filepath.Join(home, "s") {
		t.Errorf("tilde: %s", got)
	}
	t.Setenv("KOINE_SESSION_DIR", "/from/env")
	if got := startIn(t, proj, trusted).SessionDir(); got != "/from/env" {
		t.Errorf("env: %s", got)
	}
	flag := Options{Trust: true, RunOptions: RunOptions{SessionDir: "/from/flag"}}
	if got := startIn(t, proj, flag).SessionDir(); got != "/from/flag" {
		t.Errorf("flag: %s", got)
	}
	t.Setenv("KOINE_SESSION_DIR", "")
	// an untrusted project cannot choose where sessions are written
	if got := startIn(t, proj, Options{}).SessionDir(); strings.HasPrefix(got, proj) {
		t.Errorf("untrusted project redirected sessions to %s", got)
	}

	// defaultTools
	settings(`{"defaultTools":["read","bash"]}`)
	if got := toolNames(startIn(t, proj, trusted)); got != "bash,read" {
		t.Errorf("plain names: %s", got)
	}
	settings(`{"defaultTools":["read","+grep","-read"]}`)
	if got := toolNames(startIn(t, proj, trusted)); got != "grep" {
		t.Errorf("plain then adjustments: %s", got)
	}
	settings(`{"defaultTools":["-bash"]}`)
	if got := toolNames(startIn(t, proj, trusted)); strings.Contains(got, "bash") || !strings.Contains(got, "read") {
		t.Errorf("only removals keep everything else: %s", got)
	}
	settings(`{"defaultTools":[]}`)
	if got := toolNames(startIn(t, proj, trusted)); got != "" {
		t.Errorf("[] removes the built-in tools: %s", got)
	}
	settings(`{"defaultTools":["bash"]}`)
	cli := Options{Trust: true, RunOptions: RunOptions{Tools: []string{"read"}}}
	if got := toolNames(startIn(t, proj, cli)); got != "read" {
		t.Errorf("-t must beat the setting: %s", got)
	}
	// a project list of only +/- entries adjusts the user's selection; a plain list replaces it
	writeFile(t, filepath.Join(home, ".koine", "settings.json"), `{"defaultTools":["read","bash","grep"]}`)
	settings(`{"defaultTools":["-bash"]}`)
	if got := toolNames(startIn(t, proj, trusted)); got != "grep,read" {
		t.Errorf("adjust: %s", got)
	}
	settings(`{"defaultTools":["ls"]}`)
	if got := toolNames(startIn(t, proj, trusted)); got != "ls" {
		t.Errorf("replace: %s", got)
	}
}

func TestSettingsDiagnostics(t *testing.T) {
	isolate(t)
	home := os.Getenv("HOME")
	proj := t.TempDir()
	writeFile(t, filepath.Join(home, ".koine", "config.json"), `{"maxTurns":3}`)
	writeFile(t, filepath.Join(home, ".koine", "settings.json"), `{"defaultProjectTrust":"ask","theme":"dark"}`)
	writeFile(t, filepath.Join(proj, ".koine", "settings.json"),
		`{"defaultProjectTrust":"always","warnings":{"anthropicExtraUsage":true},"enableAnalytics":true,"bogus":1,"compaction":{"enabled":"yes"}}`)
	a := startIn(t, proj, Options{})
	got := strings.Join(a.Warnings, "\n")
	for _, want := range []string{"config.json is the old name", "defaultProjectTrust can only be set in the user settings",
		"warnings.anthropicExtraUsage is not supported yet", "enableAnalytics is not used by Koine by design", "unknown setting bogus",
		"compaction.enabled should be boolean"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	userGlobal := filepath.Join(home, ".koine", "settings.json") + ": defaultProjectTrust can only be set"
	if strings.Contains(got, "theme") || strings.Contains(got, userGlobal) {
		t.Errorf("inert keys and the user's own global keys must not be reported as misplaced:\n%s", got)
	}
	if d := SettingsDiagnostics(proj, ""); len(d) == 0 {
		t.Error("project file is still checked with no home")
	}
}
