# Cockpit P1-frontend — 版本頁接真 API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `cockpit_frontend/` 版本頁（index.html + app.js）從 mock 換成 P1 Go 後端真 API：清單/changelog/更新/SSE log/中止/重新檢查，並在 **OrbStack VM** 內做端到端驗收（不在 mac 本機跑被測服務）。

**Architecture:** 後端不動（API 以 P1 已建為準）；前端就地適配契約差異。app.js 已標 `[API]` 接線點（L13 解構 MOCK、initFilters、openChangelog、startUpdate、streamJob、abortJob、renderRecentJobs、check-btn）。其他頁（machine/topology/manage）仍用 mock，本計畫不碰。binary 重 build 後 `//go:embed` 自動帶入新前端。

**Tech Stack:** vanilla JS（fetch + EventSource）、Go 重 build、OrbStack（`orb create ubuntu`）+ linux/arm64 交叉編譯。

---

## 契約 → 實際後端 對照（前端適配規則，勿改後端）

| 契約（api-contract.md） | P1 Go 實際 | 前端適配 |
|---|---|---|
| `POST /api/jobs {install_id}` | `POST /api/installs/{sw}/{m}/update` → `{job_id}`；409=已有進行中 | `id` 形如 `sw::machine` → `split("::")` 組路徑；409 顯示提示 |
| `GET /api/jobs/{id}/stream` | `GET /api/jobs/{id}/log/stream` | 改路徑 |
| SSE `log` data=JSON `{line}` | data=**純文字行** | `e.data` 直接當行 |
| SSE `status` 事件 | 無 | 略（觸發後直接視為 running） |
| SSE `done` data=JSON `{status,new_version}` | data=**純文字 status** | done 後重抓 `/api/installs` + `/api/jobs` 取最新狀態 |
| `GET /api/machines` | 無 | 由 installs rows 取 unique machine |
| `Install.checked_at` | `last_checked` | 映射 |
| `Job.log: string[]` | `log: string`（\n join） | `split("\n")` 去尾空行 |
| `Job.install_id` | 有 `software`+`machine` | 前端組 `software+"::"+machine` |
| `POST /api/check` → 202 | → 200 `{started:true}` | 觸發後 ~2s 重抓 installs |

enum 一致（status/kind/update_kind），`updating` 仍為前端暫態。

---

### Task FE-1: 資料層換真 API（installs / machines / changelog / recent jobs）

**Files:** Modify `cockpit_frontend/app.js`, `cockpit_frontend/index.html`

先完整讀 `cockpit_frontend/app.js` 與 `index.html`。

- [ ] **Step 1:** index.html 移除 `mock-data.js` 與 `store.js` 的 `<script>`（版本頁不再用 mock；其他頁面各自載入、不受影響）。
- [ ] **Step 2:** app.js 頂部：刪 `const { MACHINES, INSTALLS, VERSIONS, JOBS, JOB_SCRIPTS } = window.MOCK;`，改為模組內 `let INSTALLS = [], MACHINES = [], JOBS = [];` + 載入函式：

```js
  async function api(path, opts) {
    const r = await fetch(path, opts);
    if (!r.ok) throw new Error(`${path} → ${r.status}`);
    return r.status === 204 ? null : r.json();
  }
  async function loadInstalls() {
    const rows = await api("/api/installs");
    INSTALLS = rows.map((r) => ({ ...r, checked_at: r.last_checked }));
    MACHINES = [...new Set(INSTALLS.map((i) => i.machine))].sort();
  }
  async function loadJobs() {
    const rows = await api("/api/jobs");
    JOBS = rows.map((j) => ({
      ...j,
      id: String(j.id),
      installId: j.software + "::" + j.machine,
      log: (j.log || "").split("\n").filter(Boolean),
    }));
  }
```
- [ ] **Step 3:** 啟動流程改 async：`(async () => { try { await loadInstalls(); await loadJobs(); } catch(e) { /* 顯示載入失敗 banner */ } initFilters(); render(); renderRecentJobs(); ... })()`。沿用既有 render/filter 邏輯（純前端過濾不動）。若 app.js 既有啟動段不是函式，包成 async IIFE。載入失敗時在表格區顯示「無法連線後端」訊息（不要 silent）。
- [ ] **Step 4:** `openChangelog(key)` 改打 `GET /api/changelog/{software}/{version}`（key 解析沿用既有呼叫端形狀；讀 app.js 確認 key 是什麼——以實際為準），回填 modal：`changelog_zh`（md 渲染沿用）、`changelog_raw`（折疊區）、`released_at`。404 → modal 顯示「尚無 changelog」。
- [ ] **Step 5:** `renderRecentJobs()` 資料源改 `JOBS`（已由 loadJobs 填充），點選歷史 job 補抓 `GET /api/jobs/{id}` 再顯示（log split 同上）。
- [ ] **Step 6:** 手動驗證（暫用 go 本機跑可以——僅這步是開發中間態，最終驗收在 FE-3 的 VM）：`go build -o /tmp/ck . 略`——本 task 先只做 `node --check cockpit_frontend/app.js`（語法檢查）+ `go build ./...`（embed 重編）。
- [ ] **Step 7:** Commit: `git add cockpit_frontend/ && git commit -m "feat(web): version page loads installs/jobs/changelog from real api"`

