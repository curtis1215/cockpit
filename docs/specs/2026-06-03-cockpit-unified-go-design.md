# Cockpit — 架構設計：統一 Go 服務（監控 + 版本追蹤 + 拓樸 + 管理）

- **日期**：2026-06-03
- **狀態**：架構設計待核可（brainstorming 產出，待逐階段轉 writing-plans）
- **專案**：`cockpit`（`/Users/curtis/Dev/cockpit`）
- **本 spec 範圍**：把 cockpit 重寫為**單一 Go 服務**，整合子系統 1（設備/服務監控 + 拓樸）與子系統 2（軟體版本追蹤），加上機器 enrollment、管理與一行安裝/升級。**分 5 階段（P0–P4）**，每階段各自 plan、各自能跑。
- **取代**：現有 Python 版 cockpit server（`cockpit/` 套件、`docs/specs/2026-06-03-cockpit-version-tracker-design.md` 與 `…-agent-daemon-design.md` 的 Python 實作）——退場為 legacy（git 歷史保留）。**重用**：現有 Go agent（`agent/`）當種子；**前端正式版**＝`cockpit_frontend/`（多頁）。
- **不在範圍**：Cloudflare Tunnel / Access 設定（屬 devops，部署時處理）；以 cockpit 取代既有外部服務。

---

## 1. 背景與動機

現況：Python/FastAPI server（版本追蹤，已實作）+ Go agent + 簡版前端；監控未做。痛點與新方向：

1. **單一輕量服務**：不要多服務（cockpit server + Beszel hub）；監控**原生**做進同一服務，去除 Python 依賴問題，產出**單一靜態 binary**、低資源。
2. **統一語言 Go**：server 與 agent 合進同一 Go module，**單一 binary**用 `cockpit serve|agent` 切換角色，重用既有 Go agent 與 agent↔server 協定。
3. **完整控制平面**：`cockpit_frontend/` 已定義 — 版本追蹤、設備/服務監控（30 天圖表）、三層拓樸（機器→服務→軟體）、機器 enrollment、軟體/機器管理。
4. **一行安裝/升級**：GoReleaser 出三平台（macOS/Linux/Windows）binary + `curl|sh` 安裝器 + `cockpit upgrade` 自升級。
5. **去 Tailscale**：沿用 agent-daemon 設計 — agent 只 outbound HTTPS、經 CF tunnel（devops 設）連 server、Bearer token。

## 2. 範圍分解（P0–P4）

| 階段 | 內容 | 驗收（能跑） |
|---|---|---|
| **P0 核心骨架** | Go 單 binary（`serve`/`agent`/`upgrade`）；config；內嵌前端服務（`//go:embed`）；SQLite（`modernc.org/sqlite` 純 Go）；agent enrollment（token）+ 傳輸骨架（Bearer 認證、long-poll、health）。 | `cockpit serve` 起得來、前端載入、`cockpit agent` 能 enroll 並 heartbeat、systems 表出現該機 |
| **P1 版本追蹤（port 到 Go）** | port 版本來源（npm/github/pypi/brew/claude-plugin/custom）、`build_update`、jobs 佇列（claim/record/abort）、SSE log、inventory（含 agent_token）、changelog 翻譯（`claude -p`）；前端版本頁 + changelog modal 接通。 | 等價現有 Python 功能、改 Go；端到端更新流程可跑 |
| **P2 原生監控 + 拓樸** | agent 用 gopsutil 收 CPU/Mem/Disk/Net/Load/Temp/uptime + GPU（nvidia-smi）+ 容器（docker/podman）→ 每 ~15s 回報；server 存 30 天降採樣 + metrics/services/topology API；前端拓樸頁 + 趨勢圖表頁 + 機器詳情頁接通。 | 各機指標即時 + 30 天圖表、拓樸圖、容器狀態 |
| **P3 enrollment + 管理** | 機器 CRUD + `enroll-token`（UI 加機器→token→agent 連上→pending→online）；軟體/install 管理 CRUD；manage 頁接通。 | UI 可加/刪/命名機器、發 token、增刪改軟體 |
| **P4 打包/部署** | GoReleaser（mac/linux/win × arm64/amd64）；內嵌前端單 binary；`curl\|sh` 安裝器 + `cockpit upgrade` 自升級；service 安裝（launchd/systemd/Windows Service，用 `kardianos/service`）；deploy runbook。 | 一行安裝/升級、三平台服務常駐 |

