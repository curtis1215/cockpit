# Cockpit — 設計規格：軟體版本追蹤器（子系統 2）

- **日期**：2026-06-03
- **狀態**：設計已核可，待轉交 writing-plans
- **專案**：`cockpit`（新獨立專案，`/Users/curtis/Dev/cockpit`）
- **本 spec 範圍**：共用骨架 + 子系統 2（軟體版本追蹤器）。子系統 1（設備監控）另立 spec。

---

## 1. 背景與動機

使用者管理多台機器（mac mini、ubuntu_llm、gcp、其它雲 VPS）與大量軟體（本地安裝、CLI、Claude Code plugin、容器化專案）。兩個長期痛點：

1. **看不到全局**：缺一個介面同時看設備拓樸、設備狀態、服務狀態。
2. **版本追蹤靠手動**：反覆手動查某軟體有沒有新版、新版改了什麼（還要翻成中文）、然後手動更新。簡單的（npm/brew/plugin）一條指令搞定；複雜的（如 multica：同步上游 → 重 build 鏡像 → 重新部署）目前是丟給 Codex/Claude agent 執行。

`cockpit` 是一個自架於 mac mini、透過 Cloudflare Tunnel 私有存取的控制台，所有操作在 Web UI 內完成。

## 2. 範圍與分解

整體拆成兩個**獨立子系統**，共用一層骨架：

| 子系統 | 內容 | 狀態 |
|---|---|---|
| 1. 設備監控 | 拓樸圖 + 設備狀態 + 服務狀態 | **另立 spec**（用 Beszel + 自建拓樸圖） |
| 2. 軟體版本追蹤 | 版本追蹤 + changelog 翻譯 + 更新觸發（含 agent 驅動） | **本 spec 主體** |

每個子系統各自走 spec → plan → 實作 的循環。本次先做子系統 2。

## 3. 共用骨架

| 地基 | 選擇 | 理由 |
|---|---|---|
| 部署主機 | mac mini `100.106.177.80` | 常開、已在 Tailscale、已跑 OrbStack |
| 人的存取面 | **Cloudflare Tunnel + Cloudflare Access** | 任何瀏覽器可上；origin 不開公網 port |
| └ 信任 IP | Access **Bypass** policy（依來源 IP） | 名單內直接放行、免登入 |
| └ 名單外 IP | Access **Allow**（Email OTP / Google 登入） | 浮動 IP / 在外也能進，多一道關 |
| 內部採集面 | **Tailscale**（agent 遙測、SSH 探測與遠端執行） | 不上公網 |
| 資料儲存 | SQLite（單檔） | 狀態/版本歷史/job 量小 |
| 互動模型 | **全部在 Web UI**（無 Telegram） | 偵測→列出→點按更新→即時看 log→完成，授權靠 Cloudflare Access |

兩個運行件（刻意精簡）：

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
                         │ 採集/更新job
                         ▼ local / SSH over Tailscale
                     各機器（讀版本、跑 update_cmd 或 agent）
```

> `<domain>`：部署時填入（候選 `sitruc.co`，使用者已有該域名在 Cloudflare）。一條 cloudflared tunnel 路由多個 hostname。

## 4. 子系統 2：軟體版本追蹤器 — 詳細設計

### 4.1 資料模型 — YAML 清單（單一事實來源）

機器與軟體分開宣告。每個 install 宣告「怎麼讀目前版、怎麼更新」。更新有兩種型態：`command`（簡單）與 `agent`（委派 Codex/Claude agent 做多步任務）。此檔進 git 版控。

```yaml
# inventory.yaml
machines:
  mac:        { host: 100.106.177.80, ssh_user: curtis, local: true }
  ubuntu_llm: { host: <tailscale-ip>, ssh_user: curtis }
  gcp:        { host: <tailscale-ip>, ssh_user: curtis }
  vps_tokyo:  { host: <tailscale-ip>, ssh_user: root }

