package app

import (
	"strings"

	"koine/core/tools"
)

// isAdjustment says whether a tool-list entry is a "+name" or "-name" change to a selection rather than a plain name.
func isAdjustment(e string) bool { return strings.HasPrefix(e, "+") || strings.HasPrefix(e, "-") }

// builtinNames lists the tools Koine ships (read, write, edit, bash, ls, glob, grep).
func (a *App) builtinNames() map[string]bool {
	b := tools.NewRegistry()
	tools.RegisterBuiltins(b, a.Cwd)
	set := map[string]bool{}
	for _, n := range b.Names() {
		set[n] = true
	}
	return set
}

// matching returns the names among all that any of the entries names (plain names or * patterns).
func matching(all, entries []string) map[string]bool {
	set := map[string]bool{}
	for _, e := range entries {
		for _, n := range all {
			if tools.Match(e, n) {
				set[n] = true
			}
		}
	}
	return set
}

// adjust applies "+name" and "-name" entries to set, in order. Names and patterns that match no tool change nothing.
func adjust(set map[string]bool, all, entries []string) {
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e, "+"):
			for n := range matching(all, []string{e[1:]}) {
				set[n] = true
			}
		case strings.HasPrefix(e, "-"):
			for n := range matching(all, []string{e[1:]}) {
				delete(set, n)
			}
		}
	}
}

// defaultSelection applies the defaultTools setting. Plain names are the base selection (or everything, when there are none); "+name" adds
// and "-name" removes, in order. An empty list removes the built-in tools and keeps the rest. "builtin:" entries select extensions in Pi
// and are ignored here.
func (a *App) defaultSelection(spec []string) map[string]bool {
	all := a.Tools.Names()
	var plain, rest []string
	for _, e := range spec {
		switch {
		case strings.HasPrefix(e, "builtin:"):
		case isAdjustment(e):
			rest = append(rest, e)
		default:
			plain = append(plain, e)
		}
	}
	set := map[string]bool{}
	if len(plain) > 0 {
		set = matching(all, plain)
	} else {
		for _, n := range all {
			set[n] = true
		}
		if len(spec) == 0 {
			for n := range a.builtinNames() {
				delete(set, n)
			}
		}
	}
	adjust(set, all, rest)
	return set
}

// selectTools decides which tools the model is offered, from the command line and the defaultTools setting.
//
//	-nt                           none
//	-t a,b,mcp__x__*              exactly those (names or patterns)
//	otherwise                     the defaultTools selection, or everything; -nbt removes the built-in tools from it,
//	                              and "-t +name,-name" entries adjust it
//	-xt a,b                       always applied last, to whatever was selected
//
// With no tool options and no defaultTools setting the registry is offered as it is, so tools that arrive later stay visible.
func (a *App) selectTools() {
	o, l := a.opts, a.opts.Load
	if !o.NoTools && len(o.Tools) == 0 && !l.NoBuiltinTools && len(l.ExcludeTools) == 0 && a.Cfg.DefaultTools == nil {
		return
	}
	all := a.Tools.Names()
	var plain, adj []string
	for _, e := range o.Tools {
		if isAdjustment(e) {
			adj = append(adj, e)
		} else {
			plain = append(plain, e)
		}
	}
	var set map[string]bool
	switch {
	case o.NoTools:
		set = map[string]bool{}
	case len(plain) > 0:
		set = matching(all, plain)
	default:
		set = map[string]bool{}
		if a.Cfg.DefaultTools != nil {
			set = a.defaultSelection(a.Cfg.DefaultTools)
		} else {
			for _, n := range all {
				set[n] = true
			}
		}
		if l.NoBuiltinTools {
			for n := range a.builtinNames() {
				delete(set, n)
			}
		}
		adjust(set, all, adj)
	}
	for n := range matching(all, l.ExcludeTools) {
		delete(set, n)
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
