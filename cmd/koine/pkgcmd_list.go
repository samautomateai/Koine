package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"koine/core/pkgs"
)

func (c *pkgRun) scopes() []pkgs.Scope {
	if c.a.Local {
		return []pkgs.Scope{pkgs.Project}
	}
	return []pkgs.Scope{pkgs.User, pkgs.Project}
}

func (c *pkgRun) list() int {
	code := 0
	for _, sc := range c.scopes() {
		d := pkgs.DirsFor(sc, c.m.Home, c.m.Cwd)
		states, err := c.m.States(sc)
		if err != nil {
			code = c.fail("list", err)
			continue
		}
		if len(states) == 0 {
			fmt.Fprintf(c.st.Out, "%s packages: none (%s)\n", sc, d.Settings)
			continue
		}
		note := ""
		if sc == pkgs.Project && !c.m.Trusted {
			note = " [not loaded: this folder is not trusted]"
		}
		lock, _ := pkgs.ReadLock(d.Lock)
		fmt.Fprintf(c.st.Out, "%s packages (%s)%s:\n", sc, d.Settings, note)
		for _, ps := range states {
			status := "not installed"
			if ps.Installed {
				status = countsOf(ps)
			}
			at := ""
			if e, ok := lock.Packages[ps.Source.Identity]; ok {
				at = " " + short(firstNonEmpty(e.Commit, e.Version))
			}
			pin := ""
			if ps.Source.Pinned() && ps.Source.Kind != pkgs.Local {
				pin = " pinned"
			}
			fmt.Fprintf(c.st.Out, "  %s%s%s: %s\n    %s\n", ps.Entry.Source, at, pin, status, ps.Dir)
			for _, p := range ps.Problems {
				fmt.Fprintf(c.st.Err, "koine: warning: %s: %s\n", ps.Entry.Source, p)
			}
		}
	}
	return code
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// countsOf summarises the enabled resources of a package out of those it supplies.
func countsOf(ps pkgs.PackageState) string {
	on, all := map[string]int{}, map[string]int{}
	for _, r := range ps.Resources {
		all[r.Kind]++
		if r.Enabled {
			on[r.Kind]++
		}
	}
	var parts []string
	for _, k := range pkgs.Kinds {
		if all[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d/%d %s", on[k], all[k], k))
		}
	}
	if len(parts) == 0 {
		return "no resources"
	}
	return strings.Join(parts, ", ")
}

// config shows every resource of every package with a check mark when it is enabled. `config enable|disable <source> <resource>` switches
// one; in a terminal, `config` alone lets you switch resources by number.
func (c *pkgRun) config() int {
	if len(c.a.Rest) > 0 {
		verb := c.a.Rest[0]
		if (verb != "enable" && verb != "disable") || len(c.a.Rest) != 3 {
			fmt.Fprint(c.st.Err, "koine: config takes `enable|disable <source> <resource>`, or nothing to list\n"+pkgUsage)
			return 2
		}
		if err := c.m.SetEnabled(c.scopeFlag(), c.a.Rest[1], c.a.Rest[2], verb == "enable"); err != nil {
			return c.fail("config", err)
		}
		fmt.Fprintf(c.st.Out, "%sd %s in %s\n", verb, c.a.Rest[2], c.a.Rest[1])
		return 0
	}
	if c.st.InTTY && c.st.In != nil {
		return c.configLoop(c.st.In)
	}
	_, code := c.printConfig()
	return code
}

func (c *pkgRun) scopeFlag() pkgs.Scope {
	if c.a.Local {
		return pkgs.Project
	}
	return pkgs.User
}

type cfgRow struct {
	scope pkgs.Scope
	src   string
	kind  string
	rel   string
	on    bool
}

// printConfig lists the resources with numbers and returns the rows, so the interactive loop can act on a number.
func (c *pkgRun) printConfig() ([]cfgRow, int) {
	var rows []cfgRow
	code := 0
	for _, sc := range c.scopes() {
		states, err := c.m.States(sc)
		if err != nil {
			code = c.fail("config", err)
			continue
		}
		for _, ps := range states {
			fmt.Fprintf(c.st.Out, "%s (%s)\n", ps.Entry.Source, sc)
			if !ps.Installed {
				fmt.Fprintln(c.st.Out, "  not installed")
			}
			for _, r := range ps.Resources {
				mark := "[ ]"
				if r.Enabled {
					mark = "[x]"
				}
				rows = append(rows, cfgRow{sc, ps.Entry.Source, r.Kind, r.Res.Rel, r.Enabled})
				fmt.Fprintf(c.st.Out, "  %3d %s %-10s %s\n", len(rows), mark, r.Kind, r.Res.Rel)
			}
		}
	}
	if len(rows) == 0 && code == 0 {
		fmt.Fprintln(c.st.Out, "no packages (install one with `koine install <source>`)")
	}
	return rows, code
}

// configLoop is the terminal form of config: it lists the resources, then toggles the one whose number is typed, until a blank line.
func (c *pkgRun) configLoop(in io.Reader) int {
	sc := bufio.NewScanner(in)
	for {
		rows, code := c.printConfig()
		if code != 0 || len(rows) == 0 {
			return code
		}
		fmt.Fprint(c.st.Out, "number to toggle (blank to finish): ")
		if !sc.Scan() {
			fmt.Fprintln(c.st.Out)
			return 0
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || line == "q" {
			return 0
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(rows) {
			fmt.Fprintf(c.st.Out, "no resource %q\n", line)
			continue
		}
		r := rows[n-1]
		if err := c.m.SetEnabled(r.scope, r.src, r.rel, !r.on); err != nil {
			fmt.Fprintf(c.st.Err, "koine: config: %v\n", err)
		}
	}
}
