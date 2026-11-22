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
	// 警告走 agent log，不經 onLine：onLine 也用於版本偵測，警告文字會被誤解析
	if len(lines) != 0 {
		t.Fatalf("超長行警告不應經 onLine: %q", lines)
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
	timeout := 1 * time.Second
	// 背景行程先印出自己的 PID（setsid 後 session/群組 ID 即此 PID），供確認已啟動並在 Cleanup 殺掉
	var pgid string
	t.Cleanup(func() {
		if pgid != "" {
			exec.Command("pkill", "-KILL", "-g", pgid).Run()
		}
	})
	// setsid 讓背景行程脫離程序群組，卻仍繼承 stdout；超時後 killGroup 殺不到它
	res := Run(context.Background(), "setsid sh -c 'echo started $$; sleep 5' & echo hi", "", timeout, func(l string) { lines = append(lines, l) })
	d := time.Since(start)
	for _, l := range lines {
		if strings.HasPrefix(l, "started ") {
			pgid = strings.TrimSpace(strings.TrimPrefix(l, "started "))
		}
	}
	if pgid == "" {
		t.Fatalf("背景行程未啟動或未寫入 stdout，lines=%q", lines)
	}
	// 未達 timeout 代表背景行程沒握住 pipe，此測試就沒測到超時路徑
	if d < timeout {
		t.Fatalf("未觸發超時（耗時 %v），背景行程可能沒有握住 pipe", d)
	}
	if d > 3*time.Second {
		t.Fatalf("orphan holding stdout blocked Run for %v", d)
	}
	// shell 以 0 結束，但超時必須回報失敗，不能被誤判為 success
	if res.ExitCode != -1 {
		t.Fatalf("超時應回報 ExitCode -1，實際 %d", res.ExitCode)
	}
	for _, l := range lines {
		if strings.Contains(l, "1MB") {
			t.Fatalf("超時不應回報單行超限: %q", lines)
		}
	}
}
