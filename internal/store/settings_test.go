package store

import (
	"path/filepath"
	"testing"
)

func TestSettings(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := s.GetSetting("translate.endpoint"); got != "" {
		t.Fatalf("missing key should return empty, got %q", got)
	}
	if err := s.SetSetting("translate.endpoint", "http://lm:1234"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetSetting("translate.endpoint"); got != "http://lm:1234" {
		t.Fatalf("got %q", got)
	}
	// upsert 覆寫
	if err := s.SetSetting("translate.endpoint", "http://other:1234"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetSetting("translate.endpoint"); got != "http://other:1234" {
		t.Fatalf("after overwrite got %q", got)
	}
	// 清空
	if err := s.SetSetting("translate.endpoint", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.GetSetting("translate.endpoint"); got != "" {
		t.Fatalf("after clear got %q", got)
	}
}
