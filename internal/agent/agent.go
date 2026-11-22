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
	"sync"
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
	exit       func(int)
	clientOnce sync.Once // RunJob 的 control 輪詢與最後 flush 會併發呼叫 c()
	client     *httpx.Client
	pollClient *httpx.Client
	col        *collect.Collector
	docker     *dockerstat.Collector
	vmenum     *vmenum.Enumerator
}

func (a *Agent) c() *httpx.Client {
	a.clientOnce.Do(func() { a.client = httpx.New(a.ServerURL, 20*time.Second) })
	return a.client
}

// pollHTTP 回傳 long-poll 專用 client：timeout 必須大於 server 端等待秒數
// （waitSec），否則 client 會在 server 正常回應前先斷線；經代理時 server 感知
// 不到斷線，可能把 job claim 給已死的連線，造成 job 永遠卡在 running。
func (a *Agent) pollHTTP(waitSec int) *httpx.Client {
	timeout := time.Duration(waitSec)*time.Second + 15*time.Second
	if a.pollClient == nil || a.pollClient.Timeout() < timeout {
		a.pollClient = httpx.New(a.ServerURL, timeout)
	}
	return a.pollClient
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
	code, err := a.pollHTTP(waitSec).GetJSON(fmt.Sprintf("/api/agent/poll?wait=%d", waitSec), a.Token, &pr)
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

const (
	logBatchSize       = 50          // 緩衝剛好達到這個行數就喚醒背景 sender（非阻塞，add 本身不送）
	logBufMax          = 5000        // shortcut: 送不出去時最多暫存 5000 行且總計 4MiB，超過丟最舊的；要完整保留就改成落地暫存檔
	logBufMaxBytes     = 4 << 20     // 5000 行 × 近 1MiB 會占用數 GiB，所以另設位元組上限
	logPostMaxBytes    = 1 << 20     // 單次 POST 的位元組上限，超過就分批送
	logFinalFlushTries = 3           // job 結束後最後一次 flush 的嘗試次數（含第一次）
	logFinalFlushWait  = time.Second // 最後 flush 每次嘗試之間的間隔
)

// logBatcher 把 job log 攢成批次送出，避免每行一次 HTTP 拖慢指令輸出。
// bufMu 只保護 buf，add 在鎖內 append 後立即返回、絕不做網路 I/O；
// sendMu 保證同一時間只有一個 sender，行序不亂。
type logBatcher struct {
	bufMu    sync.Mutex
	buf      []string
	bufBytes int
	sendMu   sync.Mutex
	kick     chan struct{} // 容量 1，達 logBatchSize 時非阻塞通知 run 送出
	send     func([]string) error
}

func (b *logBatcher) add(line string) {
	b.bufMu.Lock()
	defer b.bufMu.Unlock()
	b.buf = append(b.buf, line)
	b.bufBytes += len(line)
	b.trimLocked()
	// == 而非 >=：送失敗回補後不重複觸發，server 掛掉時不會每行都打一次 HTTP
	if len(b.buf) == logBatchSize {
		select {
		case b.kick <- struct{}{}:
		default:
		}
	}
}

// run 背景送出：每秒一次，或被 add 喚醒時送出；stop 關閉後返回。
func (b *logBatcher) run(stop <-chan struct{}) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
		case <-b.kick:
		}
		b.flush()
	}
}

// flush 把整個緩衝分批送出（每次 POST ≤ logPostMaxBytes）；失敗就把未送出的行放回最前面，下次再送。
func (b *logBatcher) flush() error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	b.bufMu.Lock()
	pending := b.buf
	b.buf, b.bufBytes = nil, 0
	b.bufMu.Unlock()
	for len(pending) > 0 {
		n, size := 0, 0
		for n < len(pending) && (n == 0 || size+len(pending[n]) <= logPostMaxBytes) {
			size += len(pending[n])
			n++
		}
		if err := b.send(pending[:n]); err != nil {
			b.requeue(pending)
			return err
		}
		pending = pending[n:]
	}
	return nil
}

