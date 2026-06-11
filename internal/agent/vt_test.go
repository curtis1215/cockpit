package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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
