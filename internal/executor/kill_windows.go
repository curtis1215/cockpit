//go:build windows

package executor

import (
	"os/exec"
	"strconv"
)

// setPgid is a no-op on Windows; there is no direct process-group equivalent.
// NOTE: executor.Run uses "bash -lc" which does not exist on Windows by default.
// Running cockpit serve/agent on Windows is experimental and requires Git Bash or WSL.
func setPgid(c *exec.Cmd) {}

// killGroup terminates the process tree on Windows using taskkill /T /F.
func killGroup(c *exec.Cmd) {
	if c.Process != nil {
		exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid)).Run()
	}
}
