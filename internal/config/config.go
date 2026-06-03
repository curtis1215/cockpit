package config

import (
	"encoding/json"
	"os"
)

type ServeConfig struct {
	Listen       string `json:"listen"`
	DBPath       string `json:"db_path"`
	EnrollSecret string `json:"enroll_secret"`
}

type AgentConfig struct {
	ServerURL    string `json:"server_url"`
	EnrollSecret string `json:"enroll_secret"`
	AgentToken   string `json:"agent_token"`
	HeartbeatSec int    `json:"heartbeat_sec"`
	path         string // 來源檔，供寫回 agent_token 用
}

func LoadServe(path string) (ServeConfig, error) {
	var c ServeConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.DBPath == "" {
		c.DBPath = "cockpit.db"
	}
	return c, nil
}

func LoadAgent(path string) (AgentConfig, error) {
	var c AgentConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.HeartbeatSec == 0 {
		c.HeartbeatSec = 15
	}
	c.path = path
	return c, nil
}

// SaveAgentToken 把 enroll 換得的 agent_token 寫回原 config 檔（保留其它欄位）。
func (c *AgentConfig) SaveAgentToken(token string) error {
	c.AgentToken = token
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o600)
}
