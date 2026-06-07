package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/store"
)

// ── helper: send JSON request ────────────────────────────────────────────────

func doJSON(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// ── Test 1: POST /api/systems creates pending system with enroll_token ───────

func TestCreateSystemPending(t *testing.T) {
	srv, _ := vtServer(t)

	// Create a new pending system
	rec := doJSON(t, srv, "POST", "/api/systems", `{"label":"newbox","role":"worker"}`)
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp["id"] == "" {
		t.Fatalf("missing id: %v", resp)
	}
	if !strings.HasPrefix(resp["enroll_token"], "ck_enroll_") {
		t.Fatalf("bad enroll_token: %q", resp["enroll_token"])
	}
	if resp["label"] != "newbox" {
		t.Fatalf("label: %v", resp)
	}

	// GET /api/systems should show the pending system
	rec2 := doJSON(t, srv, "GET", "/api/systems", "")
	b := rec2.Body.String()
	if !strings.Contains(b, `"status":"pending"`) {
		t.Fatalf("pending not in list: %s", b)
	}

	// Duplicate label → 409
	rec3 := doJSON(t, srv, "POST", "/api/systems", `{"label":"newbox"}`)
	if rec3.Code != 409 {
		t.Fatalf("dup want 409 got %d: %s", rec3.Code, rec3.Body.String())
	}
}

// ── Test 2: Enroll with token → online; second use → 401 ────────────────────

func TestEnrollWithToken(t *testing.T) {
	srv, st := vtServer(t)

	// Create pending system
	id, enrollToken, err := st.CreateSystemPending("mybox", "")
	if err != nil {
		t.Fatal(err)
	}

	// First enroll → should succeed
	enrollBody := `{"enroll_token":"` + enrollToken + `","os":"linux","arch":"arm64"}`
	rec := postJSON(t, srv, "/api/agent/enroll", "", enrollBody)
	if rec.Code != 200 {
		t.Fatalf("enroll: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp["system_id"] != id {
		t.Fatalf("system_id: want %q got %q", id, resp["system_id"])
	}
	if resp["agent_token"] == "" {
		t.Fatalf("no agent_token: %v", resp)
	}

	// Verify system is now online with os/arch filled
	sys, err := st.SystemByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if sys.Status != "online" {
		t.Fatalf("status: %q", sys.Status)
	}
	if sys.OS != "linux" || sys.Arch != "arm64" {
		t.Fatalf("os/arch: %q/%q", sys.OS, sys.Arch)
	}
	if sys.EnrollToken != "" {
		t.Fatalf("enroll_token should be cleared, got %q", sys.EnrollToken)
	}

	// Second enroll with same token → 401 (one-time use)
	rec2 := postJSON(t, srv, "/api/agent/enroll", "", enrollBody)
	if rec2.Code != 401 {
		t.Fatalf("second enroll want 401 got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// ── Test 3: RegenEnrollToken invalidates old token ───────────────────────────

func TestRegenEnrollToken(t *testing.T) {
	srv, st := vtServer(t)

	// Create pending system
	id, oldToken, err := st.CreateSystemPending("regenbox", "")
	if err != nil {
		t.Fatal(err)
	}

	// Regen token
	rec := doJSON(t, srv, "POST", "/api/systems/"+id+"/enroll-token", "")
	if rec.Code != 200 {
		t.Fatalf("regen: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	newToken := resp["enroll_token"]
	if newToken == "" || newToken == oldToken {
		t.Fatalf("new token should differ: old=%q new=%q", oldToken, newToken)
	}

	// Old token should now be invalid
	enrollBody := `{"enroll_token":"` + oldToken + `","os":"linux","arch":"amd64"}`
	rec2 := postJSON(t, srv, "/api/agent/enroll", "", enrollBody)
	if rec2.Code != 401 {
		t.Fatalf("old token want 401 got %d: %s", rec2.Code, rec2.Body.String())
	}

	// New token should work
	enrollBody2 := `{"enroll_token":"` + newToken + `","os":"linux","arch":"amd64"}`
	rec3 := postJSON(t, srv, "/api/agent/enroll", "", enrollBody2)
	if rec3.Code != 200 {
		t.Fatalf("new token: %d %s", rec3.Code, rec3.Body.String())
	}
}

// ── Test 4: PATCH label/role; install-conflict → 409; DELETE cascade ─────────

func TestPatchAndDelete(t *testing.T) {
	srv, st := vtServer(t)

	// Create a new pending system
	id, _, err := st.CreateSystemPending("mac2", "")
	if err != nil {
		t.Fatal(err)
	}

	// PATCH role only — should work
	rec := doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"role":"worker"}`)
	if rec.Code != 200 {
		t.Fatalf("patch role: %d %s", rec.Code, rec.Body.String())
	}
	sys, err := st.SystemByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if sys.Role != "worker" {
		t.Fatalf("role not updated: %q", sys.Role)
	}

	// Create a system whose label matches an inventory machine ("mac" has installs in vtServer)
	macID, err := st.EnsureSystemForMachine("mac")
	if err != nil {
		t.Fatal(err)
	}

	// PATCH label of "mac" system → 409 because inventory installs reference "mac"
	rec2 := doJSON(t, srv, "PATCH", "/api/systems/"+macID, `{"label":"mac-renamed"}`)
	if rec2.Code != 409 {
		t.Fatalf("rename mac want 409 got %d: %s", rec2.Code, rec2.Body.String())
	}

	// ── DELETE cascade test ──
	// Add metrics and services to the system to verify cascade
	id2, _, err := st.CreateSystemPending("cascade-test", "")
	if err != nil {
		t.Fatal(err)
	}
	// Insert metrics_latest
	cpu := 50.0
	st.UpsertMetricsLatest(id2, store.MetricRow{TS: 1000, CPU: &cpu})
	// Insert services
	st.ReplaceServices(id2, []store.ServiceRow{{Name: "nginx", Kind: "service", Status: "running"}})

	// DELETE the system
	rec3 := doJSON(t, srv, "DELETE", "/api/systems/"+id2, "")
	if rec3.Code != 204 {
		t.Fatalf("delete: %d %s", rec3.Code, rec3.Body.String())
	}

	// Verify cascade: system gone
	_, err = st.SystemByID(id2)
	if err == nil {
		t.Fatal("system should be deleted")
	}

	// Verify metrics_latest gone
	rows, _ := st.SystemsWithLatest()
	for _, r := range rows {
		if r.ID == id2 {
			t.Fatal("system still in SystemsWithLatest after delete")
		}
	}

	// Verify services gone
	svcs, _ := st.ListServices()
	for _, svc := range svcs {
		if svc.SystemID == id2 {
			t.Fatal("services should be deleted")
		}
	}
}

// ── group / effective_group ──────────────────────────────────────────────────

func TestSystemsGroupAndEffectiveGroup(t *testing.T) {
	srv, st := vtServer(t)

	// host 設群組「工作」
	hostID, _, err := st.CreateSystemPending("ghost1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSystemGroup(hostID, "工作"); err != nil {
		t.Fatal(err)
	}
	// guest1：VM、未覆寫 → 繼承 host
	guest1, _, err := st.CreateSystemPending("gguest1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkVM(hostID, "uuid-g1", guest1); err != nil {
		t.Fatal(err)
	}
	// guest2：VM、覆寫成「個人」
	guest2, _, err := st.CreateSystemPending("gguest2", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkVM(hostID, "uuid-g2", guest2); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSystemGroup(guest2, "個人"); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, srv, "GET", "/api/systems", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	find := func(id string) map[string]any {
		for _, m := range list {
			if m["id"] == id {
				return m
			}
		}
		t.Fatalf("system %s not in list", id)
		return nil
	}
	h := find(hostID)
	if h["group"] != "工作" || h["effective_group"] != "工作" {
		t.Fatalf("host: group=%v eff=%v", h["group"], h["effective_group"])
	}
	g1 := find(guest1)
	if g1["group"] != "" || g1["effective_group"] != "工作" {
		t.Fatalf("guest1 should inherit: group=%v eff=%v", g1["group"], g1["effective_group"])
	}
	g2 := find(guest2)
	if g2["group"] != "個人" || g2["effective_group"] != "個人" {
		t.Fatalf("guest2 should override: group=%v eff=%v", g2["group"], g2["effective_group"])
	}
}

func TestEffectiveGroupHostMissing(t *testing.T) {
	srv, st := vtServer(t)
	// VM 的 host 不存在（懸空 host_id）→ effective_group 視為未分組
	guest, _, err := st.CreateSystemPending("orphanvm", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkVM("sys_ghosthost", "uuid-x", guest); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, srv, "GET", "/api/systems", "")
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m["id"] == guest {
			if m["effective_group"] != "" {
				t.Fatalf("orphan vm eff = %v, want empty", m["effective_group"])
			}
			return
		}
	}
	t.Fatal("guest not found")
}

func TestPatchSystemGroup(t *testing.T) {
	srv, st := vtServer(t)
	id, _, err := st.CreateSystemPending("pbox", "")
	if err != nil {
		t.Fatal(err)
	}

	// 設定群組（含前後空白 → 應 trim）
	rec := doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"group":"  工作  "}`)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["group"] != "工作" {
		t.Fatalf("group = %v, want 工作 (trimmed)", resp["group"])
	}

	// 只動 group 不應影響 label / role
	if resp["label"] != "pbox" {
		t.Fatalf("label changed: %v", resp["label"])
	}

	// 清空群組
	rec = doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"group":""}`)
	if rec.Code != 200 {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["group"] != "" {
		t.Fatalf("group = %v, want empty", resp["group"])
	}

	// 超過 64 字元 → 400
	long := strings.Repeat("超", 65)
	rec = doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"group":"`+long+`"}`)
	if rec.Code != 400 {
		t.Fatalf("too long: %d %s", rec.Code, rec.Body.String())
	}

	// 不帶 group 欄位 → 不變
	if err := st.SetSystemGroup(id, "保留"); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"role":"web"}`)
	if rec.Code != 200 {
		t.Fatalf("role-only patch: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["group"] != "保留" {
		t.Fatalf("group = %v, want 保留 (untouched)", resp["group"])
	}
}
