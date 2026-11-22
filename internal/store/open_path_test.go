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
