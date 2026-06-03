package agent

import (
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/curtis1215/cockpit/internal/httpx"
)

type Agent struct {
	ServerURL    string
	Secret       string
	Token        string
	Version      string
	HeartbeatSec int
	SaveToken    func(string) error
	client       *httpx.Client
}

func (a *Agent) c() *httpx.Client {
	if a.client == nil {
		a.client = httpx.New(a.ServerURL, 20*time.Second)
	}
	return a.client
}

func hostLabel() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unnamed"
	}
	return h
}

// ensureEnrolled：若無 token，用 secret 換 token 並落地。
func (a *Agent) ensureEnrolled() error {
	if a.Token != "" {
		return nil
	}
	if a.Secret == "" {
		return errors.New("agent: no agent_token and no enroll_secret")
	}
	var out struct {
		AgentToken string `json:"agent_token"`
	}
	_, err := a.c().PostJSON("/api/agent/enroll", "", map[string]string{
		"label": hostLabel(), "os": runtime.GOOS, "arch": runtime.GOARCH, "enroll_secret": a.Secret,
	}, &out)
	if err != nil {
		return err
	}
	if out.AgentToken == "" {
		return errors.New("agent: enroll returned empty token")
	}
	a.Token = out.AgentToken
	if a.SaveToken != nil {
		return a.SaveToken(a.Token)
	}
	return nil
}

func (a *Agent) heartbeat() error {
	_, err := a.c().PostJSON("/api/agent/heartbeat", a.Token,
		map[string]string{"agent_version": a.Version}, nil)
	return err
}

// RunOnce：enroll（必要時）+ 一次 heartbeat。供測試/驗證用。
func (a *Agent) RunOnce() error {
	if err := a.ensureEnrolled(); err != nil {
		return err
	}
	return a.heartbeat()
}

// Run：enroll（必要時）後進入 heartbeat 迴圈，直到 process 結束。
func (a *Agent) Run() error {
	if err := a.ensureEnrolled(); err != nil {
		return err
	}
	interval := a.HeartbeatSec
	if interval <= 0 {
		interval = 15
	}
	for {
		_ = a.heartbeat() // 失敗就下個週期重試
		time.Sleep(time.Duration(interval) * time.Second)
	}
}
