package jobs

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

func invCmd() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{"mac": {Name: "mac", Host: "x", SSHUser: "c", Local: true}},
		Software: []inventory.Software{{Name: "cc", Kind: "npm", LatestSource: "npm:cc",
			Installs: []inventory.Install{{Machine: "mac", CurrentCmd: "cc --version",
				Update: inventory.Update{Type: "command", Cmd: "npm i -g cc@latest"}}}}},
	}
}
func seed(t *testing.T) *store.Store {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	t.Cleanup(func() { s.Close() })
	s.AddVersion("cc", "2.1.101", "", "raw", "中文")
	s.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	return s
}

func TestBuildCommand(t *testing.T) {
	iv := invCmd()
	cmd, m, err := BuildUpdate(iv, iv.Software[0], iv.Software[0].Installs[0], "2.1.101", "2.1.98", "")
	if err != nil || cmd != "npm i -g cc@latest" || m.Name != "mac" {
		t.Fatalf("build cmd: %q %v %v", cmd, m, err)
	}
}
func TestBuildAgentCodex(t *testing.T) {
	up := inventory.Update{Type: "agent", Runner: "codex_exec", Cwd: "/srv/x", Prompt: "update to {latest_version}"}
	inst := inventory.Install{Machine: "mac", CurrentCmd: "x", Update: up}
	iv := inventory.Inventory{Machines: map[string]inventory.Machine{"mac": {Name: "mac"}}, Software: []inventory.Software{{Name: "x", Installs: []inventory.Install{inst}}}}
	cmd, _, err := BuildUpdate(iv, iv.Software[0], inst, "0.9.0", "0.8.0", "")
	if err != nil || !strings.Contains(cmd, "codex exec --cd ") || !strings.Contains(cmd, "update to 0.9.0") {
		t.Fatalf("agent cmd: %q %v", cmd, err)
	}
}
func TestClaimAndRecord(t *testing.T) {
	s := seed(t)
	iv := invCmd()
	jid, err := StartJob(s, iv, "cc", "mac")
	if err != nil || jid == 0 {
		t.Fatalf("start: %v %d", err, jid)
	}
	claimed, _ := ClaimNextJob(s, iv, "mac")
	if claimed == nil || claimed.ShellCmd != "npm i -g cc@latest" || claimed.CurrentCmd != "cc --version" {
		t.Fatalf("claim: %+v", claimed)
	}
	RecordResult(s, jid, "success", 0, "2.1.101")
	job, _ := s.GetJob(jid)
	inst, _ := s.GetInstall("cc", "mac")
	if job.Status != "success" || inst.CurrentVersion != "2.1.101" || inst.Status != "up_to_date" {
		t.Fatalf("record: job=%+v inst=%+v", job, inst)
	}
}
func TestRequestAbort(t *testing.T) {
	s := seed(t)
	iv := invCmd()
	jid, _ := StartJob(s, iv, "cc", "mac") // queued
	job, _ := RequestAbort(s, jid)
	if job.Status != "aborted" {
		t.Fatalf("queued abort: %+v", job)
	}
	jid2, _ := StartJob(s, iv, "cc", "mac")
	ClaimNextJob(s, iv, "mac")
	job2, _ := RequestAbort(s, jid2)
	if job2.Status != "running" || !s.AbortRequested(jid2) {
		t.Fatalf("running abort: %+v", job2)
	}
}
