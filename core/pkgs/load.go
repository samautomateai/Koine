package pkgs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Prefs are the user-level settings that change how packages are installed. They are read from the user's settings file only: a project
// file must not be able to pick the program that installs packages, or switch install scripts on.
type Prefs struct {
	NpmCommand   []string // npmCommand
	AllowScripts bool     // allowInstallScripts (Koine only)
	Collapse     bool     // collapseChangelog
	// ModelCatalogURL is modelCatalogUrl, where `koine update --models` fetches the model catalog from (a Koine-only setting).
	ModelCatalogURL string
}

// ReadPrefs reads Prefs from the user's settings file. A missing or unreadable file gives the defaults.
func ReadPrefs(home string) Prefs {
	var p Prefs
	d, err := readDoc(DirsFor(User, home, "").Settings, true)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(d.vals["npmCommand"], &p.NpmCommand)
	_ = json.Unmarshal(d.vals["allowInstallScripts"], &p.AllowScripts)
	_ = json.Unmarshal(d.vals["collapseChangelog"], &p.Collapse)
	_ = json.Unmarshal(d.vals["modelCatalogUrl"], &p.ModelCatalogURL)
	return p
}

// Loaded is one package ready to be used: where it is, and what it supplies after the entry's filters.
type Loaded struct {
	Source Source
	Scope  Scope
	Dir    string
	Entry  Entry
	// Resources are the package's resources after the entry's filters; All are the resources before them.
	Resources, All Resources
	Manifest       Manifest
}

// LoadResult is the packages to use and what to tell the user about the rest.
type LoadResult struct {
	Packages []Loaded
	Warnings []string
	// SkippedProject counts project packages left out because the folder is not trusted.
	SkippedProject int
}

// Load reads the packages of both scopes and resolves them. Project entries replace user entries for the same package; an entry with
// autoload false instead narrows the user's entry. Project packages are loaded only when trusted. Packages that are not installed, or whose
// files no longer match the lockfile, are reported and left out. Nothing is installed or run here.
func Load(home, cwd string, trusted bool) LoadResult {
	var res LoadResult
	ud, pd := DirsFor(User, home, cwd), DirsFor(Project, home, cwd)
	user, problems, err := ReadEntries(ud.Settings)
	res.Warnings = append(res.Warnings, problems...)
	if err != nil {
		res.Warnings = append(res.Warnings, err.Error())
	}
	var proj []Entry
	if trusted {
		p, problems, err := ReadEntries(pd.Settings)
		proj = p
		res.Warnings = append(res.Warnings, problems...)
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		}
	} else if p, _, _ := ReadEntries(pd.Settings); len(p) > 0 {
		res.SkippedProject = len(p)
	}

	type item struct {
		e Entry
		s Source
		d Dirs
	}
	identity := map[string]int{} // identity -> index in items
	var items []item
	add := func(e Entry, d Dirs) {
		s, err := Parse(e.Source, d.BaseDir(), home)
		if err != nil {
			res.Warnings = append(res.Warnings, d.Settings+": "+err.Error())
			return
		}
		if i, dup := identity[s.Identity]; dup {
			if d.Scope == Project && e.Autoload != nil && !*e.Autoload { // a delta over the user's package
				items[i].e = narrow(items[i].e, e)
				return
			}
			if d.Scope == Project {
				items[i] = item{e, s, d}
			}
			return
		}
		if d.Scope == Project && e.Autoload != nil && !*e.Autoload {
			return // nothing to narrow
		}
		identity[s.Identity] = len(items)
		items = append(items, item{e, s, d})
	}
	for _, e := range user {
		add(e, ud)
	}
	for _, e := range proj {
		add(e, pd)
	}
	for _, it := range items {
		l, warns, ok := loadOne(it.e, it.s, it.d)
		res.Warnings = append(res.Warnings, warns...)
		if ok {
			res.Packages = append(res.Packages, l)
		}
	}
	// project packages first: they win name clashes
	var proj2, user2 []Loaded
	for _, l := range res.Packages {
		if l.Scope == Project {
			proj2 = append(proj2, l)
		} else {
			user2 = append(user2, l)
		}
	}
	res.Packages = append(proj2, user2...)
	return res
}

// narrow applies a project entry with autoload false to the user's entry: its lists are added after the user's, and an empty list means
// none.
func narrow(base, delta Entry) Entry {
	merge := func(a, b []string) []string {
		switch {
		case b == nil:
			return a
		case len(b) == 0:
			return []string{}
		}
		return append(append([]string{}, a...), b...)
	}
	base.Extensions, base.Skills = merge(base.Extensions, delta.Extensions), merge(base.Skills, delta.Skills)
	base.Prompts, base.Themes = merge(base.Prompts, delta.Prompts), merge(base.Themes, delta.Themes)
	return base
}

func loadOne(e Entry, s Source, d Dirs) (Loaded, []string, bool) {
	dir := PackageDir(d, s)
	name := s.Raw
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		hint := "run `koine update` to install it"
		if s.Kind == Local {
			hint = "check the path"
		}
		return Loaded{}, []string{fmt.Sprintf("package %s (%s) is not installed at %s: %s", name, d.Scope, dir, hint)}, false
	}
	var warns []string
	if s.Kind != Local {
		lock, err := ReadLock(d.Lock)
		var want LockEntry
		var ok bool
		if err == nil {
			want, ok = lock.Packages[s.Identity]
		}
		switch {
		case err != nil:
			warns = append(warns, err.Error())
		case !ok:
			warns = append(warns, fmt.Sprintf("package %s has no entry in %s; run `koine update` to record it", name, filepath.Base(d.Lock)))
		default:
			got, herr := HashTree(dir)
			if herr != nil || got != want.Integrity {
				return Loaded{}, append(warns, fmt.Sprintf("package %s NOT loaded: its files do not match %s; "+
					"if you changed them on purpose, remove and install the package again", name, d.Lock)), false
			}
		}
	}
	m, _ := ReadManifest(dir)
	for _, w := range DependencyWarnings(m) {
		warns = append(warns, fmt.Sprintf("package %s: %s", name, w))
	}
	all, problems := Resolve(dir)
	for _, p := range problems {
		warns = append(warns, fmt.Sprintf("package %s: %s", name, p))
	}
	l := Loaded{Source: s, Scope: d.Scope, Dir: dir, Entry: e, All: all, Manifest: m}
	l.Resources = Resources{
		Extensions: Filter(all.Extensions, e.Extensions), Skills: Filter(all.Skills, e.Skills),
		Prompts: Filter(all.Prompts, e.Prompts), Themes: Filter(all.Themes, e.Themes),
	}
	return l, warns, true
}
