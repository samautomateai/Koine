package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putFile(t *testing.T, path, body string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appIn(t *testing.T, cwd string, o RunOptions) *App {
	t.Helper()
	a, err := NewWithOptions(context.Background(), cwd, Options{RunOptions: o})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func TestAgentDirOverrideMovesSettingsSessionsAndPrompts(t *testing.T) {
	isolate(t)
	dir, proj := t.TempDir(), t.TempDir()
	t.Setenv("KOINE_AGENT_DIR", dir)
	putFile(t, filepath.Join(dir, "settings.json"), `{"defaultModel":"from-agent-dir"}`)
	putFile(t, filepath.Join(dir, "SYSTEM.md"), "USER SYSTEM")
	putFile(t, filepath.Join(dir, "APPEND_SYSTEM.md"), "USER APPEND")
	a := appIn(t, proj, RunOptions{})
	if a.Agent.Model != "from-agent-dir" || a.Agent.Base != "USER SYSTEM" {
		t.Errorf("model=%q base=%q", a.Agent.Model, a.Agent.Base)
	}
	if !strings.Contains(strings.Join(a.Agent.ContextFiles, "|"), "USER APPEND") {
		t.Errorf("append file missing: %v", a.Agent.ContextFiles)
	}
	if a.Session == nil || !strings.HasPrefix(a.Session.Path, filepath.Join(dir, "sessions")) {
		t.Errorf("sessions belong under the agent dir: %v", a.Session)
	}
}

func TestProjectPromptFilesNeedTrustAndOverrideTheUsersWithoutCombining(t *testing.T) {
	isolate(t)
	home := os.Getenv("HOME")
	proj := t.TempDir()
	putFile(t, filepath.Join(home, ".koine", "SYSTEM.md"), "USER SYSTEM")
	putFile(t, filepath.Join(proj, ".koine", "SYSTEM.md"), "PROJECT SYSTEM")
	putFile(t, filepath.Join(proj, ".pi", "APPEND_SYSTEM.md"), "PI PROJECT APPEND")
	a := appIn(t, proj, RunOptions{})
	if a.Agent.Base != "PROJECT SYSTEM" || strings.Contains(a.Agent.Base, "USER") {
		t.Errorf("a trusted project file replaces the user's: %q", a.Agent.Base)
	}
	if !strings.Contains(strings.Join(a.Agent.ContextFiles, "|"), "PI PROJECT APPEND") {
		t.Errorf("Pi's directory is read too: %v", a.Agent.ContextFiles)
	}

	t.Setenv("KOINE_TRUST", "")
	u := appIn(t, proj, RunOptions{})
	if u.Trusted || u.Agent.Base != "USER SYSTEM" || strings.Contains(strings.Join(u.Agent.ContextFiles, "|"), "PI PROJECT") {
		t.Errorf("an untrusted project's prompt files must be ignored: trusted=%v base=%q ctx=%v", u.Trusted, u.Agent.Base, u.Agent.ContextFiles)
	}

	t.Setenv("KOINE_TRUST", "1")
	f := appIn(t, proj, RunOptions{System: "FLAG SYSTEM"})
	if f.Agent.Base != "FLAG SYSTEM" {
		t.Errorf("--system-prompt wins over SYSTEM.md: %q", f.Agent.Base)
	}
}

func TestPiAgentDirIsReadAsAFallbackAndBlankFilesAreIgnored(t *testing.T) {
	isolate(t)
	home := os.Getenv("HOME")
	proj := t.TempDir()
	putFile(t, filepath.Join(home, ".pi", "agent", "SYSTEM.md"), "PI USER SYSTEM")
	putFile(t, filepath.Join(home, ".koine", "SYSTEM.md"), "  \n")
	if a := appIn(t, proj, RunOptions{}); a.Agent.Base != "PI USER SYSTEM" {
		t.Errorf("%q", a.Agent.Base)
	}
}
