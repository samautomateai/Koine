package pkgs

import (
	"os"
	"strings"
	"testing"
)

const sampleChangelog = `# Changelog

## [2.0.0] - 2026-03-01
- breaking thing

## v1.2.0
- added x

## 1.1.0
- fixed y

## 1.0.0
- first
`

func TestChangelogSectionsBetweenVersions(t *testing.T) {
	got := sectionsBetween(sampleChangelog, "1.0.0", "1.2.0")
	if !strings.Contains(got, "added x") || !strings.Contains(got, "fixed y") || strings.Contains(got, "breaking") || strings.Contains(got, "first") {
		t.Errorf("%q", got)
	}
	if sectionsBetween(sampleChangelog, "2.0.0", "2.0.0") != "" {
		t.Error("nothing is new")
	}
	if got := sectionsBetween(sampleChangelog, "", "9.0.0"); !strings.Contains(got, "first") || !strings.Contains(got, "breaking") {
		t.Errorf("from nothing shows everything up to the new version: %q", got)
	}
}

func TestCompareVersions(t *testing.T) {
	order := []string{"0.9.0", "1.0.0-alpha", "1.0.0-beta", "1.0.0", "1.0.1", "1.2", "2", "10.0.0"}
	for i := 0; i < len(order)-1; i++ {
		if compareVersions(order[i], order[i+1]) >= 0 || compareVersions(order[i+1], order[i]) <= 0 {
			t.Errorf("%s should sort before %s", order[i], order[i+1])
		}
	}
	if compareVersions("v1.0.0+build5", "1.0.0") != 0 {
		t.Error("build metadata and a v prefix are ignored")
	}
}

func TestChangelogNoticeRules(t *testing.T) {
	pkg := localPkg(t, "p")
	write(t, pkg, map[string]string{"CHANGELOG.md": sampleChangelog})
	load := func() Loaded {
		_, home, cwd := env(t, true)
		setEntries(t, DirsFor(User, home, cwd), Entry{Source: pkg})
		r := Load(home, cwd, true)
		if len(r.Packages) != 1 {
			t.Fatalf("%+v", r)
		}
		return r.Packages[0]
	}
	l := load()
	if n := ChangelogNotice(l, "1.0.0", "1.1.0"); !strings.Contains(n, "fixed y") {
		t.Errorf("default CHANGELOG.md: %q", n)
	}
	write(t, pkg, map[string]string{
		"docs/HISTORY.md": "## 1.1.0\n- from the custom file\n", "package.json": `{"name":"p","pi":{"changelogPath":"docs/HISTORY.md"}}`,
	})
	if n := ChangelogNotice(load(), "1.0.0", "1.1.0"); !strings.Contains(n, "custom file") {
		t.Errorf("pi.changelogPath: %q", n)
	}
	write(t, pkg, map[string]string{"package.json": `{"name":"p","pi":{"changelogPath":""}}`})
	if n := ChangelogNotice(load(), "1.0.0", "1.1.0"); n != "" {
		t.Errorf("an empty changelogPath turns the notice off: %q", n)
	}
	write(t, pkg, map[string]string{"package.json": `{"name":"p","pi":{"changelogPath":"../../etc/passwd"}}`})
	if n := ChangelogNotice(load(), "1.0.0", "1.1.0"); n != "" {
		t.Errorf("a changelog outside the package is never read: %q", n)
	}
	// a package without extensions does not qualify
	bare := localPkg(t, "bare")
	write(t, bare, map[string]string{"CHANGELOG.md": sampleChangelog})
	_ = os.RemoveAll(bare + "/extensions")
	_, home, cwd := env(t, true)
	setEntries(t, DirsFor(User, home, cwd), Entry{Source: bare})
	if n := ChangelogNotice(Load(home, cwd, true).Packages[0], "1.0.0", "1.1.0"); n != "" {
		t.Errorf("only packages with extensions show a changelog: %q", n)
	}
	if FirstLine("p", "1", "2") != "p 1 -> 2" {
		t.Error("collapsed form")
	}
}
