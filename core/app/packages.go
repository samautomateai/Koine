package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"koine/core/agent"
	"koine/core/agentdir"
	"koine/core/events"
	"koine/core/pkgs"
	"koine/core/prompts"
	"koine/core/skills"
	"koine/core/srcinfo"
)

// loadPackages reads the packages of both scopes (pkgs.Load): user packages always, project packages only in a trusted folder. Nothing is
// installed or run here; a package that is missing or no longer matches its lockfile is reported and left out.
func (a *App) loadPackages() {
	a.pkgRes = pkgs.LoadResult{}
	if a.Home == "" && os.Getenv(agentdir.EnvVar) == "" {
		return
	}
	a.pkgRes = pkgs.Load(a.Home, a.Cwd, a.Trusted)
	a.Warnings = append(a.Warnings, a.pkgRes.Warnings...)
}

// Packages are the packages in use, for the frontends.
func (a *App) Packages() []pkgs.Loaded { return a.pkgRes.Packages }

// within reports whether p is dir or below it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sourceOfPath classifies something found on disk that no package supplied: it is the project's when it lies under the working directory
// and the user's otherwise; "auto" when it sits in one of the conventional directories, "local" when a setting named it.
func (a *App) sourceOfPath(p string) srcinfo.Info {
	info := srcinfo.Info{Path: p, Source: "local", Scope: "user", Origin: "top-level"}
	if within(a.Cwd, p) {
		info.Scope = "project"
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(p)), "/") {
		switch part {
		case "extensions", "skills", "prompts", "commands", "command", "skill", "plugin", "plugins":
			info.Source = "auto"
		}
	}
	return info
}

// path is the file or directory an extension target was found at.
func (t extTarget) path() string {
	switch {
	case t.js != "":
		return t.js
	case t.spec != nil && t.spec.Dir != "":
		return t.spec.Dir
	}
	return t.name
}

// targetSource is the provenance of an extension: what discovery recorded (packages, -e), or where it lies.
func (a *App) targetSource(t extTarget) srcinfo.Info {
	if !t.src.IsZero() {
		return t.src
	}
	if t.spec != nil && t.spec.Dir == "" { // an extension object written in a settings file; the merged config cannot say which file
		return srcinfo.Info{Path: t.name, Source: "local", Scope: "user", Origin: "top-level"}
	}
	return a.sourceOfPath(t.path())
}

func resourcePaths(rs []pkgs.Resource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Path
	}
	return out
}

// packageExtensionTargets are the extensions the loaded packages supply, after the entries' filters. A resource that is a script is
// itself an extension; a directory is read as an extension directory (extension.json, package.json pi.extensions, index file).
func (a *App) packageExtensionTargets() []extTarget {
	var out []extTarget
	for _, l := range a.pkgRes.Packages {
		for _, r := range l.Resources.Extensions {
			var ts []extTarget
			if st, err := os.Stat(r.Path); err != nil {
				continue
			} else if st.IsDir() {
				ts = dirTargets(r.Path, filepath.Base(r.Path))
			} else if filepath.Base(r.Path) == "extension.json" {
				ts = dirTargets(filepath.Dir(r.Path), filepath.Base(filepath.Dir(r.Path)))
			} else if jsExt[filepath.Ext(r.Path)] {
				ts = []extTarget{{name: strings.TrimSuffix(filepath.Base(r.Path), filepath.Ext(r.Path)), js: r.Path}}
			}
			for _, t := range ts {
				t.src = srcinfo.Package(r.Path, l.Source.Raw, string(l.Scope), l.Dir)
				out = append(out, t)
			}
		}
	}
	return out
}

// packageSkills loads the skills of the loaded packages. They come after the conventional directories, so those win name clashes.
func (a *App) packageSkills() []skills.Skill {
	var out []skills.Skill
	for _, l := range a.pkgRes.Packages {
		found, bad, problems := skills.LoadPaths(resourcePaths(l.Resources.Skills))
		a.refused(bad)
		for _, p := range problems {
			a.Warnings = append(a.Warnings, fmt.Sprintf("package %s: %s", l.Source.Raw, p))
		}
		for i := range found {
			found[i].Source = srcinfo.Package(found[i].Location, l.Source.Raw, string(l.Scope), l.Dir)
		}
		out = skills.Merge(out, found)
	}
	return out
}

