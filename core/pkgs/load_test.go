package pkgs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// localPkg writes a package with an extension, two skills and two prompts.
func localPkg(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	write(t, dir, map[string]string{
		"package.json":        `{"name":"` + name + `","version":"1.0.0"}`,
		"extensions/a.ts":     "",
		"extensions/b.ts":     "",
		"skills/one/SKILL.md": "---\nname: one\ndescription: d\n---\n",
		"skills/two/SKILL.md": "---\nname: two\ndescription: d\n---\n",
		"prompts/p1.md":       "x",
		"prompts/p2.md":       "y",
	})
	return dir
}

func setEntries(t *testing.T, d Dirs, es ...Entry) {
	t.Helper()
	if err := UpdateEntries(d.Settings, func([]Entry) []Entry { return es }); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAppliesEntryFilters(t *testing.T) {
	m, home, cwd := env(t, true)
	pkg := localPkg(t, "p")
	setEntries(t, DirsFor(User, home, cwd), Entry{
		Source: pkg, Extensions: []string{"extensions/*.ts", "!extensions/b.ts"}, Skills: []string{}, Prompts: []string{"-prompts/p2.md"},
	})
	_ = m
	r := Load(home, cwd, true)
	if len(r.Packages) != 1 || len(r.Warnings) != 0 {
		t.Fatalf("%+v", r)
	}
	l := r.Packages[0]
	if !reflect.DeepEqual(rels(l.Resources.Extensions), []string{"extensions/a.ts"}) || len(l.Resources.Skills) != 0 ||
		!reflect.DeepEqual(rels(l.Resources.Prompts), []string{"prompts/p1.md"}) {
		t.Errorf("filtered: ext %v skills %v prompts %v", rels(l.Resources.Extensions), rels(l.Resources.Skills), rels(l.Resources.Prompts))
	}
	if len(l.All.Extensions) != 2 || len(l.All.Skills) != 2 {
		t.Errorf("filters must keep the unfiltered list for config: %+v", l.All)
	}
}

func TestProjectEntryReplacesUserEntryOfTheSamePackage(t *testing.T) {
	_, home, cwd := env(t, true)
	pkg := localPkg(t, "p")
	setEntries(t, DirsFor(User, home, cwd), Entry{Source: pkg})
	rel, _ := filepath.Rel(filepath.Join(cwd, ".koine"), pkg)
	setEntries(t, DirsFor(Project, home, cwd), Entry{Source: rel, Skills: []string{"skills/one"}})
	r := Load(home, cwd, true)
	if len(r.Packages) != 1 {
		t.Fatalf("one package, not two: %+v", r.Packages)
	}
	if l := r.Packages[0]; l.Scope != Project || !reflect.DeepEqual(rels(l.Resources.Skills), []string{"skills/one"}) || len(l.Resources.Prompts) != 2 {
		t.Errorf("the project entry replaces the user entry: %+v scope %s", rels(l.Resources.Skills), l.Scope)
	}
}

func TestAutoloadFalseNarrowsTheUserPackage(t *testing.T) {
	_, home, cwd := env(t, true)
	pkg := localPkg(t, "p")
	no := false
	setEntries(t, DirsFor(User, home, cwd), Entry{Source: pkg, Prompts: []string{"prompts/p1.md"}})
	rel, _ := filepath.Rel(filepath.Join(cwd, ".koine"), pkg)
	setEntries(t, DirsFor(Project, home, cwd),
		Entry{Source: rel, Autoload: &no, Skills: []string{"-skills/two"}, Prompts: []string{"prompts/p2.md"}},
		Entry{Source: "./not-in-user-scope", Autoload: &no})
	r := Load(home, cwd, true)
	if len(r.Packages) != 1 {
		t.Fatalf("%+v", r)
	}
	l := r.Packages[0]
	if l.Scope != User {
		t.Errorf("the package stays the user's: %s", l.Scope)
	}
	if !reflect.DeepEqual(rels(l.Resources.Skills), []string{"skills/one"}) {
		t.Errorf("skills: %v", rels(l.Resources.Skills))
	}
	if got := rels(l.Resources.Prompts); !reflect.DeepEqual(got, []string{"prompts/p1.md", "prompts/p2.md"}) {
		t.Errorf("the delta adds to the user's list: %v", got)
	}
}

func TestNarrowRules(t *testing.T) {
	base := Entry{Source: "x", Extensions: []string{"a"}, Skills: nil}
	got := narrow(base, Entry{Extensions: []string{"!b"}, Skills: []string{}, Prompts: []string{"p"}})
	if !reflect.DeepEqual(got.Extensions, []string{"a", "!b"}) || got.Skills == nil || len(got.Skills) != 0 ||
		!reflect.DeepEqual(got.Prompts, []string{"p"}) || got.Themes != nil {
		t.Errorf("%+v", got)
	}
}

func TestUntrustedProjectPackagesAreNotLoaded(t *testing.T) {
	_, home, cwd := env(t, false)
	pkg := localPkg(t, "p")
	setEntries(t, DirsFor(Project, home, cwd), Entry{Source: pkg})
	r := Load(home, cwd, false)
	if len(r.Packages) != 0 || r.SkippedProject != 1 {
		t.Errorf("%+v", r)
	}
	// and the user's entry for the same package is not touched by a project that cannot be trusted
	setEntries(t, DirsFor(User, home, cwd), Entry{Source: pkg})
	no := false
	setEntries(t, DirsFor(Project, home, cwd), Entry{Source: pkg, Autoload: &no, Extensions: []string{}})
	r = Load(home, cwd, false)
	if len(r.Packages) != 1 || len(r.Packages[0].Resources.Extensions) != 2 || r.SkippedProject != 1 {
		t.Errorf("%+v", r)
	}
}

func TestLoadReportsProblemsWithoutFailing(t *testing.T) {
	_, home, cwd := env(t, true)
	good := localPkg(t, "good")
	ud := DirsFor(User, home, cwd)
	setEntries(t, ud, Entry{Source: "npm:not-installed@1.0.0"}, Entry{Source: "./absent"}, Entry{Source: "nonsense"}, Entry{Source: good})
	r := Load(home, cwd, true)
	if len(r.Packages) != 1 || r.Packages[0].Dir != good {
		t.Fatalf("%+v", r.Packages)
	}
	joined := strings.Join(r.Warnings, "\n")
	for _, want := range []string{"npm:not-installed@1.0.0 (user) is not installed", "koine update", "check the path", "unrecognised package source"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
	// settings that cannot be read are reported too
	_ = os.WriteFile(ud.Settings, []byte(`{"packages": 4}`), 0o644)
	if r := Load(home, cwd, true); len(r.Warnings) == 0 {
		t.Error("bad settings are silent")
	}
}

func TestLoadWarnsAboutHostPackagesInDependenciesAndMissingLock(t *testing.T) {
	reg, _ := fakeNPM(t)
	publish(t, reg, "dup", "1.0.0", map[string]string{"package.json": `{"name":"dup","version":"1.0.0","dependencies":{"typebox":"^1"}}`})
	m, home, cwd := env(t, false)
	in, err := m.Install(bg, User, "npm:dup@1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Warnings) != 1 || !strings.Contains(in.Warnings[0], "peerDependencies") {
		t.Errorf("install warnings: %v", in.Warnings)
	}
	r := Load(home, cwd, false)
	if len(r.Packages) != 1 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "typebox") {
		t.Errorf("load: %+v", r)
	}
	_ = os.Remove(DirsFor(User, home, cwd).Lock)
	r = Load(home, cwd, false)
	if len(r.Packages) != 1 || !strings.Contains(strings.Join(r.Warnings, "\n"), "no entry in packages.lock.json") {
		t.Errorf("a package missing from the lock still loads, with a warning: %+v", r)
	}
}

func TestProjectPackagesComeFirst(t *testing.T) {
	_, home, cwd := env(t, true)
	a, b := localPkg(t, "a"), localPkg(t, "b")
	setEntries(t, DirsFor(User, home, cwd), Entry{Source: a})
	setEntries(t, DirsFor(Project, home, cwd), Entry{Source: b})
	r := Load(home, cwd, true)
	if len(r.Packages) != 2 || r.Packages[0].Scope != Project || r.Packages[1].Scope != User {
		t.Errorf("%+v", r.Packages)
	}
}
