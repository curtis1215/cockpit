# Cockpit P3 — Enrollment 收斂 + 管理 API + 管理頁 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 單一 token 模型（順修 issues #1/#2/#3）、機器生命週期管理（新增→每機 enroll token→agent 上線→改名/刪除）、軟體定義 CRUD（回寫 inventory YAML + 熱載）、`manage.html` 全接真。

**Architecture:** (a) **token 收斂**：`agentSystem` 統一 resolver 全面接管（heartbeat 也走它；VT 端點接受 systems token——機器名 = system label）。(b) **每機 enroll token**：`POST /api/systems` 建 pending 機器 + 一次性 enroll token；agent enroll body 同時支援 `enroll_token`（per-machine，綁定該 system）與 legacy `enroll_secret`（共享 bootstrap）。(c) **inventory 熱載 + 回寫**：Server.inv 改受 RWMutex 保護（`getInv()/setInv()`），軟體 CRUD 端點改寫記憶體 inv 並 marshal 回 `InventoryPath`（有設定才寫檔）。(d) manage 頁脫 mock。

**Tech Stack:** 既有 stack；yaml.v3 Marshal 回寫。

---

## API 契約（新/改）

- `POST /api/systems` `{label, role?}` → 200 `{id, label, enroll_token}`（status=pending，enroll_token=`ck_enroll_`+randHex(12)；label 重複 → 409）
- `PATCH /api/systems/{id}` `{label?, role?}` → 200 systemMap；改 label 連動：無（installs 以 label 字串對應——PATCH label 時若該機在 installs 有資料回 409 `{"error":"machine has installs; rename not supported yet"}`，避免斷鏈）
- `DELETE /api/systems/{id}` → 204（連帶刪 metrics/metrics_latest/services/該機 vms 列與 linked 標記；installs 不動）
- `POST /api/systems/{id}/enroll-token` → 200 `{enroll_token}`（重生；舊 token 失效）
- `POST /api/agent/enroll` body 擴充：`{enroll_token}` 優先（查 systems.enroll_token → 綁定該列：寫 os/arch/agent_version、status online、發 agent_token、**清空 enroll_token**（一次性））；否則 legacy `{enroll_secret, label, os, arch}` 走 P0 路徑。錯誤一律 401。
- 軟體 CRUD（manage 頁用；全部變更後熱載 inv + 觸發 RefreshUpstream(該軟體) 可省略為全量 onCheck）：
  - `POST /api/software` `{name, kind?, latest_source, changelog?, machine, current_cmd, version_regex?, update:{type, cmd?|runner?, prompt?, cwd?, invoke?}}` → 200 `{ok}`；同名 software 已存在 → 追加 install（同 machine 已有 → 409）
  - `PATCH /api/software/{name}/{machine}` 同欄位部分更新（latest_source/changelog 屬 software 層、其餘 install 層）→ 200
  - `DELETE /api/software/{name}/{machine}` → 204（最後一個 install 時整個 software 移除；同時刪 store 的該 install 列）
- token 收斂（issues #1/#2/#3）：
  - heartbeat handler 改用 `agentSystem`（任一 token 可心跳）。
  - `vtMachine` 改：先 inventory token；否則 systems token → 回 system **label** 當機器名。
  - `vtJobSub`：解析 id 後 `GetJob` 比對 `job.Machine != machineName` → 403。
  - `ClaimOldestQueued`：UPDATE 加 `AND status='queued'`，`RowsAffected()==0` → 回 nil。

## 檔案結構
```
internal/store/store.go        # systems 補：CreateSystemPending/SystemByEnrollToken/ConsumeEnrollToken/
                               #   RegenEnrollToken/UpdateSystem/DeleteSystemCascade/DeleteInstall
internal/server/server.go      # inv → invMu+getInv()/setInv()；所有 s.inv 讀取改 getInv()
internal/server/manage_api.go  # systems CRUD + enroll-token + software CRUD + yaml 回寫
internal/server/agent_api.go   # enroll 擴充 enroll_token；heartbeat 改 agentSystem
internal/server/agent_vt_api.go# vtMachine 收斂 + vtJobSub 403
internal/inventory/inventory.go# Marshal(inv) ([]byte, error)（round-trip 寫回）
cockpit_frontend/manage.js     # 接真（P3-FE task）
cockpit_frontend/manage.html   # 移除 mock scripts
```

