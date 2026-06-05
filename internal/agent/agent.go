package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/curtis1215/cockpit/internal/collect"
	"github.com/curtis1215/cockpit/internal/dockerstat"
	"github.com/curtis1215/cockpit/internal/executor"
	"github.com/curtis1215/cockpit/internal/httpx"
	"github.com/curtis1215/cockpit/internal/selfupdate"
	"github.com/curtis1215/cockpit/internal/version"
	"github.com/curtis1215/cockpit/internal/vmenum"
)

// machineUUID returns the hardware UUID of this machine.
// linux: reads /sys/class/dmi/id/product_uuid (trimmed; "" on error/no-permission).
// darwin: runs `ioreg -rd1 -c IOPlatformExpertDevice` and parses IOPlatformUUID.
// other: "".
func machineUUID() string {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/sys/class/dmi/id/product_uuid")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	case "darwin":
		out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			return ""
		}
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			line := scanner.Text()
			// Look for: "IOPlatformUUID" = "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX"
			if strings.Contains(line, "IOPlatformUUID") {
				i := strings.Index(line, `"IOPlatformUUID"`)
				if i < 0 {
					continue
				}
				rest := line[i+len(`"IOPlatformUUID"`):]
				// find = then first "..."
				eq := strings.Index(rest, "=")
				if eq < 0 {
					continue
				}
				rest = strings.TrimSpace(rest[eq+1:])
				if len(rest) >= 2 && rest[0] == '"' {
					end := strings.Index(rest[1:], `"`)
					if end >= 0 {
						return rest[1 : end+1]
					}
				}
			}
		}
		return ""
	default:
		return ""
	}
}

type Agent struct {
	ServerURL    string
	Secret       string
	EnrollToken  string
	Token        string
	Version      string
	HeartbeatSec int
	SaveToken    func(string) error
	// doUpgrade is called on "upgrade" poll events. Returns (updated, error).
	// Defaults to a wrapper around selfupdate.Run using COCKPIT_REPO env or
	// the default repo "curtis1215/cockpit". Inject in tests to avoid real network calls.
	doUpgrade func() (bool, error)
	// exit terminates the process. Defaults to os.Exit. Injectable for testing.
	exit   func(int)
	client *httpx.Client
	col    *collect.Collector
	docker *dockerstat.Collector
	vmenum *vmenum.Enumerator
}

func (a *Agent) c() *httpx.Client {
	if a.client == nil {
		a.client = httpx.New(a.ServerURL, 20*time.Second)
	}
	return a.client
}

// defaultDoUpgrade returns the default upgrade func: wraps selfupdate.Run with
// COCKPIT_REPO env or the hardcoded default repo, current version, and empty targetPath.
func (a *Agent) defaultDoUpgrade() func() (bool, error) {
	return func() (bool, error) {
		repo := os.Getenv("COCKPIT_REPO")
		if repo == "" {
			repo = "curtis1215/cockpit"
		}
		return selfupdate.Run(
			&http.Client{Timeout: 60 * time.Second},
			"https://api.github.com",
			repo,
			a.Version,
			"",
		)
	}
}

func (a *Agent) getDoUpgrade() func() (bool, error) {
	if a.doUpgrade != nil {
		return a.doUpgrade
	}
	return a.defaultDoUpgrade()
}

func (a *Agent) getExit() func(int) {
	if a.exit != nil {
		return a.exit
	}
	return os.Exit
}

func hostLabel() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unnamed"
	}
	return h
}

// ensureEnrolled：若無 token，用 enroll_token（每機一次性，優先）或 enroll_secret 換 token 並落地。
func (a *Agent) ensureEnrolled() error {
	if a.Token != "" {
		return nil
	}
	if a.Secret == "" && a.EnrollToken == "" {
		return errors.New("agent: no agent_token and no enroll_token/enroll_secret")
	}
	body := map[string]string{
		"label": hostLabel(), "os": runtime.GOOS, "arch": runtime.GOARCH,
	}
	if uuid := machineUUID(); uuid != "" {
		body["machine_uuid"] = uuid
	}
	if a.EnrollToken != "" {
		body["enroll_token"] = a.EnrollToken
	} else {
		body["enroll_secret"] = a.Secret
	}
	var out struct {
		AgentToken string `json:"agent_token"`
	}
	_, err := a.c().PostJSON("/api/agent/enroll", "", body, &out)
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
	body := map[string]string{"agent_version": a.Version}
	if uuid := machineUUID(); uuid != "" {
		body["machine_uuid"] = uuid
	}
	_, err := a.c().PostJSON("/api/agent/heartbeat", a.Token, body, nil)
	return err
}

// RunOnce：enroll（必要時）+ 一次 heartbeat。供測試/驗證用。
func (a *Agent) RunOnce() error {
	if err := a.ensureEnrolled(); err != nil {
		return err
	}
	return a.heartbeat()
}

