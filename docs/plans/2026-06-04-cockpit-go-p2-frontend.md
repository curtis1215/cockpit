# Cockpit P2-frontend — 拓樸/機器/趨勢頁接真 API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `topology.html`（拓樸）與 `machine.html`（機器詳情+趨勢圖）脫離 mock，改吃 P2 真 API（systems enriched / services / vms / installs / metrics?range），在 OrbStack VM 驗收。

**Architecture:** 新增 `cockpit_frontend/api-data.js` 轉接層：async 抓資料 → 組出與 mock 同形的 `window.TOPO`（MACHINE_META/MACHINE_ORDER/SERVICES）與 `window.TRENDS`（RANGES/series/fmt；metrics 預抓 4 個 range 進快取讓 `series()` 維持同步介面）→ **資料就緒後動態注入** `topo.js` / `trends.js`（渲染碼不動或極小改）。兩頁移除 mock-data.js/topo-data.js/store.js/trends-data.js。manage 頁不動（P3）。

**Tech Stack:** vanilla JS fetch；後端 API 已存在不改。

---

## API → mock 形狀對照（轉接規則）

`GET /api/systems` 每列 → `MACHINE_META[id]`（key 用 system `id`）：
| mock 欄位 | 來源 | 轉換 |
|---|---|---|
| label/role/os/arch/status | 同名 | role 空字串照給；status online/warn/offline 直通 |
| cpu/mem/disk/gpu/temp | 同名 | null 直通（前端已處理 null） |
| net | net_up/net_down | `{up, down}`（null→null 整組：兩者皆 null 時 net=null） |
| load | load | `[load]`（null→null） |
| uptime | uptime 秒 | 人類化：`<90m→"Xm"`、`<48h→"X.Xh"`、其餘 `"X.Xd"`；null→"—" |
| agent | agent_version | 空字串→"—" |
| agent_status | 同名 | 直通 |
| last_seen | last_seen | 相對時間："剛剛"(<30s)/"X 秒前"/"X 分鐘前"/"X 小時前"；解析失敗給原字串。注意值為 UTC（`YYYY-MM-DD HH:MM:SS` 無時區），parse 時補 `+"Z"` 換 ISO |
| spark | 同名 | 直通（空陣列→null） |
| warnings | 計算 | status==warn 時依門檻組訊息（如 "mem 91%"）；否則 [] |

`MACHINE_ORDER`：systems 依 label 排序的 id 陣列。

`SERVICES`（陣列）：
1. `GET /api/services` 每列 → `{ id: "svc_"+system_id+"_"+name, machine: system_id, name, kind, status, cpu, mem, port, software: software_ids||[], depends: depends||[] }`。
2. 每台機器若在 `GET /api/installs` 有安裝（machine 名比對 system **label**），合成一個 bundle：`{ id: "bundle_"+system_id, machine: system_id, name: "軟體", kind: "bundle", status: "ok", software: [該機 install id 列表] }`（mock 既有 bundle 形狀以 topo-data.js 實際為準——**先讀 topo-data.js 的 SERVICES 範例與 topo.js 的消費方式**，缺欄位補成它要的樣子）。
3. `GET /api/vms`：linked 的 VM 本身就是 systems 列（kind=vm、host_id）會自然出現在 MACHINE_META；在該 VM 的 meta `role` 前綴 `"VM@"+host label`（若 role 空則 role=`"VM @ "+hostLabel`）。未 linked 的 VM 以 pending 機器卡呈現：`MACHINE_META["vm_"+uuid] = { label: name, status: "pending", role: "未連線 VM @ "+hostLabel, os: guest_os, ... 其餘 null }` 並加入 MACHINE_ORDER 尾端。

`TRENDS`：`RANGES` 沿用 trends-data.js 的 `{1h,12h,24h,7d}`（label/stepMin 保留供 UI）；啟動時對當前機器 4 個 range 各抓一次 `GET /api/systems/{id}/metrics?range=` 進 `cache[range]`；`series(machineId, metric, range)` 同步從 cache 組 `{metric, points, times, min, max, avg, last, unit, pct, color}`（欄位對應 metric→cpu/mem/disk/gpu/netUp(net_up)/netDown(net_down)/load/temp；times 由 `t`（unix 秒）格式化 HH:MM、7d 用 M/D；unit/pct/color 沿用 trends-data.js 的 metricBase 表——把那張表搬進 api-data.js）。資料點為 null 的 metric（如 gpu 全 null）→ series 回 null（trends.js 已有 filter(Boolean)）。30s 定時重抓當前 range 並重畫（呼叫 trends.js 既有重繪入口；若無全域入口，dispatch `window.dispatchEvent(new Event("trends:refresh"))` 並在 trends.js 加 3 行監聽——唯一允許的 trends.js 修改）。