> 每階段獨立 spec→plan→實作。本 spec 是總架構；P0 先轉 plan。

## 3. 架構總覽

```
                         單一 Go binary: cockpit
   ┌─────────────────────────┐        ┌──────────────────────────┐
   │ cockpit serve (mac mini)│        │ cockpit agent (每台機器) │
   │  ├ HTTP: 內嵌前端 + /api │◄──────►│  ├ enroll (token)        │
   │  ├ SQLite (純 Go)        │  HTTPS │  ├ 版本讀取/更新執行       │
   │  ├ 版本追蹤引擎          │ (CF    │  ├ gopsutil 指標 + docker │
   │  ├ 監控聚合 + 降採樣     │ tunnel)│  ├ GPU(nvidia-smi)        │
   │  ├ 拓樸/服務模型         │ Bearer │  └ outbound only (NAT 友善)│
   │  └ jobs 佇列 + SSE       │ token  │                          │
   └─────────────────────────┘        └──────────────────────────┘
   人經 CF Access(login) 看 UI；agent 經 /api/agent/*（token）回報/取 job
```

- **單進程、單 binary、單 SQLite 檔**；server 永不主動連 agent（agent 全 outbound）。
- 前端以 `//go:embed` 夾進 binary，server 直接服務（免外部靜態檔）。
- CF tunnel + Access 由 devops 在前面架（本 spec 假設 server 對外為 `cockpit.<domain>`）。

## 4. Binary 與模式

```
cockpit serve   [--config /etc/cockpit/config.json]   # 服務端
cockpit agent   [--config /etc/cockpit/agent.json]    # agent
cockpit upgrade                                        # 自升級（拉最新 release 換掉自己）
cockpit version                                        # 版本
```

- config（JSON）：serve 端（db_path、listen、inventory_path、check_hours、claude 路徑…）；agent 端（server_url、enroll_token/agent_token、intervals、beszel 無關了…）。
- `upgrade`：查 GitHub Releases 最新版 → 下載對應平台 binary → 原子替換（`selfupdate` 風格）→ 重啟服務（由 service manager）。

## 5. 資料模型（SQLite，純 Go）

| 表 | 重點欄位 | 用途 |
|---|---|---|
| `systems` | id, label, role, os, arch, status(online\|warn\|offline\|pending), agent_version, agent_status(ok\|stale\|behind\|pending), last_seen, enroll_token, agent_token, created | 機器（含 enrollment 狀態） |
| `metrics` | system_id, ts, type(`1m\|10m\|15m\|60m`…), cpu, mem, disk, gpu, net_up, net_down, load, temp | 時序（降採樣，見 §9） |
| `metrics_latest` | system_id, （同上欄位的最新值）, spark(json) | 現況快照 + sparkline |
| `services` | id, system_id, name, kind, status, cpu, mem, port, software_ids(json), depends(json) | 拓樸服務層（容器/程序） |
| `installs` | software, machine, current_version, status, last_checked | 版本：每機安裝（沿用） |
| `versions` | software, version, released_at, changelog_raw, changelog_zh, fetched_at | 上游版本歷史（沿用） |
| `jobs` | id, software, machine, kind, runner, status, …, cmd, cwd, current_cmd, version_regex, abort_requested, log | 更新工作（沿用 agent-daemon 模型） |
| `events` | ts, type, software, machine, detail | 稽核 |

- **inventory**（machines + software 定義 + 每機 `agent_token`）仍為版本追蹤的事實來源（YAML 或改入 DB——P1 決定；初期沿用 YAML + `agent_token`）。
- `systems` 與 inventory machines 的關係：enrollment 寫 `systems`；inventory machines 提供版本追蹤定義；P3 收斂兩者（machine 由 UI 管理寫 DB）。

## 6. Agent ↔ Server 協定（重用 + 擴充 agent-daemon）

所有 `/api/agent/*` 需 `Authorization: Bearer <agent_token>`；token→machine 解析。沿用既有：`GET /poll`(long-poll job/check/204)、`GET /installs`、`POST /report-versions`、`POST /jobs/{id}/log`、`POST /jobs/{id}/result`、`GET /jobs/{id}/control`。**新增**：

