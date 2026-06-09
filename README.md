# cockpit

**Homelab 控制平面** — 單一 binary，整合監控、版本追蹤與拓樸管理。

- **機器監控**：CPU、記憶體、磁碟、GPU、網路流量、溫度 — 輕量 agent 回傳中央伺服器，Web UI 即時顯示拓樸與 30 天走勢
- **版本追蹤**：跨機器追蹤軟體版本（npm / GitHub / PyPI / Homebrew / 自訂指令）、changelog 繁中摘要、Web UI 一鍵觸發更新（shell 指令或委派 AI agent），即時 log 串流、可中止
- **服務化**：launchd（macOS）/ systemd（Linux），開機自啟、`cockpit upgrade` 自我更新

---

## 安裝流程總覽

```
┌─ 1. Server（控制台主機，一行） ──────────────────────────────┐
│  curl …install.sh | sudo sh -s -- serve                      │
│  → 裝 binary、建目錄與設定、裝服務並啟動、印出 UI 網址        │
└──────────────────────────────────────────────────────────────┘
                  │  開啟 http://<server>:8787/
                  ▼
┌─ 2. UI：管理 → 新增機器 ─────────────────────────────────────┐
│  輸入機器名稱 → 取得該機專屬的一行安裝指令（含一次性 token） │
└──────────────────────────────────────────────────────────────┘
                  │  把指令貼到目標機器
                  ▼
┌─ 3. Agent（每台被監控機器，一行） ───────────────────────────┐
│  curl …install.sh | sudo sh -s -- agent <server_url> <token> │
│  → 裝 binary、寫設定、enroll 換正式 token、裝服務並啟動      │
└──────────────────────────────────────────────────────────────┘
                  ▼
        約 20 秒內機器上線（拓樸 / 機器頁可見）
```

## 1. 安裝 Server（一行）

```sh
curl -fsSL https://raw.githubusercontent.com/curtis1215/cockpit/main/install.sh | sudo sh -s -- serve
```

自動完成：下載對應平台 binary → `/usr/local/bin/cockpit` → 建立 `/etc/cockpit/`（`serve.json`，自動產生隨機 `enroll_secret`；空 `inventory.yaml`）與 `/var/lib/cockpit/`（SQLite db）→ 安裝系統服務並啟動 → 印出 UI 網址。

設定檔已存在時不會覆蓋（冪等，可重複執行）。

## 2. 安裝 Agent（每台機器，一行）

開啟 UI → **管理 → 新增機器** → 輸入名稱 → modal 會直接給你這台機器專屬的一行指令（含一次性 enroll token，附複製按鈕），形如：

```sh
curl -fsSL https://raw.githubusercontent.com/curtis1215/cockpit/main/install.sh | sudo sh -s -- agent http://<server>:8787 ck_enroll_xxxxxxxx
```

貼到目標機器執行即可。agent 首次連線用 enroll token 換取正式 `agent_token`（寫回設定檔、enroll token 即作廢），之後每 15 秒回報指標、自動偵測 Docker 容器與 VMware Fusion VM、long-poll 等候更新工作。

### Windows agent（PowerShell）

> ⚠️ Windows 支援為實驗性、**尚未經實機驗證**。可運作：CPU／記憶體／磁碟指標、心跳、軟體版本回報與更新。**不支援/受限**：load average、溫度、GPU 指標（Windows 無對應來源）、硬體 UUID 識別（改用主機名）、VM 列舉（僅支援 macOS 的 VMware Fusion / OrbStack）。Windows 服務安裝（kardianos SCM）尚未經 CI 驗證。

Linux/macOS 用的 `install.sh` 是 POSIX 腳本，Windows 改用 `install.ps1`。**以系統管理員身分開啟 PowerShell**（安裝服務需要），執行：

```powershell
# 下載安裝腳本
irm https://raw.githubusercontent.com/curtis1215/cockpit/main/install.ps1 -OutFile install.ps1

# 安裝並註冊為 Windows 服務（-Server / -Token 取自管理頁「新增機器」）
.\install.ps1 -Subcommand agent -Server http://<server>:8787 -Token ck_enroll_xxxxxxxx
```

