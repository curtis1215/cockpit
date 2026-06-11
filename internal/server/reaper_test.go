package server

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/curtis1215/cockpit/internal/store"
)

// claimJob 模擬 agent 透過 poll 領取 job，回傳 job id。
func claimJob(t *testing.T, srv *Server, st *store.Store) int64 {
	t.Helper()
	jid, err := st.CreateJobUnique("cc", "mac", "command", "")
	if err != nil || jid == 0 {
		t.Fatalf("create job: id=%d err=%v", jid, err)
	}
	r := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("poll claim: %d %s", rec.Code, rec.Body.String())
	}
	job, _ := st.GetJob(jid)
	if job.Status != "running" {
		t.Fatalf("job not claimed: %q", job.Status)
	}
	return jid
}

// 失聯的 running job（claim 後超過 grace 無任何 agent 活動）必須被收割成
// failed，否則 CreateJobUnique 會永遠擋住同 software+machine 的重新觸發。
func TestReaperFailsOrphanedRunningJob(t *testing.T) {
	srv, st := vtServer(t)
	jid := claimJob(t, srv, st)

	n := srv.reapStaleJobs(time.Now().Add(4*time.Minute), 3*time.Minute)
	if n != 1 {
		t.Fatalf("reaped %d jobs, want 1", n)
	}
	job, _ := st.GetJob(jid)
	if job.Status != "failed" {
		t.Fatalf("status=%q want failed", job.Status)
	}
	if !strings.Contains(job.Log, "失聯") {
		t.Fatalf("log missing reaper note: %q", job.Log)
	}
	// 卡死解除後必須能重新觸發
	jid2, err := st.CreateJobUnique("cc", "mac", "command", "")
	if err != nil || jid2 == 0 {
		t.Fatalf("re-trigger blocked after reap: id=%d err=%v", jid2, err)
	}
}

// grace 期間內的 running job 不可被收割。
func TestReaperKeepsRecentlyClaimedJob(t *testing.T) {
	srv, st := vtServer(t)
	jid := claimJob(t, srv, st)

	if n := srv.reapStaleJobs(time.Now().Add(2*time.Minute), 3*time.Minute); n != 0 {
		t.Fatalf("reaped %d jobs, want 0", n)
	}
	job, _ := st.GetJob(jid)
	if job.Status != "running" {
		t.Fatalf("status=%q want running", job.Status)
	}
}

// agent 的 control 輪詢必須刷新存活時間：執行中（持續 control poll）的長任務
// 不可被收割。
func TestReaperControlPollRefreshesLiveness(t *testing.T) {
	srv, st := vtServer(t)
	jid := claimJob(t, srv, st)
	// 把 claim 時的存活紀錄倒退 10 分鐘，模擬「很久以前 claim」
	srv.jobSeen.Store(jid, time.Now().Add(-10*time.Minute))

	// agent 執行中會每 2 秒打一次 control
	r := httptest.NewRequest("GET", "/api/agent/jobs/"+strconv.FormatInt(jid, 10)+"/control", nil)
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("control: %d", rec.Code)
	}

	if n := srv.reapStaleJobs(time.Now().Add(time.Minute), 3*time.Minute); n != 0 {
		t.Fatalf("reaped %d jobs, want 0 (control poll should refresh liveness)", n)
	}
	job, _ := st.GetJob(jid)
	if job.Status != "running" {
		t.Fatalf("status=%q want running", job.Status)
	}
}
