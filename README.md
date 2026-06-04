# cockpit

**Homelab 控制平面** — 單一 binary，整合監控、版本追蹤與拓樸管理。

- **機器監控**：CPU、記憶體、磁碟、GPU、網路流量、溫度 — 透過輕量 agent 回傳至中央伺服器，Web UI 即時顯示拓樸狀態
- **版本追蹤**：跨機器追蹤軟體版本、翻譯 changelog（繁體中文）、在 Web UI 觸發更新（指令或委派 AI agent 執行多步任務），串流即時 log
- **服務化**：支援 launchd（macOS）與 systemd（Linux），一行指令完成開機自啟

> ⚠️ 本 repo 含 homelab 基礎設施細節，維持 **private**。

---

## 一行安裝

```sh
curl -fsSL https://raw.githubusercontent.com/curtis1215/cockpit/main/install.sh | sh
```

安裝後確認：

```sh
cockpit version
```

---

## 快速開始

### 1. 伺服器（serve）

建立設定檔 `/etc/cockpit/serve.json`（最小範例）：

```json
{
  "listen": "127.0.0.1:8787",
  "db_path": "/var/lib/cockpit/cockpit.db",
  "enroll_secret": "your-shared-secret",
  "inventory_path": "/etc/cockpit/inventory.yaml"
}
```

> **注意**：SQLite db 檔請固定由同一使用者存取——以 root（systemd 服務）執行時，不要沿用先前以一般使用者手動執行所建立的 db/-wal/-shm 檔（會得到 `attempt to write a readonly database`）。換執行身份時請換 db 路徑或先刪除舊 db。

- `enroll_secret`：agent 申請 token 時使用的共享密鑰，`inventory_path` 指定機器清單（每台機器取得 `agent_token` 後寫入此檔）

啟動（前景）：

```sh
cockpit serve -config /etc/cockpit/serve.json
```

### 2. Agent（agent）

建立設定檔 `/etc/cockpit/agent.json`（最小範例）：

```json
{
  "server_url": "http://your-serve-host:8787",
  "enroll_token": "one-time-token-from-manage-page",
  "heartbeat_sec": 15
}
```

- `enroll_token`：在 serve 管理頁面為每台機器產生的一次性 token；首次連線後自動換取 `agent_token` 並寫回 config

啟動（前景）：

```sh
cockpit serve -mode agent -config /etc/cockpit/agent.json
```

### 3. 安裝為系統服務

```sh
# 安裝並啟動 serve
sudo cockpit service install -mode serve -config /etc/cockpit/serve.json
sudo cockpit service start

# 安裝並啟動 agent（各被監控機器）
sudo cockpit service install -mode agent -config /etc/cockpit/agent.json
sudo cockpit service start
```

支援 macOS（launchd）與 Linux（systemd），自動選擇。

### 4. 自動更新

```sh
cockpit upgrade
```

從 GitHub Releases 檢查並下載最新版本，原子替換執行檔。已是最新版時安全退出。

---

## inventory.yaml 範例

```yaml
machines:
  mac:
    host: 192.168.1.10
    ssh_user: curtis
    local: true

software:
  - name: claude-code
    kind: npm
    latest_source: "npm:@anthropic-ai/claude-code"
    changelog: "github:anthropics/claude-code"
    installs:
      - machine: mac
        current_cmd: "claude --version"
        update:
          type: command
          cmd: "npm i -g @anthropic-ai/claude-code@latest"
```

---

## 開發

```sh
# 執行所有測試
go test ./...

# 靜態分析
go vet ./...

# 跨平台建置矩陣（6 個 target）
for os in darwin linux windows; do
  for arch in amd64 arm64; do
    echo "== $os/$arch"
    GOOS=$os GOARCH=$arch go build ./...
  done
done

# 本地 release 預覽（需安裝 goreleaser）
goreleaser build --snapshot --clean
```

---

## 文件

- 設計規格：[`docs/specs/`](docs/specs/)
- 實作計畫：[`docs/plans/`](docs/plans/)