| Method | Path | 用途 |
|---|---|---|
| `POST` | `/api/agent/enroll` | 用一次性 `enroll_token` 換長期 `agent_token`，建立/啟用 `systems` 列（pending→online）。 |
| `POST` | `/api/agent/report-metrics` | `{system, ts, cpu, mem, disk, gpu, net_up, net_down, load, temp, uptime, spark?}` → 寫 metrics_latest + metrics（~15s 一次）。 |
| `POST` | `/api/agent/report-services` | `[{name, kind, status, cpu, mem, port, software_ids?, depends?}]` → 更新該機 services（容器/程序）。 |

> 指標與服務回報與「版本回報/job」共用同一 long-poll/report 通道與認證；agent 用各自的 interval。

## 7. 前端（內嵌 `cockpit_frontend/`，多頁）

server 以 `//go:embed cockpit_frontend/*` 服務這些頁，並提供其 API（取代 `store.js`/`*-data.js` 的 mock 層）：

| 頁 | 檔 | 主要 API |
|---|---|---|
| 版本清單 | `index.html` + `app.js` | `/api/installs`、`/api/changelog/{sw}/{v}`、`/api/jobs`、`POST /api/installs/{sw}/{m}/update`、`/api/jobs/{id}/log/stream`(SSE)、`/api/jobs/{id}/abort`、`/api/machines` |
| 拓樸 | `topology.html` + `topo.js` + `topo-data.js` | `/api/systems`(含 metrics_latest)、`/api/services` |
| 機器詳情 | `machine.html` | `/api/systems/{id}`、`/api/systems/{id}/metrics?range=1h\|12h\|24h\|7d` |
| 趨勢 | `trends.js` | 同上 metrics range |
| 管理 | `manage.html` + `manage.js` | `POST/PATCH/DELETE /api/systems`、`POST /api/systems/{id}/enroll-token`、`POST/PATCH/DELETE /api/installs` |

前端接線（P1–P3）逐頁把 `window.MOCK/TOPO` + `store.js` 換成真 fetch；保留 UI/DOM。

## 8. API 面（server，CF Access 後；`/api/agent/*` 為 token）

人/operator 面（前端用）：
- 版本：`GET /api/installs`（enriched：id、kind、update_kind、behind_count、error、status 即時 compare）、`GET /api/changelog/{sw}/{v}`、`GET /api/jobs?limit=`、`GET /api/jobs/{id}`、`POST /api/installs/{sw}/{m}/update`(202)、`POST /api/jobs/{id}/abort`、`GET /api/jobs/{id}/log/stream`(SSE)、`POST /api/check`、`GET /api/machines`。
- 監控/拓樸：`GET /api/systems`（list + 現況指標）、`GET /api/systems/{id}`、`GET /api/systems/{id}/metrics?range=…`、`GET /api/services`。
- 管理：`POST /api/systems`、`PATCH /api/systems/{id}`、`DELETE /api/systems/{id}`、`POST /api/systems/{id}/enroll-token`、`POST/PATCH/DELETE /api/installs`、`GET/PUT /api/inventory`。

## 9. 監控細節（gopsutil + 30 天降採樣）

- **agent 收集**（`cockpit agent`）：gopsutil → cpu%、mem%、disk%、net up/down、load、temp、uptime；GPU 用 `nvidia-smi --query-gpu=utilization.gpu,temperature.gpu`；容器用 docker/podman API（或 `docker ps`/`stats`）。每 ~15s `report-metrics` + 較疏的 `report-services`。
- **server 降採樣**（仿 Beszel，30 天 ≈ 數百筆/機）：寫入 `1m`；背景 cron 聚合成 `10m/15m/60m` 並刪舊細粒度。range→type 對應：1h→1m、12h→10m、24h→15m、7d→60m、30d→480m（或同 60m）。`GET …/metrics?range` 回對應 type 的點陣 `[{t,cpu,mem,disk,gpu,net_up,net_down,load,temp}]`。
- **現況**：`metrics_latest` 存最新值 + `spark`(近 24 點 CPU) 供拓樸頁卡片即時顯示。
- status 判定：online（近 N 秒有回報）、warn（門檻：mem/gpu/temp 過高）、offline（last_seen 逾時）、pending（已 enroll 未回報）。

## 10. 拓樸（機器→服務→軟體）

- 機器層 = `systems`；服務層 = `services`（agent 回報容器/程序，kind: docker/service/daemon/proxy/db/plugin/runtime/bundle；`bundle`=系統套件，掛該機的 install 軟體）；軟體層 = `installs`。
- `services.software_ids` 連到 installs；`services.depends` 畫服務↔服務次要連線。
- 「系統套件」bundle：把該機非容器化的追蹤軟體（CLI/plugin）掛上，維持三層連通。

