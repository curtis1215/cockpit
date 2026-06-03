//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

func setPgid(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(c *exec.Cmd) {
	if c.Process != nil {
		syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