software:
  # --- 簡單更新：一條指令 ---
  - name: claude-code
    kind: npm                                    # npm|github|pypi|brew|claude-plugin|custom
    latest_source: "npm:@anthropic-ai/claude-code"
    changelog:     "github:anthropics/claude-code"
    installs:
      - machine: mac
        current_cmd: "claude --version"
        update: { type: command, cmd: "npm i -g @anthropic-ai/claude-code@latest" }
      - machine: ubuntu_llm
        current_cmd: "claude --version"
        update: { type: command, cmd: "npm i -g @anthropic-ai/claude-code@latest" }

  # --- agent 驅動更新：多步任務委派 agent ---
  - name: multica
    kind: custom
    latest_source: "github:<upstream>/multica"     # 上游最新 tag/release
    changelog:     "github:<upstream>/multica"
    installs:
      - machine: macmini
        current_cmd: "docker inspect multica --format '{{ index .Config.Labels \"version\" }}'"
        update:
          type: agent
          runner: codex_exec        # codex_exec | claude_p | custom
          machine: macmini          # agent 在哪台跑（預設同 install.machine）
          cwd: "/path/to/multica"   # 專案目錄，給 agent 上下文
          # custom runner 才需要；codex_exec/claude_p 有內建模板：
          #   codex_exec → codex exec --cd {cwd} "{prompt}"
          #   claude_p   → claude -p "{prompt}"（於 {cwd} 執行）
          invoke: "codex exec --cd {cwd} \"{prompt}\""
          prompt: |
            multica 上游有新版 {latest_version}（目前 {current_version}）。請：
            1. 同步上游到最新
            2. 重新 build 鏡像
            3. 重新部署容器
            完成後回報新版本號與部署結果。
```

- prompt / invoke 可用變數：`{name} {machine} {current_version} {latest_version} {changelog_zh} {cwd}`。
- `runner` 與 `prompt`/`cmd` 全部來自版控 YAML — 是安全邊界（見 4.6）。

**設計理由**：異質性（簡單指令 vs 多步 agent 任務、不同機器/目錄）全收進宣告；執行引擎保持通用，新增軟體只動 YAML。

### 4.2 SQLite 資料表

| 表 | 欄位（重點） | 用途 |
|---|---|---|
| `software` | id, name, kind, latest_source, changelog_source | 軟體定義 |
| `installs` | id, software_id, machine, current_version, last_checked, status | 每台機器目前安裝版 |
| `versions` | id, software_id, version, released_at, changelog_raw, changelog_zh, fetched_at | 上游版本歷史 |
| `jobs` | id, install_id, kind(command/agent), status, started_at, finished_at, exit_code, log | 更新工作（含即時 log） |
| `events` | id, ts, type, software_id, machine, detail | 稽核（檢查/更新動作） |

- `installs.status`：`up_to_date` / `behind` / `unknown` / `error`。
- `jobs.status`：`queued` / `running` / `success` / `failed`。
- `jobs.log`：增量追加，供 UI 即時串流。

### 4.3 採集流程（排程，預設每日；可在 config 調整）

排程器：FastAPI 進程內的 **APScheduler**（單進程、免額外 cron）。

```
排程觸發（或 UI 手動「立即檢查」）
  → 對每個 software：抓上游最新版（依 latest_source）+ changelog（依 changelog）
  → 對每個 install：到該機器跑 current_cmd → 解析目前版
       · local:true → 本地直接執行
       · 其餘 → SSH over Tailscale 執行
  → 比對 current vs latest：current < latest ⇒ status=behind
  → 對「新出現的版本」翻譯 changelog → 存 changelog_zh
  → 結果寫入 DB（不發任何外部通知；使用者開 Web UI 查看）
```

### 4.4 版本來源解析（依 kind）

| kind | 最新版來源 | changelog 來源 |
|---|---|---|
| `npm` | npm registry API（dist-tags.latest） | 對應 GitHub releases |
| `github` | GitHub releases/tags API（最新 release tag） | release body |
| `pypi` | PyPI JSON API | GitHub releases |
| `brew` | `brew info --json` 或 formulae API | formula / GitHub |
| `claude-plugin` | plugin 來源 repo 最新 tag/release | 該 repo releases |
| `custom` | YAML 內自訂 `latest_cmd` 輸出（如 docker 鏡像 tag） | YAML 內自訂 URL |

- 目前版解析：對 `current_cmd` stdout 套用每 kind 的版本擷取（正則抓 semver）；YAML 可覆寫 `version_regex`。
- 對外 HTTP 用 `httpx`；GitHub API 帶 token（避免 rate limit），token 從 1Password 取。

### 4.5 changelog 翻譯

- **引擎**：mac 上 headless `claude -p`（用既有 Claude Code 登入，免額外 API key / 免成本）。
- **產出**：繁體中文**重點摘要 + 條列重要變更**；原文與中文都存入 `versions`。
- **冪等**：同一 `(software, version)` 已翻譯過就不重翻；失敗保留原文、`changelog_zh = null`。

### 4.6 更新執行流程（Web UI 內，job 模型）

```
Web UI 列出「有更新」項 → 使用者點 [更新]
  → cockpit 建立 job（依 install.update.type）
  → 背景執行：
       · type=command → 在目標機器跑 cmd（local 或 SSH over Tailscale）
       · type=agent   → 渲染 prompt 變數 → 依 runner 呼叫
                         （codex_exec / claude_p / custom invoke 模板），帶 cwd/machine
  → stdout/stderr 增量寫入 jobs.log，UI 透過 SSE 即時串流（多分鐘任務可盯著看）
  → 完成：重讀 current_cmd 確認新版 → 更新 installs → job 標記 success/failed → 寫 events
