//go:build !windows

package coder

import (
	"os/exec"
	"syscall"
)

// setProcGroup gives cmd its own process group and kills the whole group on
// cancel, so child processes are never orphaned. exec.CommandContext otherwise
// signals only the direct child — and a coder subprocess routinely shells out
// to python or bash, which would survive.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// processAlive reports whether pid is still running. Signal 0 performs error
// checking without delivering a signal.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