// packagePrompts loads the prompt templates of the loaded packages.
func (a *App) packagePrompts() []prompts.Prompt {
	var out []prompts.Prompt
	for _, l := range a.pkgRes.Packages {
		found, problems := prompts.LoadPaths(resourcePaths(l.Resources.Prompts))
		for _, p := range problems {
			a.Warnings = append(a.Warnings, fmt.Sprintf("package %s: %s", l.Source.Raw, p))
		}
		for i := range found {
			found[i].Source = srcinfo.Package(found[i].Path, l.Source.Raw, string(l.Scope), l.Dir)
		}
		out = prompts.Merge(out, found)
	}
	return out
}

// discoverResources sends resources_discover to the extensions and loads the skills and prompt templates they name, after the ones that
// are already loaded. reason is "startup" or "reload". Theme paths are accepted and reported as unused: Koine has no themes.
func (a *App) discoverResources(ctx context.Context, reason string) {
	d := a.Host.Emit(ctx, events.ResourcesDiscover, map[string]any{"cwd": a.Cwd, "reason": reason})
	r := d.Resources
	if r.Empty() {
		return
	}
	from := func(p string) srcinfo.Info {
		return srcinfo.Info{Path: p, Source: "resources_discover", Scope: "temporary", Origin: "top-level"}
	}
	if len(r.SkillPaths) > 0 && !a.opts.Load.NoSkills {
		found, bad, problems := skills.LoadPaths(r.SkillPaths)
		a.refused(bad)
		a.Warnings = append(a.Warnings, problems...)
		for i := range found {
			found[i].Source = from(found[i].Location)
		}
		if len(found) > 0 {
			a.Agent.Skills = skills.Merge(a.Agent.Skills, found)
			a.Tools.Register(skills.Tool(a.Agent.Skills))
			a.Tools.SetSource("skill", srcinfo.Builtin("skill"))
			a.selectTools()
		}
	}
	if len(r.PromptPaths) > 0 && !a.opts.Load.NoPromptTemplates {
		found, problems := prompts.LoadPaths(r.PromptPaths)
		a.Warnings = append(a.Warnings, problems...)
		for i := range found {
			found[i].Source = from(found[i].Path)
		}
		a.Prompts = prompts.Merge(a.Prompts, found)
	}
	if len(r.ThemePaths) > 0 {
		a.Info = append(a.Info, fmt.Sprintf("%d theme path(s) from extensions ignored: Koine has no themes", len(r.ThemePaths)))
	}
}

// Provenance is one row of /sources: what a loaded thing is and where it came from.
type Provenance struct {
	Kind, Name string
	Source     srcinfo.Info
}

// Provenance lists the source of every loaded tool, skill, prompt template and extension command (Pi's sourceInfo), sorted by kind and name.
func (a *App) Provenance() []Provenance {
	var out []Provenance
	for _, n := range a.Tools.Names() {
		if s, ok := a.Tools.SourceOf(n); ok {
			out = append(out, Provenance{"tool", n, s})
		}
	}
	for _, s := range a.Agent.Skills {
		out = append(out, Provenance{"skill", s.Name, s.Source})
	}
	for _, p := range a.Prompts {
		out = append(out, Provenance{"prompt", p.Name, p.Source})
	}
	for _, c := range a.Host.Commands() {
		out = append(out, Provenance{"command", c.Name, c.Source})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// packageCommand handles /packages and /sources. It reports whether name was one of them.
func (a *App) packageCommand(name, args string, emit func(agent.Event)) bool {
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	switch name {
	case "packages":
		if len(a.pkgRes.Packages) == 0 {
			notice("(no packages; install one with `koine install <source>`)")
		}
		for _, l := range a.pkgRes.Packages {
			r := l.Resources
			notice(fmt.Sprintf("%s (%s): %d extensions, %d skills, %d prompts, %d themes  %s",
				l.Source.Raw, l.Scope, len(r.Extensions), len(r.Skills), len(r.Prompts), len(r.Themes), l.Dir))
		}
		return true
	case "sources":
		rows := a.Provenance()
		n := 0
		for _, p := range rows {
			if args != "" && !strings.Contains(p.Kind+" "+p.Name+" "+p.Source.Source, args) {
				continue
			}
			n++
			notice(fmt.Sprintf("%-7s %-24s %s  scope=%s origin=%s  %s", p.Kind, p.Name, p.Source.Source, p.Source.Scope, p.Source.Origin, p.Source.Path))
		}
		if n == 0 {
			notice("(nothing matches)")
		}
		return true
	}
	return false
}

// markBuiltin records the built-in tools' provenance.
func (a *App) markBuiltin(names ...string) {
	if len(names) == 0 {
		names = a.Tools.Names()
	}
	for _, n := range names {
		if _, known := a.Tools.SourceOf(n); !known {
			a.Tools.SetSource(n, srcinfo.Builtin(n))
		}
	}
}
