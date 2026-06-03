package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServeDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "serve.json")
	os.WriteFile(p, []byte(`{"enroll_secret":"s3cret"}`), 0o600)
	c, err := LoadServe(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8787" || c.DBPath != "cockpit.db" || c.EnrollSecret != "s3cret" {
		t.Fatalf("bad serve defaults: %+v", c)
	}
}

func TestLoadAgentRequired(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.json")
	os.WriteFile(p, []byte(`{"server_url":"https://x"}`), 0o600)
	c, err := LoadAgent(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "https://x" || c.HeartbeatSec != 15 {
		t.Fatalf("bad agent cfg: %+v", c)
	}
	if _, err := LoadAgent(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
