package main

import (
	"testing"
)

func TestBuildSvcConfig_Serve(t *testing.T) {
	cfg, err := buildSvcConfig("serve", "/etc/cockpit/serve.json")
	if err != nil {
		t.Fatalf("buildSvcConfig: %v", err)
	}
	if cfg.Name != "cockpit-serve" {
		t.Errorf("Name = %q, want cockpit-serve", cfg.Name)
	}
	if cfg.DisplayName != "Cockpit serve" {
		t.Errorf("DisplayName = %q, want Cockpit serve", cfg.DisplayName)
	}
	// service ImagePath 必須走 `service run` 入口，才會呼叫 kardianos service.Run()
	// 進入 Windows SCM dispatcher（否則 SCM 1053 逾時）。mac/linux 同樣經此入口。
	want := []string{"service", "run", "-mode", "serve", "-config", "/etc/cockpit/serve.json"}
	if len(cfg.Arguments) != len(want) {
		t.Fatalf("Arguments length = %d, want %d; got %v", len(cfg.Arguments), len(want), cfg.Arguments)
	}
	for i, a := range want {
		if cfg.Arguments[i] != a {
			t.Errorf("Arguments[%d] = %q, want %q", i, cfg.Arguments[i], a)
		}
	}
}

func TestBuildSvcConfig_Agent(t *testing.T) {
	cfg, err := buildSvcConfig("agent", "/etc/cockpit/agent.json")
	if err != nil {
		t.Fatalf("buildSvcConfig: %v", err)
	}
	if cfg.Name != "cockpit-agent" {
		t.Errorf("Name = %q, want cockpit-agent", cfg.Name)
	}
	want := []string{"service", "run", "-mode", "agent", "-config", "/etc/cockpit/agent.json"}
	for i, a := range want {
		if cfg.Arguments[i] != a {
			t.Errorf("Arguments[%d] = %q, want %q", i, cfg.Arguments[i], a)
		}
	}
}

func TestBuildSvcConfig_InvalidMode(t *testing.T) {
	_, err := buildSvcConfig("web", "/some/path")
	if err == nil {
		t.Fatal("expected error for invalid mode, got nil")
	}
}