腳本會：下載對應架構的 `cockpit_<版本>_windows_<arch>.zip` → 解壓 `cockpit.exe` 到 `%ProgramFiles%\cockpit` → 設定檔寫入 `%ProgramData%\cockpit\agent.json` → 透過 Windows SCM 註冊服務並啟動。驗證：

```powershell
Get-Service cockpit*        # 應為 Running
& "$env:ProgramFiles\cockpit\cockpit.exe" version
```

僅想產生設定、不裝服務（前景測試）：加 `-NoService`，再手動執行 `cockpit.exe agent -config %ProgramData%\cockpit\agent.json`。設定目錄可用 `-Dir` 覆寫——**勿**沿用 Linux 的 `/etc/cockpit`，在 Windows 會解析成 `C:\etc\cockpit`。

## 3. 日常操作

```sh
cockpit upgrade                          # 自我更新到最新 release（server / agent 都適用）
sudo cockpit service status -mode serve  # 服務狀態（install/uninstall/start/stop/status）
cockpit version
```

- **加軟體追蹤**：管理頁「新增軟體」（來源：npm / github / pypi / brew / claude-plugin / custom）
- **更新軟體**：清單頁「更新」→ 即時 log 串流、可中止；完成後自動驗證新版本
- **重新檢查**：清單頁「立即檢查」

---

## 進階：手動設定

不想用 `setup` 一鍵流程時，可自行管理設定檔。

### setup 參數

```sh
sudo cockpit setup serve [-listen 0.0.0.0:8787] [-dir /etc/cockpit] [-data /var/lib/cockpit] [-no-service]
sudo cockpit setup agent -server <url> -token <enroll_token> [-no-service]
```

非 root 且無權限寫 `/etc` 時，自動 fallback 到 `~/.config/cockpit` 與 `~/.local/share/cockpit`。

### serve.json

```json
{
  "listen": "0.0.0.0:8787",
  "db_path": "/var/lib/cockpit/cockpit.db",
  "enroll_secret": "your-shared-secret",
  "inventory_path": "/etc/cockpit/inventory.yaml",
  "check_hours": 24
}
```

> **注意**：SQLite db 檔請固定由同一使用者存取——以 root（系統服務）執行時，不要沿用先前以一般使用者手動執行所建立的 db/-wal/-shm 檔（會得到 `attempt to write a readonly database`）。換執行身份時請換 db 路徑或刪除舊 db。

### agent.json

```json
{
  "server_url": "http://your-serve-host:8787",
  "enroll_token": "one-time-token-from-manage-page",
  "heartbeat_sec": 15
}
```

`enroll_token`（每機一次性，管理頁產生）與 `enroll_secret`（共享密鑰，serve.json 內）擇一即可；enroll 成功後 `agent_token` 自動寫回此檔。

### 前景執行（除錯用）

```sh
cockpit serve -config /etc/cockpit/serve.json
cockpit agent -config /etc/cockpit/agent.json
```

### 手動服務化

```sh
sudo cockpit service install -mode serve -config /etc/cockpit/serve.json
sudo cockpit service start   -mode serve

sudo cockpit service install -mode agent -config /etc/cockpit/agent.json
sudo cockpit service start   -mode agent
```

---

## inventory.yaml 範例

軟體定義可全部從管理頁 UI 操作（會自動回寫此檔）；也可直接手寫：

```yaml
machines: {}   # 機器由 UI / DB 管理；此區段為 legacy，可留空

software:
  - name: claude-code
    kind: npm
    latest_source: "npm:@anthropic-ai/claude-code"
    changelog: "github:anthropics/claude-code"
    installs:
      - machine: mac            # 對應 UI 中的機器名稱（system label）
        current_cmd: "claude --version"
        update:
          type: command
          cmd: "npm i -g @anthropic-ai/claude-code@latest"
  - name: my-docker-app
    kind: custom
    latest_source: "github:owner/repo"
    installs:
      - machine: nas
        current_cmd: "docker inspect --format '{{.Config.Image}}' my-app"
        version_regex: "my-app:([0-9.]+)"
        update:
          type: agent           # 委派 AI agent 執行多步更新
          runner: codex_exec    # codex_exec | claude_p | custom
          cwd: /srv/my-app
          prompt: "把 my-app 更新到 {latest_version}，更新 compose 檔並重啟，驗證健康檢查通過"
```

