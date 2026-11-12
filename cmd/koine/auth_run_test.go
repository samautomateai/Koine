package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestARunUsesTheStoredKeyBeforeTheEnvironmentAndTheFlagBeforeBoth(t *testing.T) {
	dir := authEnv(t)
	rec := newRecorder(t, "", nil)
	t.Setenv("ANTHROPIC_BASE_URL", rec.URL)
	t.Setenv("ANTHROPIC_API_KEY", "fake-env-key")
	if code, _, errs := authCLI(t, "fake-stored-key\n", true, "login", "anthropic", "--api-key"); code != 0 {
		t.Fatal(errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if code, _, errs := cli("", "-C", proj, "--no-session", "-p", "hi"); code != 0 {
		t.Fatalf("run: %s", errs)
	}
	if code, _, errs := cli("", "-C", proj, "--no-session", "--model", "claude-sonnet-5-5", "--api-key", "fake-flag-key", "-p", "hi"); code != 0 {
		t.Fatalf("run: %s", errs)
	}
	if len(rec.Keys) != 2 || rec.Keys[0] != "fake-stored-key" || rec.Keys[1] != "fake-flag-key" {
		t.Errorf("keys seen by the server: %v", rec.Keys)
	}
}

func TestARunWithoutAKeyExplainsHowToGetOne(t *testing.T) {
	authEnv(t)
	code, _, errs := cli("", "-C", t.TempDir(), "--no-session", "--model", "groq/llama", "-p", "hi")
	if code == 0 || !strings.Contains(errs, "GROQ_API_KEY") || !strings.Contains(errs, "koine auth login groq") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}
