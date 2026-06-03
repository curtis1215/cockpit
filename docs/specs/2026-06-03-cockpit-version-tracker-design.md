# Cockpit — 設計規格：軟體版本追蹤器（子系統 2）

- **日期**：2026-06-03
- **狀態**：設計已核可，待轉交 writing-plans
- **專案**：`cockpit`（新獨立專案，`/Users/curtis/Dev/cockpit`）
- **本 spec 範圍**：共用骨架 + 子系統 2（軟體版本追蹤器）。子系統 1（設備監控）另立 spec。

---

## 1. 背景與動機

使用者管理多台機器（mac mini、ubuntu_llm、gcp、其它雲 VPS）與大量軟體（本地安裝、CLI、Claude Code plugin）。兩個長期痛點：

1. **看不到全局**：缺一個介面同時看設備拓樸、設備狀態、服務狀態。
2. **版本追蹤靠手動**：反覆手動查某軟體有沒有新版、新版改了什麼（還要翻成中文）、然後手動更新。mem0 歷史顯示「查 Claude Code 版本 + 翻譯 changelog」這件事已被手動重複執行多次。

`cockpit` 是一個自架於 mac mini、透過 Cloudflare Tunnel 私有存取的控制台，解決上述兩件事。

## 2. 範圍與分解

整體拆成兩個**獨立子系統**，共用一層骨架：

| 子系統 | 內容 | 狀態 |
|---|---|---|
| 1. 設備監控 | 拓樸圖 + 設備狀態 + 服務狀態 | **另立 spec**（用 Beszel + 自建拓樸圖） |
| 2. 軟體版本追蹤 | 版本追蹤 + changelog 翻譯 + 更新觸發 | **本 spec 主體** |

每個子系統各自走 spec → plan → 實作 的循環。本次先做子系統 2（重複手動最多、且無現成工具）。

## 3. 共用骨架

| 地基 | 選擇 | 理由 |
|---|---|---|
| 部署主機 | mac mini `100.106.177.80` | 常開、已在 Tailscale、已跑 OrbStack、磁碟餘裕充足 |
| 人的存取面 | **Cloudflare Tunnel + Cloudflare Access** | 任何瀏覽器可上；origin 不開公網 port |
| └ 信任 IP | Access **Bypass** policy（依來源 IP） | 名單內直接放行、免登入 |
| └ 名單外 IP | Access **Allow**（Email OTP / Google 登入） | 浮動 IP / 在外也能進，多一道關 |
| 內部採集面 | **Tailscale**（agent 遙測、SSH 探測） | 遙測與遠端執行不上公網 |
| 資料儲存 | SQLite（單檔） | 狀態/版本歷史量小 |
| 通知 / 觸發 | Telegram Bot | 更新提醒與確認觸發 |

兩個運行件（刻意精簡，無多餘聚合著陸頁）：

1. **Beszel Hub + 各主機 agent** — 子系統 1 的設備/服務健康（現成 UI）。
2. **Cockpit App（自建）** — 子系統 2 版本追蹤器；未來加入子系統 1 的拓樸圖 view。

### 架構圖

```
   人的存取面（公開但受控）                 內部採集面（私有）
   任何瀏覽器/手機/設備                  Beszel agent ─Tailscale─► Beszel Hub
        │ https
        ▼
   Cloudflare Edge
   └ Access policy:
     ├ Bypass ← 信任 IP 名單（免登入）
     └ Allow  ← 名單外（Email/Google 登入）
        │
        ▼ Cloudflare Tunnel（cloudflared @ mac mini）
   ┌────┴──────────────┐
   ▼                   ▼
 beszel.<domain>     cockpit.<domain>
 設備健康(子系統1)    版本追蹤器(子系統2) + 拓樸圖(子系統1，後續)
```

> `<domain>`：部署時填入（候選 `sitruc.co`，使用者已有該域名在 Cloudflare）。
> 一條 cloudflared tunnel 路由多個 hostname。

## 4. 子系統 2：軟體版本追蹤器 — 詳細設計

### 4.1 資料模型 — YAML 清單（單一事實來源）

機器與軟體分開宣告。每個軟體宣告「裝在哪些機器、怎麼讀目前版、怎麼更新」。此檔進 git 版控。