---

### Task FE-2: 動作流（更新 / SSE / 中止 / 重新檢查）

**Files:** Modify `cockpit_frontend/app.js`

- [ ] **Step 1:** `startUpdate(id)` 重寫：刪 JOB_SCRIPTS 模擬。
```js
  async function startUpdate(id) {
    const it = INSTALLS.find((i) => i.id === id);
    if (!it) return;
    const [sw, machine] = id.split("::");
    let resp;
    try {
      resp = await api(`/api/installs/${encodeURIComponent(sw)}/${encodeURIComponent(machine)}/update`, { method: "POST" });
    } catch (e) {
      // 409 → 已有進行中
      toast?.("已有進行中的更新") ?? alert("已有進行中的更新");
      return;
    }
    const job = { id: String(resp.job_id), installId: id, software: sw, machine,
      kind: it.update_kind || "command", status: "running", log: [] };
    // 沿用既有「目前工作面板」呈現（renderCurrentJob / openJobPanel 等，以 app.js 實際函式為準）
    beginJobUI(job);     // ← 用既有 UI 進入點
    streamJob(job);
  }
```
（`beginJobUI` 代表 app.js 既有開面板邏輯——讀檔後沿用實際函式名，不要發明新 UI。）
- [ ] **Step 2:** `streamJob(job)` 改 EventSource：
```js
  function streamJob(job) {
    const es = new EventSource(`/api/jobs/${job.id}/log/stream`);
    es.addEventListener("log", (e) => { job.log.push(e.data); appendLogLine(job, e.data); });
    es.addEventListener("done", async (e) => {
      es.close();
      job.status = e.data; // success | failed | aborted
      try { await loadInstalls(); await loadJobs(); } catch (_) {}
      render(); renderRecentJobs();
      finishJob(job, { result: job.status });  // 沿用既有收尾 UI（徽章/重試鈕）
    });
    es.addEventListener("error", () => {});    // SSE error 事件（job not found）→ 由 onerror 統一收
    es.onerror = () => { /* 連線斷：保守關閉並補抓一次 job 狀態 */ };
  }
```
`setInterval` 模擬與 `state.streamTimers` 相關碼移除（或改存 EventSource 以便 abort 後 close）。
- [ ] **Step 3:** `abortJob(jobId)` → `await api("/api/jobs/"+jobId+"/abort", {method:"POST"})`；UI 等 SSE `done`（data=aborted）收尾，不在前端先改狀態。
- [ ] **Step 4:** `finishJob` 內原本 `CockpitStore.applyUpdate(...)` 移除（已無 store.js；資料以重抓為準）。
- [ ] **Step 5:** `#check-btn` → `await api("/api/check", {method:"POST"})`，按鈕轉圈 2s 後 `loadInstalls()+render()` 還原。
- [ ] **Step 6:** `node --check cockpit_frontend/app.js` + `go build ./...`。grep 確認 `JOB_SCRIPTS`、`window.MOCK`、`CockpitStore` 在 app.js 中零引用。
- [ ] **Step 7:** Commit: `git add cockpit_frontend/ && git commit -m "feat(web): real update flow — trigger/sse/abort/check wired to go api"`

