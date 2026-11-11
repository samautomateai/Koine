package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestApplyEditsMatchesAgainstTheOriginal(t *testing.T) {
	content := "alpha\nbeta\ngamma\ndelta\n"
	// The second edit's oldText exists only in the original, and the first edit's newText must not be searched.
	got, err := ApplyEdits(content, []Edit{{"alpha", "beta"}, {"beta", "BETA"}}, "f")
	if err != nil || got != "beta\nBETA\ngamma\ndelta\n" {
		t.Fatalf("%q %v", got, err)
	}
	// Order in the call does not matter.
	got, err = ApplyEdits(content, []Edit{{"delta", "D"}, {"alpha", "A"}}, "f")
	if err != nil || got != "A\nbeta\ngamma\nD\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestApplyEditsErrors(t *testing.T) {
	content := "one\ntwo\ntwo\nthree\n"
	cases := []struct {
		name  string
		edits []Edit
		want  string
	}{
		{"none", nil, "at least one"},
		{"empty old", []Edit{{"", "x"}}, "oldText must not be empty"},
		{"empty old in a list", []Edit{{"one", "1"}, {"", "x"}}, "edits[1].oldText must not be empty"},
		{"missing", []Edit{{"zzz", "x"}}, "Could not find the exact text in f"},
		{"missing in a list", []Edit{{"one", "1"}, {"zzz", "x"}}, "Could not find edits[1] in f"},
		{"duplicate", []Edit{{"two", "2"}}, "Found 2 occurrences"},
		{"overlap", []Edit{{"one\ntwo\ntwo", "x"}, {"three", "y"}, {"two\ntwo\nthree", "z"}}, "overlap"},
		{"identical", []Edit{{"one", "one"}}, "No changes made"},
	}
	for _, c := range cases {
		if _, err := ApplyEdits(content, c.edits, "f"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (want %q)", c.name, err, c.want)
		}
	}
}

func TestApplyEditsFuzzyMatching(t *testing.T) {
	// Smart quotes, an en dash, a non-breaking space and trailing whitespace in the file; plain ASCII in the model's oldText.
	content := "say \u201chi\u201d \u2013 it\u2019s fine   \nnext\u00a0line\t\nkeep\n"
	got, err := ApplyEdits(content, []Edit{{"say \"hi\" - it's fine", "SAID"}, {"next line", "NEXT"}}, "f")
	if err != nil {
		t.Fatal(err)
	}
	if got != "SAID\nNEXT\nkeep\n" {
		t.Fatalf("%q", got)
	}
	if NormalizeForFuzzyMatch("a \t\nb\u2014c\u3000d") != "a\nb-c d" {
		t.Fatal(NormalizeForFuzzyMatch("a \t\nb\u2014c\u3000d"))
	}
}

func TestLineEndingAndBOMHelpers(t *testing.T) {
	if m, s := SplitBOM("\xef\xbb\xbfhi"); m != "\xef\xbb\xbf" || s != "hi" {
		t.Fatal(m, s)
	}
	if m, s := SplitBOM("hi"); m != "" || s != "hi" {
		t.Fatal(m, s)
	}
	for in, want := range map[string]string{"a\r\nb\n": "\r\n", "a\nb\r\n": "\n", "no newline": "\n", "\nx": "\n"} {
		if got := DetectLineEnding(in); got != want {
			t.Errorf("DetectLineEnding(%q)=%q", in, got)
		}
	}
	if NormalizeToLF("a\r\nb\rc\n") != "a\nb\nc\n" || RestoreLineEnding("a\nb", "\r\n") != "a\r\nb" || RestoreLineEnding("a\nb", "\n") != "a\nb" {
		t.Fatal("line endings")
	}
}

