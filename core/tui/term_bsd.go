//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package tui

import "syscall"

const (
	tcget = syscall.TIOCGETA
	tcset = syscall.TIOCSETA
)
