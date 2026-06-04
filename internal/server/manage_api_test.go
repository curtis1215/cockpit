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
