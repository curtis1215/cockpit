# Cockpit — 設計規格：Agent Daemon（子系統 2 執行層重設計）

- **日期**：2026-06-03
- **狀態**：設計待核可（brainstorming 產出，待轉 writing-plans）
- **專案**：`cockpit`（`/Users/curtis/Dev/cockpit`）
- **本 spec 範圍**：子系統 2（軟體版本追蹤器）的**執行傳輸層**改為「每機 agent daemon」。
- **取代 / 修改**：取代既有後端 `2026-06-03-cockpit-version-tracker-design.md` 中「中央以 SSH over Tailscale 拉取版本與執行更新」的模型；其餘（上游抓取、changelog 翻譯、SQLite、Web UI、job/SSE）大致保留。

---

## 1. 背景與動機

既有後端（已實作、54 測試綠）採「中央拉取」：cockpit（mac mini）以 **SSH over Tailscale** 連到各機器跑 `current_cmd` 讀版本、跑 update 指令。本 spec 改為「邊緣執行」：

1. **去除 Tailscale relay**：所有跨機通訊改走 **Cloudflare Tunnel 公開域名**，由各機 agent **主動對外 HTTPS**。
2. **統一 agent**：各機器原本為設備監控跑的服務，整併為單一 **`cockpit-agent` daemon**；它**督管 Beszel agent 子進程**（metrics 仍由 Beszel 負責、回報 Beszel hub），同時負責子系統 2 的本機版本檢查與更新執行。
3. **中央只編排**：cockpit server 不再遠端（或本地）執行任何 install 指令；連 mac mini host 自己的 install 也由「host 上的 agent」執行。

> 監控 metrics 的儲存與 UI（取代 Beszel hub）**不在本 spec**，屬子系統 1，另立 spec。本 spec 的 agent 只「督管」Beszel，不接管其 metrics 職責。

## 2. 範圍與不變量

**在範圍內**：cockpit-agent（Go binary）、agent↔server 協定、server 端執行模型由「SSH 拉取」改為「佇列 + agent 回報」、相關 DB/端點/排程調整、認證、測試、部署。

**保留不變**：
- 上游版本抓取（`sources/` npm·github·pypi·brew·claude-plugin·custom）— server 端。
- changelog 翻譯（`claude -p`）— server 端自身功能（mac mini 有 claude 登入）。
- 版本解析/比較（`version_parse`）、SQLite、inventory 為單一事實來源、Web UI 主清單 + changelog modal + job 面板 + 瀏覽器 SSE 即時 log。
- 安全核心：UI/agent 只能觸發 **inventory 宣告好的**指令；指令渲染與引號處理（`build_update` + `shlex.quote`）留在 server。

**不在範圍**：子系統 1 的 metrics 收集/儲存/監控 UI；以 agent 取代 Beszel 的監控本體。

## 3. 架構總覽

```
   人的存取面（CF Access 後）              邊緣執行面（agent 主動對外）
   瀏覽器/手機                            每台機器:
      │ https                              cockpit-agent (Go daemon)
      ▼                                      ├─ 督管 beszel-agent (子進程) ─► Beszel hub
   Cloudflare Edge ─ Access policy            └─ outbound HTTPS (Bearer token)
      │  ├ 人: Bypass(信任IP)/Allow(登入)            │  long-poll job / 回報版本 / 串 log
      │  └ /api/agent/*: Bypass(僅 app token)        ▼
      ▼ Cloudflare Tunnel (cloudflared @ mac mini)
   cockpit server (FastAPI, mac mini) ── 只編排
      ├ 上游抓取 + 翻譯 + 比對 + SQLite
      ├ 前端 API + 瀏覽器 SSE
      └ /api/agent/*  (job 佇列、版本回報、log/結果、abort 控制)
```

- **一條 cloudflared tunnel**：`cockpit.<domain>` 同時服務瀏覽器與 agent；`/api/agent/*` 路徑在 Access 設 **Bypass**（或獨立 hostname），由 app 層 **每-agent Bearer token** 把關並識別機器。
- agent 一律 **outbound HTTPS**（NAT 友善），server 永不回連 agent。

## 4. 關鍵分工：server 渲染指令、agent 笨執行

