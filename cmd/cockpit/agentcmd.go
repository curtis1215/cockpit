package main

import (
	"flag"
	"log"

	"github.com/curtis1215/cockpit/internal/agent"
	"github.com/curtis1215/cockpit/internal/config"
)

func runAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/cockpit/agent.json", "agent config json")
	fs.Parse(args)

	cfg, err := config.LoadAgent(*cfgPath)
	if err != nil {
		log.Fatalf("agent config: %v", err)
	}
	a := &agent.Agent{
		ServerURL:    cfg.ServerURL,
		Secret:       cfg.EnrollSecret,
		EnrollToken:  cfg.EnrollToken,
		Token:        cfg.AgentToken,
		Version:      version,
		HeartbeatSec: cfg.HeartbeatSec,
		SaveToken:    cfg.SaveAgentToken,
	}
	if err := a.Run(); err != nil {
		log.Fatalf("agent: %v", err)
	}
}