// finalFlush 在背景 sender 停止後呼叫：最多嘗試 logFinalFlushTries 次，全部失敗就放棄並記 log。
// 呼叫端仍須回報 result，不能因 log 送不出去而不結束 job。
func (b *logBatcher) finalFlush() {
	var err error
	for i := 0; i < logFinalFlushTries; i++ {
		if i > 0 {
			time.Sleep(logFinalFlushWait)
		}
		if err = b.flush(); err == nil {
			return
		}
	}
	b.bufMu.Lock()
	n := len(b.buf)
	b.bufMu.Unlock()
	log.Printf("agent: job log 最後 flush 失敗，放棄 %d 行: %v", n, err)
}

// requeue 把未送出的行放回緩衝最前面，超過上限就丟最舊的。
func (b *logBatcher) requeue(lines []string) {
	b.bufMu.Lock()
	defer b.bufMu.Unlock()
	for _, l := range lines {
		b.bufBytes += len(l)
	}
	b.buf = append(append([]string(nil), lines...), b.buf...)
	b.trimLocked()
}

// trimLocked 超過行數或位元組上限就從最舊的行開始丟。呼叫端須持有 bufMu。
func (b *logBatcher) trimLocked() {
	drop := 0
	for drop < len(b.buf) && (len(b.buf)-drop > logBufMax || b.bufBytes > logBufMaxBytes) {
		b.bufBytes -= len(b.buf[drop])
		drop++
	}
	clear(b.buf[:drop]) // 底層陣列仍會參照被丟的字串，不清掉 GC 就無法回收
	b.buf = b.buf[drop:]
}

// RunJob 執行 server 渲染好的指令：串流 log、輪詢 abort、成功後驗證新版本、回報結果。
func (a *Agent) RunJob(job Job, controlInterval, execTimeout time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var aborted atomic.Bool
	ctlStop, ctlDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ctlDone)
		tk := time.NewTicker(controlInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctlStop:
				return
			case <-tk.C:
				var ctrl struct {
					Abort bool `json:"abort"`
				}
				// 不在 abort 後 return：版本驗證期間 control 輪詢仍須持續，直到收尾才停。
				if _, err := a.c().GetJSON(fmt.Sprintf("/api/agent/jobs/%d/control", job.ID), a.Token, &ctrl); err == nil && ctrl.Abort {
					aborted.Store(true)
					cancel()
				}
			}
		}
	}()
	logs := &logBatcher{kick: make(chan struct{}, 1), send: func(lines []string) error {
		_, err := a.c().PostJSON(fmt.Sprintf("/api/agent/jobs/%d/log", job.ID), a.Token, map[string]any{"lines": lines}, nil)
		return err
	}}
	logStop, logDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(logDone)
		logs.run(logStop)
	}()
	res := executor.Run(ctx, job.ShellCmd, job.Cwd, execTimeout, logs.add)
	// 截斷只寫 job log：server 看不到 agent 本機 log；版本偵測的 Run 不經這裡
	if res.Truncated {
		logs.add("■ 輸出單行超過 1MB，其餘輸出略過")
	}
	// 只讀一次：log 與結果必須一致，abort 若在此之後才到，就視為指令已正常完成。
	wasAborted := aborted.Load()
	status, newVer := "success", ""
	switch {
	case wasAborted:
		logs.add("■ 已由使用者中止")
		status = "aborted"
	case res.ExitCode != 0:
		status = "failed"
	default:
		// 驗證期間 control 輪詢仍在跑，server 才不會把執行中的 job 當成失聯。
		executor.Run(context.Background(), job.CurrentCmd, job.Cwd, execTimeout, func(l string) {
			if v := version.Parse(l, job.VersionRegex); v != "" && newVer == "" {
				newVer = v
			}
		})
	}
	// 收尾：先停 log sender 並做最後 flush（可能重試數秒），control 輪詢撐到 result 送出前才停。
	// log 必須早於 result 送達；flush 只在 sender 停止後呼叫，不與它併發。
	close(logStop)
	<-logDone
	logs.finalFlush()
	close(ctlStop)
	<-ctlDone
	a.reportResult(job.ID, status, res.ExitCode, newVer)
}

func (a *Agent) reportResult(jobID int64, status string, exit int, newVersion string) {
	a.c().PostJSON(fmt.Sprintf("/api/agent/jobs/%d/result", jobID), a.Token,
		map[string]any{"status": status, "exit_code": exit, "new_version": newVersion}, nil)
}
