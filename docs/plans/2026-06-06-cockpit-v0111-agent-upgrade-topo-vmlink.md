# Cockpit v0.1.11 — UI 升級 agent / 拓樸摺疊 / VM 對帳修正 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development。

**Goal:** ① Web UI 一鍵升級各機 agent；② 拓樸機器節點可展開/收起（persist）；③ VM↔已註冊機器以 **machine UUID** 確定性對帳（SMBIOS 端序正規化）+ UI 手動連結後備，消除「VM 卡 + 機器卡」重複。

---

### T1: UI 升級 agent（Go + FE）

機制（沿用 long-poll，零新協定）：
- store：`machine_state` 加 `upgrade_requested INTEGER NOT NULL DEFAULT 0`（CREATE TABLE 已 IF NOT EXISTS——直接改表定義即可，舊庫可重建；**生產庫不可重建** → 用 `ALTER TABLE ... ADD COLUMN` 防衛：Open 後執行 `ALTER TABLE machine_state ADD COLUMN upgrade_requested INTEGER NOT NULL DEFAULT 0` 並忽略 duplicate column error）。新函式 `SetUpgradeRequested(machine)`、`TakeUpgradeRequested(machine) bool`（一次性，同 check 模式）。
- server：`POST /api/systems/{id}/upgrade-agent` → 以該 system 的 **label** 設旗標 → `{ok}`（apiSystemSub 加分支）。`vtPoll` 迴圈優先序：job → upgrade → check（upgrade 在 check 前）：`TakeUpgradeRequested` → 回 `{"type":"upgrade"}`。
- agent：`Run()` 的 switch 加 `case "upgrade"`：呼叫 `selfupdate.Run(nil, githubBase, repo(env COCKPIT_REPO 或預設), version, "")`；成功（實際有更新）→ log + `os.Exit(0)`（systemd Restart=always / launchd KeepAlive 自動以新 binary 重啟）；已最新或失敗 → log 繼續迴圈（fail-safe）。注意 selfupdate.Run 目前回傳 error；需可區分「已最新」與「已更新」→ 若現簽名不可分，改回傳 `(updated bool, err error)` 並同步 upgrade.go 呼叫端。
- FE（manage.js）：機器列顯示 agent_version；當 `agent_version != serverVersion`（serverVersion 來自 /api/version，已有）→ 顯示「⬆ 升級 agent」鈕 → POST → toast「已派送升級，約 30 秒後生效」；列表 30s 後自動重抓。
- 測試：store 旗標一次性；server endpoint 設旗標 + vtPoll 回 upgrade（httptest）；agent case 用注入避免真 exit（將 selfupdate 呼叫與 exit 包成可注入欄位 `a.doUpgrade func() `預設真實作，測試覆寫驗證被呼叫）。

### T2: 拓樸節點展開/收起（FE）

先讀 topo.js——機器卡右上已有「−」鈕（mock 時代可能已有部分實作）。要求：
- 點「−/+」收起/展開該機器：隱藏其 services 與（經 services 鏈到的）software 節點、邊重繪；收起時機器卡顯示摘要（N 服務 · M 軟體）。
- 狀態存 localStorage（key 含 system id），載入時還原。
- 30s 自動刷新（topo:refresh）後維持摺疊狀態。
- 若既有「−」鈕已有功能，以其為基礎補 persist + 邊重繪正確性。

### T3: VM 對帳（Go + FE）

- agent：新增 `machineUUID()`（`internal/collect` 或 agent 內）——linux 讀 `/sys/class/dmi/id/product_uuid`（無權限/不存在回 ""）；darwin `ioreg -rd1 -c IOPlatformExpertDevice` 抓 IOPlatformUUID；其餘 ""。enroll body 與 heartbeat body 都帶 `machine_uuid`（heartbeat 帶是為了讓既有已註冊機器回填）。
- store：systems 加 `machine_uuid TEXT`（ALTER 防衛同 T1）；`SetMachineUUID(id, uuid)`（空值不寫）；HeartbeatByID 簽名加 uuid 參數或新函式。
- server reconcile（reportVMs 內）匹配優先序：
  1. **UUID**：`normalizeUUID(vm.UUID)` 與 `normalizeUUID(system.machine_uuid)` 相等。`normalizeUUID`：去 dash/空白/小寫 → 32 hex；**SMBIOS 端序**：vmx `uuid.bios` 是原始位元組序，guest 的 product_uuid 前三組（4-2-2 bytes）是 little-endian 表示 → 對其中一方產生「swap 變體」（reverse bytes of group1,2,3）後雙向比對（兩個 candidate set 任一相等即 match）。單元測試用真實樣本：vmx `564d98e4399f8e80-a3ec5a13a0a490f5` ↔ guest `E4984D56-9F39-808E-A3EC-5A13A0A490F5`（注意第二組 399f→9F39、第三組 8e80→808E）。
  2. label == vm.Name（既有）。
  3. 寬鬆名稱：normalize（小寫去非英數）後 `vmName contains label || label contains vmName` 且長度 ≥4——`curtishomeservice` vs `homeservice` 可中。
- 手動連結 API：`POST /api/vms/{hostSystemID}/{uuid}/link` body `{system_id}` → LinkVM；`DELETE` 同路徑 → unlink（vms.linked_system_id 清空 + guest system 的 kind/host_id 還原 physical/NULL）。
- FE（api-data.js / topo.js）：未連結 VM 的 pending 卡加「連結到已註冊機器…」下拉（列出 kind=physical 且未被其它 vm link 的 systems）→ POST link → 重抓。已連結後：pending 卡消失（既有邏輯），對應機器卡 role 已標 `VM @ host`。
- 驗證現實案例：發版後 mini agent（host）下一輪 report-vms 觸發 reconcile——home-service 的 agent 是 0.1.7 不會回報 uuid，但**寬鬆名稱規則**（curtishomeservice ⊃ homeservice）應直接命中完成連結；upgrade home-service agent 後 uuid 回填成為確定性錨。

### 驗收
- 單元/httptest 全綠 + CI 三平台綠。
- OrbStack VM e2e：T1 全流程（佈 0.1.10 → server 端旗標 → agent 收 upgrade → selfupdate 到 release 最新 → 服務重啟回報新版本）⚠️ agent 在 VM 跑的是 dev build 路徑 /tmp/orbtest——selfupdate 會去抓 GitHub release 替換該檔，可接受。T2/T3 FE 用 Chrome 驗。
- 生產：升 mini（serve+agent）→ 觀察 CurtisHomeService 自動連結 home-service → UI 確認重複消失；用新按鈕從 UI 升級 ubuntu-llm。
