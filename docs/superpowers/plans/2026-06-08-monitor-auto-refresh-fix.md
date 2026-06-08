# 監控數據前端自動更新修復 — Implementation Plan

> **For agentic workers:** 本 plan 交由 Codex 實作。TDD 的 red 測試（`cockpit_frontend/topo-state.test.js`）已由 Claude 預先建立並確認失敗。你的任務是讓它變綠並完成 api-data.js / html 改動。Steps 用 checkbox（`- [ ]`）追蹤。

**Goal:** 修掉前端 stale-reference bug，使監控當前值（含服務狀態、軟體版本）在不重整頁面下自動更新；自動刷新間隔 30s→15s。

**Architecture:** 方案 A「in-place 更新穩定物件」。`api-data.js` 改為持有模組級穩定容器（`MACHINE_META`/`MACHINE_ORDER`/`SERVICES`/`INSTALLS`），`window.TOPO`/`window.MOCK` 只 assign 一次；`loadAll()` 每輪以新增的 `applyObjectInPlace`/`applyArrayInPlace` 工具原地更新容器內容，參考永不改變，故 `topo.js`/`trends.js` 頂層解構的舊參考自然看到新值。`topo.js`/`trends.js` **不動**。

**Tech Stack:** vanilla JS（無 build pipeline）、node:test（零依賴、Node 內建）。

**設計文件：** `docs/superpowers/specs/2026-06-08-monitor-auto-refresh-fix-design.md`

---

## File Structure

- **Create** `cockpit_frontend/topo-state.js` — 純資料工具，雙模式匯出（瀏覽器 `window.CockpitTopoState` / node `module.exports`）。職責：in-place apply 工具 + 刷新間隔常數。
- **Exists (red)** `cockpit_frontend/topo-state.test.js` — node:test，已建立。實作後須全綠。
- **Modify** `cockpit_frontend/api-data.js` — 穩定容器 + loadAll 改 in-place + interval 常數。
- **Modify** `cockpit_frontend/machine.html`、`cockpit_frontend/topology.html` — 載入 topo-state.js。
- **Untouched** `topo.js`、`trends.js`、`manage.js`、所有後端 Go / agent 檔案。

---

## Task 1: 建立 `topo-state.js` 讓紅燈測試變綠

**Files:**
- Create: `cockpit_frontend/topo-state.js`
- Test: `cockpit_frontend/topo-state.test.js`（已存在）

- [ ] **Step 1: 確認測試目前為 red**

Run: `node --test cockpit_frontend/`
Expected: FAIL（`Cannot find module './topo-state.js'`）。

- [ ] **Step 2: 建立 `cockpit_frontend/topo-state.js`**

完整內容如下（逐字）：

```js
/* =============================================================
   cockpit · topo-state.js — 共享監控狀態的 in-place 更新工具
   雙模式匯出：瀏覽器以 <script> 載入掛 window.CockpitTopoState；
   node 以 require 載入（供 topo-state.test.js 測試）。
   存在理由：loadAll 每輪必須原地更新同一個共享狀態物件，
   topo.js / trends.js 頂層解構的參考才看得到新資料（修 stale-ref）。
   ============================================================= */
(function (root) {
  "use strict";

  // 前端自動刷新間隔（ms）。唯一真實來源。對齊 agent metrics 採集頻率 15s。
  const REFRESH_INTERVAL_MS = 15000;

  // 原地更新物件：刪除 target 上 source 沒有的 key，再把 source 的 key 覆寫進 target。
  // 回傳同一個 target 參考（不可 new），以保住既有解構參考的有效性。
  function applyObjectInPlace(target, source) {
    for (const k of Object.keys(target)) {
      if (!Object.prototype.hasOwnProperty.call(source, k)) delete target[k];
    }
    for (const k of Object.keys(source)) {
      target[k] = source[k];
    }
    return target;
  }

  // 原地替換陣列內容，回傳同一個 target 參考。
  function applyArrayInPlace(target, items) {
    target.length = 0;
    for (let i = 0; i < items.length; i++) target.push(items[i]);
    return target;
  }

  const api = { applyObjectInPlace, applyArrayInPlace, REFRESH_INTERVAL_MS };

  if (typeof module !== "undefined" && module.exports) {
    module.exports = api; // node / test
  }
  if (root) {
    root.CockpitTopoState = api; // browser
  }
})(typeof window !== "undefined" ? window : null);
```

- [ ] **Step 3: 跑測試確認全綠**

Run: `node --test cockpit_frontend/`
Expected: PASS，`# pass 6 # fail 0`。

- [ ] **Step 4: Commit**

```bash
git add cockpit_frontend/topo-state.js cockpit_frontend/topo-state.test.js
git commit -m "feat(web): topo-state in-place update utils + tests"
```

---

## Task 2: `api-data.js` 改用穩定容器 + in-place 更新 + 15s 間隔

**Files:**
- Modify: `cockpit_frontend/api-data.js`

關鍵錨點（現況行號，實作時以內容為準）：`const MACHINE_META = {};`@170、`const MACHINE_ORDER = [];`@171、`const INSTALLS = ...`@265、`const SERVICES = [];`@280、`window.TOPO = {...}`@340、`window.MOCK = {...}`@348、`setInterval(..., 30000)`@~417。

- [ ] **Step 1: 在 IIFE 頂層、`loadAll` 之外建立穩定容器（只初始化一次）**

在 `async function loadAll()` 定義**之前**插入：

