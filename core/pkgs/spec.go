// Package pkgs implements Pi packages: sources (npm, git, local paths), the `packages` setting, installation into a Koine-owned
// directory, a lockfile with integrity data, the package.json `pi` manifest and the resource filters of the settings file.
//
// Packages are third-party code. Nothing here runs a package; installation runs only `npm` and `git`, and never a package's install
// scripts unless the user asked for that. Callers (core/app, cmd/koine) keep project-scoped packages behind the trust gate (ADR 0002).
package pkgs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// Kind is the kind of a package source.
type Kind string

// The three kinds of source.
const (
	NPM   Kind = "npm"
	Git   Kind = "git"
	Local Kind = "local"
)

// Source is a parsed package source.
type Source struct {
	Raw     string // as written in the settings file or on the command line
	Kind    Kind
	Name    string // npm: the package name
	Version string // npm: the version, range or tag after the last @ ("" = the latest)
	URL     string // git: what to clone
	Ref     string // git: the tag, commit or branch after the last @ ("" = the default branch)
	Path    string // local: the absolute path
	// Identity is what decides that two entries are the same package: the npm name, the git URL without the ref, the absolute path.
	Identity string
}

var npmName = regexp.MustCompile(`^(@[A-Za-z0-9][A-Za-z0-9._~-]*/)?[A-Za-z0-9][A-Za-z0-9._~-]*$`)

// Pinned says that updates leave the package where it is: an npm package with a version, a git package at a tag or a commit. A git branch
// and an npm package without a version follow their upstream. The git case needs the repository, so it is decided at install time
// (Installed.Pinned); this reports the cases that can be told from the text.
func (s Source) Pinned() bool {
	switch s.Kind {
	case NPM:
		return s.Version != "" && s.Version != "latest"
	case Git:
		return s.Ref != ""
	}
	return true // a local path is never moved
}

// Parse reads a package source. Relative local paths are taken from base; a leading ~ is home.
//
//	npm:name, npm:@scope/name@1.2.3     an npm package (a version, range or tag pins it)
//	git:github.com/user/repo@v1         a git repository; @ref is a tag, commit or branch
//	https://host/user/repo.git          a git repository (also http, ssh, git and file URLs)
//	./dir, ../dir, /abs/dir, ~/dir      a directory, used where it is
func Parse(raw, base, home string) (Source, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return Source{}, errors.New("empty package source")
	case strings.HasPrefix(raw, "npm:"):
		return parseNPM(raw)
	case strings.HasPrefix(raw, "git:"):
		return parseGit(raw, strings.TrimPrefix(raw, "git:"), base, home)
	case hasURLScheme(raw) || strings.HasPrefix(raw, "git@") && strings.Contains(raw, ":"):
		return parseGit(raw, raw, base, home)
	case raw == "." || raw == ".." || strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") || strings.HasPrefix(raw, "~/") || raw == "~" ||
		filepath.IsAbs(raw):
		p := expandPath(raw, base, home)
		return Source{Raw: raw, Kind: Local, Path: p, Identity: p}, nil
	}
	return Source{}, fmt.Errorf("unrecognised package source %q: use npm:name[@version], git:host/path[@ref], a git URL or a directory path", raw)
}

func parseNPM(raw string) (Source, error) {
	spec := strings.TrimPrefix(raw, "npm:")
	name, version := spec, ""
	if i := strings.LastIndex(spec, "@"); i > 0 {
		name, version = spec[:i], spec[i+1:]
	}
	if !npmName.MatchString(name) {
		return Source{}, fmt.Errorf("package source %q: %q is not an npm package name", raw, name)
	}
	if strings.ContainsAny(version, " \t\r\n") || strings.HasPrefix(version, "-") || strings.HasSuffix(spec, "@") {
		return Source{}, fmt.Errorf("package source %q: bad version %q", raw, version)
	}
	return Source{Raw: raw, Kind: NPM, Name: name, Version: version, Identity: "npm:" + name}, nil
}

func hasURLScheme(s string) bool {
	for _, p := range []string{"https://", "http://", "ssh://", "git://", "file://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func parseGit(raw, spec, base, home string) (Source, error) {
	spec = strings.TrimSpace(spec)
	ref := ""
	if i := strings.LastIndex(spec, "@"); i > 0 && !strings.ContainsAny(spec[i+1:], "/:") && spec[i+1:] != "" {
		spec, ref = spec[:i], spec[i+1:]
	}
	if spec == "" || strings.HasPrefix(spec, "-") || strings.HasPrefix(ref, "-") || strings.ContainsAny(spec+ref, " \t\r\n") {
		return Source{}, fmt.Errorf("package source %q: not a git repository", raw)
	}
	if strings.Contains(spec, "::") { // git's remote-helper syntax (ext::sh -c …) runs commands
		return Source{}, fmt.Errorf("package source %q: remote helpers are not allowed", raw)
	}
	s := Source{Raw: raw, Kind: Git, Ref: ref}
	switch {
	case hasURLScheme(spec):
		u, err := url.Parse(spec)
		if err != nil || (u.Host == "" && u.Scheme != "file") {
			return Source{}, fmt.Errorf("package source %q: bad URL", raw)
		}
		s.URL = spec
		if u.Scheme == "file" {
			s.Identity = "git:" + filepath.Clean(u.Path)
		} else {
			s.Identity = "git:" + strings.ToLower(u.Host) + cleanRepoPath(u.Path)
		}
	case strings.HasPrefix(spec, "git@") && strings.Contains(spec, ":"): // scp-like: git@github.com:user/repo
		host, path, _ := strings.Cut(strings.TrimPrefix(spec, "git@"), ":")
		s.URL = spec
		s.Identity = "git:" + strings.ToLower(host) + cleanRepoPath("/"+path)
	case filepath.IsAbs(spec) || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "~/"):
		p := expandPath(spec, base, home)
		s.URL, s.Identity = p, "git:"+p
	default: // host/user/repo
		if !strings.Contains(spec, "/") || strings.Contains(spec, "..") {
			return Source{}, fmt.Errorf("package source %q: expected host/user/repo", raw)
		}
		host, path, _ := strings.Cut(spec, "/")
		s.URL = "https://" + spec
		s.Identity = "git:" + strings.ToLower(host) + cleanRepoPath("/"+path)
	}
	return s, nil
}

func cleanRepoPath(p string) string {
	p = strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(p, "/"), ".git"), "/")
	return p
}

func expandPath(p, base, home string) string {
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(home, p[2:])
	case !filepath.IsAbs(p):
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// safeName is a directory name for the package that is unique to its identity and holds nothing a path can misuse.
func (s Source) safeName() string {
	sum := sha256.Sum256([]byte(s.Identity))
	h := hex.EncodeToString(sum[:])[:8]
	label := s.Name
	if s.Kind == Git {
		label = filepath.Base(s.Identity)
	}
	label = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, label)
	label = strings.TrimLeft(label, ".")
	if label == "" {
		label = "pkg"
	}
	return label + "-" + h
}
