package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"koine/core/ext"
	"koine/core/prompts"
	"koine/core/skills"
)

// LoadOptions are the command line's choices of tools, resources and models beyond RunOptions. Like RunOptions they survive /reload.
type LoadOptions struct {
	ExcludeTools   []string // -xt: tools to remove, by name or * pattern, after every other selection
	NoBuiltinTools bool     // -nbt: drop the built-in tools from the default selection, keep extension and custom tools

	Extensions        []string // -e: extension files or directories, loaded even with -ne
	NoExtensions      bool     // -ne: skip discovered and configured extensions
	Skills            []string // --skill: skill files or directories
	NoSkills          bool     // -ns: skip discovered skills
	PromptTemplates   []string // --prompt-template: files or directories
	NoPromptTemplates bool     // -np: skip discovered prompt templates
	NoContextFiles    bool     // -nc: do not read AGENTS.md and CLAUDE.md files
	Themes            []string // --theme, --use-theme, --no-themes: accepted, but Koine has no themes yet
	NoThemes          bool

	Provider string   // --provider: restricts --model to one provider
	APIKey   string   // --api-key: key for this run, never written anywhere
	Models   []string // --models: patterns for the startup scope (replaces the enabledModels setting)

	ExtFlags map[string]any // values of the options that extensions registered with pi.registerFlag
}

// resolveUser expands a leading ~ and makes a relative path relative to the project, for paths given on the command line.
func (a *App) resolveUser(p string) string { return a.resolvePath(strings.TrimSpace(p)) }

// loadSkills is the discovered skills (unless -ns) behind the ones named with --skill.
func (a *App) loadSkills() []skills.Skill {
	l := a.opts.Load
	var found []skills.Skill
	if !l.NoSkills {
		var bad []skills.Skipped
		found, bad = skills.DiscoverReport(a.Cwd, a.Home)
		a.refused(bad)
	}
	if len(l.Skills) == 0 {
		return found
	}
	paths := make([]string, len(l.Skills))
	for i, p := range l.Skills {
		paths[i] = a.resolveUser(p)
	}
	explicit, bad, problems := skills.LoadPaths(paths)
	a.refused(bad)
	a.Warnings = append(a.Warnings, problems...)
	return skills.Merge(explicit, found)
}

func (a *App) refused(bad []skills.Skipped) {
	for _, b := range bad {
		a.Warnings = append(a.Warnings, fmt.Sprintf("skill %s refused: %s", b.Path, b.Reason))
	}
}

// loadPrompts is the discovered prompt templates (unless -np) behind the ones named with --prompt-template.
func (a *App) loadPrompts() []prompts.Prompt {
	l := a.opts.Load
	var found []prompts.Prompt
	if !l.NoPromptTemplates {
		found = prompts.Discover(a.Cwd, a.Home)
	}
	if len(l.PromptTemplates) == 0 {
		return found
	}
	paths := make([]string, len(l.PromptTemplates))
	for i, p := range l.PromptTemplates {
		paths[i] = a.resolveUser(p)
	}
	explicit, problems := prompts.LoadPaths(paths)
	a.Warnings = append(a.Warnings, problems...)
	return prompts.Merge(explicit, found)
}

// explicitExtensions turns the -e paths into extension targets. They are the user's own command line, not something found in the project,
// so the trust gate (which guards discovery) does not apply to them. A directory is itself an extension when it has extension.json or an
// index file, otherwise the extensions inside it are loaded as in a discovery directory.
func (a *App) explicitExtensions() []extTarget {
	var out []extTarget
	for _, raw := range a.opts.Load.Extensions {
		if strings.HasPrefix(raw, "builtin:") {
			a.Warnings = append(a.Warnings, "extension "+raw+": Koine has no built-in extensions")
			continue
		}
		p := a.resolveUser(raw)
		st, err := os.Stat(p)
		switch {
		case err != nil:
			a.Warnings = append(a.Warnings, fmt.Sprintf("extension %s: %v", raw, err))
		case !st.IsDir() && filepath.Base(p) == "extension.json":
			if t, ok := dirTarget(filepath.Dir(p), filepath.Base(filepath.Dir(p))); ok {
				out = append(out, t)
			} else {
				a.Warnings = append(a.Warnings, "extension "+raw+": no command in extension.json")
			}
		case !st.IsDir() && jsExt[filepath.Ext(p)]:
			out = append(out, extTarget{name: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), js: p})
		case !st.IsDir():
			a.Warnings = append(a.Warnings, "extension "+raw+": not a script (.js, .mjs, .cjs, .ts, .mts, .cts) or an extension.json")
		default:
			if t, ok := dirTarget(p, filepath.Base(p)); ok {
				out = append(out, t)
			} else if found := scanDir(p); len(found) > 0 {
				out = append(out, found...)
			} else {
				a.Warnings = append(a.Warnings, "extension "+raw+": no extension found there")
			}
		}
	}
	return out
}

// extensionTargets lists what to start: the -e paths first, then (unless -ne) the discovered and configured extensions. skipped counts
// the untrusted project entries that were left out.
func (a *App) extensionTargets() (out []extTarget, skipped int) {
	out = a.explicitExtensions()
	if !a.opts.Load.NoExtensions {
		found, n := discoverExtensions(a.Cwd, a.Home, a.Cfg, a.Trusted)
		out, skipped = append(out, found...), n
	}
	return out, skipped
}

// themeWarnings says that the theme options had no effect.
func (a *App) themeWarnings() {
	if l := a.opts.Load; len(l.Themes) > 0 || l.NoThemes {
		a.Warnings = append(a.Warnings, "themes are not supported yet: --theme, --use-theme and --no-themes were ignored")
	}
}

// ExtFlags lists the options the loaded extensions registered with pi.registerFlag.
func (a *App) ExtFlags() []ext.Flag { return a.Host.Flags() }

// SetExtFlags gives the extensions the command-line values of their options and remembers them for /reload.
func (a *App) SetExtFlags(values map[string]any) {
	a.opts.Load.ExtFlags = values
	a.Host.SetFlags(values)
}
