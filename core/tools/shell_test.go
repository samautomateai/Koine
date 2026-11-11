package tools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestBashRunsInTheWorkingDirectoryAndReportsFailures(t *testing.T) {
	r, dir := setup(t)
	out, err := run(t, r, "bash", `{"command":"pwd"}`)
	if err != nil || !strings.Contains(out, filepath.Base(dir)) {
		t.Fatalf("%q %v", out, err)
	}
	out, err = run(t, r, "bash", `{"command":"echo boom; echo err >&2; exit 3"}`)
	if err == nil || !strings.Contains(out, "boom") || !strings.Contains(out, "err") || !strings.HasSuffix(out, "Command exited with code 3") {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := run(t, r, "bash", `{"command":"true"}`); err != nil || out != "(no output)" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestBashTimeoutKillsTheWholeProcessTree(t *testing.T) {
	r, dir := setup(t)
	pidFile := filepath.Join(dir, "child.pid")
	// The shell starts a background child that outlives it unless the whole group is killed.
	start := time.Now()
	out, err := run(t, r, "bash", `{"command":"sleep 30 & echo $! > child.pid; sleep 30","timeout":1}`)
	if err == nil || !strings.Contains(err.Error(), "timed out after 1 seconds") || !strings.HasSuffix(out, "Command timed out after 1 seconds") {
		t.Fatalf("%q %v", out, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // the background child is gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("background child %d survived the timeout", pid)
}

func TestBashAbortKillsTheCommand(t *testing.T) {
	r, _ := setup(t)
	tl, _ := r.Get("bash")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	out, err := tl.Run(ctx, []byte(`{"command":"echo started; sleep 30"}`))
	if err == nil || !strings.Contains(out, "started") || !strings.HasSuffix(out, "Command aborted") {
		t.Fatalf("%q %v", out, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestBashKeepsTheTailAndSavesTheFullOutput(t *testing.T) {
	r, _ := setup(t)
	out, err := run(t, r, "bash", `{"command":"seq 1 5000"}`)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\[Showing lines (\d+)-5000 of 5000\. Full output: (\S+\.log)\]`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no truncation note in the tail of %q", out[max(0, len(out)-200):])
	}
	defer func() { _ = os.Remove(m[2]) }()
	if !strings.HasPrefix(out, "3001\n") || !strings.Contains(out, "\n5000\n\n[Showing") || m[1] != "3001" {
		t.Fatalf("expected the last 2000 lines, got %q... from line %s", out[:20], m[1])
	}
	full, err := os.ReadFile(m[2])
	if err != nil || !strings.HasPrefix(string(full), "1\n2\n3\n") || !strings.HasSuffix(string(full), "\n5000\n") {
		t.Fatalf("temp file: %v, %d bytes", err, len(full))
	}
}

func TestBashByteLimitAndALongLastLine(t *testing.T) {
	r, _ := setup(t)
	// 1000 lines of 100 bytes: over 50KB before the 2000 line limit.
	out, err := run(t, r, "bash", `{"command":"for i in $(seq 1 1000); do printf '%0100d\n' $i; done"}`)
	if err != nil || !strings.Contains(out, "(50.0KB limit). Full output: ") {
		t.Fatalf("%q %v", out[max(0, len(out)-200):], err)
	}
	cleanLogs(t, out)
	// One huge line without a newline keeps only its end.
	out, err = run(t, r, "bash", `{"command":"head -c 120000 /dev/zero | tr '\\0' z"}`)
	if err != nil || !strings.Contains(out, "[Showing last 50.0KB of line 1 (line is 117.2KB). Full output: ") {
		t.Fatalf("%q %v", out[max(0, len(out)-200):], err)
	}
	cleanLogs(t, out)
}

func cleanLogs(t *testing.T, out string) {
	t.Helper()
	if m := regexp.MustCompile(`Full output: (\S+\.log)`).FindStringSubmatch(out); m != nil {
		_ = os.Remove(m[1])
	}
}

func TestBashOutputBeyondTheMemoryWindowStillReachesTheFile(t *testing.T) {
	r, _ := setup(t)
	// 1MB of output: more than the accumulator keeps in memory, so the file is opened while the command runs.
	out, err := run(t, r, "bash", `{"command":"head -c 1000000 /dev/zero | tr '\\0' q | fold -w 100"}`)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Full output: (\S+\.log)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("%q", out[max(0, len(out)-200):])
	}
	defer func() { _ = os.Remove(m[1]) }()
	st, err := os.Stat(m[1])
	if err != nil || st.Size() < 1000000 {
		t.Fatalf("full output file: %v %v", st, err)
	}
}

func TestBashStripsTerminalEscapes(t *testing.T) {
	r, _ := setup(t)
	out, err := run(t, r, "bash", `{"command":"printf '\\033[31mred\\033[0m\\r\\nplain\\n'"}`)
	if err != nil || out != "red\nplain" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestBashReportsPartialOutputWhileRunning(t *testing.T) {
	r, _ := setup(t)
	tl, _ := r.Get("bash")
	var mu sync.Mutex
	var updates []string
	ctx := WithUpdates(context.Background(), func(s string) { mu.Lock(); updates = append(updates, s); mu.Unlock() })
	out, err := tl.Run(ctx, []byte(`{"command":"echo first; sleep 0.3; echo second"}`))
	if err != nil || out != "first\nsecond" {
		t.Fatalf("%q %v", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(updates) == 0 || updates[0] != "first" {
		t.Fatalf("updates: %q", updates)
	}
}

func TestShellPathAndCommandPrefix(t *testing.T) {
	dir := t.TempDir()
	// A custom shell that records how it was called, then runs the command with sh.
	custom := filepath.Join(dir, "myshell")
	script := "#!/bin/sh\necho \"shell: $0 $1\" >&2\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(custom, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := Shell{Path: custom, Prefix: "export GREETING=hello", Cwd: dir}
	res, err := sh.Run(context.Background(), `echo "$GREETING world"`, 0, nil)
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Output, "shell: "+custom+" -c") || !strings.HasSuffix(res.Output, "hello world") {
		t.Fatalf("%+v %v", res, err)
	}
	// ~ in shellPath expands against Home.
	sh = Shell{Path: "~/myshell", Cwd: dir, Home: dir}
	if res, err := sh.Run(context.Background(), "echo hi", 0, nil); err != nil || !strings.HasSuffix(res.Output, "hi") {
		t.Fatalf("%+v %v", res, err)
	}
	// Through the registered tool, from the options.
	r := NewRegistry()
	RegisterWith(r, Options{Cwd: dir, Home: dir, ShellPath: custom, ShellCommandPrefix: "X=from-prefix", Ripgrep: "-"})
	out, err := run(t, r, "bash", `{"command":"echo $X"}`)
	if err != nil || !strings.HasSuffix(out, "from-prefix") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestShellErrors(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := (Shell{Path: filepath.Join(dir, "none"), Cwd: dir}).Run(ctx, "true", 0, nil); err == nil || !strings.Contains(err.Error(), "shellPath") {
		t.Fatal(err)
	}
	if _, err := (Shell{Cwd: filepath.Join(dir, "gone")}).Run(ctx, "true", 0, nil); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatal(err)
	}
	// A shell that is not executable cannot start.
	plain := filepath.Join(dir, "noexec")
	_ = os.WriteFile(plain, []byte("x"), 0o644)
	if _, err := (Shell{Path: plain, Cwd: dir}).Run(context.Background(), "true", 0, nil); err == nil {
		t.Fatal("expected a start error")
	}
}

func TestUserBashHasNoDefaultTimeout(t *testing.T) {
	dir := t.TempDir()
	res, err := (Shell{Cwd: dir}).Run(context.Background(), "echo ok", 0, nil)
	if err != nil || res.Output != "ok" || res.TimedOut || res.Cancelled {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestFormatShellResult(t *testing.T) {
	if out, err := FormatShellResult(ShellResult{Output: "fine"}, time.Second); err != nil || out != "fine" {
		t.Fatal(out, err)
	}
	if out, err := FormatShellResult(ShellResult{Output: "x", ExitCode: 2}, time.Second); err == nil || out != "x\n\nCommand exited with code 2" {
		t.Fatal(out, err)
	}
	out, err := FormatShellResult(ShellResult{TimedOut: true}, 1500*time.Millisecond)
	if err == nil || out != "(no output)\n\nCommand timed out after 1.5 seconds" {
		t.Fatal(out, err)
	}
	if out, err := FormatShellResult(ShellResult{Cancelled: true, Output: "p"}, time.Second); err == nil || out != "p\n\nCommand aborted" {
		t.Fatal(out, err)
	}
}
