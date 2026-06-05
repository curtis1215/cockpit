package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/kardianos/service"
)

// ── 純函式：供 setup 指令及測試呼叫 ───────────────────────────────────────────

// writeServeConfig 在 dir/serve.json 寫入初始設定。
// 若檔案已存在則不覆寫（回傳 created=false）。
// listen 空字串時使用 "0.0.0.0:8787"。
func writeServeConfig(dir, data, listen string) (path string, created bool, err error) {
	path = filepath.Join(dir, "serve.json")
	if _, e := os.Stat(path); e == nil {
		// 已存在，不覆寫
		return path, false, nil
	}
	if listen == "" {
		listen = "0.0.0.0:8787"
	}
	secret, err := randHex(16)
	if err != nil {
		return "", false, fmt.Errorf("generate enroll_secret: %w", err)
	}
	cfg := map[string]any{
		"listen":         listen,
		"db_path":        filepath.Join(data, "cockpit.db"),
		"enroll_secret":  "ck_secret_" + secret,
		"inventory_path": filepath.Join(dir, "inventory.yaml"),
		"check_hours":    24,
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// writeAgentConfig 在 dir/agent.json 寫入設定。
// 若 token 非空，寫入 enroll_token；若 secret 非空，寫入 enroll_secret（向後相容）。
// 若檔案已存在則不覆寫（回傳 created=false）。
func writeAgentConfig(dir, server, token, secret string) (path string, created bool, err error) {
	path = filepath.Join(dir, "agent.json")
	if _, e := os.Stat(path); e == nil {
		return path, false, nil
	}
	cfg := map[string]any{
		"server_url": server,
	}
	if token != "" {
		cfg["enroll_token"] = token
	}
	if secret != "" {
		cfg["enroll_secret"] = secret
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// randHex 產生 n 個隨機 bytes 並以十六進位字串回傳（長度 2n）。
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeInventoryIfMissing 若 inventory.yaml 不存在則建立空的。
func writeInventoryIfMissing(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte("machines: {}\nsoftware: []\n"), 0o644)
}

// serviceControl 執行 install 或 start（對應 runService 的子集），以供 setup 程式呼叫。
// 不使用 os.Exit，改以 error 回傳。
func serviceControl(action, mode, cfgPath string) error {
	absCfg, err := filepath.Abs(cfgPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	if _, err := os.Stat(absCfg); err != nil {
		return fmt.Errorf("config file %q not found: %w", absCfg, err)
	}
	svcCfg, err := buildSvcConfig(mode, absCfg)
	if err != nil {
		return err
	}
	prg := &program{mode: mode, cfgPath: absCfg}
	s, err := service.New(prg, svcCfg)
	if err != nil {
		return fmt.Errorf("service.New: %w", err)
	}
	if err := service.Control(s, action); err != nil {
		return fmt.Errorf("service %s: %w", action, err)
	}
	return nil
}

// resolveDirs 回傳 (dir, data)：優先使用 defaultDir/defaultData，
// 若寫入失敗且非 root 時 fallback 至家目錄。
// 若 fallback 亦失敗則回傳錯誤。
func resolveDirs(defaultDir, defaultData string) (dir, data string, didFallback bool, err error) {
	// 嘗試建立預設路徑
	if mkdirErr := os.MkdirAll(defaultDir, 0o755); mkdirErr == nil {
		if mkdirErr2 := os.MkdirAll(defaultData, 0o755); mkdirErr2 == nil {
			return defaultDir, defaultData, false, nil
		}
	}
	// 若已是 root，不 fallback，直接報錯
	if os.Getuid() == 0 {
		return "", "", false, fmt.Errorf("無法建立目錄 %q 或 %q（以 root 執行但仍失敗）", defaultDir, defaultData)
	}
	// fallback 至家目錄
	home, e := os.UserHomeDir()
	if e != nil {
		return "", "", false, fmt.Errorf("取得家目錄失敗：%w", e)
	}
	fbDir := filepath.Join(home, ".config", "cockpit")
	fbData := filepath.Join(home, ".local", "share", "cockpit")
	if err := os.MkdirAll(fbDir, 0o755); err != nil {
		return "", "", false, err
	}
	if err := os.MkdirAll(fbData, 0o755); err != nil {
		return "", "", false, err
	}
	return fbDir, fbData, true, nil
}

// ── 主要入口 ───────────────────────────────────────────────────────────────────

func runSetup(args []string) {
	if len(args) < 1 {
		setupUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "serve":
		runSetupServe(args[1:])
	case "agent":
		runSetupAgent(args[1:])
	default:
		setupUsage()
		os.Exit(2)
	}
}

func setupUsage() {
	fmt.Fprintln(os.Stderr, "用法：cockpit setup <serve|agent> [旗標]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  serve   一鍵設定控制台伺服器（建立設定檔、安裝並啟動服務）")
	fmt.Fprintln(os.Stderr, "  agent   一鍵設定監控 agent（建立設定檔、安裝並啟動服務）")
}

// ── setup serve ────────────────────────────────────────────────────────────────

func runSetupServe(args []string) {
	fs := flag.NewFlagSet("setup serve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：cockpit setup serve [-listen 0.0.0.0:8787] [-dir /etc/cockpit] [-data /var/lib/cockpit] [-no-service]")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	listenFlag := fs.String("listen", "0.0.0.0:8787", "監聽位址")
	dirFlag := fs.String("dir", "/etc/cockpit", "設定檔目錄")
	dataFlag := fs.String("data", "/var/lib/cockpit", "資料目錄（存放 DB）")
	noService := fs.Bool("no-service", false, "不安裝系統服務（僅產生設定檔）")
	_ = fs.Parse(args)

	// 1. 建立目錄（支援 fallback）
	dir, data, didFallback, err := resolveDirs(*dirFlag, *dataFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 建立目錄失敗：%v\n", err)
		os.Exit(1)
	}
	if didFallback {
		fmt.Printf("⚠️  無權限寫入 %s / %s，改用家目錄路徑：\n", *dirFlag, *dataFlag)
		fmt.Printf("   設定目錄：%s\n", dir)
		fmt.Printf("   資料目錄：%s\n", data)
	}

	// 2. 寫入（或保留）serve.json
	cfgPath, created, err := writeServeConfig(dir, data, *listenFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 無法寫入設定檔：%v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("✅ 設定檔已建立：%s\n", cfgPath)
	} else {
		fmt.Printf("ℹ️  已存在，沿用：%s\n", cfgPath)
	}

	// 3. 確保 inventory.yaml 存在
	if err := writeInventoryIfMissing(filepath.Join(dir, "inventory.yaml")); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  無法建立 inventory.yaml：%v\n", err)
	}

	// 4. 安裝並啟動服務
	if !*noService {
		fmt.Println("🔧 正在安裝系統服務…")
		if err := serviceControl("install", "serve", cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "❌ 服務安裝失敗：%v\n", err)
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "請改用以下方式手動操作：")
			fmt.Fprintf(os.Stderr, "  sudo cockpit setup serve\n")
			fmt.Fprintf(os.Stderr, "  或：cockpit serve -config %s\n", cfgPath)
			os.Exit(1)
		}
		fmt.Println("🔧 正在啟動服務…")
		if err := serviceControl("start", "serve", cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  服務啟動失敗：%v\n", err)
			fmt.Fprintf(os.Stderr, "  可手動執行：cockpit serve -config %s\n", cfgPath)
		}
	}

	// 5. 計算 UI URL
	uiURL := buildUIURL(*listenFlag)

	fmt.Println()
	fmt.Println("🎉 cockpit serve 設定完成！")
	fmt.Printf("   UI 網址    ：%s\n", uiURL)
	fmt.Printf("   設定檔     ：%s\n", cfgPath)
	fmt.Printf("   資料目錄   ：%s\n", data)
	fmt.Println()
	fmt.Println("下一步：開啟 UI → 管理 → 新增機器，取得安裝指令後在目標機器執行。")
}

// buildUIURL 把監聽位址轉換成可點擊的 URL。
// 若 host 是 "0.0.0.0" 或空，則改用本機 hostname。
func buildUIURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		if h, e := os.Hostname(); e == nil {
			host = h
		} else {
			host = "localhost"
		}
	}
	return fmt.Sprintf("http://%s:%s/", host, port)
}

