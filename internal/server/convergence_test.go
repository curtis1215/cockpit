package server

import (
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

// twoMachineInv builds an inventory with two machines and software only on mac.
func twoMachineInv() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{
			"mac": {Name: "mac", AgentToken: "tok-mac"},
			"box": {Name: "box", AgentToken: "tok-box"},
		},
		Software: []inventory.Software{{
			Name: "cc", Kind: "npm", LatestSource: "npm:cc",
			Installs: []inventory.Install{{
				Machine: "mac", CurrentCmd: "cc --version",
				Update: inventory.Update{Type: "command", Cmd: "x"},
			}},
		}},
	}
}

// convergenceServer creates a server with a two-machine inventory.
func convergenceServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.AddVersion("cc", "2.1.101", "2026-04-10", "raw", "zh")
	st.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	return NewWithInventory(st, "s3cret", twoMachineInv()), st
}

// TestHeartbeatWithInventoryToken: POST /api/agent/heartbeat with an inventory
// machine token (tok-mac) should return 204 and update the system status.
func TestHeartbeatWithInventoryToken(t *testing.T) {
	srv, st := convergenceServer(t)

	r := httptest.NewRequest("POST", "/api/agent/heartbeat",
		strings.NewReader(`{"agent_version":"9.9.9"}`))
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)

	if rec.Code != 204 {
		t.Fatalf("heartbeat with inventory token: want 204, got %d body=%s", rec.Code, rec.Body.String())
	}

	// The system should appear in ListSystems with label "mac" and version "9.9.9"
	systems, err := st.ListSystems()
	if err != nil {
		t.Fatalf("ListSystems: %v", err)
	}
	var found bool
	for _, sys := range systems {
		if sys.Label == "mac" && sys.AgentVersion == "9.9.9" && sys.Status == "online" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("system mac with version 9.9.9 online not found in: %+v", systems)
	}
}

// TestVTWithSystemsToken: a system registered via RegisterSystem should be able
// to use its agent token on GET /api/agent/installs and get software for its label.
func TestVTWithSystemsToken(t *testing.T) {
	_, st := convergenceServer(t)
	// Register a system with label "mac" (matches inventory machine name)
	_, tok, err := st.RegisterSystem("mac", "linux", "arm64")
	if err != nil {
		t.Fatalf("RegisterSystem: %v", err)
	}

	// Build a server with the two-machine inventory for this test
	srv := NewWithInventory(st, "s3cret", twoMachineInv())

	r := httptest.NewRequest("GET", "/api/agent/installs", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)

	if rec.Code != 200 {
		t.Fatalf("installs with systems token: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cc --version") {
		t.Fatalf("installs body missing 'cc --version': %s", rec.Body.String())
	}
}

// TestCrossMachineJob403: submitting a log for a job belonging to machine "mac"
// using the token for machine "box" should return 403.
func TestCrossMachineJob403(t *testing.T) {
	srv, st := convergenceServer(t)

	// Create a job for machine "mac"
	jid, err := st.CreateJobUnique("cc", "mac", "command", "")
	if err != nil || jid == 0 {
		t.Fatalf("CreateJobUnique: %v %d", err, jid)
	}
	// Claim it so it is running
	st.ClaimOldestQueued("mac")

	idStr := strconv.FormatInt(jid, 10)

	// tok-box tries to post log for mac's job
	r := httptest.NewRequest("POST", "/api/agent/jobs/"+idStr+"/log",
		strings.NewReader(`{"lines":["hacked"]}`))
	r.Header.Set("Authorization", "Bearer tok-box")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)

	if rec.Code != 403 {
		t.Fatalf("cross-machine log: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "another machine") {
		t.Fatalf("wrong error body: %s", rec.Body.String())
	}
}
