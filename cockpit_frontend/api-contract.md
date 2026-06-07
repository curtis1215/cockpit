# cockpit 版本追蹤器 — 後端接線契約（API + htmx + SSE）

> 目的：讓目前的 vanilla prototype 能被 **FastAPI + htmx + SSE** 後端「就地接管」，
> 外觀與 DOM 結構不變、只把 mock 換成真實資料來源。
>
> ⚠️ 下表欄位形狀依 `cockpit/mock-data.js` 草擬。**正式欄位名以 repo spec
> `docs/specs/2026-06-03-cockpit-version-tracker-design.md` 為準**——若不一致，
> 改 spec 那邊或在後端 serializer 做映射（見 §6 對照表）。
> 授權由 Cloudflare Access 處理，前端不做登入。

---

## 0. 傳輸策略（JSON 與 htmx 共用同一組 route）

每個 route 依 `HX-Request` header 決定回傳格式：

| 來源 | header | 回傳 |
|---|---|---|
| 此 prototype / 未來 SPA | （無） | `application/json` |
| htmx 直接接管 | `HX-Request: true` | HTML partial 片段 |

```python
# FastAPI 範例
@app.get("/api/installs")
def list_installs(req: Request, machine: str = "", status: str = "", q: str = ""):
    rows = query_installs(machine, status, q)
    if req.headers.get("HX-Request"):
        return templates.TemplateResponse("partials/install_rows.html", {"rows": rows})
    return rows  # → JSON
```

---

## 1. 資料模型

### 1.1 `Install`（主清單，一列 = 軟體 × 機器）
| 欄位 | 型態 | 說明 |
|---|---|---|
| `id` | string | 穩定主鍵（prototype: `i01`…）。htmx 用作 `#row-{id}` target |
| `software` | string | 軟體名 |
| `kind` | enum | `npm \| github \| pypi \| brew \| claude-plugin \| custom` |
| `machine` | string | 機器代號，需出現在 `GET /api/machines` |
| `current_version` | string | 目前版（等寬顯示） |
| `latest_version` | string \| null | 上游最新版；`unknown`/`error` 時可為 `null` |
| `status` | enum | `up_to_date \| behind \| unknown \| error`（`updating` 為**前端暫態**，後端不需回） |
| `behind_count` | int | 落後版本數；非 `behind` 時為 `0` |
| `update_kind` | enum \| null | `command \| agent`。`agent` 代表更新會委派 AI agent 多步執行 |
| `error` | string \| null | `status=error` 時的錯誤訊息（顯示在徽章 tooltip） |
| `checked_at` | datetime | （建議新增）此列上次檢查時間 |

### 1.2 `Version`（changelog，modal 用）
| 欄位 | 型態 | 說明 |
|---|---|---|
| `software` | string | |
| `version` | string | |
| `released_at` | date | |
| `changelog_zh` | markdown | 繁中重點摘要（前端以極簡 md 渲染：`**粗體**`、`` `code` ``、`- 條列`） |
| `changelog_raw` | string | 原文 changelog（折疊區，純文字） |

### 1.3 `Job`（更新工作）
| 欄位 | 型態 | 說明 |
|---|---|---|
| `id` | string | job 主鍵 |
| `install_id` | string | 關聯的 `Install.id`；成功後用來更新該列 |
| `software` / `machine` | string | 冗餘存放，方便面板直接顯示 |
| `kind` | enum | `command`（單一指令）\| `agent`（委派 AI agent） |
| `runner` | string \| null | `kind=agent` 時：`codex exec` / `claude -p` … |
| `prompt` | string \| null | `kind=agent` 時：所用 prompt（面板可折疊顯示） |
| `status` | enum | `queued \| running \| success \| failed \| aborted` |
| `new_version` | string \| null | 成功後的新版本號 |
| `log` | string[] | 已產生的 log 行（重新整理 / 補抓歷史用） |
| `started_at` / `finished_at` | datetime | |

**log 行慣例**（前端依首字元上色，請後端沿用）：
`▶` 標題步驟 · `→` 子步驟 · `✓` 成功 · `✗` 失敗 · `■` 中止 · 其餘為一般輸出。

### 1.4 `System`（機器 / 管理與監控頁）

`GET /api/systems` 回傳每台機器的監控摘要與拓樸欄位，另包含群組欄位：