// ── setup agent ────────────────────────────────────────────────────────────────

func runSetupAgent(args []string) {
	fs := flag.NewFlagSet("setup agent", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：cockpit setup agent -server <url> -token <enroll_token> [-dir /etc/cockpit] [-no-service]")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	serverFlag := fs.String("server", "", "控制台伺服器 URL（必填）")
	tokenFlag := fs.String("token", "", "Enroll token（必填；由控制台「新增機器」頁面產生）")
	secretFlag := fs.String("secret", "", "Enroll secret（向後相容，寫入 enroll_secret 欄位）")
	dirFlag := fs.String("dir", "/etc/cockpit", "設定檔目錄")
	noService := fs.Bool("no-service", false, "不安裝系統服務（僅產生設定檔）")
	_ = fs.Parse(args)

	if *serverFlag == "" {
		fmt.Fprintln(os.Stderr, "❌ -server 為必填參數")
		fs.Usage()
		os.Exit(2)
	}
	if *tokenFlag == "" && *secretFlag == "" {
		fmt.Fprintln(os.Stderr, "❌ -token 或 -secret 二者至少須提供一個")
		fs.Usage()
		os.Exit(2)
	}

	// 1. 建立設定目錄（資料目錄對 agent 無意義，傳空字串等同於 dir）
	dir, _, didFallback, err := resolveDirs(*dirFlag, *dirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 建立目錄失敗：%v\n", err)
		os.Exit(1)
	}
	if didFallback {
		fmt.Printf("⚠️  無權限寫入 %s，改用家目錄路徑：%s\n", *dirFlag, dir)
	}

	// 2. 寫入（或保留）agent.json
	cfgPath, created, err := writeAgentConfig(dir, *serverFlag, *tokenFlag, *secretFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 無法寫入設定檔：%v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("✅ 設定檔已建立：%s\n", cfgPath)
	} else {
		fmt.Printf("ℹ️  已存在，沿用：%s\n", cfgPath)
	}

	// 3. 安裝並啟動服務
	if !*noService {
		fmt.Println("🔧 正在安裝系統服務…")
		if err := serviceControl("install", "agent", cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "❌ 服務安裝失敗：%v\n", err)
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "請改用以下方式手動操作：")
			fmt.Fprintf(os.Stderr, "  sudo cockpit setup agent -server %s -token %s\n", *serverFlag, *tokenFlag)
			fmt.Fprintf(os.Stderr, "  或：cockpit agent -config %s\n", cfgPath)
			os.Exit(1)
		}
		fmt.Println("🔧 正在啟動服務…")
		if err := serviceControl("start", "agent", cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  服務啟動失敗：%v\n", err)
			fmt.Fprintf(os.Stderr, "  可手動執行：cockpit agent -config %s\n", cfgPath)
		}
	}

	fmt.Println()
	fmt.Println("🎉 cockpit agent 設定完成！")
	fmt.Printf("   設定檔     ：%s\n", cfgPath)
	fmt.Printf("   伺服器     ：%s\n", *serverFlag)
	fmt.Println()
	fmt.Println("✅ 機器將於約 20 秒內出現在控制台。")
}
