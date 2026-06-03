package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`{"server_url":"https://cockpit.example","agent_token":"tok"}`), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "https://cockpit.example" || c.AgentToken != "tok" {
		t.Fatalf("bad parse: %+v", c)
	}
	if c.PollTimeoutSec != 25 || c.ReportIntervalSec != 3600 || c.ControlIntervalSec != 2 {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`{"server_url":""}`), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for missing fields")
	}
}
