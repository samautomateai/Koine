//go:build linux

package tui

import "syscall"

const (
	tcget = syscall.TCGETS
	tcset = syscall.TCSETS
)
