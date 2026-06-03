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

func vtInv() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{"mac": {Name: "mac", AgentToken: "tok-mac"}},
		Software: []inventory.Software{{Name: "cc", Kind: "npm", LatestSource: "npm:cc",
			Installs: []inventory.Install{{Machine: "mac", CurrentCmd: "cc --version", Update: inventory.Update{Type: "command", Cmd: "x"}}}}},
	}
}
func vtServer(t *testing.T) (*Server, *store.Store) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	t.Cleanup(func() { st.Close() })
	st.AddVersion("cc", "2.1.101", "2026-04-10", "raw", "中文")
	st.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	return NewWithInventory(st, "s3cret", vtInv()), st
}

func TestInstallsEnriched(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/installs", nil))
	b := rec.Body.String()
	if !strings.Contains(b, `"id":"cc::mac"`) || !strings.Contains(b, `"behind_count":3`) ||
		!strings.Contains(b, `"update_kind":"command"`) || !strings.Contains(b, `"status":"behind"`) {
		t.Fatalf("installs: %s", b)
	}
}
func TestChangelog(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/changelog/cc/2.1.101", nil))
	if !strings.Contains(rec.Body.String(), `"changelog_zh":"中文"`) {
		t.Fatalf("changelog: %s", rec.Body.String())
	}
}
func TestTriggerAndConflict(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/installs/cc/mac/update", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"job_id"`) {
		t.Fatalf("trigger: %d %s", rec.Code, rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("POST", "/api/installs/cc/mac/update", nil))
	if rec2.Code != 409 {
		t.Fatalf("conflict want 409 got %d", rec2.Code)
	}
}
func TestJobGetAndAbort(t *testing.T) {
	srv, st := vtServer(t)
	jid, _ := st.CreateJobUnique("cc", "mac", "command", "")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+strconv.FormatInt(jid, 10), nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"queued"`) {
		t.Fatalf("get job: %d %s", rec.Code, rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("POST", "/api/jobs/"+strconv.FormatInt(jid, 10)+"/abort", nil))
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), `"aborted"`) {
		t.Fatalf("abort: %d %s", rec2.Code, rec2.Body.String())
	}
}
func TestSSEEndsOnDone(t *testing.T) {
	srv, st := vtServer(t)
	jid, _ := st.CreateJobUnique("cc", "mac", "command", "")
	st.AppendJobLog(jid, "line A")
	st.FinishJob(jid, "success", 0, "2.1.101")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+strconv.FormatInt(jid, 10)+"/log/stream", nil))
	b := rec.Body.String()
	if !strings.Contains(b, "line A") || !strings.Contains(b, "event: done") {
		t.Fatalf("sse: %s", b)
	}
}
