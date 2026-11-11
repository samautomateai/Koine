package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPathsFilesDirectoriesAndProblems(t *testing.T) {
	d := t.TempDir()
	w := func(rel, c string) string {
		p := filepath.Join(d, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	one := w("one.md", "---\ndescription: One\n---\nDo $ARGUMENTS")
	w("dir/a.md", "A")
	w("dir/b.md", "B")
	w("dir/notes.txt", "ignored")
	w("empty/x.txt", "ignored")
	txt := w("plain.txt", "not markdown")

	got, problems := LoadPaths([]string{one, filepath.Join(d, "dir"), filepath.Join(d, "empty"), txt, filepath.Join(d, "none"), one})
	names := []string{}
	for _, p := range got {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "one,a,b" {
		t.Errorf("names %v", names)
	}
	if got[0].Description != "One" || got[0].Expand("it") != "Do it" {
		t.Errorf("%+v", got[0])
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"empty: no .md files", "plain.txt: not a readable", "none", "one.md: not a readable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
}

func TestMergeKeepsFirstOfEachName(t *testing.T) {
	got := Merge([]Prompt{{Name: "a", Body: "explicit"}}, []Prompt{{Name: "a", Body: "found"}, {Name: "b"}})
	if len(got) != 2 || got[0].Body != "explicit" || got[1].Name != "b" {
		t.Errorf("%+v", got)
	}
}
