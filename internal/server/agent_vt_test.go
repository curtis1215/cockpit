package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/store"
)

func TestAgentVTInstallsAndPoll(t *testing.T) {
	srv, st := vtServer(t)
	r := httptest.NewRequest("GET", "/api/agent/installs", nil)
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"current_cmd":"cc --version"`) {
		t.Fatalf("installs: %d %s", rec.Code, rec.Body.String())
	}
	bad := httptest.NewRecorder()
	srv.Handler().ServeHTTP(bad, httptest.NewRequest("GET", "/api/agent/installs", nil))
	if bad.Code != 401 {
		t.Fatalf("noauth want 401 got %d", bad.Code)
	}
	st.CreateJobUnique("cc", "mac", "command", "")
	pr := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	pr.Header.Set("Authorization", "Bearer tok-mac")
	prec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prec, pr)
	if !strings.Contains(prec.Body.String(), `"type":"job"`) || !strings.Contains(prec.Body.String(), `"shell_cmd":"x"`) {
		t.Fatalf("poll: %s", prec.Body.String())
	}
	// nothing queued now → 204
	prec2 := httptest.NewRecorder()
	pr2 := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	pr2.Header.Set("Authorization", "Bearer tok-mac")
	srv.Handler().ServeHTTP(prec2, pr2)
	if prec2.Code != 204 {
		t.Fatalf("empty poll want 204 got %d", prec2.Code)
	}
}

// TestPollDoesNotClaimForDisconnectedClient: 若 agent 已斷線（request context
// 已取消），vtPoll 不可把 queued job claim 成 running——否則 job 會變成永遠
// 卡在 running 的孤兒（agent 根本沒收到），且 CreateJobUnique 會擋住重新觸發。
func TestPollDoesNotClaimForDisconnectedClient(t *testing.T) {
	srv, st := vtServer(t)
	jid, err := st.CreateJobUnique("cc", "mac", "command", "")
	if err != nil || jid == 0 {
		t.Fatalf("create job: id=%d err=%v", jid, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client 已斷線
	pr := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil).WithContext(ctx)
	pr.Header.Set("Authorization", "Bearer tok-mac")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, pr)
	job, _ := st.GetJob(jid)
	if job.Status != "queued" {
		t.Fatalf("job claimed by dead connection: status=%q want queued (resp=%d)", job.Status, rec.Code)
	}
}

func TestAgentVTCheckSignal(t *testing.T) {
	srv, st := vtServer(t)
	st.SetCheckRequested("mac")
	pr := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	pr.Header.Set("Authorization", "Bearer tok-mac")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, pr)
	if !strings.Contains(rec.Body.String(), `"type":"check"`) {
		t.Fatalf("check signal: %s", rec.Body.String())
	}
}

// TestUpgradeAgentEndpoint verifies POST /api/systems/{id}/upgrade-agent sets the
// upgrade flag, and that vtPoll returns {"type":"upgrade"} before a check signal.
func TestUpgradeAgentEndpoint(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	t.Cleanup(func() { st.Close() })
	st.AddVersion("cc", "2.1.101", "2026-04-10", "raw", "zh")
	st.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	srv := NewWithInventory(st, "s3cret", vtInv())

	// Enroll mac so it has a system ID we can look up.
	sysID, _, _ := st.RegisterSystem("mac", "linux", "amd64")

	// POST /api/systems/{id}/upgrade-agent → 200 {ok:true}
	req := httptest.NewRequest("POST", "/api/systems/"+sysID+"/upgrade-agent", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("upgrade-agent endpoint: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown system ID → 404
	rec404 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec404, httptest.NewRequest("POST", "/api/systems/sys_unknown/upgrade-agent", nil))
	if rec404.Code != 404 {
		t.Fatalf("unknown id want 404 got %d", rec404.Code)
	}

	// vtPoll for "mac" should return {"type":"upgrade"} — upgrade has priority over check.
	st.SetCheckRequested("mac")
	pr := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	pr.Header.Set("Authorization", "Bearer tok-mac")
	prec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prec, pr)
	if !strings.Contains(prec.Body.String(), `"type":"upgrade"`) {
		t.Fatalf("poll should return upgrade, got: %s", prec.Body.String())
	}

	// After upgrade is consumed, the check signal is next.
	pr2 := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	pr2.Header.Set("Authorization", "Bearer tok-mac")
	prec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prec2, pr2)
	if !strings.Contains(prec2.Body.String(), `"type":"check"`) {
		t.Fatalf("second poll should return check, got: %s", prec2.Body.String())
	}
}

func TestAgentVTReportAndResult(t *testing.T) {
	srv, st := vtServer(t)
	rr := httptest.NewRequest("POST", "/api/agent/report-versions", strings.NewReader(`[{"software":"cc","current_version":"2.1.98"}]`))
	rr.Header.Set("Authorization", "Bearer tok-mac")
	rrec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrec, rr)
	if rrec.Code != 200 {
		t.Fatalf("report: %d", rrec.Code)
	}
	jid, _ := st.CreateJobUnique("cc", "mac", "command", "")
	st.ClaimOldestQueued("mac")
	idStr := strconv.FormatInt(jid, 10)
	lr := httptest.NewRequest("POST", "/api/agent/jobs/"+idStr+"/log", strings.NewReader(`{"lines":["added 1 package"]}`))
	lr.Header.Set("Authorization", "Bearer tok-mac")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), lr)
	cr := httptest.NewRequest("GET", "/api/agent/jobs/"+idStr+"/control", nil)
	cr.Header.Set("Authorization", "Bearer tok-mac")
	crec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(crec, cr)
	if !strings.Contains(crec.Body.String(), `"abort":false`) {
		t.Fatalf("control: %s", crec.Body.String())
	}
	res := httptest.NewRequest("POST", "/api/agent/jobs/"+idStr+"/result", strings.NewReader(`{"status":"success","exit_code":0,"new_version":"2.1.101"}`))
	res.Header.Set("Authorization", "Bearer tok-mac")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), res)
	job, _ := st.GetJob(jid)
	if job.Status != "success" || !strings.Contains(job.Log, "added 1 package") {
		t.Fatalf("job: %+v", job)
	}
}
