package executor

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Result struct {
	ExitCode int
}

// Run executes `bash -lc cmd` in its own process group, streaming stdout/stderr
// lines to onLine. The process is killed when ctx is cancelled or timeout elapses.
func Run(ctx context.Context, cmd, cwd string, timeout time.Duration, onLine func(string)) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.Command("bash", "-lc", cmd)
	if cwd != "" {
		c.Dir = cwd
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group → kill children too
	stdout, err := c.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1}
	}
	c.Stderr = c.Stdout // merge stderr into stdout

	if err := c.Start(); err != nil {
		return Result{ExitCode: -1}
	}

	// kill the whole process group when ctx ends
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if c.Process != nil {
				syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if onLine != nil {
			onLine(line)
		}
	}
	err = c.Wait()
	close(done)

	return Result{ExitCode: exitCode(err)}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
	}
	return -1
}
