package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 路徑含 SQLite URI 語法字元（# ? %）時，必須開到原檔名，不能被截斷或開到別的檔案。
// Windows 檔名不能含 ?，改用 # %（仍會踩到 # 截斷的舊 bug）。
func TestOpenPathWithURISpecialChars(t *testing.T) {
	name := "a#b?c%d.db"
	if runtime.GOOS == "windows" {
		name = "a#b%c.db"
	}
	path := filepath.Join(t.TempDir(), name)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db 未建立在原檔名: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.GetSetting("k"); got != "v" {
		t.Fatalf("重開後讀回 %q, want v", got)
	}
}

// openRoundTrip 以 dsn 開庫、寫入一筆設定，確認 file 存在後重開讀回。
func openRoundTrip(t *testing.T, dsn, file string) {
	t.Helper()
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("db 未建立在預期檔名 %s: %v", file, err)
	}
	s2, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.GetSetting("k"); got != "v" {
		t.Fatalf("重開後讀回 %q, want v", got)
	}
}

// db_path 已是 file: URI（例如 file:xxx.db?mode=rwc）時，要開到 URI 指定的檔案，不能把整串當成檔名。
func TestOpenFileURIPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u.db")
	openRoundTrip(t, "file:"+filepath.ToSlash(path)+"?mode=rwc", path)
}

// 雙斜線開頭的絕對路徑（//tmp/x.db）不能被組成 file://tmp/...，否則 tmp 會被當成 authority。
func TestOpenDoubleSlashAbsPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 沒有雙斜線絕對路徑")
	}
	path := "/" + filepath.Join(t.TempDir(), "d.db")
	openRoundTrip(t, path, path)
}
