package collector

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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
	tr := func(raw string) (string, error) { return "中文摘要", nil }
	RefreshUpstream(s, iv(), fetch, tr)
	if v, _ := s.GetVersion("cc", "2.1.101"); v.ChangelogZh != "中文摘要" {
		t.Fatalf("version: %+v", v)
	}
}

func TestRefreshUpstream_SetsReadyStatus(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## notes"}, nil
	}
	tr := func(raw string) (string, error) { return "中文", nil }
	RefreshUpstream(s, iv(), fetch, tr)
	v, err := s.GetVersion("cc", "2.1.101")
	if err != nil {
		t.Fatal(err)
	}
	if v.TranslateStatus != "ready" {
		t.Fatalf("TranslateStatus=%q want ready", v.TranslateStatus)
	}
	if v.ChangelogZh != "中文" {
		t.Fatalf("ChangelogZh=%q", v.ChangelogZh)
	}
	if v.TranslateError != "" {
		t.Fatalf("TranslateError=%q want empty", v.TranslateError)
	}
}

func TestRefreshUpstream_SetsFailedStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "c.db")
	s, _ := store.Open(dbPath)
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## notes"}, nil
	}
	tr := func(raw string) (string, error) { return "", errors.New("timeout after 300s") }
	RefreshUpstream(s, iv(), fetch, tr)

	v, err := s.GetVersion("cc", "2.1.101")
	if err != nil {
		t.Fatal(err)
	}
	if v.TranslateStatus != "failed" {
		t.Fatalf("TranslateStatus=%q want failed", v.TranslateStatus)
	}
	if !strings.Contains(v.TranslateError, "timeout") {
		t.Fatalf("TranslateError=%q want timeout", v.TranslateError)
	}
	s.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var detail string
	if err := db.QueryRow(
		"SELECT detail FROM events WHERE type='error' AND software='cc' AND detail LIKE 'translate failed%'",
	).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "timeout") {
		t.Fatalf("error event detail=%q want timeout", detail)
	}
}

func TestRefreshUpstream_SkipsWhenZhExists(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	if err := s.AddVersion("cc", "2.1.101", "", "old raw", "既有中文"); err != nil {
		t.Fatal(err)
	}
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## new notes"}, nil
	}
	tr := func(raw string) (string, error) {
		t.Fatal("translate should not be called when zh exists")
		return "", nil
	}
	RefreshUpstream(s, iv(), fetch, tr)
	v, err := s.GetVersion("cc", "2.1.101")
	if err != nil {
		t.Fatal(err)
	}
	if v.ChangelogZh != "既有中文" {
		t.Fatalf("ChangelogZh=%q", v.ChangelogZh)
	}
	if v.TranslateStatus != "ready" {
		t.Fatalf("TranslateStatus=%q want ready", v.TranslateStatus)
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
	trFail := func(raw string) (string, error) { return "", nil } // 模擬空翻譯
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

// refresh 翻譯失敗不可清掉既有 zh（與 retry 並行時 race 保護）。
func TestRefreshUpstream_TranslateFailureKeepsExistingZh(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	// 模擬：refresh 開始時尚無 zh（會進翻譯路徑），翻譯期間 retry 已寫入 zh。
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## notes"}, nil
	}
	tr := func(raw string) (string, error) {
		if err := s.UpdateTranslateResult("cc", "2.1.101", "重試已寫入", "ready", ""); err != nil {
			t.Fatal(err)
		}
		return "", errors.New("timeout after 300s")
	}
	RefreshUpstream(s, iv(), fetch, tr)
	v, err := s.GetVersion("cc", "2.1.101")
	if err != nil {
		t.Fatal(err)
	}
	if v.ChangelogZh != "重試已寫入" {
		t.Fatalf("ChangelogZh=%q want kept", v.ChangelogZh)
	}
	if v.TranslateStatus != "failed" {
		t.Fatalf("TranslateStatus=%q want failed", v.TranslateStatus)
	}
	if !strings.Contains(v.TranslateError, "timeout") {
		t.Fatalf("TranslateError=%q", v.TranslateError)
	}
}

// 翻譯成功時不可誤記 error event。
func TestRefreshUpstream_TranslateSuccessNoErrorEvent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "c.db")
	s, _ := store.Open(dbPath)
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## real notes"}, nil
	}
	tr := func(raw string) (string, error) { return "中文摘要", nil }
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
