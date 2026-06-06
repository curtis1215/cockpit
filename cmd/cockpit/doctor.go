package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/config"
	"github.com/curtis1215/cockpit/internal/dockerstat"
	"github.com/curtis1215/cockpit/internal/httpx"
	"github.com/curtis1215/cockpit/internal/vmenum"
	"github.com/kardianos/service"
)

// runDoctor is the entry point for `cockpit doctor`.
func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	cfgPath := fs.String("config", "", "agent.json 路徑（選填；預設依序探查 /etc/cockpit/agent.json 及 ~/.config/cockpit/agent.json）")
	_ = fs.Parse(args)

	var hasError bool
	print := func(line string) { fmt.Println(line) }
	fail := func(line string) { fmt.Println(line); hasError = true }

	// 1. Binary path + version
	checkBinary(print)

	// 2. PATH visibility
	checkPath(print)

	// 3. Docker
	checkDocker(print)

	// 4. vmrun
	checkVmrun(print)

	// 5. 服務狀態（serve, agent）
	checkServices(print)

	// 6. Server connectivity + token (requires agent config)
	agentCfg, cfgLoaded := loadAgentConfig(*cfgPath)
	checkServer(print, fail, agentCfg, cfgLoaded)

	if hasError {
		os.Exit(1)
	}
}

// ── 1. binary ─────────────────────────────────────────────────────────────────

func checkBinary(print func(string)) {
	exe, err := os.Executable()
	if err != nil {
		print("⚠️ binary：無法取得可執行檔路徑：" + err.Error())
		return
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	print(fmt.Sprintf("✅ binary：%s（版本 %s）", resolved, version))
}

// ── 2. PATH visibility ────────────────────────────────────────────────────────

// sudoSecurePaths 是常見 sudo secure_path 目錄。
var sudoSecurePaths = []string{
	"/usr/local/bin",
	"/usr/bin",
	"/usr/sbin",
	"/bin",
	"/sbin",
	"/opt/homebrew/bin",
}

// inSudoSecurePath 回報目錄是否為 sudo secure_path 常見清單之一。
func inSudoSecurePath(dir string) bool {
	for _, p := range sudoSecurePaths {
		if dir == p {
			return true
		}
	}
	return false
}

func checkPath(print func(string)) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)

	pathEnv := os.Getenv("PATH")
	inPath := false
	for _, p := range filepath.SplitList(pathEnv) {
		if p == dir {
			inPath = true
			break
		}
	}

	if !inPath {
		print(fmt.Sprintf("⚠️ PATH 可見性：%s 不在 $PATH 中（直接以絕對路徑呼叫仍可用）", dir))
	} else {
		print(fmt.Sprintf("✅ PATH 可見性：%s 在 $PATH 中", dir))
	}

	if !inSudoSecurePath(dir) {
		print(fmt.Sprintf("⚠️ sudo 路徑：%s 不在 sudo secure_path 常見目錄中，sudo 下請用絕對路徑或 ln -s 至 /usr/local/bin", dir))
	} else {
		print(fmt.Sprintf("✅ sudo 路徑：%s 在 sudo secure_path 常見目錄中", dir))
	}
}

// ── 3. Docker ─────────────────────────────────────────────────────────────────

func checkDocker(print func(string)) {
	p := dockerstat.ResolvedDocker()
	if p == "" {
		print("⚠️ docker：未找到 docker 二進位（該機無容器層屬正常）")
	} else {
		print("✅ docker：" + p)
	}
}

// ── 4. vmrun ──────────────────────────────────────────────────────────────────

func checkVmrun(print func(string)) {
	p := vmenum.ResolvedVmrun()
	if p == "" {
		print("⚠️ vmrun：未找到 vmrun（非 VMware Fusion 主機屬正常）")
	} else {
		print("✅ vmrun：" + p)
	}
	if v := vmenum.ResolvedVirsh(); v != "" {
		print("✅ virsh：" + v + "（libvirt/KVM 列舉啟用）")
	}
}

// ── 5. 服務狀態 ───────────────────────────────────────────────────────────────

func checkServices(print func(string)) {
	for _, mode := range []string{"serve", "agent"} {
		checkOneService(print, mode)
	}
}

