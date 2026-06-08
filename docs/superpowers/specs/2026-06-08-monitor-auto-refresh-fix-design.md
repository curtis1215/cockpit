# 監控數據前端自動更新修復 — 設計文件

- 日期：2026-06-08
- 分支：`fix/monitor-auto-refresh-stale-ref`
- 範圍：純前端（`cockpit_frontend/`），不動後端 Go

## 1. 背景與根因

使用者回報：web UI 的機器監控數據不會自動更新，必須手動重整頁面才看得到新值。

調查確認**並非缺少自動更新機制**，而是一個前端 stale-reference bug：

- `api-data.js` 的 `loadAll()` 每輪（目前 30 秒）都用 `const MACHINE_META = {}`（`api-data.js:170`）建立**全新物件**，並在 `api-data.js:340` 把 `window.TOPO` 整包 reassign 成新物件；`window.MOCK`（含 `INSTALLS`）同理（`api-data.js:348`）。
- 但 `topo.js` 與 `trends.js` 在腳本載入當下就以**頂層解構**綁定了首次的物件參考：
  - `topo.js:8` `const { INSTALLS, VERSIONS, JOB_SCRIPTS } = window.MOCK;`
  - `topo.js:9` `const { MACHINE_META, SERVICES, MACHINE_ORDER } = window.TOPO;`
  - `trends.js:7` `const { MACHINE_META, MACHINE_ORDER, SERVICES } = window.TOPO;`
  - `trends.js:8` `const { INSTALLS } = window.MOCK;`
- 30 秒後 `loadAll` 換掉了 `window.TOPO` 指向的物件，但 `topo.js`/`trends.js` 的區域變數仍指向**首次**的舊物件 → `topo:refresh`/`trends:refresh` 觸發的 render 永遠畫首次快照。重整頁面才會重新解構到最新值，因此「重整才看得到」。

時序圖表會更新是因為它走 `series()`，讀的是模組級 `const _cache`（`api-data.js:88`，被 in-place mutate，參考不變）—— 這正好反證：**只要保持物件參考穩定，stale 問題就消失。**

> 結論：此 bug 與傳輸方式（polling vs WebSocket）無關。改用 WS 也無法修復，因為前端仍持舊參考。WS 非本次範圍。

## 2. 目標 / 非目標

**目標**
- P0：修掉 stale-reference，使拓樸頁、機器頁的監控**當前值**在不重整下自動更新。同類受影響的 `SERVICES`（服務狀態）與 `INSTALLS`（軟體版本）一併修復。
- P1：前端自動刷新間隔 30 秒 → 15 秒，對齊 agent metrics 採集頻率。
- 以 node:test（零依賴）建立回歸測試，鎖死 bug 不復發。

**非目標**
- 不引入 WebSocket / SSE。
- 不引入 vitest/jsdom 或任何 npm 依賴。
- 不重構 `topo.js` / `trends.js`（兩檔一行都不改）。
- 不動後端 Go 與 agent 回報頻率。

## 3. 關鍵決策

| 決策 | 選擇 | 理由 |
|---|---|---|
| 前端測試 | **node:test 零依賴** | bug 本質是純資料邏輯（參考穩定性），不需真實 DOM；Node 內建、Codex 易實作、不增依賴 |
| 修法方向 | **方案 A：in-place 更新穩定物件** | 只動資料層，`topo.js`/`trends.js` 不動，回歸風險最低 |
| 搬移幅度 | **最小搬移（A'）** | 只把「in-place apply 小工具」抽到可測模組；`loadAll` 內的映射邏輯**原地保留**，僅將末端賦值改為套用至穩定容器。避免搬移 `fmtUptime`/`fmtLastSeen`/`computeWarnings` 等 helper 帶來的額外風險 |

## 4. 詳細設計

### 4.1 新增 `cockpit_frontend/topo-state.js`（純資料工具，雙模式匯出）

無 build pipeline，採瀏覽器 + node 雙相容：檔案在瀏覽器以 `<script>` 載入並掛到 `window.CockpitTopoState`；在 node 以 `require` 載入。檔尾：

```js
if (typeof module !== "undefined" && module.exports) {
  module.exports = { applyObjectInPlace, applyArrayInPlace, REFRESH_INTERVAL_MS };
}
```

匯出內容與行為：

- `REFRESH_INTERVAL_MS = 15000` — P1 常數，唯一真實來源。
- `applyObjectInPlace(target, source)`：
  - 刪除 `target` 上不存在於 `source` 的 key；
  - 將 `source` 的所有 own enumerable key 複製到 `target`（淺層覆寫）；
  - **回傳同一個 `target` 參考**（不可 new）。
