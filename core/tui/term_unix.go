//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package tui

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// This file is the whole of Koine's contact with a real terminal. It has no logic worth testing without one; everything it feeds
// (Decode, UI, Screen) is covered by tests of pure functions.

type winsize struct{ Row, Col, X, Y uint16 }

type tty struct {
	fd, out int // input and output descriptors (0 and 1 for the process's terminal)
	orig    syscall.Termios
	paused  atomic.Bool
	parked  atomic.Bool
	inRead  atomic.Bool
	resize  chan struct{}
	started bool
}

func isTerminal(fd int) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), tcget, uintptr(unsafe.Pointer(&t))) //nolint:gosec // ioctl needs the pointer
	return e == 0
}

// openTTY returns the terminal when both stdin and stdout are terminals, else nil.
func openTTY() Terminal {
	if !isTerminal(0) || !isTerminal(1) {
		return nil
	}
	return &tty{fd: 0, out: 1, resize: make(chan struct{}, 1)}
}

func (t *tty) Size() (int, int) {
	var ws winsize
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(t.out), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))) //nolint:gosec // ioctl needs the pointer
	if e != 0 || ws.Col == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}

func (t *tty) Output() io.Writer { return os.Stdout }
func (t *tty) Input() io.Reader  { return t }

func (t *tty) ResizeCh() <-chan struct{} {
	if !t.started {
		t.started = true
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		go func() {
			for range ch {
				select {
				case t.resize <- struct{}{}:
				default:
				}
			}
		}()
	}
	return t.resize
}

func (t *tty) set(tm *syscall.Termios) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(t.fd), tcset, uintptr(unsafe.Pointer(tm))) //nolint:gosec // ioctl needs the pointer
	if e != 0 {
		return e
	}
	return nil
}

// Raw switches to raw mode. Reads time out after a tenth of a second (VMIN 0, VTIME 1) so the reader can notice Pause.
func (t *tty) Raw() (func(), error) {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(t.fd), tcget, uintptr(unsafe.Pointer(&t.orig))); e != 0 { //nolint:gosec // ioctl
		return nil, e
	}
	if err := t.rawMode(); err != nil {
		return nil, err
	}
	return func() { _ = t.set(&t.orig) }, nil
}

func (t *tty) rawMode() error {
	r := t.orig
	r.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	r.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	r.Cflag &^= syscall.CSIZE | syscall.PARENB
	r.Cflag |= syscall.CS8
	r.Cc[syscall.VMIN], r.Cc[syscall.VTIME] = 0, 1
	return t.set(&r)
}

// Read implements io.Reader for the input goroutine; it parks while a child program owns the terminal.
func (t *tty) Read(p []byte) (int, error) {
	t.inRead.Store(true)
	defer t.inRead.Store(false)
	for {
		if t.paused.Load() {
			t.parked.Store(true)
			time.Sleep(20 * time.Millisecond)
			continue
		}
		t.parked.Store(false)
		n, err := syscall.Read(t.fd, p)
		if n > 0 {
			return n, nil
		}
		if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
			return 0, err
		}
	}
}

func (t *tty) pause() {
	t.paused.Store(true)
	for t.inRead.Load() && !t.parked.Load() { // a read in progress notices within a tenth of a second
		time.Sleep(5 * time.Millisecond)
	}
}

func (t *tty) resume() { t.paused.Store(false) }

func (t *tty) Suspend() error {
	t.pause()
	defer t.resume()
	_ = t.set(&t.orig)
	err := syscall.Kill(syscall.Getpid(), syscall.SIGTSTP)
	_ = t.rawMode()
	return err
}

func (t *tty) Exec(cmd *exec.Cmd) error {
	t.pause()
	defer t.resume()
	_ = t.set(&t.orig)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	_ = t.rawMode()
	return err
}
