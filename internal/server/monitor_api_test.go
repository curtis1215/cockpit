package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/store"
)

func postJSON(t *testing.T, srv *Server, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func TestReportMetricsWithInventoryToken(t *testing.T) {
	srv, st := vtServer(t) // inventory: machine mac, token tok-mac
	rec := postJSON(t, srv, "/api/agent/report-metrics", "tok-mac",
		`{"ts":1000,"cpu":42.5,"mem":61.0,"disk":70.1,"net_up":1.5,"net_down":3.2,"load":0.7,"uptime":3600}`)
	if rec.Code != 200 {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	rows, _ := st.SystemsWithLatest()
	if len(rows) != 1 || rows[0].Label != "mac" || *rows[0].Latest.CPU != 42.5 {
		t.Fatalf("latest: %+v", rows)
	}
	// 同 token 再報 → 同一 system（不重複建）
	postJSON(t, srv, "/api/agent/report-metrics", "tok-mac", `{"ts":1060,"cpu":43.0}`)
	rows2, _ := st.SystemsWithLatest()
	if len(rows2) != 1 {
		t.Fatalf("dup system: %+v", rows2)
	}
	pts, _ := st.QueryMetrics(rows2[0].ID, "1m", 0)
	if len(pts) != 2 {
		t.Fatalf("1m rows: %d", len(pts))
	}
	// 無 token → 401
	if rec := postJSON(t, srv, "/api/agent/report-metrics", "", `{}`); rec.Code != 401 {
		t.Fatalf("noauth: %d", rec.Code)
	}
}

func TestReportServices(t *testing.T) {
	srv, st := vtServer(t)
	rec := postJSON(t, srv, "/api/agent/report-services", "tok-mac",
		`[{"name":"redis","kind":"docker","status":"running","cpu":1.2,"mem":0.8,"port":6379}]`)
	if rec.Code != 200 {
		t.Fatalf("services: %d", rec.Code)
	}
	rows, _ := st.ListServices()
	if len(rows) != 1 || rows[0].Name != "redis" || rows[0].Port != 6379 {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestReportVMsAndReconcile(t *testing.T) {
	srv, st := vtServer(t)
	// guest 先以 system 存在（label=ubuntu-vm，模擬 VM 內 agent 已回報過）
	guestID, _ := st.EnsureSystemForMachine("ubuntu-vm")
	rec := postJSON(t, srv, "/api/agent/report-vms", "tok-mac",
		`[{"name":"ubuntu-vm","uuid":"u-1","vmx_path":"/x.vmx","state":"running","vcpu":4,"ram_mb":4096,"guest_os":"ubuntu-64"},
		  {"name":"ghost-vm","uuid":"u-2","state":"stopped"}]`)
	if rec.Code != 200 {
		t.Fatalf("vms: %d %s", rec.Code, rec.Body.String())
	}
	vms, _ := st.ListVMs()
	if len(vms) != 2 {
		t.Fatalf("vms rows: %+v", vms)
	}
	byUUID := map[string]string{}
	for _, v := range vms {
		byUUID[v.UUID] = v.LinkedSystemID
	}
	if byUUID["u-1"] != guestID || byUUID["u-2"] != "" {
		t.Fatalf("reconcile: %+v", byUUID)
	}
	_ = store.ServiceRow{} // keep import
}