```yaml
# inventory.yaml
machines:
  mac:        { host: 100.106.177.80, ssh_user: curtis, local: true }
  ubuntu_llm: { host: <tailscale-ip>, ssh_user: curtis }
  gcp:        { host: <tailscale-ip>, ssh_user: curtis }
  vps_tokyo:  { host: <tailscale-ip>, ssh_user: root }

software:
  - name: claude-code
    kind: npm                                    # npm|github|pypi|brew|claude-plugin|custom
    latest_source: "npm:@anthropic-ai/claude-code"
    changelog:     "github:anthropics/claude-code"   # 取 releases body
    installs:
      - machine: mac
        current_cmd: "claude --version"
        update_cmd:  "npm i -g @anthropic-ai/claude-code@latest"
      - machine: ubuntu_llm
        current_cmd: "claude --version"
        update_cmd:  "npm i -g @anthropic-ai/claude-code@latest"

  - name: super-telegram
    kind: claude-plugin
    latest_source: "github:curtis1215/super-telegram-plugin"   # 取最新 tag/release
    changelog:     "github:curtis1215/super-telegram-plugin"
    installs:
      - machine: mac
        current_cmd: "claude plugin list"
        update_cmd:  "claude plugin update super-telegram"
```

**設計理由**：異質性全部收進宣告；collector 邏輯保持通用，新增軟體只動 YAML。

### 4.2 SQLite 資料表

| 表 | 欄位（重點） | 用途 |
|---|---|---|
| `software` | id, name, kind, latest_source, changelog_source | 軟體定義 |
| `installs` | id, software_id, machine, current_version, last_checked, status | 每台機器目前安裝版 |
| `versions` | id, software_id, version, released_at, changelog_raw, changelog_zh, fetched_at | 上游版本歷史 |
| `events` | id, ts, type, software_id, machine, detail, exit_code, stdout | 稽核（檢查/更新動作與結果） |

`installs.status` 枚舉：`up_to_date` / `behind` / `unknown` / `error`。

### 4.3 採集流程（排程，預設每日；可在 config 調整）

排程器：FastAPI 進程內的 **APScheduler**（單進程、免額外 cron）。

```
排程觸發
  → 對每個 software：抓上游最新版（依 latest_source）+ changelog（依 changelog）
  → 對每個 install：到該機器跑 current_cmd → 解析目前版
       · local:true → 本地直接執行
       · 其餘 → SSH over Tailscale 執行
  → 比對 current vs latest：
       · current < latest ⇒ status=behind，標記「有更新」
  → 對「新出現的版本」翻譯 changelog → 存 changelog_zh
  → 對「有更新」的 install 推 Telegram 提醒（含中文摘要）
```

### 4.4 版本來源解析（依 kind）

| kind | 最新版來源 | changelog 來源 |
|---|---|---|
| `npm` | npm registry API（`/<pkg>` 的 dist-tags.latest） | 對應 GitHub releases |
| `github` | GitHub releases/tags API（最新 release tag） | release body |
| `pypi` | PyPI JSON API（`/pypi/<pkg>/json`） | GitHub releases 或 release body |
| `brew` | `brew info --json` 或 formulae API | formula 頁 / GitHub |
| `claude-plugin` | plugin 來源 repo 的最新 tag/release | 該 repo releases |
| `custom` | YAML 內自訂 `latest_cmd` 的輸出 | YAML 內自訂 URL |

- 目前版解析：對 `current_cmd` 的 stdout 套用每 kind 的版本擷取（正則抓 semver）；必要時 YAML 可覆寫 `version_regex`。
- 對外 HTTP 用 `httpx`；GitHub API 帶 token（避免 rate limit），token 從 1Password 取。

### 4.5 changelog 翻譯

- **引擎**：mac 上 headless `claude -p`（使用既有 Claude Code 登入，免額外 API key / 免成本）。
- **呼叫**：`claude -p "<翻譯指令 + changelog 原文>"`，逾時保護，失敗則保留原文並標記 `changelog_zh = null`。
- **產出**：繁體中文**重點摘要 + 條列重要變更**（非逐字硬翻）；原文與中文都存入 `versions`。
- **冪等**：同一 `(software, version)` 已翻譯過就不重翻。

### 4.6 更新觸發流程（安全是重點）