```js
// 共享監控狀態：穩定參考，loadAll 每輪原地更新內容（修 stale-ref，見 topo-state.js）
const MACHINE_META = {};
const MACHINE_ORDER = [];
const SERVICES = [];
const INSTALLS = [];
const { applyObjectInPlace, applyArrayInPlace, REFRESH_INTERVAL_MS } =
  window.CockpitTopoState;
window.TOPO = { MACHINE_META, MACHINE_ORDER, SERVICES }; // 只 assign 一次
window.MOCK = { INSTALLS, VERSIONS: {}, JOB_SCRIPTS: {} }; // 只 assign 一次
```

- [ ] **Step 2: 在 `loadAll()` 內，把 4 個原本 new 的區域變數改名為暫存**

把 `loadAll` 函式體內所有出現處整批改名（限 loadAll 內部）：
- `MACHINE_META` → `nextMeta`（含 `const MACHINE_META = {};`@170 → `const nextMeta = {};`，以及 `MACHINE_META[...]`、`Object.entries(MACHINE_META)` 等）
- `MACHINE_ORDER` → `nextOrder`（含宣告@171、`MACHINE_ORDER.push`@178/@251、`MACHINE_ORDER.forEach`@305、`MACHINE_ORDER.map`@341 附近）
- `INSTALLS` → `nextInstalls`（含宣告@265、`INSTALLS.filter`@312）
- `SERVICES` → `nextServices`（含宣告@280、`SERVICES.push`@285/@323、`SERVICES.some`@318-319）

映射邏輯本身**完全不變**，只是寫進暫存物件。

- [ ] **Step 3: 將末端的 window reassign 改為 in-place apply**

把 `window.TOPO = { MACHINE_META, MACHINE_ORDER, SERVICES };`@340 與 `window.MOCK = { INSTALLS, VERSIONS: {}, JOB_SCRIPTS: {} };`@348 這兩段 reassign **刪除**，改成：

```js
applyObjectInPlace(MACHINE_META, nextMeta);
applyArrayInPlace(MACHINE_ORDER, nextOrder);
applyArrayInPlace(SERVICES, nextServices);
applyArrayInPlace(INSTALLS, nextInstalls);
```

放在原 `window.TOPO = ...` 的位置（即 SERVICES bundle 合成完成之後、`CockpitGroups`/prefetch 之前）。`window.TRENDS = {...}`@372 **維持原樣不動**。

注意：`CockpitGroups.init`、`IS_MACHINE` 的 prefetch、`systemLabelToId` 等後續若引用這些名稱，改讀穩定容器（`MACHINE_META`/`MACHINE_ORDER`）即可，內容此時已等同暫存。

- [ ] **Step 4: 刷新間隔 30000 → 常數**

把 `setInterval(async () => { ... }, 30000);`（約@417）的 `30000` 改為 `REFRESH_INTERVAL_MS`。

- [ ] **Step 5: 確認沒有殘留**

Run: `grep -nE "window\.(TOPO|MOCK) ?=|= \{\};?\s*$|, 30000\)" cockpit_frontend/api-data.js`
Expected: `window.TOPO`/`window.MOCK` 各只剩頂層那一次 assign；無 `, 30000)`。

- [ ] **Step 6: 測試仍綠（api-data.js 未被 node 測試直接載入，但確保沒破壞測試套件）**

Run: `node --test cockpit_frontend/`
Expected: PASS，`# pass 6 # fail 0`。

- [ ] **Step 7: Commit**

```bash
git add cockpit_frontend/api-data.js
git commit -m "fix(web): in-place update shared topo state so UI auto-refreshes without reload"
```

---

## Task 3: html 載入 `topo-state.js`（須在 api-data.js 之前）

**Files:**
- Modify: `cockpit_frontend/machine.html`、`cockpit_frontend/topology.html`

- [ ] **Step 1: machine.html**

在 `machine.html:183` 的 `<script src="api-data.js"></script>` **上一行**插入：

```html
  <script src="topo-state.js"></script>
```

- [ ] **Step 2: topology.html**

在 `topology.html:309` 的 `<script src="api-data.js"></script>` **上一行**插入：

```html
  <script src="topo-state.js"></script>
```

- [ ] **Step 3: 確認順序**

Run: `grep -n "topo-state.js\|api-data.js" cockpit_frontend/machine.html cockpit_frontend/topology.html`
Expected: 每個檔案中 `topo-state.js` 行號小於 `api-data.js`。

- [ ] **Step 4: Commit**

```bash
git add cockpit_frontend/machine.html cockpit_frontend/topology.html
git commit -m "fix(web): load topo-state.js before api-data.js on machine/topology pages"
```

---

## Task 4: 驗收

- [ ] **Step 1: 前端測試全綠**

Run: `node --test cockpit_frontend/`
Expected: `# pass 6 # fail 0`。

- [ ] **Step 2: 後端不受影響**

Run: `go test ./...`
Expected: 與改動前一致（無 Go 變更）。

- [ ] **Step 3: 手動驗證（若可啟動 server）**

開啟拓樸頁與機器頁，**不重整**，觀察 CPU/MEM 等當前值是否在 ≤15 秒內自動跳動；服務狀態、軟體版本同樣自動更新。瀏覽器 console 無 `CockpitTopoState is undefined` 等錯誤。

---

## Self-Review（已由作者執行）

- **Spec coverage:** P0（MACHINE_META/SERVICES/INSTALLS in-place）→ Task 2；P1（15s）→ Task 1 常數 + Task 2 Step 4；node:test → Task 1。✓
- **Placeholder scan:** 無 TBD/TODO；topo-state.js 全碼、api-data.js 改動逐點具體。✓
- **Type consistency:** `applyObjectInPlace`/`applyArrayInPlace`/`REFRESH_INTERVAL_MS` 名稱於 plan、測試、實作三處一致。✓
