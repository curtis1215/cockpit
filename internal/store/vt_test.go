package store

import (
	"path/filepath"
	"testing"
)

func vtOpen(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInstallsAndVersions(t *testing.T) {
	s := vtOpen(t)
	s.AddVersion("cc", "2.1.101", "", "raw", "中文")
	if v, _ := s.GetVersion("cc", "2.1.101"); v.ChangelogZh != "中文" {
		t.Fatalf("version: %+v", v)
	}
	s.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	s.UpsertInstall("cc", "mac", "2.1.101", "up_to_date", "t2") // upsert
	rows, _ := s.ListInstalls()
	if len(rows) != 1 || rows[0].CurrentVersion != "2.1.101" || rows[0].Status != "up_to_date" {
		t.Fatalf("installs: %+v", rows)
	}
	if lv := s.LatestVersionMap()["cc"]; lv != "2.1.101" {
		t.Fatalf("latest map: %v", lv)
	}
}

func TestJobQueue(t *testing.T) {
	s := vtOpen(t)
	jid, err := s.CreateJobUnique("cc", "mac", "command", "")
	if err != nil || jid == 0 {
		t.Fatalf("create: %v %d", err, jid)
	}
	if dup, _ := s.CreateJobUnique("cc", "mac", "command", ""); dup != 0 {
		t.Fatal("want 0 for active duplicate")
	}
	claimed, _ := s.ClaimOldestQueued("mac")
	if claimed == nil || claimed.ID != jid {
		t.Fatalf("claim: %+v", claimed)
	}
	s.SetJobDispatch(jid, "npm i", "", "cc --version", "")
	s.AppendJobLog(jid, "line1")
	s.FinishJob(jid, "success", 0, "2.1.101")
	job, _ := s.GetJob(jid)
	if job.Status != "success" || job.NewVersion != "2.1.101" || job.Cmd != "npm i" || job.Log != "line1\n" {
		t.Fatalf("job: %+v", job)
	}
}

func TestClaimAtomicGuard(t *testing.T) {
	s := vtOpen(t)

	// Create a job and claim it
	jid, err := s.CreateJobUnique("cc", "mac", "command", "")
	if err != nil || jid == 0 {
		t.Fatalf("create job: %v %d", err, jid)
	}
	claimed, err := s.ClaimOldestQueued("mac")
	if err != nil || claimed == nil || claimed.ID != jid {
		t.Fatalf("first claim should succeed: %v %+v", err, claimed)
	}

	// Second claim with no queued jobs left → nil, nil
	claimed2, err2 := s.ClaimOldestQueued("mac")
	if err2 != nil || claimed2 != nil {
		t.Fatalf("second claim should return nil,nil got: err=%v claimed=%+v", err2, claimed2)
	}

	// Also verify the atomic guard: create another job, simulate race by
	// manually setting it to running before the UPDATE guard fires.
	jid2, _ := s.CreateJobUnique("cc", "mac2", "command", "")
	// Directly advance status to running to simulate another caller winning the race
	s.db.Exec(`UPDATE jobs SET status='running' WHERE id=?`, jid2)
	// Now ClaimOldestQueued sees no queued jobs for mac2
	claimed3, err3 := s.ClaimOldestQueued("mac2")
	if err3 != nil || claimed3 != nil {
		t.Fatalf("guard should block already-running job: err=%v claimed=%+v", err3, claimed3)
	}
}

func TestHeartbeatByID(t *testing.T) {
	s := vtOpen(t)
	id, _, err := s.RegisterSystem("box", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatByID(id, "1.2.3"); err != nil {
		t.Fatalf("HeartbeatByID: %v", err)
	}
	sys, err := s.SystemByID(id)
	if err != nil {
		t.Fatalf("SystemByID: %v", err)
	}
	if sys.AgentVersion != "1.2.3" || sys.Status != "online" || sys.AgentStatus != "ok" {
		t.Fatalf("after heartbeat: %+v", sys)
	}
	// non-existent id → ErrNotFound
	if err := s.HeartbeatByID("sys_nope", "1.0"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAbortAndCheckFlags(t *testing.T) {
	s := vtOpen(t)
	jid, _ := s.CreateJobUnique("cc", "mac", "command", "")
	s.RequestAbort(jid)
	if !s.AbortRequested(jid) {
		t.Fatal("abort flag")
	}
	s.SetCheckRequested("mac")
	if !s.TakeCheckRequested("mac") || s.TakeCheckRequested("mac") {
		t.Fatal("check flag once")
	}
}
