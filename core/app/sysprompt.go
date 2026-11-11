package app

import (
	"os"
	"path/filepath"
	"strings"

	"koine/core/agentdir"
)

// promptFile finds SYSTEM.md or APPEND_SYSTEM.md. A trusted project's file wins over the user's; the two are never combined. Project files
// need trust like everything else in the project (ADR 0002). Koine's directories come first, then Pi's, for projects that use both.
func (a *App) promptFile(name string) string {
	var dirs []string
	if a.Trusted {
		dirs = append(dirs, filepath.Join(a.Cwd, ".koine"), filepath.Join(a.Cwd, ".pi"))
	}
	if a.Home != "" || os.Getenv(agentdir.EnvVar) != "" {
		dirs = append(dirs, agentdir.Of(a.Home))
	}
	if a.Home != "" {
		dirs = append(dirs, filepath.Join(a.Home, ".pi", "agent"))
	}
	for _, d := range dirs {
		if b, err := os.ReadFile(filepath.Join(d, name)); err == nil && strings.TrimSpace(string(b)) != "" {
			return string(b)
		}
	}
	return ""
}

// applyPromptFiles installs SYSTEM.md (replaces the default system prompt) and APPEND_SYSTEM.md (added after the context files). The
// command-line options are applied afterwards and take their place.
func (a *App) applyPromptFiles() {
	if s := a.promptFile("SYSTEM.md"); s != "" {
		a.Agent.Base = s
	}
	if s := a.promptFile("APPEND_SYSTEM.md"); s != "" {
		a.Agent.ContextFiles = append(a.Agent.ContextFiles, s)
	}
}
