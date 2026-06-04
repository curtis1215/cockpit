package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func swServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, _ := store.Open(filepath.Join(t.TempDir(), "sw.db"))
	t.Cleanup(func() { st.Close() })
	st.UpsertInstall("cc", "mac", "2.0.0", "ok", "t")
	inv := inventory.Inventory{
		Machines: map[string]inventory.Machine{
			"mac": {Name: "mac", Host: "1.2.3.4", SSHUser: "curtis", Local: true, AgentToken: "tok-mac"},
		},
		Software: []inventory.Software{{
			Name:         "cc",
			Kind:         "npm",
			LatestSource: "npm:cc",
			Installs: []inventory.Install{{
				Machine:    "mac",
				CurrentCmd: "cc --version",
				Update:     inventory.Update{Type: "command", Cmd: "npm i -g cc@latest"},
			}},
		}},
	}
	return NewWithInventory(st, "secret", inv), st
}

// ── POST /api/software ────────────────────────────────────────────────────────

func TestPostSoftwareNewInstallOnExistingSoftware(t *testing.T) {
	srv, st := swServer(t)
	// Add a second machine so we can add an install on it.
	st.CreateSystemPending("box", "worker")
	// Register it properly so LabelExists returns true.
	_ = st // label comes from inventory in this case; let's add to inventory directly via POST

	// Add new install on the existing "cc" software for a known machine "mac" but with a different
	// software first — let's POST a brand-new software to trigger the "new software" path.
	body := `{"name":"widget","kind":"npm","latest_source":"npm:widget","machine":"mac","current_cmd":"widget --version","update":{"type":"command","cmd":"npm i -g widget"}}`
	rec := doJSON(t, srv, "POST", "/api/software", body)
	if rec.Code != 200 {
		t.Fatalf("POST software: %d %s", rec.Code, rec.Body.String())
	}

	// Verify /api/installs now contains widget::mac after UpsertInstall is done by the vtInstalls side.
	// The in-memory inv was updated, so vtInstalls should return widget.
	r := httptest.NewRequest("GET", "/api/agent/installs", nil)
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, r)
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), "widget") {
		t.Fatalf("vtInstalls after POST: %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestPostSoftware409Duplicate(t *testing.T) {
	srv, _ := swServer(t)
	body := `{"name":"cc","machine":"mac","current_cmd":"cc --version","update":{"type":"command","cmd":"npm i -g cc"}}`
	rec := doJSON(t, srv, "POST", "/api/software", body)
	if rec.Code != 409 {
		t.Fatalf("want 409, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPostSoftwareUnknownMachine(t *testing.T) {
	srv, _ := swServer(t)
	body := `{"name":"cc","machine":"ghost","current_cmd":"cc --version","update":{"type":"command","cmd":"x"}}`
	rec := doJSON(t, srv, "POST", "/api/software", body)
	if rec.Code != 400 {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPostSoftwareMissingLatestSource(t *testing.T) {
	srv, _ := swServer(t)
	// "newpkg" doesn't exist yet, so latest_source is required.
	body := `{"name":"newpkg","machine":"mac","current_cmd":"newpkg --version","update":{"type":"command","cmd":"x"}}`
	rec := doJSON(t, srv, "POST", "/api/software", body)
	if rec.Code != 400 {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// ── PATCH /api/software/{name}/{machine} ─────────────────────────────────────

func TestPatchSoftwareInstall(t *testing.T) {
	srv, _ := swServer(t)

	body := `{"current_cmd":"cc2 --version","version_regex":"v(\\d+)"}`
	rec := doJSON(t, srv, "PATCH", "/api/software/cc/mac", body)
	if rec.Code != 200 {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}

	// Confirm the change is live via vtInstalls.
	r := httptest.NewRequest("GET", "/api/agent/installs", nil)
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, r)
	if !strings.Contains(rec2.Body.String(), "cc2 --version") {
		t.Fatalf("vtInstalls after PATCH: %s", rec2.Body.String())
	}
}

func TestPatchSoftware404(t *testing.T) {
	srv, _ := swServer(t)
	body := `{"current_cmd":"x"}`
	rec := doJSON(t, srv, "PATCH", "/api/software/ghost/mac", body)
	if rec.Code != 404 {
		t.Fatalf("want 404, got %d %s", rec.Code, rec.Body.String())
	}
}

// ── DELETE /api/software/{name}/{machine} ────────────────────────────────────

func TestDeleteSoftwareInstall(t *testing.T) {
	srv, st := swServer(t)

	rec := doJSON(t, srv, "DELETE", "/api/software/cc/mac", "")
	if rec.Code != 204 {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}

	// Verify gone from in-memory inventory.
	r := httptest.NewRequest("GET", "/api/agent/installs", nil)
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, r)
	if strings.Contains(rec2.Body.String(), "cc") {
		t.Fatalf("cc still in installs after delete: %s", rec2.Body.String())
	}

	// Verify deleted from store.
	_, err := st.GetInstall("cc", "mac")
	if err == nil {
		t.Fatal("install should be deleted from store, got nil error")
	}
}

func TestDeleteSoftware404(t *testing.T) {
	srv, _ := swServer(t)
	rec := doJSON(t, srv, "DELETE", "/api/software/ghost/mac", "")
	if rec.Code != 404 {
		t.Fatalf("want 404, got %d %s", rec.Code, rec.Body.String())
	}
}

// ── Persist: SetInventoryPath + writeback ─────────────────────────────────────

func TestInventoryWriteback(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "sw.db"))
	t.Cleanup(func() { st.Close() })

	// Write initial YAML file.
	dir := t.TempDir()
	invPath := filepath.Join(dir, "inventory.yaml")
	initYAML := `
machines:
  mac:
    host: 1.2.3.4
    ssh_user: curtis
    local: true
    agent_token: tok-mac
software:
  - name: cc
    kind: npm
    latest_source: "npm:cc"
    installs:
      - machine: mac
        current_cmd: "cc --version"
        update:
          type: command
          cmd: "npm i -g cc@latest"
`
	if err := os.WriteFile(invPath, []byte(initYAML), 0644); err != nil {
		t.Fatal(err)
	}

	inv, err := inventory.Load(invPath)
	if err != nil {
		t.Fatal(err)
	}

	srv := NewWithInventory(st, "secret", inv)
	srv.SetInventoryPath(invPath)

	// POST new software (adds to existing machine mac, new software "widget").
	body := `{"name":"widget","kind":"npm","latest_source":"npm:widget","machine":"mac","current_cmd":"widget --version","update":{"type":"command","cmd":"npm i -g widget"}}`
	rec := doJSON(t, srv, "POST", "/api/software", body)
	if rec.Code != 200 {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}

	// Verify file was written and is loadable.
	b, err := os.ReadFile(invPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	loaded, err := inventory.LoadText(b)
	if err != nil {
		t.Fatalf("reload YAML: %v", err)
	}
	found := false
	for _, sw := range loaded.Software {
		if sw.Name == "widget" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("widget not found in written YAML: %s", b)
	}
}

// ── POST then poll via vtPoll ─────────────────────────────────────────────────

func TestPostSoftwareThenVtPollCanClaimJob(t *testing.T) {
	srv, st := swServer(t)
	// Add version so StartJob can find it later if triggered.
	st.AddVersion("widget", "1.0.0", "2026-01-01", "raw", "zh")

	body := `{"name":"widget","kind":"npm","latest_source":"npm:widget","machine":"mac","current_cmd":"widget --version","update":{"type":"command","cmd":"npm i -g widget"}}`
	rec := doJSON(t, srv, "POST", "/api/software", body)
	if rec.Code != 200 {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}

	// Manually create a queued job for widget::mac to simulate dispatch.
	jid, err := st.CreateJobUnique("widget", "mac", "command", "")
	if err != nil {
		t.Fatal(err)
	}
	if jid == 0 {
		t.Fatal("no job created")
	}

	// vtPoll should return the job for mac.
	pr := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	pr.Header.Set("Authorization", "Bearer tok-mac")
	prec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prec, pr)
	if prec.Code != 200 {
		t.Fatalf("poll: %d %s", prec.Code, prec.Body.String())
	}
	if !strings.Contains(prec.Body.String(), `"software":"widget"`) {
		t.Fatalf("expected widget job from poll, got: %s", prec.Body.String())
	}
}

// ── Second install / addInstallToExistingSoftware ────────────────────────────

func TestPostSoftwareSecondInstall(t *testing.T) {
	srv, st := swServer(t)
	// Create a system labeled "box" so it's known.
	_, _, err := st.CreateSystemPending("box", "worker")
	if err != nil {
		t.Fatal(err)
	}

	// Add install of existing "cc" software to "box".
	body := `{"name":"cc","machine":"box","current_cmd":"cc2 --version","update":{"type":"command","cmd":"npm i -g cc"}}`
	rec := doJSON(t, srv, "POST", "/api/software", body)
	if rec.Code != 200 {
		t.Fatalf("second install: %d %s", rec.Code, rec.Body.String())
	}

	// Inventory should now have 2 installs for cc.
	inv := srv.getInv()
	count := 0
	for _, sw := range inv.Software {
		if sw.Name == "cc" {
			count = len(sw.Installs)
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 installs for cc, got %d", count)
	}
}
