# Cockpit P4 — 打包部署（upgrade / service / 一行安裝 / release）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `cockpit upgrade` 自我更新、`cockpit service` 服務化（launchd/systemd）、GoReleaser 多平台建置、`curl|sh` 一行安裝，tag v0.1.0 發 GitHub Release，VM 驗收。

**Tech Stack:** GitHub Releases API（`COCKPIT_REPO` 預設 `curtis1215/cockpit`）；`github.com/kardianos/service`；GoReleaser v2；POSIX install.sh。

---

### Task 1: `cockpit upgrade`（自我更新）

**Files:** Create `internal/selfupdate/selfupdate.go` + test; Modify `cmd/cockpit/main.go`（upgrade 分支）; Create `cmd/cockpit/upgrade.go`

行為：
- `selfupdate.Latest(hc *http.Client, base, repo string) (tag string, assets map[string]string, err error)`：GET `{base}/repos/{repo}/releases/latest` → tag_name + assets[]{name, browser_download_url} map。
- `selfupdate.AssetName(goos, goarch, version string) string` → `cockpit_<version>_<goos>_<goarch>.tar.gz`（與 .goreleaser.yaml name_template 一致；version 無 v 前綴）。
- `selfupdate.Run(currentVersion string, opts...)`：Latest → tag 去 v 前綴 == currentVersion → 印「已是最新」return；否則下載對應 asset（tar.gz 內含 `cockpit` binary）→ 解壓到 temp → `os.Rename` 原子替換 `os.Executable()` 路徑（同 filesystem；先寫 `<exe>.new` 再 rename；舊檔先 rename `<exe>.old` 後刪，Windows 限制留註解）→ 印新版本。
- 測試：httptest 假 release server（latest JSON + tar.gz asset 內含假 binary 檔案內容）→ Run 後目標檔內容被替換、是新內容；已最新 → 不動作。Run 接受目標路徑注入（測試不替換真執行檔）。
- main.go：`case "upgrade": runUpgrade(args)`；upgrade.go 用真 GitHub base。
- Commit：`feat(go): cockpit upgrade self-update from github releases`

### Task 2: `cockpit service`（launchd/systemd）

**Files:** `go get github.com/kardianos/service`; Create `cmd/cockpit/servicecmd.go`; Modify main.go（service 分支）

行為：
- `cockpit service install|uninstall|start|stop|status -mode serve|agent -config <path>`：service.Config{Name: "cockpit-"+mode, DisplayName, Arguments: []string{mode, "-config", absConfigPath}}；Executable 預設自身。install 前 config 檔需存在（fatal 否則）。
- program struct 實作 service.Interface：Start → go runServe/runAgent（呼叫既有函式）；Stop → os.Exit(0)（graceful 留 issue #8）。注意：runServe/runAgent 簽名 `(args []string)`——service Start 用 `go runServe([]string{"-config", cfgPath})`。
- 非 root/非互動環境的錯誤直接回報（kardianos 會給明確錯誤）。
- 測試：單元測 config 組裝（mode/name/arguments）；install/start 實測留 Task 4 VM。
- Commit：`feat(go): cockpit service install/uninstall via kardianos (launchd/systemd)`

### Task 3: GoReleaser + install.sh + README

**Files:** Create `.goreleaser.yaml`, `install.sh`, Modify `README.md`（quickstart）

- `.goreleaser.yaml`（v2 schema）：project_name cockpit；builds: main ./cmd/cockpit, env CGO_ENABLED=0, goos [darwin,linux,windows], goarch [amd64,arm64], ldflags `-s -w -X main.version={{.Version}}`；archives: formats tar.gz（windows zip），name_template `cockpit_{{.Version}}_{{.Os}}_{{.Arch}}`；checksum；release: github。驗證：`goreleaser check`（無 goreleaser 則 `brew install goreleaser` 或跳過註記）+ 手動矩陣 build 迴圈驗證可編譯（windows 含 executor build tag——**若 windows 編不過**（kill_unix 無 windows 對應），補 `internal/executor/kill_windows.go`（`//go:build windows`：setPgid no-op、killGroup 用 `exec.Command("taskkill","/T","/F","/PID",pid)`）。
- `install.sh`：POSIX；偵測 uname OS/ARCH（darwin/linux × arm64/amd64）→ gh api releases/latest 不可用改 curl `https://api.github.com/repos/curtis1215/cockpit/releases/latest` 取 tag → 下載 `https://github.com/curtis1215/cockpit/releases/download/<tag>/cockpit_<ver>_<os>_<arch>.tar.gz` → 解壓 → install 到 `/usr/local/bin/cockpit`（無權限 sudo 提示或 `~/.local/bin` fallback）→ 印版本與下一步（`cockpit serve|agent|service`）。
- README quickstart：一行安裝、serve.json/agent.json 範例、service install、upgrade。
- Commit：`feat(release): goreleaser config + one-line install.sh + quickstart`

### Task 4: Release v0.1.0 + VM 驗收

- `git tag v0.1.0 && git push origin v0.1.0`；goreleaser release（GITHUB_TOKEN=`gh auth token`）→ GitHub Release 上線（assets 6 平台 + checksums）。
- VM 驗收：
  1. VM 內 `curl -fsSL https://raw.githubusercontent.com/curtis1215/cockpit/main/install.sh | sh`（或先本地檔案測再上線）→ `/usr/local/bin/cockpit version` = 0.1.0。
  2. `cockpit service install -mode serve -config ...` + start → systemd unit active、API 可 curl。agent 同。
  3. `cockpit upgrade` → 「已是最新」。
- 清理 + push。

## Self-Review
- upgrade 原子替換與 asset 命名跟 goreleaser name_template 對齊；service Stop 不 graceful（issue #8 已存在）；windows executor 補檔列入 T3 驗證；install.sh fallback 路徑。
