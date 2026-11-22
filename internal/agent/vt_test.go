package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/curtis1215/cockpit/internal/executor"
)

func TestReportVersionsOnce(t *testing.T) {
	var reported int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{{"software": "cc", "current_cmd": "echo cc 2.1.98", "version_regex": nil}})
		case "/api/agent/report-versions":
			atomic.AddInt32(&reported, 1)
			w.Write([]byte(`{"applied":1}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.ReportVersions(10 * time.Second)
	if atomic.LoadInt32(&reported) != 1 {
		t.Fatalf("reported=%d", reported)
	}
}

// TestReportVersionsIgnoresTruncationWarning: 版本指令輸出超長行時，超限警告不能被當成版本
// （version_regex (\d+) 不應從「1MB」解析出 1）。
func TestReportVersionsIgnoresTruncationWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 無 head/tr")
	}
	var mu sync.Mutex
	var reports []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{{"software": "cc", "current_cmd": `head -c 2000000 /dev/zero | tr '\0' a; echo`, "version_regex": `(\d+)`}})
		case "/api/agent/report-versions":
			var body []map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			reports = append(reports, body...)
			mu.Unlock()
			w.Write([]byte(`{"applied":1}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.ReportVersions(10 * time.Second)
	if len(reports) != 1 || reports[0]["current_version"] != "" {
		t.Fatalf("reports=%v, want current_version 空字串", reports)
	}
}

func TestRunJobOnce(t *testing.T) {
	var result map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{})
		case endsWith(r.URL.Path, "/log"):
			w.WriteHeader(204)
		case endsWith(r.URL.Path, "/control"):
			w.Write([]byte(`{"abort":false}`))
		case endsWith(r.URL.Path, "/result"):
			json.NewDecoder(r.Body).Decode(&result)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.RunJob(Job{ID: 7, ShellCmd: "echo added", CurrentCmd: "echo cc 2.1.101"}, 2*time.Second, 10*time.Second)
	if result["status"] != "success" || result["new_version"] != "2.1.101" {
		t.Fatalf("result=%v", result)
	}
}

func TestRunJobFailure(t *testing.T) {
	var result map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case endsWith(r.URL.Path, "/log"):
			w.WriteHeader(204)
		case endsWith(r.URL.Path, "/control"):
			w.Write([]byte(`{"abort":false}`))
		case endsWith(r.URL.Path, "/result"):
			json.NewDecoder(r.Body).Decode(&result)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.RunJob(Job{ID: 8, ShellCmd: "exit 3", CurrentCmd: "echo x"}, 2*time.Second, 10*time.Second)
	if result["status"] != "failed" || result["exit_code"] != float64(3) {
		t.Fatalf("result=%v", result)
	}
}

func endsWith(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }

func TestPollOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/poll" {
			if r.URL.Query().Get("wait") != "0" {
				t.Errorf("wait param: %s", r.URL.Query().Get("wait"))
			}
			json.NewEncoder(w).Encode(map[string]any{"type": "job", "job": map[string]any{"id": 5, "shell_cmd": "echo hi"}})
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok"}
	evt, job, err := a.pollOnce(0)
	if err != nil || evt != "job" || job.ID != 5 || job.ShellCmd != "echo hi" {
		t.Fatalf("poll: %q %+v %v", evt, job, err)
	}
}

// TestPollClientTimeoutExceedsWait: long-poll 的 HTTP client timeout 必須大於
// server 端等待秒數，否則 client 會在 server 正常回應（或回 204）前先斷線；
// 透過代理（Cloudflare/Caddy）時 server 感知不到斷線，仍可能把 job claim 給
// 已死的連線，造成 job 永遠卡在 running。
func TestPollClientTimeoutExceedsWait(t *testing.T) {
	a := &Agent{ServerURL: "http://x", Token: "tok"}
	for _, waitSec := range []int{0, 25, 60} {
		got := a.pollHTTP(waitSec).Timeout()
		want := time.Duration(waitSec)*time.Second + 10*time.Second
		if got < want {
			t.Fatalf("poll client timeout for wait=%d: got %v, want >= %v", waitSec, got, want)
		}
	}
}

func TestPollOnce204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok"}
	evt, _, err := a.pollOnce(0)
	if err != nil || evt != "" {
		t.Fatalf("204 poll: %q %v", evt, err)
	}
}

