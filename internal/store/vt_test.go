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
