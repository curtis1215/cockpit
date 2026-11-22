package executor

import (
	"bufio"
	"context"
	"errors"
	"io"
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
			// 脫離群組的孤兒行程仍握著 pipe，關閉讀端才能讓卡住的 Scan/io.Copy 返回
			stdout.Close()
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
	if sc.Err() != nil {
		// 超時關閉 pipe 也會讓 Scan 出錯，只有單行超限才提示
		if onLine != nil && errors.Is(sc.Err(), bufio.ErrTooLong) {
			onLine("■ 輸出單行超過 1MB，其餘輸出略過")
		}
		// Scanner 停讀後不排空 pipe，子程序會卡在寫入直到 timeout
		io.Copy(io.Discard, stdout)
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
