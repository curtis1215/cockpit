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

func TestSetSettings(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	kv := map[string]string{
		"translate.endpoint":   "http://lm:1234",
		"translate.model":      "gemma",
		"translate.max_tokens": "4096",
	}
	if err := s.SetSettings(kv); err != nil {
		t.Fatal(err)
	}
	for k, want := range kv {
		if got := s.GetSetting(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	// 覆寫也走得通
	kv["translate.model"] = "qwen"
	if err := s.SetSettings(kv); err != nil {
		t.Fatal(err)
	}
	if got := s.GetSetting("translate.model"); got != "qwen" {
		t.Fatalf("after overwrite model = %q", got)
	}
}
