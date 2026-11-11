package app

import (
	"os"
	"strings"

	"koine/core/tools"
)

// RunOptions are per-run overrides from the command line. They survive /reload.
type RunOptions struct {
	Model        string   // --model: must resolve to a provider
	System       string   // --system-prompt: replaces the default base prompt (text, or the path of a file)
	AppendSystem string   // --append-system-prompt: added after the context files (text, or the path of a file)
	Tools        []string // -t: only these tools are offered (empty = all)
	NoTools      bool     // -nt: offer no tools at all
	Thinking     string   // --thinking: the thinking level (off, minimal, low, medium, high, xhigh, max)

	SessionDir string // --session-dir: where session files live (default ~/.koine/sessions/--<cwd>--)
	NoSession  bool   // --no-session: keep nothing on disk
	Continue   bool   // -c: continue the most recent session in this project
	SessionRef string // --session: resume this session (file path, id, id prefix or name)
	Fork       string // --fork: start a new session from a copy of this one
	Name       string // -n: display name for the session
}

// textOrFile returns the contents of the file named by s when there is one, otherwise s itself.
func textOrFile(s string) string {
	if s == "" {
		return ""
	}
	if b, err := os.ReadFile(s); err == nil {
		return string(b)
	}
	return s
}

func (a *App) applyRunOptions() {
	o := a.opts
	a.applyPromptFiles()
	if o.System != "" {
		a.Agent.Base = textOrFile(o.System)
	}
	if o.AppendSystem != "" {
		a.Agent.ContextFiles = append(a.Agent.ContextFiles, textOrFile(o.AppendSystem))
	}
	switch {
	case o.NoTools:
		a.Agent.Tools = a.Tools.Filtered(nil, a.Tools.Names())
	case len(o.Tools) > 0:
		a.Agent.Tools = a.Tools.Filtered(o.Tools, nil)
	case a.Cfg.DefaultTools != nil:
		a.pickTools(a.Cfg.DefaultTools)
	}
}

// pickTools applies the defaultTools setting. Plain names are the base selection (or everything, when there are none); "+name" adds,
// "-name" removes, in order. An empty list removes the built-in tools and keeps the rest. "builtin:" entries select extensions in Pi and
// are ignored here.
func (a *App) pickTools(spec []string) {
	all := a.Tools.Names()
	set := map[string]bool{}
	plain := false
	for _, e := range spec {
		if !strings.HasPrefix(e, "+") && !strings.HasPrefix(e, "-") && !strings.HasPrefix(e, "builtin:") {
			plain = true
			set[tools.Canon(e)] = true
		}
	}
	if !plain {
		for _, n := range all {
			set[n] = true
		}
		if len(spec) == 0 {
			b := tools.NewRegistry()
			tools.RegisterBuiltins(b, a.Cwd)
			for _, n := range b.Names() {
				delete(set, n)
			}
		}
	}
	for _, e := range spec {
		switch {
		case strings.HasPrefix(e, "builtin:"):
		case strings.HasPrefix(e, "+"):
			set[tools.Canon(e[1:])] = true
		case strings.HasPrefix(e, "-"):
			delete(set, tools.Canon(e[1:]))
		}
	}
	var keep []string
	for _, n := range all {
		if set[n] {
			keep = append(keep, n)
		}
	}
	if len(keep) == 0 {
		a.Agent.Tools = a.Tools.Filtered(nil, all)
		return
	}
	a.Agent.Tools = a.Tools.Filtered(keep, nil)
}
