package pkgs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Manager installs, removes and updates packages. Project-scoped operations change code that will run in the project, so they need
// Trusted (ADR 0002). Installing never runs a package's install scripts unless AllowScripts is set.
type Manager struct {
	Home, Cwd string
	Trusted   bool
	// NpmCommand is the command and leading arguments used to run npm (the npmCommand setting); empty means "npm".
	NpmCommand []string
	// AllowScripts lets npm run the install scripts of packages and their dependencies. Off by default: a script runs with the user's
	// rights as soon as it is installed, before anyone has looked at the package.
	AllowScripts bool
	// Log receives one line per step. May be nil.
	Log io.Writer
}

// ErrNotTrusted is returned for a project-scoped change in a folder that is not trusted.
var ErrNotTrusted = errors.New("this folder is not trusted, so project packages cannot be changed here: " +
	"pass --trust for this run, or trust the folder with /trust in a session")

func (m *Manager) logf(f string, a ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, f+"\n", a...)
	}
}

func (m *Manager) dirs(s Scope) Dirs { return DirsFor(s, m.Home, m.Cwd) }

func (m *Manager) gate(s Scope) error {
	if s == Project && !m.Trusted {
		return ErrNotTrusted
	}
	if m.dirs(s).Settings == "" {
		return errors.New("no home directory is known, so there is nowhere to keep user packages (set HOME or KOINE_AGENT_DIR)")
	}
	return nil
}

// PackageDir is where a package's files are: npm and git packages in the scope's install directory, a local package where it is.
func PackageDir(d Dirs, s Source) string {
	switch s.Kind {
	case NPM:
		return filepath.Join(d.Root, "npm", s.safeName(), "node_modules", filepath.FromSlash(s.Name))
	case Git:
		return filepath.Join(d.Root, "git", s.safeName())
	}
	return s.Path
}

// Installed is the outcome of getting a package's files in place.
type Installed struct {
	Source   Source
	Scope    Scope
	Dir      string
	Version  string // npm version, or the package.json version of a git or local package
	Commit   string // git commit
	Pinned   bool
	Lock     LockEntry
	Warnings []string
}

type mode int

const (
	modeInstall mode = iota // run the installer even if the files are there (the user asked for exactly this)
	modeUpdate              // move what is not pinned, install what is missing, leave the rest
)

// fetch puts the package's files in place and describes them.
func (m *Manager) fetch(ctx context.Context, d Dirs, src Source, md mode) (Installed, error) {
	in := Installed{Source: src, Scope: d.Scope, Dir: PackageDir(d, src), Pinned: src.Pinned()}
	var err error
	switch src.Kind {
	case NPM:
		err = m.fetchNPM(ctx, d, src, md, &in)
	case Git:
		err = m.fetchGit(ctx, d, src, md, &in)
	default:
		if st, serr := os.Stat(src.Path); serr != nil || !st.IsDir() {
			return in, fmt.Errorf("%s: not a directory", src.Path)
		}
	}
	if err != nil {
		return in, err
	}
	mf, _ := ReadManifest(in.Dir)
	if in.Version == "" {
		in.Version = mf.Version
	}
	in.Warnings = DependencyWarnings(mf)
	if src.Kind != Local {
		sum, err := HashTree(in.Dir)
		if err != nil {
			return in, err
		}
		in.Lock = LockEntry{Source: src.Raw, Kind: src.Kind, Version: in.Version, Commit: in.Commit, Integrity: sum}
		if src.Kind == NPM {
			in.Lock.Resolved, in.Lock.RegistryIntegrity = npmLockData(filepath.Join(d.Root, "npm", src.safeName()), src.Name)
		} else {
			in.Lock.Version = ""
		}
	}
	return in, nil
}

