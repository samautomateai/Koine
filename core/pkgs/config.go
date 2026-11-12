package pkgs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kinds are the resource kinds of a package, in the order they are listed.
var Kinds = []string{"extensions", "skills", "prompts", "themes"}

// ResourceState is one resource of a package and whether the package's entry lets it load.
type ResourceState struct {
	Kind    string
	Res     Resource
	Enabled bool
}

// PackageState is a configured package with the state of all its resources.
type PackageState struct {
	Scope     Scope
	Entry     Entry
	Source    Source
	Dir       string
	Installed bool
	Resources []ResourceState
	Problems  []string
}

func lists(e *Entry, kind string) *[]string {
	switch kind {
	case "extensions":
		return &e.Extensions
	case "skills":
		return &e.Skills
	case "prompts":
		return &e.Prompts
	}
	return &e.Themes
}

func resourcesOf(r Resources, kind string) []Resource {
	switch kind {
	case "extensions":
		return r.Extensions
	case "skills":
		return r.Skills
	case "prompts":
		return r.Prompts
	}
	return r.Themes
}

// States lists the packages configured in a scope with every resource they supply and whether it is enabled (the view `koine config`
// shows). Packages whose files are not there have no resources.
func (m *Manager) States(scope Scope) ([]PackageState, error) {
	d := m.dirs(scope)
	es, problems, err := ReadEntries(d.Settings)
	if err != nil {
		return nil, err
	}
	var out []PackageState
	for _, e := range es {
		src, perr := Parse(e.Source, d.entryBase(), m.Home)
		if perr != nil {
			problems = append(problems, perr.Error())
			continue
		}
		ps := PackageState{Scope: scope, Entry: e, Source: src, Dir: PackageDir(d, src)}
		if st, serr := os.Stat(ps.Dir); serr == nil && st.IsDir() {
			ps.Installed = true
			all, probs := Resolve(ps.Dir)
			ps.Problems = probs
			for _, kind := range Kinds {
				enabled := map[string]bool{}
				for _, r := range Filter(resourcesOf(all, kind), *lists(&e, kind)) {
					enabled[r.Path] = true
				}
				for _, r := range resourcesOf(all, kind) {
					ps.Resources = append(ps.Resources, ResourceState{Kind: kind, Res: r, Enabled: enabled[r.Path]})
				}
			}
		}
		out = append(out, ps)
	}
	if len(out) == 0 && len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return out, nil
}

// SetEnabled turns one resource of a package on or off by editing the package's entry in the scope's settings: a `-path` entry keeps it
// out, a `+path` entry puts it in even when the other patterns leave it out. path is the resource's name inside the package, as `koine
// config` lists it. A plain string entry becomes an object.
func (m *Manager) SetEnabled(scope Scope, raw, path string, on bool) error {
	if err := m.gate(scope); err != nil {
		return err
	}
	states, err := m.States(scope)
	if err != nil {
		return err
	}
	// raw is a source as typed on the command line (relative to the working directory) or as written in the settings file (relative to it)
	d := m.dirs(scope)
	ids := map[string]bool{}
	if s, err := m.ResolveSource(raw); err == nil {
		ids[s.Identity] = true
	}
	if s, err := Parse(raw, d.entryBase(), m.Home); err == nil {
		ids[s.Identity] = true
	}
	if len(ids) == 0 {
		return fmt.Errorf("%q is not a package source", raw)
	}
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	for _, ps := range states {
		if !ids[ps.Source.Identity] {
			continue
		}
		if !ps.Installed {
			return fmt.Errorf("%s is not installed; run `koine update` first", raw)
		}
		for _, rs := range ps.Resources {
			if rs.Res.Rel != path && rs.Res.Alt != path {
				continue
			}
			return m.rewrite(scope, ps, rs, on)
		}
		return fmt.Errorf("%s supplies no resource named %q (run `koine config` to list them)", raw, path)
	}
	return fmt.Errorf("%s is not in the %s packages", raw, scope)
}

func (m *Manager) rewrite(scope Scope, ps PackageState, rs ResourceState, on bool) error {
	d := m.dirs(scope)
	return UpdateEntries(d.Settings, func(es []Entry) []Entry {
		for i, e := range es {
			if s, err := Parse(e.Source, d.entryBase(), m.Home); err != nil || s.Identity != ps.Source.Identity {
				continue
			}
			l := lists(&es[i], rs.Kind)
			orig, hadPlain := *l, false
			var keep []string
			for _, p := range orig {
				if !strings.HasPrefix(p, "!") && !strings.HasPrefix(p, "+") && !strings.HasPrefix(p, "-") {
					hadPlain = true
				}
				if p == "+"+rs.Res.Rel || p == "-"+rs.Res.Rel {
					continue // an earlier decision about this very resource
				}
				keep = append(keep, p)
			}
			switch {
			case len(keep) == 0 && (orig == nil || len(orig) > 0 && !hadPlain):
				keep = nil // only adjustments were left: back to "everything"
			case keep == nil:
				keep = []string{}
			}
			in := contains(Filter([]Resource{rs.Res}, keep), rs.Res)
			switch {
			case on && !in && len(keep) == 0:
				keep = []string{rs.Res.Rel} // the list said "none": now it names this one
			case on && !in:
				keep = append(keep, "+"+rs.Res.Rel)
			case !on && in:
				keep = append(keep, "-"+rs.Res.Rel)
			}
			*l = keep
			return es
		}
		return es
	})
}

func contains(rs []Resource, r Resource) bool {
	for _, x := range rs {
		if x.Path == r.Path {
			return true
		}
	}
	return false
}