// TestRunJobBatchesLogs: 120 行輸出要批次送出（不是每行一次 HTTP），
// 行序不亂，且所有 /log 都早於 /result。
func TestRunJobBatchesLogs(t *testing.T) {
	var mu sync.Mutex
	var logBatches [][]string
	var order []string // 依到達順序記錄 "log" / "result"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case endsWith(r.URL.Path, "/log"):
			var body struct {
				Lines []string `json:"lines"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			logBatches = append(logBatches, body.Lines)
			order = append(order, "log")
			mu.Unlock()
			w.WriteHeader(204)
		case endsWith(r.URL.Path, "/control"):
			w.Write([]byte(`{"abort":false}`))
		case endsWith(r.URL.Path, "/result"):
			mu.Lock()
			order = append(order, "result")
			mu.Unlock()
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.RunJob(Job{ID: 9, ShellCmd: "for i in $(seq 1 120); do echo line$i; done", CurrentCmd: "true"}, 2*time.Second, 10*time.Second)

	var lines []string
	for _, b := range logBatches {
		lines = append(lines, b...)
	}
	if len(lines) != 120 {
		t.Fatalf("lines=%d, want 120", len(lines))
	}
	for i, l := range lines {
		if want := fmt.Sprintf("line%d", i+1); l != want {
			t.Fatalf("line %d = %q, want %q", i, l, want)
		}
	}
	if len(logBatches) > 5 {
		t.Fatalf("log requests=%d, want <= 5", len(logBatches))
	}
	if len(order) == 0 || order[len(order)-1] != "result" || len(order) != len(logBatches)+1 {
		t.Fatalf("order=%v: want all /log before the single /result", order)
	}
}

// TestRunJobRetriesFinalLogFlushBeforeResult: 最後的 flush 前兩次回 500、第三次成功，
// 所有行都要送達，且所有 /log 請求（含失敗的）都必須早於 /result。
func TestRunJobRetriesFinalLogFlushBeforeResult(t *testing.T) {
	var mu sync.Mutex
	var delivered []string
	var order []string // 依到達順序記錄 "log" / "result"
	logCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case endsWith(r.URL.Path, "/log"):
			var body struct {
				Lines []string `json:"lines"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			defer mu.Unlock()
			logCalls++
			order = append(order, "log")
			if logCalls <= 2 {
				w.WriteHeader(500)
				return
			}
			delivered = append(delivered, body.Lines...)
			w.WriteHeader(204)
		case endsWith(r.URL.Path, "/control"):
			w.Write([]byte(`{"abort":false}`))
		case endsWith(r.URL.Path, "/result"):
			mu.Lock()
			order = append(order, "result")
			mu.Unlock()
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.RunJob(Job{ID: 10, ShellCmd: "echo line1; echo line2; echo line3", CurrentCmd: "true"}, 2*time.Second, 10*time.Second)

	if got := fmt.Sprint(delivered); got != "[line1 line2 line3]" {
		t.Fatalf("delivered=%s, want [line1 line2 line3]; order=%v", got, order)
	}
	if n := len(order); n < 4 || order[n-1] != "result" {
		t.Fatalf("order=%v: want >=3 /log attempts then /result", order)
	}
	for _, o := range order[:len(order)-1] {
		if o != "log" {
			t.Fatalf("order=%v: all /log must precede /result", order)
		}
	}
}

// TestRunJobGivesUpFinalLogFlushButReportsResult: 最後的 flush 連續 3 次失敗就放棄，但 result 仍要回報。
func TestRunJobGivesUpFinalLogFlushButReportsResult(t *testing.T) {
	var mu sync.Mutex
	logAttempts := 0
	var result map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case endsWith(r.URL.Path, "/log"):
			mu.Lock()
			logAttempts++
			mu.Unlock()
			w.WriteHeader(500)
		case endsWith(r.URL.Path, "/control"):
			w.Write([]byte(`{"abort":false}`))
		case endsWith(r.URL.Path, "/result"):
			mu.Lock()
			json.NewDecoder(r.Body).Decode(&result)
			mu.Unlock()
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.RunJob(Job{ID: 11, ShellCmd: "echo x", CurrentCmd: "true"}, 2*time.Second, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if logAttempts != logFinalFlushTries {
		t.Fatalf("log attempts=%d, want %d", logAttempts, logFinalFlushTries)
	}
	if result["status"] != "success" {
		t.Fatalf("result=%v, want success", result)
	}
}

// TestRunJobAbortArrivingAfterExitIsNotAborted: 指令已正常結束，abort 輪詢的回應卻在 RunJob 收尾前才到，
// 結果仍必須是 success 且不記中止 log（aborted 只能在指令結束後讀一次）。
func TestRunJobAbortArrivingAfterExitIsNotAborted(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "polling")
	var mu sync.Mutex
	var logged []string
	var result map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case endsWith(r.URL.Path, "/log"):
			var body struct {
				Lines []string `json:"lines"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			logged = append(logged, body.Lines...)
			mu.Unlock()
			w.WriteHeader(204)
		case endsWith(r.URL.Path, "/control"):
			// 指令等這個旗標出現才結束，確保 abort 輪詢已在執行中；回應延後到指令結束之後才送達。
			os.WriteFile(flag, nil, 0o644)
			time.Sleep(500 * time.Millisecond)
			w.Write([]byte(`{"abort":true}`))
		case endsWith(r.URL.Path, "/result"):
			mu.Lock()
			json.NewDecoder(r.Body).Decode(&result)
			mu.Unlock()
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	shell := fmt.Sprintf("until [ -f '%s' ]; do sleep 0.01; done", flag)
	a.RunJob(Job{ID: 12, ShellCmd: shell, CurrentCmd: "true"}, 5*time.Millisecond, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if result["status"] != "success" {
		t.Fatalf("result=%v, want success", result)
	}
	for _, l := range logged {
		if strings.Contains(l, "中止") {
			t.Fatalf("logged=%q, 已正常完成的工作不該記中止 log", logged)
		}
	}
}

// TestLogBatcherRetriesFailedBatchInOrder: 送出失敗的整批要放回緩衝最前面，下次照原順序再送。
func TestLogBatcherRetriesFailedBatchInOrder(t *testing.T) {
	var sent []string
	fail := true
	b := &logBatcher{send: func(lines []string) error {
		if fail {
			return errors.New("server down")
		}
		sent = append(sent, lines...)
		return nil
	}}
	b.add("a")
	b.add("b")
	b.flush() // 失敗：a、b 放回緩衝
	fail = false
	b.add("c")
	b.flush()
	if got := fmt.Sprint(sent); got != "[a b c]" {
		t.Fatalf("sent=%s, want [a b c]", got)
	}
}

// TestLogBatcherAddDoesNotWaitForSlowPost: /log 每次慢 300ms 時，指令執行不能被拖住。
// 舊版在 add 內同步 POST（每 50 行一次），500 行 = 10 次 × 300ms ≈ 3s。
func TestLogBatcherAddDoesNotWaitForSlowPost(t *testing.T) {
	var mu sync.Mutex
	var got []string
	logs := &logBatcher{send: func(lines []string) error {
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		got = append(got, lines...)
		mu.Unlock()
		return nil
	}}
	start := time.Now()
	executor.Run(context.Background(), "for i in $(seq 1 500); do echo line$i; done", "", 10*time.Second, logs.add)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("executor.Run 被慢速 POST 拖住: %v", d)
	}
	logs.flush()
	if len(got) != 500 || got[0] != "line1" || got[499] != "line500" {
		t.Fatalf("got %d lines, first=%q", len(got), got[0])
	}
}

// TestLogBatcherCapsBuffer: 持續送不出去時緩衝上限 logBufMax，超過丟最舊的。
func TestLogBatcherCapsBuffer(t *testing.T) {
	var sent []string
	fail := true
	b := &logBatcher{send: func(lines []string) error {
		if fail {
			return errors.New("server down")
		}
		sent = append(sent, lines...)
		return nil
	}}
	for i := 1; i <= logBufMax+1; i++ {
		b.add(fmt.Sprintf("line%d", i))
	}
	fail = false
	b.flush()
	if len(sent) != logBufMax || sent[0] != "line2" || sent[len(sent)-1] != fmt.Sprintf("line%d", logBufMax+1) {
		t.Fatalf("sent %d lines, first=%q last=%q", len(sent), sent[0], sent[len(sent)-1])
	}
}

// TestLogBatcherCapsBytesAndSplitsPosts: 10 行 × 0.5MB 超過 logBufMaxBytes(4MiB)，只留最新 8 行；
// 每次 POST 位元組數都不超過 logPostMaxBytes(1MiB)。
func TestLogBatcherCapsBytesAndSplitsPosts(t *testing.T) {
	var posts []int
	var sent []string
	b := &logBatcher{send: func(lines []string) error {
		n := 0
		for _, l := range lines {
			n += len(l)
		}
		posts = append(posts, n)
		sent = append(sent, lines...)
		return nil
	}}
	body := strings.Repeat("x", 500_000)
	for i := 1; i <= 10; i++ {
		b.add(fmt.Sprintf("%d:%s", i, body))
	}
	b.flush()
	for _, n := range posts {
		if n > logPostMaxBytes {
			t.Fatalf("POST %d bytes, want <= %d", n, logPostMaxBytes)
		}
	}
	if len(sent) != 8 || !strings.HasPrefix(sent[0], "3:") {
		t.Fatalf("kept %d lines (want 8), first prefix %.5q (want \"3:\")", len(sent), sent[0])
	}
}
