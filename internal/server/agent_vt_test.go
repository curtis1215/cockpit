package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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