---

### Task 1: token 收斂 + 佇列硬化（issues #1/#2/#3）

**Files:** Modify `internal/server/agent_api.go`、`agent_vt_api.go`、`internal/store/store.go`; Tests in `internal/server/convergence_test.go`、store 既有測試擴充

行為（TDD，測試先行）：
1. `TestHeartbeatWithInventoryToken`：vtServer 起來後，用 `tok-mac`（inventory）打 `/api/agent/heartbeat` → 204，且 `mac` 的 system 出現且 last_seen 更新（agentSystem 路徑）。heartbeat handler 改為：`sysID, ok := s.agentSystem(r)`；ok → `s.st.Heartbeat`… 注意 P0 `Heartbeat(token,version)` 是以 token 查；改為新 store 函式 `HeartbeatByID(systemID, version string)`（UPDATE last_seen/status/agent_version WHERE id），heartbeat handler 解析 body 的 version 後呼叫。保留舊 `Heartbeat` 不刪（P0 測試相容），新 handler 用新函式。
2. `TestVTWithSystemsToken`：用 `RegisterSystem("mac","linux","arm64")` 拿 systems token（label=mac 與 inventory machine 同名）→ `/api/agent/installs` 200 且回 cc 的 install（vtMachine 接受 systems token→label）。
3. `TestCrossMachineJob403`：機器 A token 對機器 B 的 job POST log → 403。
4. store 測試：`TestClaimAtomicGuard`——手動把 job UPDATE 成 running 後再 ClaimOldestQueued → nil（RowsAffected 0 路徑用第二次 claim 模擬）。
5. Commit：`feat(go): unified token model (heartbeat/vt), cross-machine job 403, atomic claim (closes #1 #2 #3 partial)`

### Task 2: systems 管理 API + 每機 enroll token

**Files:** Modify `internal/store/store.go`（schema systems 已有 enroll_token 欄？讀 schema.sql 確認，沒有就 ALTER 加在 CREATE TABLE——注意既有 db 相容：用 `ALTER TABLE` 不可重複，直接改 CREATE TABLE IF NOT EXISTS 對新庫即可 + 容忍舊庫缺欄（本專案 db 可重建，直接改 CREATE TABLE））; Create `internal/server/manage_api.go`（systems 部分）; Modify `agent_api.go`（enroll 擴充）; Test `internal/server/manage_api_test.go`

行為（測試先行）：
1. `POST /api/systems {"label":"newbox"}` → `{id,label,enroll_token}`；重複 label → 409；GET /api/systems 出現 pending 卡（liveStatus：status 欄為 pending 且無 last_seen→不可誤判 offline——pending 系統 liveStatus 直接回 "pending"：以 `x.Status=="pending" && x.Latest.TS==0` 判）。
2. agent 用 `{"enroll_token":"...","os":"linux","arch":"arm64"}` enroll → 200 `{system_id, agent_token}`；該列 status online、os/arch 填入、enroll_token 清空；再用同 enroll_token → 401（一次性）。
3. `POST /api/systems/{id}/enroll-token` → 新 token，舊的 401。
4. `PATCH` label/role 生效；label 與 installs 衝突 → 409。`DELETE` → 204 且 metrics_latest/services 連帶清掉。
5. store 新函式：`CreateSystemPending(label, role) (id, enrollToken string, err)`（label unique 檢查）、`SystemByEnrollToken`、`ConsumeEnrollToken(id, os, arch, version) (agentToken string)`、`RegenEnrollToken(id)`、`UpdateSystem(id, label, role)`（空字串=不改）、`DeleteSystemCascade(id)`。
6. Commit：`feat(go): machine lifecycle api (create/patch/delete/enroll-token) + per-machine enroll`

### Task 3: inventory 熱載 + 軟體 CRUD 回寫