func TestEditToolMultiEditDiffAndFirstChangedLine(t *testing.T) {
	r, dir := setup(t)
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString("line " + string(rune('A'+i-1)) + "\n")
	}
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, r, "edit", `{"path":"f.txt","edits":[{"oldText":"line C\n","newText":"line C changed\n"},{"oldText":"line R\n","newText":""}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Successfully replaced 2 block(s) in f.txt.", "First changed line: 3", "--- f.txt\n+++ f.txt\n", "@@ -1,6 +1,6 @@",
		"-line C\n+line C changed\n", "-line R\n", "@@ -15,"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "line C changed\n") || strings.Contains(string(got), "line R") {
		t.Fatalf("%q", got)
	}
}

func TestEditToolKeepsBOMAndCRLF(t *testing.T) {
	r, dir := setup(t)
	path := filepath.Join(dir, "win.txt")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbfone\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The model sends LF text; the file keeps its BOM and its CRLF endings.
	if _, err := run(t, r, "edit", `{"path":"win.txt","edits":[{"oldText":"one\ntwo","newText":"1\n2\n2b"}]}`); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "\xef\xbb\xbf1\r\n2\r\n2b\r\nthree\r\n" {
		t.Fatalf("%q", got)
	}
	lf := filepath.Join(dir, "unix.txt")
	_ = os.WriteFile(lf, []byte("a\nb\n"), 0o644)
	if _, err := run(t, r, "edit", `{"path":"unix.txt","edits":[{"oldText":"a","newText":"x"}]}`); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(lf); string(got) != "x\nb\n" {
		t.Fatalf("%q", got)
	}
}

func TestEditToolAcceptsOlderArgumentShapes(t *testing.T) {
	r, dir := setup(t)
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("one two three"), 0o644)
	for _, args := range []string{
		`{"path":"f.txt","oldText":"one","newText":"1"}`,
		`{"path":"f.txt","old":"two","new":"2"}`,
		`{"path":"f.txt","old":"three"}`,
	} {
		if _, err := run(t, r, "edit", args); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != "1 2 " {
		t.Fatalf("%q", got)
	}
}

func TestEditToolErrorsLeaveTheFileAlone(t *testing.T) {
	r, dir := setup(t)
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("keep\nme\n"), 0o644)
	for _, args := range []string{
		`{"path":"f.txt"}`,
		`{"path":"f.txt","edits":[{"oldText":"keep","newText":"x"},{"oldText":"nope","newText":"y"}]}`,
		`{"path":"missing.txt","edits":[{"oldText":"a","newText":"b"}]}`,
		`{"path":"f.txt","edits":"not an array"}`,
	} {
		if _, err := run(t, r, "edit", args); err == nil {
			t.Errorf("expected an error for %s", args)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != "keep\nme\n" {
		t.Fatalf("file changed: %q", got)
	}
	_, err := run(t, r, "edit", `{"path":"f.txt","edits":[{"oldText":"zzz","newText":"y"}]}`)
	if err == nil || !strings.Contains(err.Error(), "Could not find") {
		t.Fatal(err)
	}
}

func TestEditAndWriteInParallelAreSerialisedPerFile(t *testing.T) {
	r, dir := setup(t)
	path := filepath.Join(dir, "counter.txt")
	var body strings.Builder
	for i := 0; i < 20; i++ {
		body.WriteString("slot" + string(rune('a'+i)) + "=0\n")
	}
	_ = os.WriteFile(path, []byte(body.String()), 0o644)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := string(rune('a' + i))
			if _, err := run(t, r, "edit", `{"path":"counter.txt","edits":[{"oldText":"slot`+c+`=0","newText":"slot`+c+`=1"}]}`); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "=0") {
		t.Fatalf("a parallel edit was lost:\n%s", got)
	}
}

func TestUnifiedDiff(t *testing.T) {
	if d, line := UnifiedDiff("a\nb", "a\nb", "f"); d != "" || line != 0 {
		t.Fatal(d, line)
	}
	d, line := UnifiedDiff("a\nb\nc\n", "a\nB\nc\nd\n", "f")
	want := "--- f\n+++ f\n@@ -1,4 +1,5 @@\n a\n-b\n+B\n c\n+d\n \n"
	if d != want || line != 2 {
		t.Fatalf("%q line %d", d, line)
	}
	// A pure deletion reports the line where it happened; a pure insertion the inserted line.
	if _, line := UnifiedDiff("a\nb\nc", "a\nc", "f"); line != 2 {
		t.Fatal(line)
	}
	if _, line := UnifiedDiff("a\nc", "a\nb\nc", "f"); line != 2 {
		t.Fatal(line)
	}
	if _, line := UnifiedDiff("a", "b", "f"); line != 1 {
		t.Fatal(line)
	}
	// Far-apart changes make separate hunks.
	var old, nw []string
	for i := 0; i < 30; i++ {
		old = append(old, "l")
		nw = append(nw, "l")
	}
	old[2], nw[2] = "x", "y"
	old[27], nw[27] = "p", "q"
	d, _ = UnifiedDiff(strings.Join(old, "\n"), strings.Join(nw, "\n"), "f")
	if strings.Count(d, "@@ -") != 2 {
		t.Fatalf("expected two hunks:\n%s", d)
	}
}

func TestUnifiedDiffOfAHugeChangeFallsBackToOneBlock(t *testing.T) {
	var a, b []string
	for i := 0; i < 2500; i++ {
		a = append(a, "old"+string(rune('a'+i%26))+strings.Repeat("x", i%7))
		b = append(b, "new"+string(rune('a'+i%26))+strings.Repeat("y", i%5))
	}
	d, line := UnifiedDiff(strings.Join(a, "\n"), strings.Join(b, "\n"), "big")
	if line != 1 || !strings.Contains(d, "-old") || !strings.Contains(d, "+new") {
		t.Fatal(line, len(d))
	}
}
