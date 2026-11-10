package app

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The repository ships agent configuration for Claude Code, opencode, Pi and Koine itself.
// This test fails if any of it stops loading in Koine (REQ-AGENTDEV-001).
func TestRepoAgentConfigLoads(t *testing.T) {
	isolate(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	info := a.Describe()

	for _, w := range a.Warnings {
		if strings.Contains(w, "skill") {
			t.Errorf("repo skill rejected: %s", w)
		}
	}
	for _, want := range []string{"spec-driven-change", "quality-gates", "upstream-pi-sync"} {
		if !contains(info.Skills, want) {
			t.Errorf("skill %q not discovered; have %v", want, info.Skills)
		}
	}
	for _, want := range []string{"reviewer", "docs-keeper"} {
		if !contains(info.Agents, want) {
			t.Errorf("agent %q not discovered; have %v", want, info.Agents)
		}
	}
	for _, want := range []string{"spec-check", "new-feature"} {
		if !contains(info.Commands, want) {
			t.Errorf("command %q not discovered; have %v", want, info.Commands)
		}
	}
	// the same asset reachable through several ecosystem paths must appear once
	for name, list := range map[string][]string{"skills": info.Skills, "agents": info.Agents, "commands": info.Commands} {
		s := append([]string(nil), list...)
		sort.Strings(s)
		for i := 1; i < len(s); i++ {
			if s[i] == s[i-1] {
				t.Errorf("%s: %q listed twice", name, s[i])
			}
		}
	}
	if len(a.Agent.ContextFiles) == 0 {
		t.Error("AGENTS.md / CLAUDE.md not picked up as context")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