```
Telegram 推播（inline keyboard）：
  「claude-code 有新版 2.1.77 → 2.1.80（mac）
   <中文 changelog 摘要…>      [✅確認更新] [⏭️略過]」
  → 使用者按 ✅（callback_data = 動作 token，非指令本身）
  → Hub 用動作 token 反查 DB/YAML 中宣告好的 update_cmd
  → 在目標機器執行 update_cmd（local 或 SSH over Tailscale）
  → 擷取 stdout/exit_code → 重讀 current_cmd 確認新版 → 寫入 events
  → 結果回報 Telegram（成功新版號 / 失敗輸出）
```

**安全護欄**：

1. Telegram 只傳**動作 token**；實際執行的指令一律來自版控 YAML 的 `update_cmd` — 杜絕從訊息注入任意指令。
2. 每次遠端執行進 `events` 稽核，回顯 exit code 與 stdout 摘要。
3. 動作 token 有時效與單次使用，避免重放。
4. 遠端 SSH 優先**金鑰認證**（mac mini → 各機器）；fallback 用 1Password 取密碼經 `sshpass`。
5. Telegram Bot 建議用**專屬 cockpit bot token**（與 relaydeck/super-telegram 的 router 分離，避免互搶 update）；採 long polling，無需對外 webhook。

### 4.7 UI — Cockpit 版本追蹤 view

- 技術：FastAPI + Jinja2 + **htmx** + Tailwind（極簡、少 JS；適合自架小工具）。
- 主表格：`軟體 | 機器 | 目前版 | 最新版 | 狀態(最新/落後N版) | changelog(中文) | [更新]`
- 篩選：依機器、依「有更新」。
- 點 changelog → modal 顯示**中文摘要 + 原文**。
- `[更新]` → 觸發與 Telegram 相同的確認流程（或顯示「已推送至 Telegram 待確認」）。
- 手動「立即檢查」按鈕：即時跑一次採集。

## 5. 技術選型總覽

| 面向 | 選擇 |
|---|---|
| 語言 / 框架 | Python + FastAPI |
| 排程 | APScheduler（進程內） |
| HTTP client | httpx |
| 遠端執行 | 本地 subprocess；遠端 paramiko/SSH over Tailscale（金鑰優先，sshpass fallback） |
| 翻譯 | headless `claude -p` |
| DB | SQLite |
| 前端 | Jinja2 + htmx + Tailwind |
| Telegram | 專屬 bot，Bot API + inline keyboard，long polling |
| 部署 | OrbStack 容器或 launchd 常駐 @ mac mini；cloudflared tunnel 對外 |
| Secrets | 1Password（GitHub token、SSH 密碼 fallback、bot token） |

## 6. 部署概要

1. `cloudflared` 在 mac mini 建立 tunnel，路由 `cockpit.<domain>`（與未來 `beszel.<domain>`）。
2. Cloudflare Access 設兩條 policy：Bypass（信任 IP 名單）+ Allow（Email/Google）。
3. Cockpit App 以容器或 launchd 常駐，掛載 `inventory.yaml` 與 SQLite。
4. SSH 金鑰：mac mini → 各機器免密碼登入（自動更新前置條件）。

## 7. 安全考量彙整

- origin 不開公網 port；對外僅經 Cloudflare Tunnel + Access。
- 遠端可執行指令**僅限**版控 YAML 宣告者，Telegram 不傳原始指令。
- 全部遠端執行稽核留底。
- 含機器 IP / 基礎設施細節 → **GitHub repo 設為 private**。

## 8. 後續（不在本 spec）

- 子系統 1：Beszel 部署 + Tailscale 拓樸圖 view（另立 spec）。
- 可能的擴展：per-item `auto_update: true`（免確認的安全項）、版本落後嚴重度告警、自動探索候選軟體以協助維護 YAML。

## 9. 驗收標準（子系統 2）

- 能從 `inventory.yaml` 載入多機器多軟體定義。
- 排程能跨機器讀目前版、抓上游最新版、正確標記「有更新」。
- 有更新時推 Telegram，附**繁體中文 changelog 摘要**。
- Telegram 按確認後，能在正確機器安全執行宣告好的 update_cmd，並回報結果、更新 DB。
- Web view 正確呈現狀態並可篩選、可看中文 changelog、可手動觸發檢查。
