package executor

import (
	"context"
	"os/exec"
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
func TestLongLineDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 無 head/tr/seq，且 group-kill 屬實驗性")
	}
	var lines []string
	cmd := `head -c 2000000 /dev/zero | tr '\0' a; echo; for i in $(seq 1 2000); do echo xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx; done`
	start := time.Now()
	res := Run(context.Background(), cmd, "", 10*time.Second, func(l string) { lines = append(lines, l) })
	if d := time.Since(start); d > 3*time.Second || res.ExitCode != 0 {
		t.Fatalf("long line: took %v exit=%d", d, res.ExitCode)
	}
	if len(lines) != 1 || lines[0] != "■ 輸出單行超過 1MB，其餘輸出略過" {
		t.Fatalf("lines=%q", lines)
	}
}
func TestTimeoutWithOrphanHoldingStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 無 setsid")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("無 setsid")
	}
	var lines []string
	start := time.Now()
	// setsid 讓背景行程脫離程序群組，卻仍繼承 stdout；超時後 killGroup 殺不到它
	Run(context.Background(), "setsid sh -c 'sleep 30' & echo hi", "", 1*time.Second, func(l string) { lines = append(lines, l) })
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("orphan holding stdout blocked Run for %v", d)
	}
	for _, l := range lines {
		if strings.Contains(l, "1MB") {
			t.Fatalf("超時不應回報單行超限: %q", lines)
		}
	}
}