- `group`：機器自己存的群組名（空字串 = 未分組；VM 留空 = 繼承宿主機）
- `effective_group`：含 VM 繼承計算後的有效群組（前端過濾一律用這個欄位）

`PATCH /api/systems/{id}` body 另接受 `"group"`（optional string）：trim 後存入，
長度上限 64 字元（rune），空字串 = 清除群組/恢復繼承。

---

## 2. REST 端點

| Method | Path | 用途 | 回傳 | prototype 對應 |
|---|---|---|---|---|
| `GET` | `/api/machines` | 篩選下拉用機器清單 | `string[]` | `initFilters()`（現為 `MOCK.MACHINES`） |
| `GET` | `/api/installs?machine=&status=&q=&only_updates=` | 主清單（建議**後端過濾**） | `Install[]` 或 partial | `render()` / `filtered()`（現為前端過濾） |
| `POST` | `/api/check` | 觸發重新檢查所有來源（非同步） | `202` + `{job_run_id}` | `#check-btn` 的 `[API]` 處 |
| `GET` | `/api/changelog/{software}/{version}` | 單一版本 changelog | `Version` | `openChangelog()` 的 `[API]` 處 |
| `GET` | `/api/jobs?limit=` | 最近工作清單 | `Job[]` | `renderRecentJobs()`（現為 `MOCK.JOBS`） |
| `POST` | `/api/jobs` `{install_id}` | 建立更新工作 | `201` + `Job`（`status=queued/running`） | `startUpdate()` 的 `[API]` 處 |
| `GET` | `/api/jobs/{id}` | 單一 job（含已累積 log） | `Job` | 點選最近工作時補抓 |
| `POST` | `/api/jobs/{id}/abort` | 中止執行中的 job | `Job`（`status=aborted`） | `abortJob()` |
| `GET` | `/api/jobs/{id}/stream` | **SSE** 即時 log（見 §3） | `text/event-stream` | `streamJob()` 的 `[API]` 處 |

> 重試 = 對同一 `install_id` 再次 `POST /api/jobs`（前端 `startUpdate(install_id)` 已如此設計）。

---

## 3. SSE 串流契約 — `GET /api/jobs/{id}/stream`

prototype 用 `setInterval` 逐行模擬；正式版改 `EventSource`。事件型別：

| event | data (JSON) | 前端動作 |
|---|---|---|
| `log` | `{ "line": "→ docker build …" }` | append 一行到終端機並自動捲到底 |
| `status` | `{ "status": "running" }` | 更新狀態徽章（排隊→執行中…） |
| `done` | `{ "status": "success", "new_version": "0.9.0", "finished_at": "…" }` | 收尾：成功則更新主清單該列 + 徽章轉綠；失敗顯示重試 |

**接入點**（`app.js` → `streamJob()`，已預留註解）：
```js
const es = new EventSource(`/api/jobs/${job.id}/stream`);
es.addEventListener("log",    e => { const {line} = JSON.parse(e.data); job.log.push(line); appendLogLine(job, line); });
es.addEventListener("status", e => { job.status = JSON.parse(e.data).status; renderCurrentJob(job); });
es.addEventListener("done",   e => { Object.assign(job, JSON.parse(e.data)); finishJob(job, { result: job.status }); es.close(); });
```

**FastAPI 端**（`sse-starlette`）：
```python
from sse_starlette.sse import EventSourceResponse

@app.get("/api/jobs/{job_id}/stream")
async def stream(job_id: str):
    async def gen():
        async for line in job_log_lines(job_id):       # 來自 SSH / agent runner 的 stdout
            yield {"event": "log", "data": json.dumps({"line": line})}
        result = await job_result(job_id)
        yield {"event": "done", "data": json.dumps(result)}
    return EventSourceResponse(gen())
```
- 斷線重連：支援 `Last-Event-ID`，重連時 replay 未送達的 log 行。
- 多 job 並行：每個 job 一條獨立 SSE 連線（前端已支援同時多條 → `state.streamTimers` 改為多個 `EventSource`）。

---

## 4. htmx partial 映射（後端接管時）

DOM 已標 `data-partial` 邊界，對應可被 server 片段替換的區塊：