func checkOneService(print func(string), mode string) {
	label := "服務 cockpit-" + mode

	cfg := &service.Config{Name: "cockpit-" + mode}
	svc, err := service.New(nil, cfg)
	if err != nil {
		print(fmt.Sprintf("⚠️ %s：無法初始化 service 物件：%v", label, err))
		return
	}

	st, err := svc.Status()
	var stateStr string
	var icon string
	switch {
	case err != nil && strings.Contains(err.Error(), "not installed"):
		stateStr = "未安裝"
		icon = "ℹ️ "
	case err != nil:
		stateStr = fmt.Sprintf("狀態未知（%v）", err)
		icon = "⚠️"
	case st == service.StatusRunning:
		stateStr = "運行中"
		icon = "✅"
	case st == service.StatusStopped:
		if runtime.GOOS == "darwin" && os.Geteuid() != 0 {
			// 非 root 看不到 system domain 的 LaunchDaemon，一律回報 stopped——不可靠
			stateStr = "無法確認（非 root 無法查詢系統服務；用 sudo cockpit doctor 取得正確狀態）"
			icon = "ℹ️ "
		} else {
			stateStr = "已停止"
			icon = "⚠️"
		}
	default:
		stateStr = "狀態未知"
		icon = "⚠️"
	}

	// macOS：嘗試讀 plist 中的 UserName
	extra := ""
	if runtime.GOOS == "darwin" {
		extra = macPlistUserWarning(mode)
	}

	print(fmt.Sprintf("%s %s：%s%s", icon, label, stateStr, extra))
}

// macPlistUserWarning 檢查 /Library/LaunchDaemons/cockpit-<mode>.plist 的 UserName。
// 若為 root（或未設定）則回傳警告文字，否則回傳空字串。
func macPlistUserWarning(mode string) string {
	plistPath := "/Library/LaunchDaemons/cockpit-" + mode + ".plist"
	b, err := os.ReadFile(plistPath)
	if err != nil {
		return "" // plist 不存在，服務未安裝
	}
	content := string(b)
	// 解析 <key>UserName</key><string>xxx</string>
	keyIdx := strings.Index(content, "<key>UserName</key>")
	if keyIdx < 0 {
		// 未設定 UserName → 以 root 執行
		return "\n   ⚠️  以 root 執行，claude/codex 等使用者工具不可用（建議 UserName 改為一般使用者）"
	}
	rest := content[keyIdx+len("<key>UserName</key>"):]
	start := strings.Index(rest, "<string>")
	end := strings.Index(rest, "</string>")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	userName := rest[start+len("<string>") : end]
	if userName == "" || userName == "root" {
		return "\n   ⚠️  以 root 執行，claude/codex 等使用者工具不可用（建議 UserName 改為一般使用者）"
	}
	return ""
}

// ── 6. Server connectivity + token ───────────────────────────────────────────

func loadAgentConfig(cfgPath string) (config.AgentConfig, bool) {
	candidates := []string{cfgPath}
	if cfgPath == "" {
		home, _ := os.UserHomeDir()
		candidates = []string{
			"/etc/cockpit/agent.json",
			filepath.Join(home, ".config", "cockpit", "agent.json"),
		}
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		c, err := config.LoadAgent(p)
		if err == nil {
			return c, true
		}
	}
	return config.AgentConfig{}, false
}

func checkServer(print, fail func(string), cfg config.AgentConfig, loaded bool) {
	if !loaded || cfg.ServerURL == "" {
		print("ℹ️  server 連通：無 agent 設定檔（略過）")
		return
	}

	hc := httpx.New(cfg.ServerURL, 10*time.Second)

	// 版本探查
	var vResp struct {
		Version string `json:"version"`
	}
	statusCode, err := hc.GetJSON("/api/version", "", &vResp)
	if err != nil || statusCode >= 400 {
		// /api/version 屬瀏覽器面端點：在 Cloudflare Access 等保護下會拿到登入頁（HTML），
		// 不代表 agent 不通——降級為提示，連通性以下方的 agent token 檢查為準。
		print(fmt.Sprintf("⚠️  server 版本探查：/api/version 不可達（可能受 Cloudflare Access 保護，屬正常）"))
	} else if vResp.Version != "" && vResp.Version != version {
		print(fmt.Sprintf("⚠️  server 連通：已連接 %s，server 版本 %s 與本機版本 %s 不同，建議升級",
			cfg.ServerURL, vResp.Version, version))
	} else {
		print(fmt.Sprintf("✅ server 連通：%s（版本 %s）", cfg.ServerURL, vResp.Version))
	}

	// Token 有效性
	if cfg.AgentToken == "" {
		print("ℹ️  agent token：未設定（略過）")
		return
	}

	tokenStatus, tokenErr := hc.GetJSON("/api/agent/poll?wait=0", cfg.AgentToken, nil)
	switch {
	case tokenErr == nil || tokenStatus == 200 || tokenStatus == 204:
		print(fmt.Sprintf("✅ server 連通 + agent token 有效（%s）", cfg.ServerURL))
	case tokenStatus == 401:
		fail("❌ agent token：token 無效（401 Unauthorized）")
	default:
		print(fmt.Sprintf("⚠️  agent token：HTTP %d（%v）", tokenStatus, tokenErr))
	}
}
