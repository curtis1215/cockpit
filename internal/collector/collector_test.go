package collector

import (
	"path/filepath"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/sources"
	"github.com/curtis1215/cockpit/internal/store"
)

func iv() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{"mac": {Name: "mac"}},
		Software: []inventory.Software{{Name: "cc", Kind: "npm", LatestSource: "npm:cc", Changelog: "github:o/cc",
			Installs: []inventory.Install{{Machine: "mac", CurrentCmd: "cc --version", Update: inventory.Update{Type: "command", Cmd: "x"}}}}},
	}
}

func TestRefreshUpstream(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## notes"}, nil
	}
	tr := func(raw string) string { return "中文摘要" }
	RefreshUpstream(s, iv(), fetch, tr)
	if v, _ := s.GetVersion("cc", "2.1.101"); v.ChangelogZh != "中文摘要" {
		t.Fatalf("version: %+v", v)
	}
}

func TestApplyReport(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	s.AddVersion("cc", "2.1.101", "", "raw", "中文")
	n := ApplyVersionReport(s, "mac", []Report{{Software: "cc", CurrentVersion: "2.1.98"}})
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	inst, _ := s.GetInstall("cc", "mac")
	if inst.CurrentVersion != "2.1.98" || inst.Status != "behind" {
		t.Fatalf("inst: %+v", inst)
	}
}
