//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the server in its own process group so that a signal reaches the helpers it started too.
func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// signalGroup sends SIGTERM (or SIGKILL when force is set) to the server's process group.
func signalGroup(cmd *exec.Cmd, force bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}