`build_update`（command 型直接帶 `cmd`；agent 型渲染 prompt 變數 `{name}{machine}{current_version}{latest_version}{changelog_zh}{cwd}` + runner 模板 codex_exec/claude_p/custom + `shlex.quote`）**完整留在 server**（已實作、已測、單一來源）。

agent 在 poll 時拿到的是**最終 shell 字串**，只負責：`bash -lc <shell_cmd>` 執行、逐行串 stdout 回 server、結束後跑 `current_cmd` 驗證新版、回報結果。agent **不需理解 inventory 內部結構**，注入面收斂於 server。

## 5. Agent ↔ Server 協定

所有 agent 端點位於 `/api/agent/*`，需 `Authorization: Bearer <agent_token>`；server 由 token 查出 `machine` 名稱。Access 對此路徑 Bypass（token 即關卡）。內容皆 JSON（除 SSE/long-poll 語意）。

| Method | Path | 用途 | 回應 |
|---|---|---|---|
| `GET` | `/api/agent/installs` | 該機版本讀取定義 | `[{software, current_cmd, version_regex}]` |
| `GET` | `/api/agent/poll` | **long-poll（≤25s）**取下一件事 | `200` job / `200` check 訊號 / `204` 逾時 |
| `POST` | `/api/agent/report-versions` | 回報目前版 | `{ "applied": n }` |
| `POST` | `/api/agent/jobs/{id}/log` | 增量 log | `204` |
| `POST` | `/api/agent/jobs/{id}/result` | 收尾結果 | `200` job |
| `GET` | `/api/agent/jobs/{id}/control` | **執行中**輪詢中止信號（~2s） | `{ "abort": bool }` |

### 5.1 `/api/agent/poll` 回應形狀

```json
// 有更新 job：
{ "type": "job", "job": { "id": 42, "software": "multica", "shell_cmd": "codex exec --cd /srv/multica '…'",
                          "cwd": "/srv/multica", "current_cmd": "docker inspect …", "version_regex": null } }
// 需重新檢查版本（「立即檢查」觸發）：
{ "type": "check" }
// 逾時無事：HTTP 204（agent 立即重新 long-poll）
```

- server 回 `type:job` 時，於同一交易內將該 job 標 `running`、寫入渲染後 `cmd/cwd/current_cmd/version_regex`（job 自含、即使 inventory 之後變動仍可重現）。
- `type:check` 後，agent 跑各 install `current_cmd` → `report-versions`。

### 5.2 回報

```json
POST /api/agent/report-versions
[ { "software": "claude-code", "current_version": "2.1.98" }, … ]

POST /api/agent/jobs/42/log      { "lines": ["▶ …", "→ …"] }      // server append 到 jobs.log
POST /api/agent/jobs/42/result   { "status": "success|failed|aborted", "exit_code": 0, "new_version": "0.9.0" }
```

## 6. 資料流

**更新 job**：瀏覽器 `POST /api/installs/{sw}/{m}/update` →（既有，含「同 install 單一活躍 job」護欄）建 `queued` job → 目標機 agent `poll` 取得（標 `running` + 渲染指令）→ agent `bash -lc shell_cmd`、逐行 `POST …/log` → 瀏覽器既有 `/api/jobs/{id}/log/stream` SSE 串 `jobs.log` → agent 結束跑 `current_cmd` 驗證 → `POST …/result` → server 收尾（status / new_version / `upsert_install`）。

**版本檢查**：agent 依自身間隔（與收到 `type:check`）跑 installs 的 `current_cmd` → `report-versions` → server 取該 software 已抓的上游最新版、`compare()` → `upsert_install(status, behind_count)`。**上游最新版 + changelog 翻譯**由 server APScheduler 定期執行（與 agent 解耦）。「立即檢查」= server 立即跑一次上游刷新 + 對各機設 `check_requested` 旗標。

**中止**：瀏覽器 `POST /api/jobs/{id}/abort` → server 設 `abort_requested=1` → 正執行該 job 的 agent 經 `/control` 輪詢讀到 → kill 本機程序 → `POST …/result {status:"aborted"}`。若 job 仍 `queued`（agent 尚未取走）→ server 直接標 aborted。

**Beszel 督管**：agent 啟動即以子進程跑 `beszel-agent`（路徑/參數來自 agent config），退出則退避重啟；agent 自身退出時一併收掉子進程。

