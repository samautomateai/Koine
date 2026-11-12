package pkgs

import (
	"errors"
	"strings"
	"testing"
)

func configFixture(t *testing.T) (*Manager, Dirs, string) {
	t.Helper()
	m, home, cwd := env(t, true)
	pkg := localPkg(t, "p")
	d := DirsFor(User, home, cwd)
	setEntries(t, d, Entry{Source: pkg})
	return m, d, pkg
}

func enabledMap(t *testing.T, m *Manager) map[string]bool {
	t.Helper()
	states, err := m.States(User)
	if err != nil || len(states) != 1 {
		t.Fatalf("%v %v", states, err)
	}
	out := map[string]bool{}
	for _, r := range states[0].Resources {
		out[r.Res.Rel] = r.Enabled
	}
	return out
}

func TestSetEnabledWritesTheSmallestFilter(t *testing.T) {
	m, d, pkg := configFixture(t)
	if got := enabledMap(t, m); !got["skills/one"] || !got["extensions/a.ts"] || len(got) != 6 {
		t.Fatalf("everything starts enabled: %v", got)
	}
	// disabling one thing of a plain entry writes -path and nothing else
	if err := m.SetEnabled(User, pkg, "skills/two", false); err != nil {
		t.Fatal(err)
	}
	es := entriesOf(t, d)
	if len(es[0].Skills) != 1 || es[0].Skills[0] != "-skills/two" || es[0].Extensions != nil {
		t.Errorf("%+v", es[0])
	}
	if got := enabledMap(t, m); got["skills/two"] || !got["skills/one"] {
		t.Errorf("%v", got)
	}
	// disabling again changes nothing; the SKILL.md spelling names the same skill
	if err := m.SetEnabled(User, pkg, "skills/two/SKILL.md", false); err != nil || len(entriesOf(t, d)[0].Skills) != 1 {
		t.Errorf("idempotent: %v %+v", err, entriesOf(t, d)[0].Skills)
	}
	// enabling removes the filter, and an entry with nothing left to narrow becomes plain again
	if err := m.SetEnabled(User, pkg, "skills/two", true); err != nil {
		t.Fatal(err)
	}
	if es := entriesOf(t, d); es[0].Filtered() || es[0].Skills != nil {
		t.Errorf("%+v", es[0])
	}
}

func TestSetEnabledOnEntriesWithPatterns(t *testing.T) {
	m, d, pkg := configFixture(t)
	setEntries(t, d, Entry{Source: pkg, Prompts: []string{}, Extensions: []string{"extensions/*.ts", "!extensions/b.ts"}})
	// a list that said "none" now names the one resource
	if err := m.SetEnabled(User, pkg, "prompts/p1.md", true); err != nil {
		t.Fatal(err)
	}
	if got := entriesOf(t, d)[0].Prompts; len(got) != 1 || got[0] != "prompts/p1.md" {
		t.Errorf("prompts: %v", got)
	}
	got := enabledMap(t, m)
	if !got["prompts/p1.md"] || got["prompts/p2.md"] {
		t.Errorf("only the named prompt: %v", got)
	}
	// a resource a ! pattern removed comes back with +path
	if err := m.SetEnabled(User, pkg, "extensions/b.ts", true); err != nil {
		t.Fatal(err)
	}
	if exts := entriesOf(t, d)[0].Extensions; exts[len(exts)-1] != "+extensions/b.ts" {
		t.Errorf("extensions: %v", exts)
	}
	if !enabledMap(t, m)["extensions/b.ts"] {
		t.Error("b.ts is back")
	}
	// and a +path is replaced when the decision is reversed
	if err := m.SetEnabled(User, pkg, "extensions/b.ts", false); err != nil {
		t.Fatal(err)
	}
	if exts := entriesOf(t, d)[0].Extensions; strings.Contains(strings.Join(exts, ","), "+extensions/b.ts") || enabledMap(t, m)["extensions/b.ts"] {
		t.Errorf("extensions: %v", exts)
	}
	// only adjustments left over from earlier decisions go back to "everything"
	setEntries(t, d, Entry{Source: pkg, Skills: []string{"+skills/one"}})
	if err := m.SetEnabled(User, pkg, "skills/one", false); err != nil {
		t.Fatal(err)
	}
	if got := entriesOf(t, d)[0].Skills; len(got) != 1 || got[0] != "-skills/one" {
		t.Errorf("skills: %v", got)
	}
}

func TestSetEnabledErrors(t *testing.T) {
	m, d, pkg := configFixture(t)
	if err := m.SetEnabled(User, pkg, "skills/none", false); err == nil || !strings.Contains(err.Error(), "supplies no resource") {
		t.Errorf("%v", err)
	}
	if err := m.SetEnabled(User, "./other", "x", false); err == nil || !strings.Contains(err.Error(), "not in the user packages") {
		t.Errorf("%v", err)
	}
	if err := m.SetEnabled(User, "what", "x", false); err == nil || !strings.Contains(err.Error(), "not a package source") {
		t.Errorf("%v", err)
	}
	setEntries(t, d, Entry{Source: "npm:absent@1.0.0"})
	if err := m.SetEnabled(User, "npm:absent", "x", false); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("%v", err)
	}
	states, err := m.States(User)
	if err != nil || len(states) != 1 || states[0].Installed || len(states[0].Resources) != 0 {
		t.Errorf("%+v %v", states, err)
	}
	m.Trusted = false
	if err := m.SetEnabled(Project, pkg, "x", true); !errors.Is(err, ErrNotTrusted) {
		t.Errorf("%v", err)
	}
	setEntries(t, d, Entry{Source: "nonsense"})
	if _, err := m.States(User); err == nil {
		t.Error("a settings file with only bad entries is an error")
	}
}
