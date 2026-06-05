package jobrunner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"cockpit-agent/internal/httpclient"
)

func TestRunJobCommandFlow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	var mu sync.Mutex
	logs := []string{}
	var result map[string]any
	served := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			var body struct{ Lines []string `json:"lines"` }
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock(); logs = append(logs, body.Lines...); mu.Unlock()
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/result"):
			json.NewDecoder(r.Body).Decode(&result)
			w.WriteHeader(200); w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/control"):
			json.NewEncoder(w).Encode(map[string]bool{"abort": false})
		}
		served = true
	}))
	defer srv.Close()

	c := httpclient.New(srv.URL, "tok", 5*time.Second)
	job := Job{ID: 7, ShellCmd: "echo added 1 package", CurrentCmd: "echo cc 2.1.101", VersionRegex: ""}
	RunJob(c, job, 2*time.Second, 10*time.Second)

	mu.Lock(); defer mu.Unlock()
	if !served || len(logs) == 0 || logs[0] != "added 1 package" {
		t.Fatalf("logs=%v", logs)
	}
	if result["status"] != "success" || result["new_version"] != "2.1.101" {
		t.Fatalf("result=%v", result)
	}
}

func TestRunJobAbort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	var result map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/result"):
			json.NewDecoder(r.Body).Decode(&result)
			w.WriteHeader(200); w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/control"):
			json.NewEncoder(w).Encode(map[string]bool{"abort": true}) // 立即要求中止
		}
	}))
	defer srv.Close()

	c := httpclient.New(srv.URL, "tok", 5*time.Second)
	job := Job{ID: 8, ShellCmd: "sleep 5", CurrentCmd: "echo x", VersionRegex: ""}
	start := time.Now()
	RunJob(c, job, 200*time.Millisecond, 10*time.Second)
	if time.Since(start) > 4*time.Second {
		t.Fatalf("abort too slow")
	}
	if result["status"] != "aborted" {
		t.Fatalf("result=%v", result)
	}
}
