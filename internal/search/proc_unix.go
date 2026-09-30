//go:build unix

package search

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group and makes
// context cancellation kill the whole group, so a script's grandchildren
// cannot outlive its timeout.
func setProcessGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