## 7. Server 端改動（Python）

- **移除** paramiko / SSH 與「中央執行」：`runner.py` 的本機 + SSH 執行刪除（本機執行移入 Go agent）。`ExecResult`/`execute` 不再被 server 使用。
- **保留並沿用** `jobs.build_update`（指令渲染）、`version_parse.compare`、`sources`、`translate`、`db`、`collector` 的上游抓取/翻譯部分。
- **jobs 改佇列模型**：`start_job`（沿用，含單一活躍護欄）；新增 `claim_next_job(conn, machine)`（原子取最舊 queued、標 running、回渲染指令）、`append_agent_log`（= 既有 `append_job_log`）、`record_result(conn, job_id, status, exit_code, new_version)`（以 agent 回報的 `new_version` 收尾 job + `upsert_install`）、`request_abort(conn, job_id)`、`abort_requested(conn, job_id)`。移除 `run_job` 的本機 execute；**新版驗證（跑 `current_cmd` 讀新版）移到 agent**，server 只記錄 agent 回報的結果。
- **collector 拆分**：`refresh_upstream(conn, inv)`（抓上游 + 翻譯 + 寫 versions，排程用）；目前版比對移入 `report-versions` 處理：`apply_version_report(conn, inv, machine, [{software,current_version}])`。
- **DB schema delta**：
  - `jobs` 增 `cmd TEXT, cwd TEXT, current_cmd TEXT, version_regex TEXT, abort_requested INTEGER NOT NULL DEFAULT 0`。
  - 新 `machine_state(machine TEXT PRIMARY KEY, check_requested INTEGER NOT NULL DEFAULT 0, updated_at TEXT)`（「立即檢查」旗標）。
  - `installs.status` 沿用 `up_to_date|behind|unknown|error`；agent 離線時版本資料老化（前端依 `last_checked` 呈現）。
- **agent 端點 + token 中介**：新增 `cockpit/web/agent.py`（或同 app 內路由群）實作 §5 端點；`Authorization: Bearer` → machine 解析。
- **token→machine 對應**：在（gitignored 的真實）`inventory.yaml` 每台 machine 增選用欄位 `agent_token`；`inventory.py` 載入時建 `token→machine` map（`inventory.example.yaml` 放佔位 token 註解，不放真值）。
- **`/api/check` 行為**：改為「立即跑一次 `refresh_upstream` + 對所有 machine 設 `check_requested`」。

## 8. Agent 內部（Go static binary）

- **config**（檔/env/flags）：`server_url`、`agent_token`、`beszel_cmd`/`beszel_args`、`poll_timeout`(~25s)、`report_interval`(預設與 server check_hours 相當、可短)、`control_interval`(~2s)、`exec_timeout`。
- **元件**：
  - `httpclient`：帶 `Authorization`，long-poll 友善逾時、指數退避重連。
  - `beszel supervisor`：`exec.CommandContext` 跑 beszel-agent、退出退避重啟、隨 agent 收尾。
  - `executor`：`bash -lc <cmd>`、串流 stdout（行回呼）、`exec_timeout` 看門狗、可被 cancel（kill process group）。
  - `version reporter`：跑各 `current_cmd` → 以 `version_regex`/semver 抽版本 → `report-versions`。
  - `job runner`：`poll` → 若 job：跑 `shell_cmd`（串 log，背景每 `control_interval` 查 `/control`，abort 則 kill）→ 跑 `current_cmd` 驗證 → `result`；若 check：觸發一次 version report。
- **主迴圈**：啟動 beszel supervisor + 週期 version reporter；主執行緒 long-poll 迴圈處理 job/check；同時只跑一個 job（與 server 單一活躍護欄呼應）。

## 9. 安全

- 每機獨立 `agent_token`；`/api/agent/*` 在 Access Bypass、由 app token 把關並識別 machine（token 外洩僅影響該機）。
- 指令唯一來源為 inventory；agent 只執行 server **渲染好**的字串，**無自由輸入**；`shlex.quote` 在 server。
- inventory（含可執行指令、agent_token）真實檔 gitignored；PUT `/api/inventory`（agent 可編輯，另由前端整合 spec 定義）在同授權後、寫前以 `load_inventory` 驗證。
- agent↔server 全程 TLS（CF）；token 不入 log；server 對 agent 輸入做最小信任（log 行長度上限、result 欄位白名單）。

