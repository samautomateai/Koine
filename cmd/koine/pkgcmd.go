package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"koine/core/pkgs"
	"koine/core/trust"
)

const pkgUsage = `usage: koine install [-l] [--allow-scripts] <source>...   add packages (npm:name[@version], git:host/path[@ref], a git URL or a directory)
       koine remove  [-l] <source>...                      remove packages and the files Koine installed for them
       koine update  [-l] [--allow-scripts] [<source>]     install what is missing, move what is not pinned, show changelogs
       koine list    [-l]                                  list the configured packages
       koine config  [-l] [enable|disable <source> <resource>]   show, or switch, the resources of the packages

Packages are third-party code. User packages go to <agent dir>/settings.json (default ~/.koine), -l (--local) to ./.koine/settings.json. Changing
project packages needs a trusted folder: pass --trust (-a) for one run. Installs are recorded in packages.lock.json next to the settings file
and checked every time the package loads. npm and git are run as you have them installed. npm never runs install scripts unless you pass
--allow-scripts or set allowInstallScripts in the user settings. -C DIR chooses the project directory.
`

type pkgArgs struct {
	Cmd          string
	Local        bool
	Trust        bool
	AllowScripts bool
	Help         bool
	Dir          string
	Rest         []string
}

var pkgCommands = map[string]bool{"install": true, "remove": true, "update": true, "list": true, "config": true}

func parsePkgArgs(args []string) (pkgArgs, error) {
	a := pkgArgs{Cmd: args[0]}
	for i := 1; i < len(args); i++ {
		switch s := args[i]; s {
		case "-l", "--local":
			a.Local = true
		case "-a", "--trust":
			a.Trust = true
		case "--allow-scripts":
			a.AllowScripts = true
		case "-h", "--help":
			a.Help = true
		case "-C", "--cwd":
			if i+1 >= len(args) {
				return a, fmt.Errorf("%s needs a directory", s)
			}
			i++
			a.Dir = args[i]
		default:
			if strings.HasPrefix(s, "-") && len(s) > 1 {
				return a, fmt.Errorf("unknown option %s for %s", s, a.Cmd)
			}
			a.Rest = append(a.Rest, s)
		}
	}
	return a, nil
}

