package executor

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStream(t *testing.T) {
	var lines []string
	res := Run(context.Background(), "echo hello && echo world", "", 10*time.Second, func(l string) { lines = append(lines, l) })
	if res.ExitCode != 0 || strings.Join(lines, ",") != "hello,world" {
		t.Fatalf("exit=%d lines=%v", res.ExitCode, lines)
	}
}
func TestNonzero(t *testing.T) {
	if Run(context.Background(), "exit 3", "", 10*time.Second, nil).ExitCode != 3 {
		t.Fatal("exit 3")
	}
}
func TestCancelKills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows group-kill 屬實驗性（msys 行程樹）")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	res := Run(ctx, "sleep 5", "", 10*time.Second, nil)
	if time.Since(start) > 3*time.Second || res.ExitCode == 0 {
		t.Fatalf("cancel slow/exit0: %v %d", time.Since(start), res.ExitCode)
	}
}
func TestTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows group-kill 屬實驗性（msys 行程樹）")
	}
	start := time.Now()
	res := Run(context.Background(), "sleep 5", "", 1*time.Second, nil)
	if time.Since(start) > 3*time.Second || res.ExitCode == 0 {
		t.Fatalf("timeout: %v %d", time.Since(start), res.ExitCode)
	}
}