func (m *Manager) fetchNPM(ctx context.Context, d Dirs, src Source, md mode, in *Installed) error {
	prefix := filepath.Join(d.Root, "npm", src.safeName())
	have := installedVersion(in.Dir)
	if md == modeUpdate && have != "" && src.Pinned() {
		in.Version = have
		return nil
	}
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		return err
	}
	if d.Scope == Project {
		_ = os.WriteFile(filepath.Join(d.Root, ".gitignore"), []byte("*\n"), 0o644) // installed files are not part of the project's history
	}
	stub := filepath.Join(prefix, "package.json")
	if _, err := os.Stat(stub); err != nil {
		if err := os.WriteFile(stub, []byte("{\"name\":\"koine-package-host\",\"private\":true}\n"), 0o644); err != nil {
			return err
		}
	}
	spec := src.Name + "@" + src.Version
	if src.Version == "" {
		spec = src.Name + "@latest"
	}
	m.logf("npm install %s", spec)
	args := []string{"install", "--prefix", prefix, "--no-audit", "--no-fund", "--legacy-peer-deps", "--omit=dev"}
	if !m.AllowScripts {
		args = append(args, "--ignore-scripts")
	}
	if err := m.npm(ctx, prefix, append(args, "--", spec)...); err != nil {
		return err
	}
	if in.Version = installedVersion(in.Dir); in.Version == "" {
		return fmt.Errorf("npm install %s finished but %s has no package.json", spec, in.Dir)
	}
	return nil
}

func installedVersion(dir string) string {
	m, ok := ReadManifest(dir)
	if !ok {
		return ""
	}
	if m.Version == "" {
		return "0.0.0"
	}
	return m.Version
}

var commitRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

func (m *Manager) fetchGit(ctx context.Context, d Dirs, src Source, md mode, in *Installed) error {
	dir := in.Dir
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		if d.Scope == Project {
			_ = os.MkdirAll(d.Root, 0o755)
			_ = os.WriteFile(filepath.Join(d.Root, ".gitignore"), []byte("*\n"), 0o644)
		}
		m.logf("git clone %s", src.URL)
		if _, err := m.git(ctx, "", "-c", "protocol.ext.allow=never", "clone", "--quiet", "--", src.URL, dir); err != nil {
			return err
		}
	} else if md != modeUpdate || src.Ref == "" || m.revParse(ctx, dir, src.Ref) == "" || !isTagOrCommit(ctx, m, dir, src.Ref) {
		m.logf("git fetch %s", src.URL) // a tag or commit that is already here needs no fetch
		if _, err := m.git(ctx, dir, "fetch", "--quiet", "--tags", "--force", "origin"); err != nil {
			return err
		}
	}
	target, pinned := "", false
	if src.Ref == "" {
		target = m.revParse(ctx, dir, "origin/HEAD")
		if target == "" {
			target = m.revParse(ctx, dir, "HEAD")
		}
	} else {
		for _, cand := range []string{"refs/tags/" + src.Ref, src.Ref, "origin/" + src.Ref} {
			if target = m.revParse(ctx, dir, cand); target != "" {
				pinned = strings.HasPrefix(cand, "refs/tags/") || commitRe.MatchString(src.Ref) && cand == src.Ref
				break
			}
		}
		if target == "" {
			return fmt.Errorf("git %s: no tag, commit or branch %q", src.URL, src.Ref)
		}
	}
	if target == "" {
		return fmt.Errorf("git %s: the repository has no commits", src.URL)
	}
	if _, err := m.git(ctx, dir, "checkout", "--quiet", "--force", "--detach", target); err != nil {
		return err
	}
	in.Commit, in.Pinned = target, pinned
	if mf, ok := ReadManifest(dir); ok && len(mf.Dependencies) > 0 {
		m.logf("npm install (dependencies of %s)", src.URL)
		args := []string{"install", "--prefix", dir, "--no-audit", "--no-fund", "--legacy-peer-deps", "--omit=dev", "--no-save", "--no-package-lock"}
		if !m.AllowScripts {
			args = append(args, "--ignore-scripts")
		}
		if err := m.npm(ctx, dir, args...); err != nil {
			return err
		}
	}
	return nil
}

func isTagOrCommit(ctx context.Context, m *Manager, dir, ref string) bool {
	return commitRe.MatchString(ref) || m.revParse(ctx, dir, "refs/tags/"+ref) != ""
}