type pollResp struct {
	Type string `json:"type"`
	Job  Job    `json:"job"`
}

// pollOnce 長輪詢一次：回 ("",_,nil) 表示無工作（204）。
func (a *Agent) pollOnce(waitSec int) (string, Job, error) {
	var pr pollResp
	code, err := a.c().GetJSON(fmt.Sprintf("/api/agent/poll?wait=%d", waitSec), a.Token, &pr)
	if err != nil {
		return "", Job{}, err
	}
	if code == 204 {
		return "", Job{}, nil
	}
	return pr.Type, pr.Job, nil
}

// Run：enroll 後啟動兩個迴圈——heartbeat（背景）與版本追蹤主迴圈（long-poll 取工作）。
// 注意：若設定檔放的是 inventory agent_token（而非 enroll 取得的 systems token），
// heartbeat 會 401——P1 已知雙 token 並存，P3 收斂；heartbeat 失敗只略過不中斷。
func (a *Agent) Run() error {
	if err := a.ensureEnrolled(); err != nil {
		return err
	}
	interval := a.HeartbeatSec
	if interval <= 0 {
		interval = 15
	}
	go func() {
		for {
			_ = a.heartbeat() // 失敗就下個週期重試
			time.Sleep(time.Duration(interval) * time.Second)
		}
	}()
	go a.monitorLoop()
	a.ReportVersions(60 * time.Second)
	for {
		evt, job, err := a.pollOnce(25)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		switch evt {
		case "job":
			a.RunJob(job, 2*time.Second, 30*time.Minute)
		case "upgrade":
			updated, err := a.getDoUpgrade()()
			if err != nil {
				log.Printf("agent: upgrade failed: %v — continuing", err)
				continue
			}
			if updated {
				log.Println("agent: binary replaced — restarting via supervisor")
				a.getExit()(0)
			} else {
				log.Println("agent: already up-to-date — continuing")
			}
		case "check":
			a.ReportVersions(60 * time.Second)
		}
	}
}

type installDef struct {
	Software     string `json:"software"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}

// Job 描述 server 下派的升級任務。
type Job struct {
	ID           int64  `json:"id"`
	Software     string `json:"software"`
	Machine      string `json:"machine"`
	ShellCmd     string `json:"shell_cmd"`
	Cwd          string `json:"cwd"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}

// ReportVersions 讀 server 的 install 定義、本機跑 current_cmd 解析版本、回報。
func (a *Agent) ReportVersions(execTimeout time.Duration) {
	var defs []installDef
	if _, err := a.c().GetJSON("/api/agent/installs", a.Token, &defs); err != nil {
		return
	}
	var reports []map[string]string
	for _, d := range defs {
		cur := ""
		executor.Run(context.Background(), d.CurrentCmd, "", execTimeout, func(l string) {
			if v := version.Parse(l, d.VersionRegex); v != "" && cur == "" {
				cur = v
			}
		})
		reports = append(reports, map[string]string{"software": d.Software, "current_version": cur})
	}
	if len(reports) > 0 {
		a.c().PostJSON("/api/agent/report-versions", a.Token, reports, nil)
	}
}

// RunJob 執行 server 渲染好的指令：串流 log、輪詢 abort、成功後驗證新版本、回報結果。
func (a *Agent) RunJob(job Job, controlInterval, execTimeout time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var aborted atomic.Bool
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tk := time.NewTicker(controlInterval)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				var ctrl struct {
					Abort bool `json:"abort"`
				}
				if _, err := a.c().GetJSON(fmt.Sprintf("/api/agent/jobs/%d/control", job.ID), a.Token, &ctrl); err == nil && ctrl.Abort {
					aborted.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	post := func(line string) {
		a.c().PostJSON(fmt.Sprintf("/api/agent/jobs/%d/log", job.ID), a.Token, map[string]any{"lines": []string{line}}, nil)
	}
	res := executor.Run(ctx, job.ShellCmd, job.Cwd, execTimeout, post)
	if aborted.Load() {
		post("■ 已由使用者中止")
		a.reportResult(job.ID, "aborted", res.ExitCode, "")
		return
	}
	if res.ExitCode != 0 {
		a.reportResult(job.ID, "failed", res.ExitCode, "")
		return
	}
	newVer := ""
	executor.Run(context.Background(), job.CurrentCmd, job.Cwd, execTimeout, func(l string) {
		if v := version.Parse(l, job.VersionRegex); v != "" && newVer == "" {
			newVer = v
		}
	})
	a.reportResult(job.ID, "success", res.ExitCode, newVer)
}

func (a *Agent) reportResult(jobID int64, status string, exit int, newVersion string) {
	a.c().PostJSON(fmt.Sprintf("/api/agent/jobs/%d/result", jobID), a.Token,
		map[string]any{"status": status, "exit_code": exit, "new_version": newVersion}, nil)
}