// packageCommand runs `koine install|remove|update|list|config` when the first argument is one of them. ok is false for anything else, so
// the caller goes on to the ordinary command line.
func packageCommand(args []string, st streams) (code int, ok bool) {
	if len(args) == 0 || !pkgCommands[args[0]] {
		return 0, false
	}
	a, err := parsePkgArgs(args)
	if err != nil {
		fmt.Fprintln(st.Err, "koine:", err)
		return 2, true
	}
	if a.Help {
		fmt.Fprint(st.Out, pkgUsage)
		return 0, true
	}
	home, _ := os.UserHomeDir()
	cwd := a.Dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	prefs := pkgs.ReadPrefs(home)
	m := &pkgs.Manager{
		Home: home, Cwd: cwd, NpmCommand: prefs.NpmCommand, AllowScripts: prefs.AllowScripts || a.AllowScripts, Log: st.Err,
		Trusted: a.Trust || trust.FromEnv() || trust.Open(home).IsTrusted(cwd),
	}
	scope := pkgs.User
	if a.Local {
		scope = pkgs.Project
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c := &pkgRun{m: m, a: a, st: st, scope: scope, prefs: prefs}
	switch a.Cmd {
	case "install":
		return c.install(ctx), true
	case "remove":
		return c.remove(), true
	case "update":
		return c.update(ctx), true
	case "list":
		return c.list(), true
	}
	return c.config(), true
}

type pkgRun struct {
	m     *pkgs.Manager
	a     pkgArgs
	st    streams
	scope pkgs.Scope
	prefs pkgs.Prefs
}

func (c *pkgRun) fail(what string, err error) int {
	fmt.Fprintf(c.st.Err, "koine: %s: %v\n", what, err)
	return 1
}

func (c *pkgRun) needSources() bool {
	if len(c.a.Rest) == 0 {
		fmt.Fprintf(c.st.Err, "koine: %s needs a package source\n%s", c.a.Cmd, pkgUsage)
		return false
	}
	return true
}

func countsLine(r pkgs.Resources) string {
	return fmt.Sprintf("%d extensions, %d skills, %d prompts, %d themes", len(r.Extensions), len(r.Skills), len(r.Prompts), len(r.Themes))
}

func short(v string) string {
	if len(v) == 40 {
		return v[:10]
	}
	return v
}

func (c *pkgRun) install(ctx context.Context) int {
	if !c.needSources() {
		return 2
	}
	code := 0
	for _, src := range c.a.Rest {
		in, err := c.m.Install(ctx, c.scope, src)
		if err != nil {
			code = c.fail("install "+src, err)
			continue
		}
		res, _ := pkgs.Resolve(in.Dir)
		at := in.Version
		if in.Commit != "" {
			at = short(in.Commit)
		}
		fmt.Fprintf(c.st.Out, "installed %s (%s) %s: %s\n  %s\n", src, c.scope, at, countsLine(res), in.Dir)
		for _, w := range in.Warnings {
			fmt.Fprintf(c.st.Err, "koine: warning: %s: %s\n", src, w)
		}
	}
	return code
}

func (c *pkgRun) remove() int {
	if !c.needSources() {
		return 2
	}
	code := 0
	for _, src := range c.a.Rest {
		ok, err := c.m.Remove(c.scope, src)
		switch {
		case err != nil:
			code = c.fail("remove "+src, err)
		case !ok:
			code = c.fail("remove "+src, fmt.Errorf("not in the %s packages", c.scope))
		default:
			fmt.Fprintf(c.st.Out, "removed %s (%s)\n", src, c.scope)
		}
	}
	return code
}

func (c *pkgRun) update(ctx context.Context) int {
	if len(c.a.Rest) > 1 {
		fmt.Fprintf(c.st.Err, "koine: update takes at most one package source\n")
		return 2
	}
	only := ""
	if len(c.a.Rest) == 1 {
		only = c.a.Rest[0]
	}
	scopes := []pkgs.Scope{pkgs.User, pkgs.Project}
	if c.a.Local {
		scopes = []pkgs.Scope{pkgs.Project}
	}
	code, seen := 0, 0
	var changed []pkgs.Result
	for _, sc := range scopes {
		if sc == pkgs.Project && !c.m.Trusted && !c.a.Local {
			if es, _, _ := pkgs.ReadEntries(pkgs.DirsFor(sc, c.m.Home, c.m.Cwd).Settings); len(es) > 0 {
				fmt.Fprintf(c.st.Err, "koine: the %d project package(s) are skipped: this folder is not trusted (pass --trust)\n", len(es))
			}
			continue
		}
		rs, err := c.m.Update(ctx, sc, only)
		if err != nil {
			if only != "" && strings.Contains(err.Error(), "is not in the") {
				continue
			}
			code = c.fail("update", err)
			continue
		}
		for _, r := range rs {
			seen++
			if c.report(r) {
				code = 1
			}
			if r.Changed() {
				changed = append(changed, r)
			}
		}
	}
	if only != "" && seen == 0 && code == 0 {
		return c.fail("update "+only, errors.New("not in the user or project packages"))
	}
	c.changelogs(changed)
	return code
}

// report prints what update did for one package and says whether it failed.
func (c *pkgRun) report(r pkgs.Result) bool {
	name := r.Source.Raw
	switch {
	case r.Err != nil:
		fmt.Fprintf(c.st.Err, "koine: update %s (%s): %v\n", name, r.Scope, r.Err)
		return true
	case r.Installed:
		fmt.Fprintf(c.st.Out, "installed %s (%s) %s\n", name, r.Scope, short(r.NewVersion))
	case r.Changed():
		fmt.Fprintf(c.st.Out, "updated %s (%s) %s -> %s\n", name, r.Scope, short(r.OldVersion), short(r.NewVersion))
	case r.Note != "":
		fmt.Fprintf(c.st.Out, "%s (%s): %s\n", name, r.Scope, r.Note)
	default:
		fmt.Fprintf(c.st.Out, "%s (%s): up to date\n", name, r.Scope)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(c.st.Err, "koine: warning: %s: %s\n", name, w)
	}
	return false
}

// changelogs prints what is new in the packages that moved to a newer version: the changelog sections between the old and the new
// version, or one line per package with collapseChangelog. Only packages that supply an extension qualify (Pi's rule).
func (c *pkgRun) changelogs(changed []pkgs.Result) {
	if len(changed) == 0 {
		return
	}
	loaded := map[string]pkgs.Loaded{}
	for _, l := range pkgs.Load(c.m.Home, c.m.Cwd, c.m.Trusted).Packages {
		loaded[string(l.Scope)+"|"+l.Source.Identity] = l
	}
	var lines []string
	for _, r := range changed {
		l, ok := loaded[string(r.Scope)+"|"+r.Source.Identity]
		if !ok || len(l.All.Extensions) == 0 || r.OldPackageVersion == r.NewPackageVersion {
			continue
		}
		name := l.Manifest.Name
		if name == "" {
			name = r.Source.Raw
		}
		if c.prefs.Collapse {
			lines = append(lines, pkgs.FirstLine(name, r.OldPackageVersion, r.NewPackageVersion))
		} else if n := pkgs.ChangelogNotice(l, r.OldPackageVersion, r.NewPackageVersion); n != "" {
			lines = append(lines, fmt.Sprintf("%s %s -> %s\n%s", name, r.OldPackageVersion, r.NewPackageVersion, n))
		}
	}
	if len(lines) > 0 {
		fmt.Fprintf(c.st.Out, "\nExtension Updates\n%s\n", strings.Join(lines, "\n\n"))
	}
}