**Bootstrap**：兩頁 `<script src="api-data.js">` 取代全部 mock script；api-data.js `(async()=>{ await loadAll(); inject("topo.js"|"trends.js"); })()`，inject = `document.createElement("script")` append。machine.html 同時需要 TOPO（header 卡）與 TRENDS。頁面用 URL 參數（讀 machine.html/topo.js 實際取 id 的方式）決定預抓對象。載入失敗顯示「無法連線後端」訊息（沿用 FE-1 的 showLoadError 樣式自行內建）。

---

### Task 1: api-data.js 轉接層 + 兩頁接線

**Files:** Create `cockpit_frontend/api-data.js`; Modify `cockpit_frontend/topology.html`, `cockpit_frontend/machine.html`（script 區塊換成 `api-data.js`）

- [ ] **Step 1:** 完整讀 `topo-data.js`（MACHINE_META/SERVICES 形狀）、`topo.js`（消費方式：解構、card 渲染、software chips 來源——特別注意是否引用 `window.MOCK.INSTALLS`）、`trends-data.js`（RANGES/metricBase/series 輸出形狀）、`trends.js`（series 呼叫點、range 切換、重繪入口）、`machine.html`（取機器 id 的方式）。
- [ ] **Step 2:** 依上方對照表實作 `api-data.js`（含 `api()` fetch helper、loadAll、TOPO 組裝、TRENDS cache+series、bootstrap inject、30s refresh、錯誤顯示）。若 topo.js 引用 `window.MOCK.INSTALLS`（software chip 名稱），api-data.js 同步提供 `window.MOCK = { INSTALLS: <真 /api/installs 列表> }` 形狀相容。
- [ ] **Step 3:** 兩頁 html：移除 mock-data.js/topo-data.js/store.js/trends-data.js 與頁面主 script 的 `<script>`，只留 `<script src="api-data.js"></script>`（topo.js/trends.js 由它注入；machine.html 若 header 渲染在 trends.js 外的 inline script，把該 inline 邏輯的觸發也掛到注入完成後——以實際為準）。
- [ ] **Step 4:** `node --check cockpit_frontend/api-data.js` + `go build ./...`（重嵌）。grep 確認兩頁不再引用 mock：`grep -n "mock-data\|topo-data\|store.js\|trends-data" cockpit_frontend/topology.html cockpit_frontend/machine.html` → 空。
- [ ] **Step 5:** Commit：`feat(web): topology/machine pages load real systems/services/vms/metrics via api-data adapter`

### Task 2: OrbStack VM 端到端驗收（Chrome）

無程式碼變更；被測服務只跑 VM。流程同 P2-T9（交叉編譯→/mnt/mac 複製→VM 起 serve+agent+docker redis）。Chrome 驗：
- [ ] 拓樸頁：vm1 機器卡顯示真 cpu/mem/disk/uptime/spark、p2redis docker 服務節點、軟體 bundle（demoapp/slowapp）、狀態色。
- [ ] 機器頁：header 真值；趨勢圖 1h 有曲線且最後點≈當前值；切 12h 顯示 10m 桶；gpu/temp 圖自動隱藏（null）；30s 自動刷新。
- [ ] 發現 bug → 回 Task 1 修補 commit。

---

## Self-Review（已執行）
1. 覆蓋：spec §7 前端三頁中 topology/machine 本計畫；manage→P3。VM 層以「linked=系統卡+role 標註、unlinked=pending 卡」最小呈現（fancy 視覺留 P3+）。
2. 無 placeholder；轉接規則含全部欄位轉換與 null 行為。
3. 唯一允許的既有渲染檔修改：trends.js 加 refresh 事件監聽（3 行）；其餘渲染碼不動。
