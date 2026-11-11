package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTerm is a Terminal driven by the test: it feeds keys through a pipe and records what is written.
type fakeTerm struct {
	inR    *io.PipeReader
	inW    *io.PipeWriter
	mu     sync.Mutex
	out    bytes.Buffer
	resize chan struct{}
	w, h   int

	rawOn, rawOff atomic.Bool
	suspended     atomic.Int32
	execs         [][]string
	execFn        func(*exec.Cmd) error
}

func newFakeTerm() *fakeTerm {
	r, w := io.Pipe()
	return &fakeTerm{inR: r, inW: w, resize: make(chan struct{}, 1), w: 80, h: 24}
}

func (f *fakeTerm) Size() (int, int)          { f.mu.Lock(); defer f.mu.Unlock(); return f.w, f.h }
func (f *fakeTerm) Input() io.Reader          { return f.inR }
func (f *fakeTerm) Output() io.Writer         { return (*lockedWriter)(f) }
func (f *fakeTerm) ResizeCh() <-chan struct{} { return f.resize }
func (f *fakeTerm) Raw() (func(), error) {
	f.rawOn.Store(true)
	return func() { f.rawOff.Store(true) }, nil
}
func (f *fakeTerm) Suspend() error { f.suspended.Add(1); return nil }
func (f *fakeTerm) Exec(c *exec.Cmd) error {
	f.mu.Lock()
	f.execs = append(f.execs, c.Args)
	f.mu.Unlock()
	if f.execFn != nil {
		return f.execFn(c)
	}
	return nil
}

type lockedWriter fakeTerm

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.out.Write(p)
}

func (f *fakeTerm) screen() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return StripANSI(f.out.String())
}

func (f *fakeTerm) send(s string) { _, _ = f.inW.Write([]byte(s)) }

func (f *fakeTerm) waitFor(t *testing.T, want string) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if strings.Contains(f.screen(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; screen:\n%s", want, f.screen())
}

func startLoop(t *testing.T, term *fakeTerm, env map[string]string, setup func(*UI)) (done chan error) {
	t.Helper()
	a := newTestApp(t, "")
	done = make(chan error, 1)
	go func() {
		done <- Loop(context.Background(), a, term, Options{Env: func(k string) string { return env[k] }})
	}()
	return done
}

func waitDone(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not exit")
	}
}

func TestLoopRunsAPromptAndExits(t *testing.T) {
	term := newFakeTerm()
	done := startLoop(t, term, nil, nil)
	term.waitFor(t, "koine")
	term.send("hello there\r")
	term.waitFor(t, "model says hi")
	if s := term.screen(); !strings.Contains(s, "hello there") || !strings.Contains(s, "esc to interrupt") && !strings.Contains(s, "claude-sonnet-5-5") {
		t.Errorf("the prompt and footer are shown:\n%s", s)
	}
	term.send("\x04") // Ctrl+D on an empty editor
	waitDone(t, done)
	out := term.out.String()
	if !term.rawOn.Load() || !term.rawOff.Load() || !strings.Contains(out, EnableKeys) || !strings.HasSuffix(out, RestoreKeys) {
		t.Error("raw mode and key modes are entered and always left")
	}
}

func TestLoopSlashCommandsChoicesAndPrefill(t *testing.T) {
	term := newFakeTerm()
	done := startLoop(t, term, nil, nil)
	term.waitFor(t, "koine")
	term.send("first question\r")
	term.waitFor(t, "model says hi")
	term.send("second question\r")
	term.waitFor(t, "second question")
	time.Sleep(50 * time.Millisecond)

	// /tree: open the navigator, show user messages only, select the first one; the app asks about a summary through Choose
	before := strings.Count(term.screen(), "first question")
	term.send("/tree\r")
	term.waitFor(t, "Session tree")
	term.send("\x15") // ctrl+u
	term.send("\x1b[A\r")
	term.waitFor(t, "Summarise the branch you are leaving?")
	term.send("\r") // No summary
	term.waitFor(t, "moved to")
	for i := 0; i < 400 && strings.Count(term.screen(), "first question") <= before+1; i++ {
		time.Sleep(5 * time.Millisecond) // the editor now shows the message the tree went back to
	}
	if strings.Count(term.screen(), "first question") <= before+1 {
		t.Error("the editor was not prefilled")
	}
	term.send("\x05\x15") // end of line, then kill back to the start: empties the editor
	term.send("/new\r")
	term.waitFor(t, "started a new session")
	term.send("/exit\r")
	waitDone(t, done)
}

func TestLoopExternalEditorSuspendCopyAndResize(t *testing.T) {
	term := newFakeTerm()
	term.execFn = func(c *exec.Cmd) error {
		return os.WriteFile(c.Args[len(c.Args)-1], []byte("typed in the editor\n"), 0o600)
	}
	done := startLoop(t, term, map[string]string{"EDITOR": "myeditor --wait", "COLORTERM": "truecolor"}, nil)
	term.waitFor(t, "koine")
	term.send("draft")
	term.send("\x07") // ctrl+g
	term.waitFor(t, "typed in the editor")
	term.mu.Lock()
	execs := term.execs
	term.mu.Unlock()
	if len(execs) != 1 || execs[0][0] != "myeditor" || execs[0][1] != "--wait" {
		t.Errorf("the editor command: %v", execs)
	}
	term.send("\x1a") // ctrl+z
	for i := 0; i < 200 && term.suspended.Load() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if term.suspended.Load() != 1 {
		t.Error("Ctrl+Z suspends")
	}
	term.mu.Lock()
	term.w, term.h = 50, 12
	term.mu.Unlock()
	term.resize <- struct{}{}
	term.send("\r")
	term.waitFor(t, "model says hi")
	term.send("\x18") // ctrl+x copies the last answer
	term.waitFor(t, "copied the last answer")
	if !strings.Contains(term.out.String(), "\x1b]52;c;") {
		t.Error("OSC 52 copy sequence")
	}
	term.send("\x1b[200~pasted\x1b[201~")
	term.send("\x1b") // a lone Escape is delivered after a short wait
	time.Sleep(100 * time.Millisecond)
	term.send("\x05\x15/exit\r")
	waitDone(t, done)

	// an input stream that ends ends the loop
	term2 := newFakeTerm()
	done2 := startLoop(t, term2, nil, nil)
	term2.waitFor(t, "koine")
	_ = term2.inW.Close()
	waitDone(t, done2)
}

func TestLoopFailedEditorIsReported(t *testing.T) {
	term := newFakeTerm()
	term.execFn = func(*exec.Cmd) error { return os.ErrPermission }
	done := startLoop(t, term, nil, nil)
	term.waitFor(t, "koine")
	term.send("\x07")
	term.waitFor(t, "permission denied")
	term.send("/exit\r")
	waitDone(t, done)
}

func TestRunFallsBackToLinesWithoutATerminal(t *testing.T) {
	// the tests' stdin is not a terminal, so Run uses the line loop (covered by TestRunLoop)
	if openTTY() != nil {
		t.Skip("running on a terminal")
	}
}