func (m *Manager) revParse(ctx context.Context, dir, rev string) string {
	out, err := m.git(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// git runs git; only protocols that fetch from a place are allowed (no remote helpers) and it never asks for a password.
func (m *Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", errors.New("git is needed to install git packages but was not found on PATH")
	}
	c := exec.CommandContext(ctx, path, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=file:git:http:https:ssh")
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", args[0], err, tail(string(out)))
	}
	return string(out), nil
}

func (m *Manager) npm(ctx context.Context, dir string, args ...string) error {
	cmd := m.NpmCommand
	if len(cmd) == 0 {
		cmd = []string{"npm"}
	}
	path, err := exec.LookPath(cmd[0])
	if err != nil {
		return fmt.Errorf("npm is needed to install npm packages but %q was not found on PATH: install Node.js, or set npmCommand in settings.json", cmd[0])
	}
	if m.AllowScripts {
		m.logf("install scripts are allowed (allowInstallScripts / --allow-scripts)")
	}
	c := exec.CommandContext(ctx, path, append(append([]string{}, cmd[1:]...), args...)...)
	c.Dir = dir
	c.Env = append(os.Environ(), "npm_config_update_notifier=false")
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("npm %s: %w: %s", args[0], err, tail(string(out)))
	}
	return nil
}

func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	return strings.Join(lines, "\n")
}

// ResolveSource parses a source typed on the command line: relative paths are taken from the working directory.
func (m *Manager) ResolveSource(raw string) (Source, error) { return Parse(raw, m.Cwd, m.Home) }

// settingsSource is the text to store in a settings file for a source: a local path is written so it still means the same thing from
// the settings file's own directory (relative for a project, so the repository is portable; absolute for the user).
func (m *Manager) settingsSource(d Dirs, s Source) string {
	if s.Kind != Local {
		return s.Raw
	}
	if d.Scope == Project {
		if rel, err := filepath.Rel(d.BaseDir(), s.Path); err == nil && within(m.Cwd, s.Path) {
			if !strings.HasPrefix(rel, ".") {
				rel = "./" + rel
			}
			return filepath.ToSlash(rel)
		}
	}
	return s.Path
}

// entryBase is the directory relative local paths of a scope's entries are taken from.
func (d Dirs) entryBase() string { return d.BaseDir() }

// Install installs a package into a scope and records it in the scope's settings and lockfile. An entry for the same package (same
// identity) is replaced, keeping its resource filters.
func (m *Manager) Install(ctx context.Context, scope Scope, raw string) (Installed, error) {
	if err := m.gate(scope); err != nil {
		return Installed{}, err
	}
	src, err := m.ResolveSource(raw)
	if err != nil {
		return Installed{}, err
	}
	d := m.dirs(scope)
	in, err := m.fetchChecked(ctx, d, src, modeInstall)
	if err != nil {
		return in, err
	}
	err = UpdateEntries(d.Settings, func(es []Entry) []Entry {
		ne := Entry{Source: m.settingsSource(d, src)}
		for i, e := range es {
			if s, perr := Parse(e.Source, d.entryBase(), m.Home); perr == nil && s.Identity == src.Identity {
				e.Source = ne.Source
				es[i] = e
				return es
			}
		}
		return append(es, ne)
	})
	return in, err
}

// fetchChecked is fetch plus the lockfile: the files of the same version or commit must be the files the lock recorded, and the lock
// then holds what was installed.
func (m *Manager) fetchChecked(ctx context.Context, d Dirs, src Source, md mode) (Installed, error) {
	in, err := m.fetch(ctx, d, src, md)
	if err != nil || src.Kind == Local {
		return in, err
	}
	lock, err := ReadLock(d.Lock)
	if err != nil {
		return in, err
	}
	if old, ok := lock.Packages[src.Identity]; ok && old.Version == in.Lock.Version && old.Commit == in.Lock.Commit && old.Integrity != in.Lock.Integrity {
		return in, fmt.Errorf("%s: the installed files do not match %s (integrity %s, expected %s): not recorded; "+
			"remove the package and install it again if you trust what is there now", src.Raw, d.Lock, in.Lock.Integrity, old.Integrity)
	}
	lock.Packages[src.Identity] = in.Lock
	return in, lock.Write(d.Lock)
}

// find returns the settings entry of the scope that is the same package as raw.
func (m *Manager) find(d Dirs, raw string) (Entry, Source, bool, error) {
	want, err := m.ResolveSource(raw)
	if err != nil {
		return Entry{}, Source{}, false, err
	}
	es, _, err := ReadEntries(d.Settings)
	if err != nil {
		return Entry{}, Source{}, false, err
	}
	for _, e := range es {
		if s, perr := Parse(e.Source, d.entryBase(), m.Home); perr == nil && s.Identity == want.Identity {
			return e, s, true, nil
		}
	}
	return Entry{}, want, false, nil
}

