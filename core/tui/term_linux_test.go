//go:build linux

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// openPTY returns the master side of a pseudo-terminal and a descriptor for its slave, so the raw-mode shim can be exercised for real.
func openPTY(t *testing.T) (*os.File, int) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	var unlock, n int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 { //nolint:gosec // ioctl
		t.Skipf("unlockpt: %v", e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 { //nolint:gosec // ioctl
		t.Skipf("ptsname: %v", e)
	}
	fd, err := syscall.Open(fmt.Sprintf("/dev/pts/%d", n), syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	return m, fd
}

func TestRawTerminalOnAPseudoTerminal(t *testing.T) {
	m, fd := openPTY(t)
	if !isTerminal(fd) || isTerminal(-1) {
		t.Fatal("isTerminal")
	}
	tt := &tty{fd: fd, out: fd, resize: make(chan struct{}, 1)}
	ws := winsize{Row: 31, Col: 101}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 { //nolint:gosec // ioctl
		t.Fatal(e)
	}
	if w, h := tt.Size(); w != 101 || h != 31 {
		t.Errorf("size %dx%d", w, h)
	}
	restore, err := tt.Raw()
	if err != nil {
		t.Fatal(err)
	}
	var now syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), tcget, uintptr(unsafe.Pointer(&now))); e != 0 { //nolint:gosec // ioctl
		t.Fatal(e)
	}
	if now.Lflag&(syscall.ICANON|syscall.ECHO|syscall.ISIG) != 0 || now.Cc[syscall.VMIN] != 0 || now.Cc[syscall.VTIME] != 1 {
		t.Errorf("raw mode: %+v", now)
	}
	if _, err := m.WriteString("hi\x03"); err != nil { // Ctrl+C arrives as a byte, not a signal
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	n, err := tt.Input().Read(buf)
	if err != nil || string(buf[:n]) != "hi\x03" {
		t.Errorf("read %q %v", buf[:n], err)
	}

	// a read waits (in slices of a tenth of a second) until input arrives
	go func() { time.Sleep(250 * time.Millisecond); _, _ = m.WriteString("late") }()
	n, err = tt.Read(buf)
	if err != nil || string(buf[:n]) != "late" {
		t.Errorf("late read %q %v", buf[:n], err)
	}

	// Exec parks the reader, runs the program in cooked mode and returns to raw mode
	if err := tt.Exec(exec.Command("true")); err != nil { //nolint:gosec // fixed test command
		t.Error(err)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), tcget, uintptr(unsafe.Pointer(&now))); e != 0 { //nolint:gosec // ioctl
		t.Fatal(e)
	}
	if now.Lflag&syscall.ICANON != 0 {
		t.Error("raw mode is back after Exec")
	}
	restore()
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), tcget, uintptr(unsafe.Pointer(&now))); e != 0 { //nolint:gosec // ioctl
		t.Fatal(e)
	}
	if now.Lflag&syscall.ICANON == 0 || now.Lflag&syscall.ECHO == 0 {
		t.Error("restore returns to cooked mode")
	}
	if tt.Output() != os.Stdout {
		t.Error("output is stdout")
	}
	ch := tt.ResizeCh()
	if ch == nil || tt.ResizeCh() != ch {
		t.Error("ResizeCh starts once")
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Error("SIGWINCH is reported")
	}
}

func TestTTYReadStopsOnError(t *testing.T) {
	tt := &tty{fd: -1}
	if _, err := tt.Read(make([]byte, 4)); err == nil || !strings.Contains(err.Error(), "bad file descriptor") {
		t.Errorf("a closed descriptor ends the input: %v", err)
	}
	if w, h := tt.Size(); w != 80 || h != 24 {
		t.Errorf("fallback size %dx%d", w, h)
	}
	if _, err := tt.Raw(); err == nil {
		t.Error("Raw on a non-terminal fails")
	}
}
