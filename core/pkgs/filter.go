package pkgs

import (
	"path"
	"strings"
)

// matchGlob matches a slash-separated name against a pattern in which * and ? stay inside one path segment, [..] is a class, and a
// segment of ** matches any number of segments (none included).
func matchGlob(pattern, name string) bool {
	return matchSegs(strings.Split(strings.TrimPrefix(pattern, "./"), "/"), strings.Split(strings.TrimPrefix(name, "./"), "/"))
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for len(pat) > 0 && pat[0] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegs(pat, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

func hasGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

// Resource is one thing a package supplies: Path is the file or directory to load, Rel its name inside the package (slash-separated),
// which is what filters are written against. For a skill Rel is its directory and Alt the SKILL.md inside it, so either spelling works.
type Resource struct {
	Path string
	Rel  string
	Alt  string
}

func (r Resource) names() []string {
	if r.Alt != "" {
		return []string{r.Rel, r.Alt}
	}
	return []string{r.Rel}
}

func (r Resource) matchesGlob(p string) bool {
	for _, n := range r.names() {
		if matchGlob(p, n) || (!strings.Contains(p, "/") && matchGlob(p, path.Base(n))) {
			return true
		}
	}
	return false
}

func (r Resource) matchesExact(p string) bool {
	p = strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, "\\", "/")), "./")
	for _, n := range r.names() {
		if n == p || r.Path == p {
			return true
		}
	}
	return false
}

// Filter applies the patterns of one resource list in a package entry to what the package supplies. Filters can only narrow: nothing
// outside res is ever added.
//
//	nil           everything
//	[]            nothing
//	pattern       only what matches (when any plain pattern is given)
//	!pattern      not what matches
//	+path         this exact path, even if the patterns left it out
//	-path         never this exact path
//
// The order is: plain patterns, then ! patterns, then + paths, then - paths, so - has the last word.
func Filter(res []Resource, patterns []string) []Resource {
	if patterns == nil {
		return res
	}
	var inc, exc, force, drop []string
	for _, p := range patterns {
		switch {
		case strings.HasPrefix(p, "!"):
			exc = append(exc, p[1:])
		case strings.HasPrefix(p, "+"):
			force = append(force, p[1:])
		case strings.HasPrefix(p, "-"):
			drop = append(drop, p[1:])
		default:
			inc = append(inc, p)
		}
	}
	var out []Resource
	for _, r := range res {
		keep := len(inc) == 0 && len(patterns) > 0 // with no plain pattern the list only adjusts: start from everything
		for _, p := range inc {
			if r.matchesGlob(p) {
				keep = true
			}
		}
		for _, p := range exc {
			if r.matchesGlob(p) {
				keep = false
			}
		}
		for _, p := range force {
			if r.matchesExact(p) {
				keep = true
			}
		}
		for _, p := range drop {
			if r.matchesExact(p) {
				keep = false
			}
		}
		if keep {
			out = append(out, r)
		}
	}
	return out
}
