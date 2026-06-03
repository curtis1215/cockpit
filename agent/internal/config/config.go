package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	ServerURL          string   `json:"server_url"`
	AgentToken         string   `json:"agent_token"`
	BeszelCmd          string   `json:"beszel_cmd"`
	BeszelArgs         []string `json:"beszel_args"`
	PollTimeoutSec     int      `json:"poll_timeout_sec"`
	ReportIntervalSec  int      `json:"report_interval_sec"`
	ControlIntervalSec int      `json:"control_interval_sec"`
	ExecTimeoutSec     int      `json:"exec_timeout_sec"`
}

func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.ServerURL == "" || c.AgentToken == "" {
		return c, fmt.Errorf("config: server_url and agent_token are required")
	}
	if c.PollTimeoutSec == 0 {
		c.PollTimeoutSec = 25
	}
	if c.ReportIntervalSec == 0 {
		c.ReportIntervalSec = 3600
	}
	if c.ControlIntervalSec == 0 {
		c.ControlIntervalSec = 2
	}
	if c.ExecTimeoutSec == 0 {
		c.ExecTimeoutSec = 900
	}
	return c, nil
}