- `applyArrayInPlace(target, items)`：
  - `target.length = 0` 後 `target.push(...items)`；
  - **回傳同一個 `target` 參考**。

純函式、無副作用、不依賴 `window`/`Date`/`localStorage`。

### 4.2 改 `api-data.js`

1. **建立模組級穩定容器**（移到 `loadAll` 之外、IIFE 內頂層，僅初始化一次）：
   ```js
   const MACHINE_META  = {};
   const MACHINE_ORDER = [];
   const SERVICES      = [];
   const INSTALLS      = [];
   window.TOPO = { MACHINE_META, MACHINE_ORDER, SERVICES };          // 只 assign 一次
   window.MOCK = { INSTALLS, VERSIONS: {}, JOB_SCRIPTS: {} };        // 只 assign 一次
   ```
2. **`loadAll()` 內**：
   - 原本宣告全新物件的地方改用區域暫存名（例如 `nextMeta` / `nextOrder` / `nextServices` / `nextInstalls`），映射邏輯（systems 排序、VM linked/pending 分支、INSTALLS map、SERVICES + bundle 合成）**邏輯完全不變**，只是寫進暫存。
   - 末端不再 `window.TOPO = {...}` / `window.MOCK = {...}` reassign，改為原地套用：
     ```js
     applyObjectInPlace(MACHINE_META, nextMeta);
     applyArrayInPlace(MACHINE_ORDER, nextOrder);
     applyArrayInPlace(SERVICES, nextServices);
     applyArrayInPlace(INSTALLS, nextInstalls);
     ```
   - `CockpitGroups.init`、`IS_MACHINE` 的 prefetch、`systemLabelToId` 等後續邏輯改讀暫存或穩定容器（內容相同，擇一即可）。
   - `window.TRENDS = { RANGES, series, fmt, prefetchMetrics }` 維持現狀（內容皆函式/常數，無 stale 問題）；`window._vmsByLinkedSystem`/`_linkedSystemIDs`/`_allSystems` 維持現狀（消費端以 `window.` 動態存取，非解構）。
3. **刷新間隔**（`api-data.js:417` 附近的 `setInterval(..., 30000)`）改用 `window.CockpitTopoState.REFRESH_INTERVAL_MS`。

### 4.3 改 html（在 `api-data.js` 前載入 topo-state.js）

- `machine.html:183` 之前插入 `<script src="topo-state.js"></script>`
- `topology.html:309` 之前插入 `<script src="topo-state.js"></script>`

### 4.4 不可變更

`topo.js`、`trends.js`、`manage.js`、後端 Go、agent 任何檔案 —— 一律不動。

## 5. 測試計畫（`cockpit_frontend/topo-state.test.js`，node:test）

執行：`node --test cockpit_frontend/`

1. **參考穩定性（鎖死 bug）**：對同一 `target` 物件呼叫 `applyObjectInPlace` 兩次（內容不同），assert 回傳值 `===` 原 target、且持有舊參考的變數能看到新內容。
2. **key 移除**：第一次有 key `a,b`，第二次只有 `a`，assert `b` 從 target 消失。
3. **陣列原地更新**：對同一 array 呼叫 `applyArrayInPlace` 兩次，assert `===` 原 array、length 與內容正確、舊參考看得到新值。
4. **常數**：`REFRESH_INTERVAL_MS === 15000`。

（映射細節 `buildTopoState` 不在本次搬移，故不測；核心 bug 已由 1–3 完整覆蓋。）

## 6. 驗收標準

- `node --test cockpit_frontend/` 全綠。
- `go test ./...` 不受影響（無 Go 變更）。
- 手動：拓樸頁 / 機器頁不重整，≤15 秒內 CPU/MEM 等當前值自動跳動；服務狀態、軟體版本同樣自動更新。

## 7. 風險與緩解

- **暫存改名遺漏**：`loadAll` 內 `MACHINE_META`/`MACHINE_ORDER`/`SERVICES`/`INSTALLS` 引用點多，改名須完整。緩解：Codex 實作後以 `node --test` + 手動載入頁面驗證；review diff 確認無殘留舊 reassign。
- **`window.MOCK.VERSIONS/JOB_SCRIPTS`**：保持空殼物件即可（消費端僅解構不依賴內容更新）。
- **載入順序**：topo-state.js 必須在 api-data.js 之前載入，否則 `window.CockpitTopoState` 未定義。已於 4.3 指定插入位置。