---

### Task FE-3: OrbStack VM 端到端驗收

**Files:** 無程式碼變更（驗收）；產出 `/tmp/orbtest/` 測試資材。

⚠️ 規則：被測的 serve/agent **只能跑在 OrbStack VM 裡**，不可在 mac 本機執行。mac 上只做編譯與 curl/瀏覽器驗證。

- [ ] **Step 1:** 建 VM（若已存在沿用）：`orb create ubuntu cockpit-test`；確認 `orb -m cockpit-test uname -m`（arm64）。
- [ ] **Step 2:** 交叉編譯：`cd /Users/curtis/Dev/cockpit && GOOS=linux GOARCH=arm64 go build -o /tmp/orbtest/cockpit-linux ./cmd/cockpit`（純 Go、無 CGO，必過）。
- [ ] **Step 3:** 準備資材（mac 檔案系統在 VM 內同路徑可見）：
```bash
mkdir -p /tmp/orbtest && cat > /tmp/orbtest/inv.yaml <<'EOF'
machines:
  vm1: { host: 127.0.0.1, ssh_user: curtis, local: true, agent_token: tok-vm1 }
software:
  - name: demoapp
    kind: custom
    latest_source: "custom:echo 2.5.0"
    installs:
      - machine: vm1
        current_cmd: "cat /tmp/demoapp.ver"
        update: { type: command, cmd: "sleep 1 && echo step1 && sleep 1 && echo demoapp 2.5.0 > /tmp/demoapp.ver && echo done" }
EOF
printf '{"listen":"0.0.0.0:8787","db_path":"/tmp/ck.db","enroll_secret":"s","inventory_path":"/tmp/orbtest/inv.yaml"}' > /tmp/orbtest/serve.json
printf '{"server_url":"http://127.0.0.1:8787","enroll_secret":"s","agent_token":"tok-vm1"}' > /tmp/orbtest/agent.json
```
（listen 0.0.0.0 → mac 瀏覽器可經 `http://cockpit-test.orb.local:8787` 進 UI。）
- [ ] **Step 4:** VM 內啟動：
```bash
orb -m cockpit-test sh -c 'echo "demoapp 1.0.0" > /tmp/demoapp.ver'
orb -m cockpit-test sh -c 'nohup /tmp/orbtest/cockpit-linux serve -config /tmp/orbtest/serve.json > /tmp/serve.log 2>&1 & sleep 1; nohup /tmp/orbtest/cockpit-linux agent -config /tmp/orbtest/agent.json > /tmp/agent.log 2>&1 &' 
```
- [ ] **Step 5:** mac 上 curl 驗證：`curl -s http://cockpit-test.orb.local:8787/api/installs` → demoapp current 1.0.0 / latest 2.5.0 / behind。
- [ ] **Step 6:** 瀏覽器驗收（Chrome → `http://cockpit-test.orb.local:8787/`）：清單顯示真資料 → 點「更新」→ 終端機面板出現 step1/done 串流 → 完成轉綠 up_to_date 2.5.0 → 最近工作出現 success → changelog modal（無 changelog 來源時顯示「尚無 changelog」不爆錯）→ 重新檢查鈕可按。
- [ ] **Step 7:** 收尾：`orb -m cockpit-test sh -c 'pkill cockpit-linux'`（VM 保留供後續 P2 測試）。如有發現 bug → 回 FE-1/FE-2 修復補 commit。

---

## Self-Review（已執行）
1. **覆蓋**：契約檢核清單 §7 全對應（machines→由 installs 衍生；installs/changelog/jobs/SSE/abort/check→FE-1/FE-2；mock 移除→FE-1 Step1 + FE-2 Step6）。後端零變更。
2. **No placeholders**：`beginJobUI`/`finishJob` 等以「沿用 app.js 既有函式」明示，由實作者讀檔對接（檔案已在 repo、接點已標 `[API]`），非 TBD。
3. **一致性**：id `sw::machine` 與後端 `/api/installs` 回傳一致；SSE 純文字 data 與 `internal/server/sse.go` 實作一致；`last_checked` 映射與 store 欄位一致。
4. **測試紀律**：被測服務僅在 OrbStack VM 內執行（使用者規則）。