## 10. 錯誤處理與韌性

- agent long-poll/report 失敗 → 指數退避重連；server 重啟期間 job 留 `queued`，agent 重連後續取。
- job timeout（agent `exec_timeout`）→ 回 `failed`；server 對長期 `running` 無回報的 job 可設過期（看門狗，選用）。
- beszel 子進程崩潰 → agent 重啟它；agent 崩潰 → systemd/launchd 重啟。
- agent 離線 → 該機 installs 不更新、`last_checked` 老化；UI 呈現「未知/過期」。
- 「同 install 單一活躍 job」護欄沿用；`claim_next_job` 原子標 running 避免雙取。

## 11. 測試策略

- **Server（pytest）**：token 認證（缺/錯→401）、`claim_next_job` 原子性與單一活躍、`report-versions` 比對寫入、`record_result` 收尾 + upsert、abort（queued 直接標、running 設旗標、`/control` 回 true）、`poll` 的 job/check/204 分支。沿用既有瀏覽器 SSE / job 護欄測試。
- **Agent（Go）**：executor（串流、timeout、cancel/kill）、version reporter（regex/semver 抽取）、job runner 對 `httptest` mock server（poll→log→result、abort 路徑）、beszel supervisor（重啟）。
- **整合**：agent 對真實 FastAPI（TestServer）跑一次 command 型更新全流程。

## 12. 部署

- 各機器：放 `cockpit-agent` binary + config（`server_url`、`agent_token`、`beszel_cmd`），以 systemd（Linux）/ launchd（macOS）常駐；移除舊的獨立 beszel service（改由 agent 督管）。
- mac mini：cockpit server（FastAPI）＋ 一份本機 agent（host 也統一走 agent；agent 對 `cockpit.<domain>` 或 `127.0.0.1` 皆可）。
- Cloudflare：`/api/agent/*` 設 Access Bypass（或獨立 agent hostname）；其餘維持 Bypass(信任IP)/Allow(登入)。
- 每機 `agent_token` 寫入該機 config 與 server 端 `inventory.yaml`（真實檔）。

## 13. 對既有程式碼的影響 / 遷移

- **取代** Task 8（SSH/local runner）：server 不再 execute；邏輯移入 Go agent。
- **修改** Task 10（collector）：拆成 upstream 刷新（server）+ 版本回報比對（agent→server）。
- **修改** Task 11（jobs）：`run_job` 本機執行 → 佇列 + agent 回報；保留 `build_update` 與單一活躍護欄。
- **不受影響**：前端↔server API（FE-A 已完成；FE-C inventory 端點、FE-D 接線、FE-E 驗證照常）；瀏覽器 SSE；上游抓取/翻譯/版本比較/DB 模型大致不變（加欄位）。
- Beszel hub 保留。

## 14. 驗收標準

- 各機 `cockpit-agent` 常駐並督管 beszel-agent（崩潰自動重啟）。
- agent 僅以 outbound HTTPS（CF tunnel + Bearer token）與 server 溝通；**全無 Tailscale**。
- 版本檢查：agent 本機讀目前版回報，server 比對上游、正確標「落後 N 版 / 最新 / 未知」。
- 偵測新版時 server 翻出繁中 changelog 摘要並存檔（不變）。
- 點 `[更新]`：server 建 job → 目標機 agent 取走並在本機執行 server 渲染好的 command／agent（codex/claude）指令 → log **即時串流**回 UI（經既有瀏覽器 SSE）→ 完成更新版本/狀態、寫稽核。
- 點「中止」：執行中 job 能被 agent kill 並標 `aborted`，UI 即時收尾。
- agent 認證失敗（缺/錯 token）被拒；指令來源僅限 inventory（無自由輸入）。

## 15. 後續（不在本 spec）

- 子系統 1：以 agent 接管 metrics 收集 + cockpit 端儲存/監控 UI（取代 Beszel hub），另立 spec。
- agent 自動更新自身、健康回報、多 job 並行（目前單機單 job）。
- job 過期看門狗、`Last-Event-ID` SSE 斷線重放強化。
