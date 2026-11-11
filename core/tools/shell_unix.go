//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the shell in its own process group, so that a kill reaches everything it started.
func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killTree kills the shell's whole process group.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