---

## 軟體追蹤配置心法（工具鏈環境）

> 💡 用 Claude Code 的話：repo 內建 `track-software` skill（`.claude/skills/track-software/`），
> 直接說「幫我在某台機器追蹤某軟體」即可走完偵測 → 驗證 → 建立 → 驗收的完整流程。

agent 以系統服務（launchd/systemd）執行指令時，環境跟你的互動 shell **不同**：沒有
`.zshrc`/homebrew PATH、沒有 `PNPM_HOME` 等變數。配置 `current_cmd` 與更新指令時依安裝方式套用：

| 安裝方式 | current_cmd / 更新指令寫法 | 原因 |
|---|---|---|
| 原生安裝（如 claude-code） | 用**絕對路徑**：`/Users/<u>/.local/bin/claude --version` | 服務環境 PATH 沒有 `~/.local/bin`；勿用 `$HOME`（服務可能以其他身份跑） |
| npm 全域（如 codex） | 前置 PATH：`PATH=/opt/homebrew/bin:$PATH /opt/homebrew/bin/codex --version` | npm launcher 的 shebang 是 `env node`，服務環境找不到 node |
| pnpm 全域（如 openspec） | 再加 PNPM_HOME：`PNPM_HOME=~/Library/pnpm PATH=~/Library/pnpm:/opt/homebrew/bin:$PATH pnpm add -g <pkg>@latest` | pnpm 全域操作需要 `PNPM_HOME`，否則 `ERR_PNPM_NO_GLOBAL_BIN_DIR` |
| uv tool（如 headroom） | 絕對路徑即可：`/Users/<u>/.local/bin/headroom --version`；更新 `/Users/<u>/.local/bin/uv tool upgrade <pkg>` | uv shim 自帶直譯器路徑 |
| brew formula | `/opt/homebrew/bin/<bin> --version`；更新 `/opt/homebrew/bin/brew upgrade <formula>` | 同 PATH 原因 |

實用驗證法：寫好指令先用**乾淨環境**模擬 agent 跑一次——

```sh
env -i HOME=$HOME bash -lc '<你的指令>'
```

跑得過才寫進配置。其它備註：

- changelog 來源 `github:owner/repo` 會自動嘗試 `v<版本>`、`<版本>`，再 fallback 掃 release 清單比對 tag 內含版本字串（涵蓋 `rust-vX.Y.Z` 等非常規命名）
- 服務以 root 跑時，使用者層工具（claude/codex 憑證、keychain）多半不可用——建議服務以一般使用者身份執行（macOS plist 加 `UserName`），並執行 `cockpit doctor` 體檢環境
- **root daemon 降權執行**（agent 是 root、但更新要以一般使用者跑，避免 root 污染 homebrew/家目錄）：用
  `sudo -u <user> -H bash -lc '<指令>'`，**不要用 `su - <user> -c`**——macOS 的 `su` 在無 TTY 的
  daemon context 會被 PAM 拒絕（log 只見 `su: Sorry`）。Linux 同理可用 `runuser -l <user> -c '<指令>'`
- 更新會重啟服務的場景：macOS LaunchAgent 用 `launchctl kickstart -k gui/<uid>/<label>`（先用
  `launchctl print gui/<uid>/<label>` 確認網域）；Linux user service 由 root 重啟用
  `systemctl --machine <user>@.host --user restart <unit>`

## 開發

```sh
go test ./...        # 全部測試
go vet ./...         # 靜態分析

# 跨平台建置矩陣（darwin/linux/windows × amd64/arm64）
for os in darwin linux windows; do for arch in amd64 arm64; do
  echo "== $os/$arch"; GOOS=$os GOARCH=$arch go build ./... ; done; done

goreleaser build --snapshot --clean   # 本地 release 預覽
```

## 文件

- 設計規格：[`docs/specs/`](docs/specs/)
- 實作計畫：[`docs/plans/`](docs/plans/)