**Files:** Modify `internal/server/server.go`（invMu RWMutex + getInv/setInv；全 server 檔案把 `s.inv` 讀取換 `s.getInv()`）、`internal/inventory/inventory.go`（`Marshal(Inventory) ([]byte,error)`：輸出與 LoadText 相容的 YAML——machines map + software list，update map 依 type 放欄位）、Create manage_api.go software 部分；Modify `cmd/cockpit/serve.go`（把 InventoryPath 傳給 server：`srv.SetInventoryPath(cfg.InventoryPath)`）; Test 追加

行為（測試先行）：
1. `TestInventoryMarshalRoundTrip`：LoadText(Marshal(LoadText(fixture))) 等值（machines/software/installs/update 全欄位）。
2. `POST /api/software`（新軟體+install）→ 200；GET /api/installs 含新列（kind/update_kind 正確）；in-memory inv 更新（vtPoll 立刻可 claim 該軟體 job）；**InventoryPath 設定時**檔案被改寫且可重 Load。manage_api 寫檔：`os.WriteFile(path, Marshal(inv), 0644)`；無 path（測試常態）只改記憶體。
3. 同 software 加第二台 install、PATCH 改 current_cmd/latest_source、DELETE 最後 install 移除整個 software + store DeleteInstall。
4. 409 案例：POST 已存在的 (software,machine)。
5. 併發安全：所有讀 inv 的 handler 改 `inv := s.getInv()` 開頭取快照（值複製便宜——struct 含 slice/map 為淺拷貝，**寫入端永遠整個替換新建的 Inventory**（從舊值 deep-copy software slice 後改），不就地修改舊值，避免讀端 data race）。`go test -race ./internal/server/` 必過。
6. Commit：`feat(go): software crud with inventory yaml writeback + hot reload (rwmutex)`

### Task 4: manage.html 接真（P3-FE）

**Files:** Modify `cockpit_frontend/manage.js`、`manage.html`（移除 mock-data.js/topo-data.js/store.js script）

先讀 manage.js/manage.html 全文。行為：
1. 機器區：列表吃 `/api/systems`（pending 顯示 enroll token 區塊——**新增機器後 modal 顯示**：enroll token + 安裝指令片段 `cockpit agent -config agent.json`（內容含 server_url + enroll_token 的 json 範例，文案簡潔）+ 複製按鈕）；新增→POST /api/systems；改名→PATCH（409 顯示「該機器有軟體綁定，暫不支援改名」）；刪除→confirm 後 DELETE；重生 token→POST enroll-token 後更新顯示。
2. 軟體區：列表吃 `/api/installs`；新增/編輯/刪除 → POST/PATCH/DELETE `/api/software...`（表單欄位映射 API 契約；update type=command 顯示 cmd 欄、type=agent 顯示 runner/prompt/cwd——沿用 mock 表單既有欄位結構，缺的補）。
3. 所有變更後重抓列表。錯誤 toast。`node --check`、`go build ./...`、grep 無 mock 引用。
4. Commit：`feat(web): manage page real machine lifecycle + software crud`

### Task 5: OrbStack VM e2e（P3 驗收）

被測只跑 VM。流程：
1. 交叉編譯→部署 VM；serve 配 inventory（含既有 demoapp）+ 新 db。
2. **管理頁**（Chrome）：新增機器 `vm2` → 拿 enroll token → VM 內起第二個 agent（config 用 enroll_token、無 agent_token）→ 機器頁/拓樸出現 vm2 online 真指標。
3. 管理頁新增軟體（custom `echo 1.2.3` / current_cmd `echo demo2 1.0.0`、command update）→ 清單頁出現、可觸發更新走完。
4. 刪除測試軟體與機器、驗 UI 同步。發現 bug 回修。

## Self-Review（已執行）
- 覆蓋 spec §11 + issues #1#2#3；inventory machines 區段轉為 optional legacy（不再必填 agent_token；software CRUD 不動 machines 區段——Marshal 保留原 machines）。
- 風險註記：yaml 回寫丟註解（可接受）；PATCH label 斷鏈以 409 防護；pending liveStatus 分支；race 用「快照讀 + 整體替換寫」策略。
