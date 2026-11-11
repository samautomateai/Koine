package tools

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExpandAndResolvePath(t *testing.T) {
	cwd, home := "/work/proj", "/home/u"
	cases := map[string]string{
		"a/b.txt":            "/work/proj/a/b.txt",
		"@a/b.txt":           "/work/proj/a/b.txt",
		"/abs/x/../y":        "/abs/y",
		"~":                  "/home/u",
		"~/notes.md":         "/home/u/notes.md",
		"@~/notes.md":        "/home/u/notes.md",
		"my\u00a0file.txt":   "/work/proj/my file.txt",
		"a\u202fb\u3000c":    "/work/proj/a b c",
		"~other/not-a-home":  "/work/proj/~other/not-a-home",
		"./here/../there.go": "/work/proj/there.go",
	}
	for in, want := range cases {
		if got := ResolvePath(in, cwd, home); got != want {
			t.Errorf("ResolvePath(%q)=%q want %q", in, got, want)
		}
	}
}

func TestResolveReadPathFindsMacOSAndCurlyNames(t *testing.T) {
	dir := t.TempDir()
	touch := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch("Screenshot 2024-01-01 at 9.41.00\u202fAM.png")
	touch("Don\u2019t.txt")
	touch("plain.txt")
	touch("Shot 1 at 2.00\u202fPM.png")
	touch("odd\u00a0name.txt")
	shot := "Screenshot 2024-01-01 at 9.41.00\u202fAM.png"
	cases := map[string]string{
		"Screenshot 2024-01-01 at 9.41.00 AM.png":      shot,
		"Screenshot 2024-01-01 at 9.41.00\u00a0AM.png": shot,
		"Don't.txt":                     "Don\u2019t.txt",
		"plain.txt":                     "plain.txt",
		"@plain.txt":                    "plain.txt",
		"Shot 1 at 2.00\u202fPM.png":    "Shot 1 at 2.00\u202fPM.png",
		"odd\u00a0name.txt":             "odd\u00a0name.txt", // the exact name, which space normalisation would break
		"missing.txt":                   "missing.txt",
		filepath.Join(dir, "Don't.txt"): filepath.Join(dir, "Don\u2019t.txt"),
	}
	for in, want := range cases {
		if got := ResolveReadPath(in, dir, "/nohome"); got != filepath.Join(dir, want) && got != want {
			t.Errorf("ResolveReadPath(%q)=%q want %q", in, got, want)
		}
	}
	if got := resolveRaw("~/x", "/c", "/h"); got != "/h/x" {
		t.Error(got)
	}
	if got := resolveRaw("~", "/c", "/h"); got != "/h" {
		t.Error(got)
	}
	if got := resolveRaw("/abs", "/c", "/h"); got != "/abs" {
		t.Error(got)
	}
}

func TestFileMutationQueueSerialisesOneFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	var inside, overlap atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = WithFileMutation(path, func() (struct{}, error) {
				if inside.Add(1) > 1 {
					overlap.Add(1)
				}
				time.Sleep(5 * time.Millisecond)
				inside.Add(-1)
				return struct{}{}, nil
			})
		}()
	}
	wg.Wait()
	if overlap.Load() != 0 {
		t.Fatal("two mutations of one file ran at once")
	}
	if queuedFiles() != 0 {
		t.Fatalf("%d queue entries leaked", queuedFiles())
	}
}

func TestFileMutationQueueSharesSymlinkAliasesAndSeparatesFiles(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	if QueueKey(link) != QueueKey(real) {
		t.Fatal("alias and target should share a queue")
	}
	if QueueKey(filepath.Join(dir, "other.txt")) == QueueKey(real) {
		t.Fatal("different files must not share a queue")
	}
	// A different file does not wait for a held one.
	hold, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = WithFileMutation(real, func() (int, error) { close(hold); <-release; return 0, nil })
	}()
	<-hold
	done := make(chan struct{})
	go func() {
		_, _ = WithFileMutation(filepath.Join(dir, "other.txt"), func() (int, error) { return 0, nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("an unrelated file waited")
	}
	close(release)
}
