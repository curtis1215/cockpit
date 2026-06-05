package executor

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunStreamsAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	var lines []string
	res := Run(context.Background(), "echo hello && echo world", "", 10*time.Second,
		func(l string) { lines = append(lines, l) })
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if strings.Join(lines, ",") != "hello,world" {
		t.Fatalf("lines=%v", lines)
	}
}

func TestRunNonzero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	res := Run(context.Background(), "exit 3", "", 10*time.Second, nil)
	if res.ExitCode != 3 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
}

func TestRunCancelKills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	res := Run(ctx, "sleep 5", "", 10*time.Second, nil)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("cancel did not kill promptly")
	}
	if res.ExitCode == 0 {
		t.Fatalf("expected nonzero on cancel")
	}
}

func TestRunTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	start := time.Now()
	res := Run(context.Background(), "sleep 5", "", 1*time.Second, nil)
	if time.Since(start) > 3*time.Second || res.ExitCode == 0 {
		t.Fatalf("timeout not enforced: dur=%v exit=%d", time.Since(start), res.ExitCode)
	}
}
