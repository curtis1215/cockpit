# Cockpit 部署（mac mini）

## 1. 安裝
```bash
cd /Users/curtis/Dev/cockpit
python3 -m venv .venv && . .venv/bin/activate && pip install -e .
cp inventory.example.yaml inventory.yaml   # 填入真實機器/軟體
```

## 2. 環境變數
- `COCKPIT_DB_PATH`（預設 cockpit.db）
- `COCKPIT_INVENTORY`（預設 inventory.yaml）
- `COCKPIT_CHECK_HOURS`（預設 24）
- `COCKPIT_GITHUB_TOKEN`（避免 GitHub API rate limit；可由 1Password 注入）

## 3. 啟動
```bash
COCKPIT_INVENTORY=inventory.yaml python -m cockpit.main   # 監聽 127.0.0.1:8787
```
建議以 launchd 常駐；前端產物放 `cockpit/web/static/`。

## 4. 對外（Cloudflare Tunnel + Access）
- `cloudflared` 在 mac mini 建 tunnel，路由 `cockpit.<domain>` → `http://127.0.0.1:8787`。
- Cloudflare Access：Bypass policy（信任 IP 名單）+ Allow policy（Email/Google 登入）。
- origin 不開公網 port。

## 5. SSH 前置
mac mini → 各機器設定金鑰免密碼登入；agent 型更新需目標機器上 `codex` / `claude` CLI 已登入。

## 6. cockpit-agent（每台機器）

每台機器跑一隻 `cockpit-agent`（取代獨立 beszel service，改由 agent 督管 beszel-agent）。

```bash
# 在開發機 build（或交叉編譯 GOOS/GOARCH）
cd agent && go build -o cockpit-agent .
# 部署到目標機
scp cockpit-agent target:/usr/local/bin/cockpit-agent
# 設定（每機唯一 agent_token，需同時寫入 server 真實 inventory.yaml 該機的 agent_token）
sudo mkdir -p /etc/cockpit-agent && sudo cp agent/deploy/config.example.json /etc/cockpit-agent/config.json && sudo vi /etc/cockpit-agent/config.json
# Linux: systemd
sudo cp agent/deploy/cockpit-agent.service /etc/systemd/system/ && sudo systemctl enable --now cockpit-agent
# macOS: launchd
sudo cp agent/deploy/cockpit-agent.plist /Library/LaunchDaemons/co.sitruc.cockpit-agent.plist && sudo launchctl load /Library/LaunchDaemons/co.sitruc.cockpit-agent.plist
```

- Cloudflare：`/api/agent/*` 設 Access **Bypass**（agent 以 app 層 Bearer token 把關），其餘路徑維持 Bypass(信任IP)/Allow(登入)。
- 架構改為 agent 主動 outbound HTTPS（CF tunnel），**不再使用 Tailscale**。
- 首次升級需刪舊 `cockpit.db`（schema 加欄位）。
