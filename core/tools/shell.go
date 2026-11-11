package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Shell runs commands for the bash tool and for the user's !cmd. Path is the shell executable ("" = bash, else sh) and Prefix is put before
// every command, on its own line (the shellPath and shellCommandPrefix settings).
type Shell struct {
	Path, Prefix, Cwd, Home string
	Env                     func() []string // extra "NAME=value" entries for each command, after the process environment
}

// ShellResult is what a command produced. Output is the tail of the output (last 2000 lines or 50KB) followed, when it was cut, by a note
// that says where the full output is.
type ShellResult struct {
	Output         string
	ExitCode       int
	TimedOut       bool
	Cancelled      bool
	Truncated      bool
	FullOutputPath string // the whole output, when Output is only the tail
}

// ErrNoShell means no shell executable could be found.
var ErrNoShell = errors.New("no shell found: install bash or set shellPath")

// resolve finds the shell executable.
func (s Shell) resolve() (string, error) {
	if s.Path != "" {
		p := ExpandPath(s.Path, s.Home)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("shellPath %q does not exist", s.Path)
		}
		return p, nil
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash", nil
	}
	for _, n := range []string{"bash", "sh"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", ErrNoShell
}

// Run executes command. A timeout of zero or less means no timeout. update, when set, is called with the tail of the output so far, at most
// every 100ms. Cancelling ctx or reaching the timeout kills the shell and everything it started. The error is only for failing to start.
func (s Shell) Run(ctx context.Context, command string, timeout time.Duration, update func(string)) (ShellResult, error) {
	path, err := s.resolve()
	if err != nil {
		return ShellResult{}, err
	}
	if st, err := os.Stat(s.Cwd); err != nil || !st.IsDir() {
		return ShellResult{}, fmt.Errorf("working directory does not exist: %s", s.Cwd)
	}
	if s.Prefix != "" {
		command = s.Prefix + "\n" + command
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var timedOut atomic.Bool
	if timeout > 0 {
		t := time.AfterFunc(timeout, func() { timedOut.Store(true); cancel() })
		defer t.Stop()
	}
	cmd := exec.CommandContext(runCtx, path, "-c", command)
	cmd.Dir = s.Cwd
	if s.Env != nil {
		cmd.Env = append(os.Environ(), s.Env()...)
	}
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
	cmd.WaitDelay = time.Second // grandchildren that outlive the kill must not keep the pipe open forever
	acc := &outputAccumulator{update: update}
	cmd.Stdout, cmd.Stderr = acc, acc
	runErr := cmd.Run()
	res := acc.finish()
	res.TimedOut = timedOut.Load()
	res.Cancelled = !res.TimedOut && ctx.Err() != nil
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		res.ExitCode = ee.ExitCode()
	case res.TimedOut || res.Cancelled:
		res.ExitCode = -1
	default:
		return ShellResult{}, runErr
	}
	if res.ExitCode == 0 && (res.TimedOut || res.Cancelled) {
		res.ExitCode = -1
	}
	return res, nil
}

// keepBytes is how much of the newest output is held in memory; older output only lives in the temp file.
const keepBytes = 4 * DefaultMaxBytes

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07]*\x07")

// outputAccumulator collects command output: the newest keepBytes in memory and, once there is more than that, everything in a temp file.
type outputAccumulator struct {
	mu       sync.Mutex
	buf      []byte
	total    int
	newlines int
	last     byte
	file     *os.File
	update   func(string)
	lastCall time.Time
}

func (a *outputAccumulator) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	a.total += len(p)
	a.newlines += strings.Count(string(p), "\n")
	a.last = p[len(p)-1]
	a.buf = append(a.buf, p...)
	if a.file != nil {
		_, _ = a.file.Write(p)
	}
	if len(a.buf) > keepBytes {
		if a.file == nil && a.openFile() {
			_, _ = a.file.Write(a.buf)
		}
		a.buf = append([]byte(nil), a.buf[len(a.buf)-keepBytes:]...)
	}
	if a.update != nil && time.Since(a.lastCall) >= 100*time.Millisecond {
		a.lastCall = time.Now()
		a.update(tailText(a.buf))
	}
	return len(p), nil
}

func (a *outputAccumulator) openFile() bool {
	f, err := os.CreateTemp("", "koine-bash-*.log")
	if err != nil {
		return false
	}
	a.file = f
	return true
}

// tailText cleans terminal noise from the newest output and cuts it to the tail limits.
func tailText(b []byte) string {
	s := ansiEscape.ReplaceAllString(string(b), "")
	s = strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	return TruncateTail(s, DefaultMaxLines, DefaultMaxBytes).Content
}

func (a *outputAccumulator) finish() ShellResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	text := ansiEscape.ReplaceAllString(string(a.buf), "")
	text = strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	t := TruncateTail(text, DefaultMaxLines, DefaultMaxBytes)
	res := ShellResult{Output: t.Content}
	if !t.Truncated && len(a.buf) == a.total {
		a.closeFile()
		return res
	}
	res.Truncated = true
	if a.file == nil && a.openFile() {
		_, _ = a.file.Write(a.buf)
	}
	if a.file != nil {
		res.FullOutputPath = a.file.Name()
	}
	a.closeFile()
	total := a.newlines
	if a.last != '\n' {
		total++
	}
	total = max(total, t.OutputLines)
	from := total - t.OutputLines + 1
	where := ""
	if res.FullOutputPath != "" {
		where = ". Full output: " + res.FullOutputPath
	}
	switch {
	case t.LastLinePartial:
		res.Output += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s)%s]", FormatSize(t.OutputBytes), total, FormatSize(a.total), where)
	case t.By == "bytes":
		res.Output += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit)%s]", from, total, total, FormatSize(DefaultMaxBytes), where)
	default:
		res.Output += fmt.Sprintf("\n\n[Showing lines %d-%d of %d%s]", from, total, total, where)
	}
	return res
}

func (a *outputAccumulator) closeFile() {
	if a.file != nil {
		_ = a.file.Close()
	}
}