```

**安全護欄**：

1. **授權**：整個 Web UI 擋在 Cloudflare Access 之後（信任 IP bypass / 其餘登入）。點 `[更新]` 即等同已授權，無需額外確認 token。
2. **可執行內容封閉**：UI 只能觸發某個 install 的**宣告好的** `cmd` / `runner`+`prompt`（全來自版控 YAML）；使用者**無法在 UI 自由輸入指令** — 杜絕注入。
3. **稽核**：每個 job 的指令、stdout、exit code 都留在 `jobs` / `events`。
4. **遠端連線**：SSH 優先**金鑰認證**（mac mini → 各機器）；fallback 用 1Password 取密碼經 `sshpass`。
5. **併發**：同一 install 同時只允許一個 running job，避免重複更新。

### 4.7 UI — Cockpit 版本追蹤 view

- 技術：FastAPI + Jinja2 + **htmx**（含 SSE extension）+ Tailwind。極簡、少 JS。
- **主表格**：`軟體 | 機器 | 目前版 | 最新版 | 狀態(最新/落後N版) | changelog(中文) | [更新]`
  - 篩選：依機器、依「有更新」。
  - 點 changelog → modal 顯示**中文摘要 + 原文**。
  - `[更新]` → 建立 job 並切到該 job 的即時 log。
- **執行中工作面板**：列出 running/最近 jobs，點開看**即時串流 log**（SSE tail）與最終狀態。
- **手動操作**：「立即檢查」按鈕即時跑一次採集。

## 5. 技術選型總覽

| 面向 | 選擇 |
|---|---|
| 語言 / 框架 | Python + FastAPI |
| 排程 | APScheduler（進程內） |
| HTTP client | httpx |
| 遠端執行 | 本地 subprocess；遠端 paramiko/SSH over Tailscale（金鑰優先，sshpass fallback） |
| Agent 呼叫 | `codex exec` / `claude -p` / 自訂 invoke 模板（machine + cwd 可自訂） |
| 翻譯 | headless `claude -p` |
| DB | SQLite |
| 前端 | Jinja2 + htmx（+ SSE extension）+ Tailwind |
| 即時 log | SSE（sse-starlette），jobs.log 增量串流 |
| 部署 | OrbStack 容器或 launchd 常駐 @ mac mini；cloudflared tunnel 對外 |
| Secrets | 1Password（GitHub token、SSH 密碼 fallback） |

## 6. 部署概要

1. `cloudflared` 在 mac mini 建立 tunnel，路由 `cockpit.<domain>`（與未來 `beszel.<domain>`）。
2. Cloudflare Access 設兩條 policy：Bypass（信任 IP 名單）+ Allow（Email/Google）。
3. Cockpit App 以容器或 launchd 常駐，掛載 `inventory.yaml` 與 SQLite。
4. SSH 金鑰：mac mini → 各機器免密碼登入（遠端執行/更新前置條件）。
5. agent 更新前置：目標機器上 `codex` / `claude` CLI 已登入可用。

## 7. 安全考量彙整

- origin 不開公網 port；對外僅經 Cloudflare Tunnel + Access。
- Web UI 只能觸發**版控 YAML 宣告好的**指令 / agent prompt，使用者不能自由輸入指令。
- 全部遠端執行（含 agent 任務）稽核留底於 `jobs` / `events`。
- 含機器 IP / 基礎設施細節 → **GitHub repo 設為 private**。

## 8. 後續（不在本 spec）

- 子系統 1：Beszel 部署 + Tailscale 拓樸圖 view（另立 spec）。
- 可能擴展：per-item `auto_update: true`（免點擊的安全項）、落後嚴重度標示、自動探索候選軟體以協助維護 YAML。

## 9. 驗收標準（子系統 2）

- 能從 `inventory.yaml` 載入多機器多軟體定義（含 command 與 agent 兩種 update）。
- 排程能跨機器讀目前版、抓上游最新版、正確標記「有更新」。
- 偵測到新版時翻出**繁體中文 changelog 摘要**並存檔。
- Web UI 正確呈現狀態、可篩選、可看中文 changelog、可手動觸發檢查。
- 點 `[更新]` 能建立 job：
  - `command` 型在正確機器執行宣告好的指令；
  - `agent` 型以 `codex exec` / `claude -p` 在指定 machine/cwd 執行渲染後的 prompt。
- job 的 log 能在 UI **即時串流**，完成後更新版本、標記成敗、寫稽核。
