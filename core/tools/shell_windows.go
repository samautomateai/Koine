//go:build windows

package tools

import (
	"os/exec"
	"strconv"
)

func setProcessGroup(cmd *exec.Cmd) {}

// killTree kills the shell and its children with taskkill.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
