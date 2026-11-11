package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func TestReadReturnsPlainTextWithoutLineNumbers(t *testing.T) {
	r, dir := setup(t)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree"), 0o644)
	out, err := run(t, r, "read", `{"path":"a.txt"}`)
	if err != nil || out != "one\ntwo\nthree" {
		t.Fatalf("%q %v", out, err)
	}
	out, err = run(t, r, "read", `{"path":"@a.txt","offset":2}`)
	if err != nil || out != "two\nthree" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestReadLimitSaysHowToContinue(t *testing.T) {
	r, dir := setup(t)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte(numbered(10)), 0o644)
	out, err := run(t, r, "read", `{"path":"a.txt","offset":3,"limit":2}`)
	want := "line 3\nline 4\n\n[6 more lines in file. Use offset=5 to continue.]"
	if err != nil || out != want {
		t.Fatalf("%q %v", out, err)
	}
	// A limit that reaches the end adds no hint.
	out, _ = run(t, r, "read", `{"path":"a.txt","offset":9,"limit":5}`)
	if out != "line 9\nline 10" {
		t.Fatalf("%q", out)
	}
}

func TestReadTruncatesAt2000LinesAndSaysWhereToContinue(t *testing.T) {
	r, dir := setup(t)
	_ = os.WriteFile(filepath.Join(dir, "big.txt"), []byte(numbered(2500)), 0o644)
	out, err := run(t, r, "read", `{"path":"big.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "line 2000\n\n[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]") || strings.Contains(out, "line 2001") {
		t.Fatalf("tail: %q", out[len(out)-120:])
	}
	out, _ = run(t, r, "read", `{"path":"big.txt","offset":2001}`)
	if !strings.HasSuffix(out, "line 2500") || strings.Contains(out, "Showing") {
		t.Fatalf("second page: %q", out[len(out)-60:])
	}
}

func TestReadTruncatesAt50KB(t *testing.T) {
	r, dir := setup(t)
	line := strings.Repeat("x", 99)
	var b strings.Builder
	for i := 0; i < 700; i++ {
		b.WriteString(line + "\n")
	}
	_ = os.WriteFile(filepath.Join(dir, "wide.txt"), []byte(b.String()), 0o644)
	out, err := run(t, r, "read", `{"path":"wide.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	// 100 bytes per line, so 512 lines fit in 50KB (51200 bytes: 512 lines of 99 plus 511 newlines is 51199).
	if !strings.Contains(out, "[Showing lines 1-512 of 701 (50.0KB limit). Use offset=513 to continue.]") {
		t.Fatalf("%q", out[len(out)-120:])
	}
}

func TestReadFirstLineOverTheByteLimit(t *testing.T) {
	r, dir := setup(t)
	_ = os.WriteFile(filepath.Join(dir, "one.txt"), []byte(strings.Repeat("y", 60000)+"\nnext"), 0o644)
	out, err := run(t, r, "read", `{"path":"one.txt"}`)
	if err != nil || !strings.HasPrefix(out, "[Line 1 is 58.6KB, exceeds 50.0KB limit. Use bash: sed -n '1p' one.txt | head -c 51200]") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestReadErrors(t *testing.T) {
	r, dir := setup(t)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nb"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("PNG\x00\x01\x02"), 0o644)
	_ = os.Mkdir(filepath.Join(dir, "d"), 0o755)
	for args, want := range map[string]string{
		`{"path":"nope.txt"}`:         "file not found: nope.txt",
		`{"path":"a.txt","offset":5}`: "offset 5 is beyond end of file (2 lines total)",
		`{"path":"bin.dat"}`:          "binary file",
		`{"path":"d"}`:                "is a directory",
	} {
		if _, err := run(t, r, "read", args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v (want %q)", args, err, want)
		}
	}
}

func TestReadDropsABOMAndExpandsHome(t *testing.T) {
	r, dir := setup(t) // Home is the temp dir
	_ = os.WriteFile(filepath.Join(dir, "bom.txt"), []byte("\xef\xbb\xbfhello"), 0o644)
	out, err := run(t, r, "read", `{"path":"~/bom.txt"}`)
	if err != nil || out != "hello" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestWriteCreatesParentsAndOverwrites(t *testing.T) {
	r, dir := setup(t)
	out, err := run(t, r, "write", `{"path":"sub/deep/a.txt","content":"hello"}`)
	if err != nil || out != "Successfully wrote 5 bytes to sub/deep/a.txt" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := run(t, r, "write", `{"path":"@sub/deep/a.txt","content":"bye"}`); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "sub/deep/a.txt")); string(got) != "bye" {
		t.Fatalf("%q", got)
	}
	if _, err := run(t, r, "write", `{"path":"`+filepath.Join(dir, "abs.txt")+`","content":"x"}`); err != nil {
		t.Fatal(err)
	}
	// A file where a directory is needed fails cleanly.
	if _, err := run(t, r, "write", `{"path":"abs.txt/inner.txt","content":"x"}`); err == nil {
		t.Fatal("expected an error")
	}
	_ = os.Mkdir(filepath.Join(dir, "adir"), 0o755)
	if _, err := run(t, r, "write", `{"path":"adir","content":"x"}`); err == nil {
		t.Fatal("expected an error writing over a directory")
	}
}
