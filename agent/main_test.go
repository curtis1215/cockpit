package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cockpit-agent/internal/httpclient"
)

func TestPollOnceHandlesCheck(t *testing.T) {
	var reported int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/poll":
			json.NewEncoder(w).Encode(map[string]any{"type": "check"})
		case "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{
				{"software": "cc", "current_cmd": "echo cc 2.1.98", "version_regex": nil},
			})
		case "/api/agent/report-versions":
			atomic.AddInt32(&reported, 1)
			json.NewEncoder(w).Encode(map[string]int{"applied": 1})
		}
	}))
	defer srv.Close()

	c := httpclient.New(srv.URL, "tok", 5*time.Second)
	// pollOnce 應在收到 check 時跑 installs + report-versions
	pollOnce(c, 5*time.Second)
	if atomic.LoadInt32(&reported) != 1 {
		t.Fatalf("expected one report, got %d", reported)
	}
}
