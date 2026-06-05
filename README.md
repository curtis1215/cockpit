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
