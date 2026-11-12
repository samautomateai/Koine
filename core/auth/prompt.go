package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// sttyRun runs stty on the terminal f and returns its trimmed output. It is a variable so tests can replace it.
var sttyRun = func(f *os.File, args ...string) (string, error) {
	c := exec.Command("stty", args...)
	c.Stdin = f
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// IsTerminal reports whether f is a terminal (a character device).
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ReadSecret prints prompt to out and reads one line from in. When tty is true and in is a terminal file, echo is switched off with
// stty for the read (the standard library has no portable way) and restored afterwards; where that is not possible (Windows, no stty) the
// line is read with echo and out says so. The line is trimmed; an empty line is an error.
func ReadSecret(in io.Reader, tty bool, out io.Writer, prompt string) (string, error) {
	f, isFile := in.(*os.File)
	hidden := false
	if tty && isFile {
		if saved, err := sttyRun(f, "-g"); err == nil && saved != "" {
			if _, err := sttyRun(f, "-echo"); err == nil {
				hidden = true
				defer func() { _, _ = sttyRun(f, saved) }()
			}
		}
		if !hidden {
			fmt.Fprintln(out, "(this terminal cannot hide input; the key will be visible)")
		}
	}
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if hidden {
		fmt.Fprintln(out)
	}
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", fmt.Errorf("no input: %w", err)
	}
	if line = strings.TrimSpace(line); line == "" {
		return "", errors.New("nothing was entered")
	}
	return line, nil
}
