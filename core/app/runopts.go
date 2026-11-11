package app

import "os"

// RunOptions are per-run overrides from the command line. They survive /reload.
type RunOptions struct {
	Model        string   // --model: must resolve to a provider
	System       string   // --system-prompt: replaces the default base prompt (text, or the path of a file)
	AppendSystem string   // --append-system-prompt: added after the context files (text, or the path of a file)
	Tools        []string // -t: only these tools are offered (empty = all)
	NoTools      bool     // -nt: offer no tools at all
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
	}
}
