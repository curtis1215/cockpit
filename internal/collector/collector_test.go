package collector

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

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

// 翻譯失敗（raw 非空但 translate 回空）必須留下 error event，
// 否則此類靜默失敗無法從事件記錄察覺（multica 0.3.18 即為此情況）。
func TestRefreshUpstream_TranslateFailureLogsErrorEvent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "c.db")
	s, _ := store.Open(dbPath)
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## real notes"}, nil
	}
	trFail := func(raw string) string { return "" } // 模擬 codex/claude 翻譯失敗回空
	RefreshUpstream(s, iv(), fetch, trFail)
	s.Close() // 關閉後再以獨立連線查 events，避免 sqlite 鎖

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var cnt int
	if err := db.QueryRow(
		"SELECT count(*) FROM events WHERE type='error' AND software='cc' AND detail LIKE 'translate failed%'",
	).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 translate-failed error event, got %d", cnt)
	}
}

// 翻譯成功時不可誤記 error event。
func TestRefreshUpstream_TranslateSuccessNoErrorEvent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "c.db")
	s, _ := store.Open(dbPath)
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## real notes"}, nil
	}
	tr := func(raw string) string { return "中文摘要" }
	RefreshUpstream(s, iv(), fetch, tr)
	s.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var cnt int
	if err := db.QueryRow(
		"SELECT count(*) FROM events WHERE type='error' AND software='cc'",
	).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("expected no error event on successful translate, got %d", cnt)
	}
}
