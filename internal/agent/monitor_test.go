package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/curtis1215/cockpit/internal/dockerstat"
)

func TestMonitorOnce(t *testing.T) {
	var gotMetrics, gotServices int32
	var lastMetrics map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/report-metrics":
			atomic.AddInt32(&gotMetrics, 1)
			json.NewDecoder(r.Body).Decode(&lastMetrics)
			w.Write([]byte(`{"ok":true}`))
		case "/api/agent/report-services":
			atomic.AddInt32(&gotServices, 1)
			w.Write([]byte(`{"applied":0}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.MonitorOnce()
	if atomic.LoadInt32(&gotMetrics) != 1 {
		t.Fatalf("metrics=%d", gotMetrics)
	}
	if lastMetrics["cpu"] == nil || lastMetrics["ts"] == nil {
		t.Fatalf("payload: %v", lastMetrics)
	}
	a.ServicesOnce()
	// docker 可能不存在 → 不送或送空都可；只要不 panic。本測試僅驗證呼叫安全。
	_ = gotServices
	_ = time.Second
}

// TestServicesOnceEmptyPostsToServer verifies that ServicesOnce POSTs an empty
// array when docker ps succeeds but reports zero containers.  This is the
// mechanism that clears stale server-side rows (fix for the 54-stale-services
// prod incident).
func TestServicesOnceEmptyPostsToServer(t *testing.T) {
	var gotServices int32
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/report-services" {
			atomic.AddInt32(&gotServices, 1)
			lastBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"applied":0}`))
		}
	}))
	defer srv.Close()

	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	// Inject a fake docker collector: ps succeeds with empty output.
	a.docker = &dockerstat.Collector{
		Run: func(args ...string) (string, error) {
			return "", nil // success, zero output
		},
	}
	a.ServicesOnce()

	if atomic.LoadInt32(&gotServices) != 1 {
		t.Fatalf("expected 1 POST to report-services, got %d", gotServices)
	}
	// Body must be a JSON array (possibly "[]" or "null" is wrong).
	var decoded []any
	if err := json.Unmarshal(lastBody, &decoded); err != nil {
		t.Fatalf("body is not a JSON array: %q err=%v", lastBody, err)
	}
	if len(decoded) != 0 {
		t.Fatalf("expected empty array in body, got %v", decoded)
	}
}
