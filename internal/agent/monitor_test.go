package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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
