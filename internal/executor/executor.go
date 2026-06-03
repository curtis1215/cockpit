package executor

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

type Result struct{ ExitCode int }

func Run(ctx context.Context, cmd, cwd string, timeout time.Duration, onLine func(string)) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.Command("bash", "-lc", cmd)
	if cwd != "" {
		c.Dir = cwd
	}
	setPgid(c)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1}
	}
	c.Stderr = c.Stdout
	if err := c.Start(); err != nil {
		return Result{ExitCode: -1}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killGroup(c)
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
		return ee.ExitCode()
	}
	return -1
}