| `data-partial` | DOM | 來源端點 | htmx 屬性建議 |
|---|---|---|---|
| `installs` | `#table-body` / `#card-list` | `GET /api/installs` | 篩選列元件加 `hx-get="/api/installs" hx-trigger="change,keyup delay:250ms from:#filters" hx-target="[data-partial=installs]" hx-swap="innerHTML"` |
| `changelog` | `#modal-card` 內容 | `GET /api/changelog/…` | changelog 連結 `hx-get hx-target="#modal-body" hx-swap="innerHTML"`，再開 modal |
| `job-current` | `#job-current` | `GET /api/jobs/{id}` | 點最近工作 `hx-get hx-target="#job-current"` |
| `recent` | `#recent-list` | `GET /api/jobs` | `hx-trigger="load, every 30s"` 或由 SSE 推送刷新 |

**更新按鈕**（建立 job + 開 SSE）：
```html
<button hx-post="/api/jobs" hx-vals='{"install_id":"i04"}'
        hx-target="#job-current" hx-swap="innerHTML">更新</button>
<!-- 回傳的 job-current 片段內含 SSE 容器： -->
<div hx-ext="sse" sse-connect="/api/jobs/{id}/stream"
     sse-swap="log" hx-target="#term" hx-swap="beforeend scroll:bottom">…</div>
```

> 純前端篩選、分組、深淺、面板版面（側欄/底部）等屬 UI 狀態，**留在前端**即可，不需後端往返。

---

## 5. 狀態與 enum 對照（前後端需一致）

```
Install.status   : up_to_date | behind | unknown | error      (前端另有暫態 updating)
Install.kind     : npm | github | pypi | brew | claude-plugin | custom
update_kind      : command | agent
Job.status       : queued | running | success | failed | aborted
Job.kind         : command | agent
```

---

## 6. mock → API 欄位對照（若 spec 命名不同，於此映射）

| prototype (mock-data.js) | 建議 API 欄位 | 備註 |
|---|---|---|
| `install.update_kind` | `update_kind` | 若 spec 叫 `updater` / `strategy`，後端 serializer 改名即可 |
| `install.behind_count` | `behind_count` | |
| `job.installId`（前端駝峰） | `install_id` | 前端 fetch 後轉一次即可 |
| `JOB_SCRIPTS[*]`（前端模擬腳本） | —（無對應） | 純 prototype 用，正式版刪除 |
| `MOCK.MACHINES` | `GET /api/machines` | |

---

## 7. 接線檢核清單

- [ ] `GET /api/machines` → 填入機器下拉
- [ ] `GET /api/installs`（含 query 過濾）→ 取代 `MOCK.INSTALLS`
- [ ] `GET /api/changelog/{sw}/{v}` → 取代 `MOCK.VERSIONS`
- [ ] `POST /api/jobs` → 取代 `startUpdate()` 內的模擬 job 建立
- [ ] `GET /api/jobs/{id}/stream` SSE → 取代 `streamJob()` 的 `setInterval`
- [ ] `POST /api/jobs/{id}/abort` → 接 `abortJob()`
- [ ] `POST /api/check` → 接 `#check-btn`
- [ ] 刪除 `mock-data.js` 與 `JOB_SCRIPTS`
- [ ] enum / 欄位名與 repo spec 對齊（§5、§6）

## 8. Server 版本與自我升級

### `GET /api/version`

回應範例：

```json
{"version":"0.2.1","latest":"0.2.2","update_available":true}
```

- `version`：目前 server binary 版本；dev build 會回 `dev`。
- `latest`：server 端由 GitHub latest release 查得的最新版本；查詢失敗或 dev build 時為空字串。
- `update_available`：`latest` 不為空且 `latest != version` 時為 `true`；dev build 一律為 `false`。

### `POST /api/server/upgrade`

觸發 server 使用既有 `internal/selfupdate` 自我升級，替換 binary 後約 1 秒退出，交由 launchd/systemd 服務管理器重啟。

回應：

- `202 {"status":"restarting"}`：已替換 binary，server 即將退出並重啟。
- `200 {"status":"up_to_date"}`：GitHub latest 與目前版本相同，未替換 binary。
- `400`：dev build 不允許自我升級。
- `409`：升級已在進行。
- `500`：升級失敗；可能原因包含 server process 無法寫入自身 binary，需調整檔案擁有者，例如 `sudo chown <service-user> <binary>`。
