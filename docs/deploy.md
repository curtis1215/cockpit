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
