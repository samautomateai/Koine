package skills

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPathsFilesDirectoriesAndProblems(t *testing.T) {
	d := t.TempDir()
	put(t, d, "one/SKILL.md", "---\nname: one\ndescription: first\n---\nbody one")
	put(t, d, "many/a/SKILL.md", "---\nname: a\ndescription: in a tree\n---\nA")
	put(t, d, "many/loose.md", "---\ndescription: loose file in the root\n---\nL")
	put(t, d, "plain.md", "---\nname: plain\ndescription: a single file\n---\nP")
	put(t, d, "nodesc.md", "---\nname: nodesc\n---\nN")
	put(t, d, "empty/readme.txt", "nothing here")
	put(t, d, "bad.md", "---\nname: bad\ndescription: hidden\n---\nzero\u200bwidth")

	got, skipped, problems := LoadPaths([]string{
		filepath.Join(d, "one"), filepath.Join(d, "many"), filepath.Join(d, "plain.md"), filepath.Join(d, "plain.md"),
		filepath.Join(d, "nodesc.md"), filepath.Join(d, "empty"), filepath.Join(d, "missing"), filepath.Join(d, "bad.md")})
	names := map[string]bool{}
	for _, s := range got {
		names[s.Name] = true
	}
	for _, n := range []string{"one", "a", "loose", "plain"} {
		if !names[n] {
			t.Errorf("missing %q in %v", n, names)
		}
	}
	if len(got) != 4 {
		t.Errorf("want 4 skills, got %d", len(got))
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Path, "bad.md") {
		t.Errorf("hidden-text skill should be refused: %v", skipped)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"nodesc.md: no description", "empty: no SKILL.md found", "missing", "duplicate name plain"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "bad.md") {
		t.Errorf("a refused file is reported through skipped only:\n%s", joined)
	}
}

func TestMergeKeepsFirstOfEachName(t *testing.T) {
	first := []Skill{{Name: "a", Description: "explicit"}}
	rest := []Skill{{Name: "a", Description: "found"}, {Name: "b"}}
	got := Merge(first, rest)
	if len(got) != 2 || got[0].Description != "explicit" || got[1].Name != "b" {
		t.Errorf("%+v", got)
	}
}