// Remove deletes a package from a scope: its settings entry, its lock entry and the files Koine installed. Local packages are never
// deleted. It reports false when the scope has no such package.
func (m *Manager) Remove(scope Scope, raw string) (bool, error) {
	if err := m.gate(scope); err != nil {
		return false, err
	}
	d := m.dirs(scope)
	_, src, ok, err := m.find(d, raw)
	if err != nil || !ok {
		return false, err
	}
	err = UpdateEntries(d.Settings, func(es []Entry) []Entry {
		var keep []Entry
		for _, e := range es {
			if s, perr := Parse(e.Source, d.entryBase(), m.Home); perr == nil && s.Identity == src.Identity {
				continue
			}
			keep = append(keep, e)
		}
		return keep
	})
	if err != nil {
		return true, err
	}
	if src.Kind != Local {
		dir := filepath.Join(d.Root, "git", src.safeName())
		if src.Kind == NPM {
			dir = filepath.Join(d.Root, "npm", src.safeName())
		}
		if within(d.Root, dir) {
			if err := os.RemoveAll(dir); err != nil {
				return true, err
			}
		}
		lock, err := ReadLock(d.Lock)
		if err != nil {
			return true, err
		}
		delete(lock.Packages, src.Identity)
		return true, lock.Write(d.Lock)
	}
	return true, nil
}

// Result is what Update did for one package.
type Result struct {
	Scope      Scope
	Source     Source
	Dir        string
	OldVersion string // version or commit before
	NewVersion string // version or commit after
	Note       string // why nothing was done ("pinned", "local")
	Installed  bool   // the files were not there and were installed
	// OldPackageVersion and NewPackageVersion are the `version` of the package's package.json before and after, whatever the source is
	// (the commit is not a version a changelog knows).
	OldPackageVersion, NewPackageVersion string
	Err                                  error
	Warnings                             []string
}

// Changed says that the package moved to a different version or commit.
func (r Result) Changed() bool {
	return r.Err == nil && r.OldVersion != "" && r.NewVersion != "" && r.OldVersion != r.NewVersion
}

func shown(in Installed) string {
	if in.Commit != "" {
		return in.Commit
	}
	return in.Version
}

// Update brings the packages of a scope in line with their settings: missing ones are installed, those that are not pinned move to the
// newest version (npm) or the tip of their branch (git), pinned ones are checked and left where they are. only, when set, names one
// package. One failing package does not stop the others.
func (m *Manager) Update(ctx context.Context, scope Scope, only string) ([]Result, error) {
	if err := m.gate(scope); err != nil {
		return nil, err
	}
	d := m.dirs(scope)
	es, _, err := ReadEntries(d.Settings)
	if err != nil {
		return nil, err
	}
	var want *Source
	if only != "" {
		s, err := m.ResolveSource(only)
		if err != nil {
			return nil, err
		}
		want = &s
	}
	lock, err := ReadLock(d.Lock)
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, e := range es {
		src, err := Parse(e.Source, d.entryBase(), m.Home)
		if err != nil {
			out = append(out, Result{Scope: scope, Source: Source{Raw: e.Source}, Err: err})
			continue
		}
		if want != nil && src.Identity != want.Identity {
			continue
		}
		r := Result{Scope: scope, Source: src, Dir: PackageDir(d, src), OldVersion: lock.Packages[src.Identity].Version}
		r.OldPackageVersion = installedVersion(r.Dir)
		if c := lock.Packages[src.Identity].Commit; c != "" {
			r.OldVersion = c
		}
		if src.Kind == Local {
			r.Note = "local, loaded in place"
			if _, err := m.fetch(ctx, d, src, modeUpdate); err != nil {
				r.Err = err
			}
			out = append(out, r)
			continue
		}
		_, statErr := os.Stat(r.Dir)
		in, err := m.fetchChecked(ctx, d, src, modeUpdate)
		if err != nil {
			r.Err = err
			out = append(out, r)
			continue
		}
		r.NewVersion, r.Warnings, r.Installed = shown(in), in.Warnings, statErr != nil
		r.NewPackageVersion = installedVersion(r.Dir)
		if in.Pinned && !r.Installed {
			r.Note = "pinned"
		}
		out = append(out, r)
	}
	if want != nil && len(out) == 0 {
		return nil, fmt.Errorf("%s is not in the %s packages", want.Raw, scope)
	}
	return out, nil
}
