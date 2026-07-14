package store

import (
	"path/filepath"
	"testing"
)

func TestUpdateTranslateResultOverwritesZh(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddVersion("cc", "1.0.0", "", "## raw", "舊中文"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTranslateResult("cc", "1.0.0", "新中文", "ready", ""); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVersion("cc", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if v.ChangelogZh != "新中文" || v.TranslateStatus != "ready" || v.TranslateError != "" {
		t.Fatalf("%+v", v)
	}
}

func TestBackfillTranslateStatus(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 直接 insert 模擬舊列（若 AddVersion 已自動設 status，改用 raw SQL 寫空 status）
	s.db.Exec(`INSERT INTO versions(software,version,changelog_raw,changelog_zh,translate_status) VALUES('a','1','raw','中文','')`)
	s.db.Exec(`INSERT INTO versions(software,version,changelog_raw,changelog_zh,translate_status) VALUES('b','1','raw','','')`)
	s.db.Exec(`INSERT INTO versions(software,version,changelog_raw,changelog_zh,translate_status) VALUES('c','1','','','')`)
	if err := s.BackfillTranslateStatus(); err != nil {
		t.Fatal(err)
	}
	va, _ := s.GetVersion("a", "1")
	vb, _ := s.GetVersion("b", "1")
	vc, _ := s.GetVersion("c", "1")
	if va.TranslateStatus != "ready" || vb.TranslateStatus != "failed" || vc.TranslateStatus != "none" {
		t.Fatalf("a=%q b=%q c=%q", va.TranslateStatus, vb.TranslateStatus, vc.TranslateStatus)
	}
}

func TestBackfillRecoversStuckTranslating(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddVersion("cc", "1.0.0", "", "## raw", "既有中文"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTranslateStatus("cc", "1.0.0", "translating", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillTranslateStatus(); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVersion("cc", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if v.TranslateStatus != "failed" || v.TranslateError != "interrupted" {
		t.Fatalf("want failed/interrupted got status=%q err=%q", v.TranslateStatus, v.TranslateError)
	}
	if v.ChangelogZh != "既有中文" {
		t.Fatalf("zh should be preserved, got %q", v.ChangelogZh)
	}
}