## 11. Enrollment 與管理

1. UI manage 頁「加機器」→ `POST /api/systems`（label/os/arch）→ server 建 pending system + 一次性 `enroll_token`，UI 顯示一行安裝指令（含 token）。
2. 目標機跑 `cockpit agent --enroll <token>`（或 config 帶 enroll_token）→ `POST /api/agent/enroll` → 換得長期 `agent_token`（落地 agent config）、system 轉 online。
3. 之後 agent 用 `agent_token` 回報指標/版本、取 job。
4. 命名/刪除機器、增刪改追蹤軟體 → 對應 `/api/systems`、`/api/installs`；刪機器連帶清 metrics/services。

## 12. 安全

- `/api/agent/*` 靠每機 `agent_token`（+ devops 在 CF 設 Bypass/service token）；enroll_token 一次性、短期。
- 人面 `/api/*` 靠 CF Access（login）——devops 設；server 本身不做使用者登入。
- 更新指令唯一來源仍是 inventory（server `build_update` 渲染 + 跨平台引號處理）；UI/agent 無自由輸入指令。
- inventory（含 agent_token）真實檔 gitignore；指標/服務回報做欄位白名單 + 數值範圍夾擠。

## 13. 打包與部署（P4）

- **GoReleaser**：build matrix mac/linux/windows × arm64/amd64；內嵌前端；產 GitHub Releases + checksums。
- **一行安裝**：`curl -fsSL https://<host>/install.sh | sh`（偵測 OS/arch、下載 binary、寫 config 範本、裝 service）。Windows 出 `.ps1`。
- **升級**：`cockpit upgrade`（自下載替換 + 由 service 重啟）；或重跑安裝器。亦可讓 cockpit 把自己納入版本追蹤（dogfood）。
- **Service**：`kardianos/service` 一套碼產 launchd/systemd/Windows Service 安裝。
- CF tunnel/Access：runbook 註明由 devops 配 `cockpit.<domain>` → `127.0.0.1:<port>`、`/api/agent/*` Bypass。

## 14. 跨平台注意（mac/ubuntu/windows）

- **Windows 多為 agent-only**；exec 更新指令：Unix 走 `bash -lc`，Windows 走 `powershell -c`（或 cmd）——以平台分支 + inventory 可標 shell。
- 行程群組 kill：Unix `setpgid`+SIGKILL（現有）；Windows 用 Job Object/`taskkill /T`。以 build tag 分檔。
- 指標：gopsutil 跨三平台；GPU/docker 視該機有無對應工具，缺則回報 null/略過。
- 純 Go SQLite + 純 Go binary → 三平台交叉編譯零 C 工具鏈。

## 15. 重用 / 退場

- **重用**：Go agent（executor 含 timeout/group-kill、httpclient、reporter、jobrunner、supervisor→改督管「無」或移除、config）；agent↔server 協定；`cockpit_frontend/`。
- **退場（legacy）**：Python `cockpit/` 套件與其測試（git 歷史保留）；FE-D 的簡版 `cockpit/web/static`；Beszel 聚合構想（改原生）。
- **agent supervisor**：原督管 beszel-agent 的 supervisor 不再需要（監控原生）；移除或改為通用子進程督管（YAGNI：先移除）。

## 16. 驗收標準（總體，逐階段細化）

- 單一 `cockpit` binary，`serve`/`agent`/`upgrade` 三模式；三平台可交叉編譯與執行（Windows 至少 agent）。
- agent 經 token 連上、回報版本 + 指標 + 服務；server 存 30 天降採樣、出 metrics/topology/version API。
- 前端（`cockpit_frontend`）內嵌服務、各頁接真 API：版本追蹤端到端（更新→SSE→完成）、拓樸圖、機器詳情 30 天圖表、管理（加機器 enroll、增刪軟體）。
- 一行安裝 + `cockpit upgrade` 自升級；service 三平台常駐。
- 指令唯一來源 inventory；agent 認證失敗被拒。

## 17. 後續（不在本架構 spec 的細節，各階段 plan 處理）

- inventory 由 YAML→DB 的收斂時機（P1/P3）。
- 多 job 並行、stuck-job 看門狗（沿用 agent-daemon 已知 follow-up）。
- 告警/通知（門檻觸發）、歷史保留可調、agent 自動更新策略。
