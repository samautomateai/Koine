//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package tui

// openTTY has no raw-mode support on this platform, so the line-mode loop is used.
func openTTY() Terminal { return nil }
