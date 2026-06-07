# 機器分組（Machine Groups）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 機器可分組（個人/工作/專案…），軟體/拓樸/機器三頁以頂部全域 segmented switcher 過濾視圖；管理頁可編輯群組。

**Architecture:** 後端只負責存 `grp` 欄位並在 `/api/systems` 回傳 `group`（原始值）與 `effective_group`（VM 繼承 host 計算後的值）；過濾完全在前端。新共用元件 `groups.js` 管理切換器與 localStorage 狀態，三頁各自在 render 時以 `effective_group` 過濾。

**Tech Stack:** Go（net/http + SQLite, `internal/store` / `internal/server`）、vanilla JS 前端（無框架、無前端測試，Go 測試走 TDD）。

**Spec:** `docs/specs/2026-06-07-machine-groups-design.md`

**重要背景知識（實作前必讀）：**

1. **前端架構**：四頁各自獨立。`index.html`→`app.js`（自己 fetch `/api/installs`）；`topology.html`/`machine.html`→`api-data.js`（fetch 後組 `window.TOPO`/`window.MOCK`/`window.TRENDS`，再動態注入 `topo.js` 或 `trends.js`）；`manage.html`→`manage.js`。
2. **stale-destructure 陷阱**：`topo.js`/`trends.js` 在模組頂部 `const { MACHINE_META, MACHINE_ORDER } = window.TOPO;` 解構一次。所以**群組過濾必須在 render 時做**（每次 render 重新呼叫 matcher），不能靠替換 `window.TOPO`。
3. **localStorage key**：`cockpit-group` 已被軟體頁的顯示分組模式占用，群組切換器必須用 `cockpit-machine-group`。
4. **installs 的 `machine` 欄位是 system label，不是 id**；`services` 的 `machine` 欄位是 system id。過濾時要注意對應。
5. **群組 sentinel**：localStorage 值 `""` = 全部、`"__none__"` = 未分組、其他 = 群組名。
6. **欄位命名**：SQLite 欄位叫 `grp`（`group` 是 SQL 保留字），JSON 對外叫 `"group"`。
7. 測試指令一律在 repo root：`cd /Users/curtis/Dev/cockpit`。

---

### Task 1: store — `grp` 欄位（migration + struct + SetSystemGroup）

**Files:**
- Modify: `internal/store/schema.sql`（systems CREATE TABLE）
- Modify: `internal/store/store.go`（Open migration、System struct、cols、scanSystem、新增 SetSystemGroup）
- Test: `internal/store/store_test.go`（檔尾附加）

- [ ] **Step 1: Write the failing test**

在 `internal/store/store_test.go` 檔尾加入（該檔已 import `path/filepath` 與 `testing`；若缺少請補）：

```go
func TestSystemGroupColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := st.CreateSystemPending("gbox", "")
	if err != nil {
		t.Fatal(err)
	}
	// 預設未分組
	sys, err := st.SystemByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if sys.Grp != "" {
		t.Fatalf("default grp = %q, want empty", sys.Grp)
	}
	// 設定群組（中文 OK）
	if err := st.SetSystemGroup(id, "工作"); err != nil {
		t.Fatal(err)
	}
	sys, _ = st.SystemByID(id)
	if sys.Grp != "工作" {
		t.Fatalf("grp = %q, want 工作", sys.Grp)
	}
	// 清空（解除分組）
	if err := st.SetSystemGroup(id, ""); err != nil {
		t.Fatal(err)
	}
	sys, _ = st.SystemByID(id)
	if sys.Grp != "" {
		t.Fatalf("grp = %q, want empty after clear", sys.Grp)
	}
	// 不存在的 id → ErrNotFound
	if err := st.SetSystemGroup("sys_nope", "x"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// migration 冪等：關掉重開同一個 db 不應報錯
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	if sys, err = st2.SystemByID(id); err != nil || sys.Grp != "" {
		t.Fatalf("after reopen: err=%v grp=%q", err, sys.Grp)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSystemGroupColumn -v`
Expected: FAIL（compile error：`sys.Grp` 與 `st.SetSystemGroup` 未定義）

- [ ] **Step 3: Implement**

3a. `internal/store/schema.sql` — systems 表的 `machine_uuid TEXT NOT NULL DEFAULT ''` 那行後面（`);` 之前）加一行：

```sql
  grp TEXT NOT NULL DEFAULT ''
```

注意前一行要補逗號。

3b. `internal/store/store.go` — `Open()` 內，現有的 `machine_uuid` 防禦式 ALTER 區塊（搜尋 `ALTER TABLE systems ADD COLUMN machine_uuid`）後面，加同款式的：

```go
	if _, err := db.Exec(`ALTER TABLE systems ADD COLUMN grp TEXT NOT NULL DEFAULT ''`); err != nil {
		if !contains(err.Error(), "duplicate column name") {
			return nil, err
		}
	}
```

3c. `System` struct — `MachineUUID` 欄位後加：

```go
	Grp          string `json:"group"`
```

3d. `cols` 常數 — 字串尾端 `machine_uuid` 後追加 `,grp`：

```go
const cols = "id,label,role,os,arch,kind,host_id,status,agent_version,agent_status,last_seen,agent_token,enroll_token,created,machine_uuid,grp"
```

3e. `scanSystem` — Scan 引數列尾端（`&machineUUID` 之後）追加 `&s.Grp`：

```go
	err := row.Scan(&s.ID, &s.Label, &s.Role, &s.OS, &s.Arch, &s.Kind, &hostID,
		&s.Status, &s.AgentVersion, &s.AgentStatus, &s.LastSeen, &agentToken, &enrollToken, &s.Created, &machineUUID, &s.Grp)
```

3f. 新增方法（放在 `UpdateSystem` 之後）：

```go
// SetSystemGroup sets the machine's group; empty string clears it (= ungrouped /
// for VMs: inherit host).
func (s *Store) SetSystemGroup(id, grp string) error {
	res, err := s.db.Exec(`UPDATE systems SET grp=? WHERE id=?`, grp, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/store/ -v`
Expected: 全部 PASS（含既有測試 — 若 ListSystems 等其他 SELECT 沒用 `cols` 常數而是手寫欄位清單，逐一檢查 `grep -n "SELECT id,label" internal/store/*.go` 是否有遺漏 grp 的 scan 對不上欄位數）

- [ ] **Step 5: Commit**

```bash
git add internal/store/schema.sql internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): systems grp column with defensive migration + SetSystemGroup"
```

---

### Task 2: server — effective group 計算 + `/api/systems` 回傳 `group` / `effective_group`

**Files:**
- Modify: `internal/server/monitor_api.go`（`systemMap` 簽名、`apiSystemsEnriched`、新增 `effectiveGroups`）
- Modify: `internal/server/manage_api.go`（`patchSystem` 末段的 systemMap 呼叫）
- Test: `internal/server/manage_api_test.go`（檔尾附加）

- [ ] **Step 1: Write the failing test**

在 `internal/server/manage_api_test.go` 檔尾加入：

```go
// ── group / effective_group ──────────────────────────────────────────────────

func TestSystemsGroupAndEffectiveGroup(t *testing.T) {
	srv, st := newTestServer(t)

	// host 設群組「工作」
	hostID, _, err := st.CreateSystemPending("ghost1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSystemGroup(hostID, "工作"); err != nil {
		t.Fatal(err)
	}
	// guest1：VM、未覆寫 → 繼承 host
	guest1, _, err := st.CreateSystemPending("gguest1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkVM(hostID, "uuid-g1", guest1); err != nil {
		t.Fatal(err)
	}
	// guest2：VM、覆寫成「個人」
	guest2, _, err := st.CreateSystemPending("gguest2", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkVM(hostID, "uuid-g2", guest2); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSystemGroup(guest2, "個人"); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, srv, "GET", "/api/systems", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	find := func(id string) map[string]any {
		for _, m := range list {
			if m["id"] == id {
				return m
			}
		}
		t.Fatalf("system %s not in list", id)
		return nil
	}
	h := find(hostID)
	if h["group"] != "工作" || h["effective_group"] != "工作" {
		t.Fatalf("host: group=%v eff=%v", h["group"], h["effective_group"])
	}
	g1 := find(guest1)
	if g1["group"] != "" || g1["effective_group"] != "工作" {
		t.Fatalf("guest1 should inherit: group=%v eff=%v", g1["group"], g1["effective_group"])
	}
	g2 := find(guest2)
	if g2["group"] != "個人" || g2["effective_group"] != "個人" {
		t.Fatalf("guest2 should override: group=%v eff=%v", g2["group"], g2["effective_group"])
	}
}

func TestEffectiveGroupHostMissing(t *testing.T) {
	srv, st := newTestServer(t)
	// VM 的 host 不存在（懸空 host_id）→ effective_group 視為未分組
	guest, _, err := st.CreateSystemPending("orphanvm", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkVM("sys_ghosthost", "uuid-x", guest); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, srv, "GET", "/api/systems", "")
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m["id"] == guest {
			if m["effective_group"] != "" {
				t.Fatalf("orphan vm eff = %v, want empty", m["effective_group"])
			}
			return
		}
	}
	t.Fatal("guest not found")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run 'TestSystemsGroupAndEffectiveGroup|TestEffectiveGroupHostMissing' -v`
Expected: FAIL（`effective_group` 欄位不存在於回應）

- [ ] **Step 3: Implement**

3a. `internal/server/monitor_api.go` — `systemMap` 函式上方新增：

```go
// effectiveGroups resolves each system's effective group: its own grp wins;
// an unset grp on a VM inherits from its host (follows host chains, cycle-safe).
func effectiveGroups(rows []store.SystemWithLatest) map[string]string {
	type node struct{ grp, kind, hostID string }
	nodes := make(map[string]node, len(rows))
	for _, x := range rows {
		nodes[x.ID] = node{grp: x.Grp, kind: x.Kind, hostID: x.HostID}
	}
	var resolve func(id string, seen map[string]bool) string
	resolve = func(id string, seen map[string]bool) string {
		n, ok := nodes[id]
		if !ok || seen[id] {
			return ""
		}
		if n.grp != "" {
			return n.grp
		}
		if n.kind == "vm" && n.hostID != "" {
			seen[id] = true
			return resolve(n.hostID, seen)
		}
		return ""
	}
	eff := make(map[string]string, len(rows))
	for _, x := range rows {
		eff[x.ID] = resolve(x.ID, map[string]bool{})
	}
	return eff
}
```

3b. `systemMap` 簽名加第二參數，map 加兩個欄位：

```go
// systemMap produces the enriched JSON object for a system.
func systemMap(x store.SystemWithLatest, eff string) map[string]any {
	st := liveStatus(x)
	return map[string]any{
		"id": x.ID, "label": x.Label, "role": x.Role, "os": x.OS, "arch": x.Arch,
		"kind": x.Kind, "host_id": x.HostID, "status": st,
		"group": x.Grp, "effective_group": eff,
		"agent_version": x.AgentVersion, "agent_status": x.AgentStatus,
		"last_seen": x.LastSeen,
		"cpu":       fv2(x.Latest.CPU), "mem": fv2(x.Latest.Mem), "disk": fv2(x.Latest.Disk),
		"gpu": fv2(x.Latest.GPU), "net_up": fv2(x.Latest.NetUp), "net_down": fv2(x.Latest.NetDown),
		"load": fv2(x.Latest.Load), "temp": fv2(x.Latest.Temp), "uptime": fv2(x.Latest.Uptime),
		"spark": x.Spark,
	}
}
```

3c. 更新所有 `systemMap` 呼叫點。先 `grep -rn "systemMap(" internal/server/` 找全部呼叫點（目前已知兩處）：

`apiSystemsEnriched`（monitor_api.go）GET 分支：

```go
		rows, err := s.st.SystemsWithLatest()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		eff := effectiveGroups(rows)
		out := []map[string]any{}
		for _, x := range rows {
			out = append(out, systemMap(x, eff[x.ID]))
		}
		writeJSON(w, 200, out)
```

`patchSystem`（manage_api.go）末段「Return updated system」：

```go
	rows, err := s.st.SystemsWithLatest()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	eff := effectiveGroups(rows)
	for _, x := range rows {
		if x.ID == id {
			writeJSON(w, 200, systemMap(x, eff[x.ID]))
			return
		}
	}
```

若 grep 找到其他呼叫點，一律補上 `eff` 參數（同樣先呼叫 `effectiveGroups(rows)`）。

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/server/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/monitor_api.go internal/server/manage_api.go internal/server/manage_api_test.go
git commit -m "feat(api): systems return group + effective_group (vm inherits host)"
```

---

### Task 3: server — PATCH `/api/systems/{id}` 接受 `group`（trim / 64 上限 / 可清空）

**Files:**
- Modify: `internal/server/manage_api.go`（`patchSystem`）
- Test: `internal/server/manage_api_test.go`（檔尾附加）

- [ ] **Step 1: Write the failing test**

在 `internal/server/manage_api_test.go` 檔尾加入：

```go
func TestPatchSystemGroup(t *testing.T) {
	srv, st := newTestServer(t)
	id, _, err := st.CreateSystemPending("pbox", "")
	if err != nil {
		t.Fatal(err)
	}

	// 設定群組（含前後空白 → 應 trim）
	rec := doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"group":"  工作  "}`)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["group"] != "工作" {
		t.Fatalf("group = %v, want 工作 (trimmed)", resp["group"])
	}

	// 只動 group 不應影響 label / role
	if resp["label"] != "pbox" {
		t.Fatalf("label changed: %v", resp["label"])
	}

	// 清空群組
	rec = doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"group":""}`)
	if rec.Code != 200 {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["group"] != "" {
		t.Fatalf("group = %v, want empty", resp["group"])
	}

	// 超過 64 字元 → 400
	long := strings.Repeat("超", 65)
	rec = doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"group":"`+long+`"}`)
	if rec.Code != 400 {
		t.Fatalf("too long: %d %s", rec.Code, rec.Body.String())
	}

	// 不帶 group 欄位 → 不變
	if err := st.SetSystemGroup(id, "保留"); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, srv, "PATCH", "/api/systems/"+id, `{"role":"web"}`)
	if rec.Code != 200 {
		t.Fatalf("role-only patch: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["group"] != "保留" {
		t.Fatalf("group = %v, want 保留 (untouched)", resp["group"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestPatchSystemGroup -v`
Expected: FAIL（group 沒被寫入，回應 `group` 仍為空）

- [ ] **Step 3: Implement**

`internal/server/manage_api.go` `patchSystem`：

3a. body struct 加欄位：

```go
	var body struct {
		Label *string `json:"label"`
		Role  *string `json:"role"`
		Group *string `json:"group"`
	}
```

3b. 在現有 `s.st.UpdateSystem(id, newLabel, newRole)` 區塊**之後**、「Return updated system」**之前**插入：

```go
	if body.Group != nil {
		g := strings.TrimSpace(*body.Group)
		if utf8.RuneCountInString(g) > 64 {
			writeJSON(w, 400, map[string]string{"error": "group too long (max 64 chars)"})
			return
		}
		if err := s.st.SetSystemGroup(id, g); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSON(w, 404, map[string]string{"error": "system not found"})
				return
			}
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
	}
```

3c. import 加 `"unicode/utf8"`（`strings`、`errors` 已有）。

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/server/ ./internal/store/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/manage_api.go internal/server/manage_api_test.go
git commit -m "feat(api): PATCH /api/systems accepts group (trim, 64-rune cap, clearable)"
```

---

### Task 4: 前端共用 `groups.js` 切換器元件 + 三頁 header 掛載

**Files:**
- Create: `cockpit_frontend/groups.js`
- Modify: `cockpit_frontend/index.html`、`cockpit_frontend/topology.html`、`cockpit_frontend/machine.html`（header 容器 + script 引入）

前端無測試框架；本 task 驗證方式為 `go build ./...` + 手動瀏覽器檢查（Task 10 統一驗收）。

- [ ] **Step 1: Create `cockpit_frontend/groups.js`**

```js
/* =============================================================
   cockpit · groups.js — 機器群組全域切換器（共用元件）
   -------------------------------------------------------------
   清單 / 拓樸 / 機器 三頁共用。頁面在資料載入後呼叫
   CockpitGroups.init(groupList, hasUngrouped)；切換時通知 onChange。
   localStorage["cockpit-machine-group"]：
     ""         = 全部
     "__none__" = 未分組
     其他字串    = 群組名
   注意：localStorage["cockpit-group"] 已被軟體頁顯示模式占用，勿混用。
   ============================================================= */
(() => {
  const KEY = "cockpit-machine-group";
  const NONE = "__none__";
  let current = localStorage.getItem(KEY) || "";
  let groups = [];          // 既有群組名（已排序）
  let hasUngrouped = false;
  const listeners = [];

  /* 樣式注入（避免三頁重複貼 CSS） */
  const style = document.createElement("style");
  style.textContent = `
    #group-switcher { display:inline-flex; gap:2px; padding:3px; border:1px solid var(--border); border-radius:9px; background:var(--surface-2); }
    #group-switcher .grp-btn { font-size:12px; font-weight:550; padding:3px 10px; border-radius:6px; border:none; background:transparent; color:var(--text-3); cursor:pointer; transition:.14s; font-family:inherit; }
    #group-switcher .grp-btn:hover { color:var(--text); }
    #group-switcher .grp-btn.active { background:var(--surface); color:var(--text); box-shadow:inset 0 0 0 1px var(--border); }
  `;
  document.head.appendChild(style);

  const escHtml = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;");

  /** effectiveGroup（可為空字串）是否落在目前選擇的群組視圖內 */
  function matches(effectiveGroup) {
    if (current === "") return true;
    if (current === NONE) return !effectiveGroup;
    return effectiveGroup === current;
  }

  function set(g) {
    current = g;
    localStorage.setItem(KEY, g);
    renderBar();
    listeners.forEach((cb) => { try { cb(g); } catch (e) { console.error("[groups] onChange:", e); } });
  }

  function renderBar() {
    const el = document.getElementById("group-switcher");
    if (!el) return;
    if (groups.length === 0) { el.innerHTML = ""; el.style.display = "none"; return; }
    el.style.display = "";
    const opts = [["", "全部"], ...groups.map((g) => [g, g])];
    if (hasUngrouped) opts.push([NONE, "未分組"]);
    el.innerHTML = opts.map(([v, label]) =>
      `<button class="grp-btn ${v === current ? "active" : ""}" data-grp="${escHtml(v)}">${escHtml(label)}</button>`
    ).join("");
  }

  /**
   * 資料載入後呼叫。groupList = 各機器 effective_group 的值（可含重複/空字串，
   * 此處會去重排序）；hasUngrouped = 是否存在未分組機器。
   */
  function init(groupList, hasUngroupedIn) {
    groups = [...new Set((groupList || []).filter(Boolean))].sort((a, b) => a.localeCompare(b));
    hasUngrouped = !!hasUngroupedIn;
    // 已選群組消失 → 退回全部
    if (current && current !== NONE && !groups.includes(current)) current = "";
    if (current === NONE && !hasUngrouped) current = "";
    renderBar();
  }

  document.addEventListener("click", (e) => {
    const b = e.target.closest("#group-switcher [data-grp]");
    if (!b) return;
    const v = b.getAttribute("data-grp");
    if (v !== current) set(v);
  });

  window.CockpitGroups = {
    init,
    matches,
    get: () => current,
    onChange: (cb) => listeners.push(cb),
  };
})();
```

- [ ] **Step 2: 三頁 header 掛載容器與 script**

2a. `cockpit_frontend/index.html`：

- 找到 header 內的 `<div class="flex-1"></div>`（nav 之後、「上次檢查」之前），緊接其後插入：

```html
      <!-- 群組切換器（groups.js 動態填充；無群組時自動隱藏） -->
      <div id="group-switcher" class="mr-1" style="display:none;"></div>
```

- 找到 `<script src="app.js"></script>`，在其**前**加入：

```html
  <script src="groups.js"></script>
```

2b. `cockpit_frontend/topology.html`：

- 找到 header 右側的 `<button id="theme-btn"`（約 line 242），在該 button **之前**插入：

```html
      <div id="group-switcher" style="display:none;margin-right:4px;"></div>
```

- 找到 `<script src="api-data.js"></script>`，在其**前**加入：

```html
  <script src="groups.js"></script>
```

2c. `cockpit_frontend/machine.html`：

- 找到 header 右側的 `<button id="theme-btn"`（約 line 126），在該 button **之前**插入：

```html
      <div id="group-switcher" style="display:none;margin-right:4px;"></div>
```

- 找到 `<script src="api-data.js"></script>`，在其**前**加入：

```html
  <script src="groups.js"></script>
```

（管理頁不掛切換器 — 依 spec 管理頁永遠顯示全部。）

- [ ] **Step 3: Build 驗證**

Run: `go build ./...`
Expected: 成功（groups.js 經 `//go:embed all:cockpit_frontend` 自動打包）

- [ ] **Step 4: Commit**

```bash
git add cockpit_frontend/groups.js cockpit_frontend/index.html cockpit_frontend/topology.html cockpit_frontend/machine.html
git commit -m "feat(web): shared group switcher component mounted on list/topology/machine headers"
```

---

### Task 5: `api-data.js` — meta 帶群組欄位 + 切換器初始化 + 機器頁初選邏輯

**Files:**
- Modify: `cockpit_frontend/api-data.js`

- [ ] **Step 1: MACHINE_META 帶群組欄位**

`loadAll()` 內 `sortedSystems.forEach((sys) => { ... MACHINE_META[id] = { ... } })` 的物件，在 `host_id:  sys.host_id || null,` 之後加兩行：

```js
        group:           sys.group || "",
        effective_group: sys.effective_group || "",
```

- [ ] **Step 2: pending VM 卡的 effective group**

unlinked VM 的 pending 卡（搜尋 `pendingId = "vm_"`）物件內，在 `kind: "vm",` 之後加：

```js
              group:           "",
              effective_group: hostSys ? (hostSys.effective_group || "") : "",
```

（`hostSys` 在該迴圈上方已查好。pending VM 還不是 system，跟隨 host 群組。）

- [ ] **Step 3: 切換器初始化**

`loadAll()` 內，緊接 `window.TOPO = { MACHINE_META, MACHINE_ORDER, SERVICES };` 之後插入：

```js
    /* ── 群組切換器：以 effective_group 集合初始化 ── */
    if (window.CockpitGroups) {
      const effs = MACHINE_ORDER.map((id) => (MACHINE_META[id] || {}).effective_group || "");
      window.CockpitGroups.init(effs.filter(Boolean), effs.some((g) => !g));
    }
```

- [ ] **Step 4: 機器頁初選機器尊重群組**

`loadAll()` 內 `if (IS_MACHINE) { ... }` 區塊，把 `firstOnline` 與 `initId` 的計算改為：

```js
    if (IS_MACHINE) {
      // 決定初始機器（localStorage 或群組內第一台線上）
      const inGroup = (id) =>
        !window.CockpitGroups ||
        window.CockpitGroups.matches((MACHINE_META[id] || {}).effective_group || "");
      const firstOnline = MACHINE_ORDER.find(
        (id) => MACHINE_META[id] && inGroup(id)
          && MACHINE_META[id].status !== "offline" && MACHINE_META[id].status !== "pending"
      ) || MACHINE_ORDER.find(inGroup) || MACHINE_ORDER[0];
      const savedId = localStorage.getItem("cockpit-machine");
      const initId  = (savedId && MACHINE_META[savedId] && inGroup(savedId)) ? savedId : firstOnline;
      if (initId) await prefetchMetrics(initId);
    }
```

- [ ] **Step 5: Build + commit**

Run: `go build ./...`
Expected: 成功

```bash
git add cockpit_frontend/api-data.js
git commit -m "feat(web): api-data carries group/effective_group, inits switcher, group-aware initial machine"
```

---

### Task 6: `topo.js` — 拓樸頁群組過濾

**Files:**
- Modify: `cockpit_frontend/topo.js`

關鍵：過濾必須在 render 時計算（stale-destructure 背景知識 #2）。被覆寫到別群組的 VM、host 不可見時 → 升為頂層 orphan 卡（spec §2）。

- [ ] **Step 1: 加 visibility helper**

`topo.js` 頂部、`buildVmGroups()` 函式**之前**加：

```js
  /* ---- 群組過濾：render 時依全域切換器判斷機器可見性 ---- */
  const grpVisible = (id) => {
    const m = MACHINE_META[id];
    if (!m) return false;
    return !window.CockpitGroups || window.CockpitGroups.matches(m.effective_group || "");
  };
```

- [ ] **Step 2: `buildVmGroups()` 過濾 VM**

把函式內的 forEach 改為（隱藏的 VM 仍須記入 `vmIds`，避免被當成 host 渲染；host 不可見時 VM 升 orphan）：

```js
    MACHINE_ORDER.forEach((id) => {
      const m = MACHINE_META[id];
      if (!m || m.kind !== "vm") return;
      vmIds.add(id);
      if (!grpVisible(id)) return; // 群組外的 VM 不渲染
      const hid = m.host_id;
      if (hid && MACHINE_META[hid] && grpVisible(hid)) {
        if (!vmsByHost[hid]) vmsByHost[hid] = { linked: [], unlinked: [] };
        if (m.status === "pending") {
          vmsByHost[hid].unlinked.push(id);
        } else {
          vmsByHost[hid].linked.push(id);
        }
      } else {
        orphanVMs.push(id);
      }
    });
```

- [ ] **Step 3: `buildRenderOrder()` 過濾 host**

```js
    const nonVmHosts = MACHINE_ORDER.filter((id) => !vmIds.has(id) && grpVisible(id));
```

- [ ] **Step 4: `renderAll()` 過濾服務與軟體欄**

`renderAll()` 內，把服務/軟體/計數的三行：

```js
    $("#col-services").innerHTML = services.map(serviceNode).join("");
    $("#col-software").innerHTML = currentSoftware().map(softwareNode).join("");
    $("#c-machine").textContent = MACHINE_ORDER.length;
    $("#c-service").textContent = services.length;
    $("#c-software").textContent = software.length;
```

改為（installs 的 machine 是 label，先建 label→id 對照）：

```js
    const labelToId = {};
    MACHINE_ORDER.forEach((mid) => { const mm = MACHINE_META[mid]; if (mm) labelToId[mm.label] = mid; });
    const visServices = services.filter((sv) => grpVisible(sv.machine));
    const visSoftware = currentSoftware().filter((it) => {
      const mid = labelToId[it.machine] || it.machine;
      return grpVisible(mid);
    });
    $("#col-services").innerHTML = visServices.map(serviceNode).join("");
    $("#col-software").innerHTML = visSoftware.map(softwareNode).join("");
    $("#c-machine").textContent = MACHINE_ORDER.filter(grpVisible).length;
    $("#c-service").textContent = visServices.length;
    $("#c-software").textContent = visSoftware.length;
```

- [ ] **Step 5: `renderSummary()` 過濾**

把 `MACHINE_ORDER.filter((id) => machineHealth(id) === ...)` 的來源改為可見集合：

```js
    const visM = MACHINE_ORDER.filter(grpVisible);
    const onM = visM.filter((id) => machineHealth(id) === "online").length;
    const offM = visM.filter((id) => machineHealth(id) === "offline").length;
```

並把同函式內顯示 `${MACHINE_ORDER.length}` 的地方改為 `${visM.length}`。

- [ ] **Step 6: `drawEdges()` 防呆檢查**

閱讀 `drawEdges()`（約 line 493 起）：確認連線端點以 DOM 查找（`document.querySelector` / `getBoundingClientRect`）且對**不存在的端點已有 null guard**（先前 commit 837c00f 已加過 install 防呆）。若發現有端點查不到會 throw 的路徑，比照處理：查無端點元素 → `continue` 跳過該條線。

- [ ] **Step 7: 註冊群組切換重渲染**

檔尾、`window.addEventListener("topo:refresh", ...)` 那行附近加：

```js
  if (window.CockpitGroups) {
    window.CockpitGroups.onChange(() => { renderAll(); renderSummary(); drawEdges(); });
  }
```

（與 `topo:refresh` 監聽器的呼叫序一致；若該監聽器還呼叫了其他函式，這裡照抄同一組。）

- [ ] **Step 8: Build + commit**

Run: `go build ./...`
Expected: 成功

```bash
git add cockpit_frontend/topo.js
git commit -m "feat(web): topology filters machines/services/software by active group; overridden VMs surface as top-level"
```

---

### Task 7: `trends.js` — 機器頁切換下拉過濾 + 自動跳台

**Files:**
- Modify: `cockpit_frontend/trends.js`

- [ ] **Step 1: 加 helper**

頂部 `const firstOnline = ...` **之前**加：

```js
  /* ---- 群組過濾 ---- */
  const grpVisible = (id) =>
    !window.CockpitGroups || window.CockpitGroups.matches((MACHINE_META[id] || {}).effective_group || "");
  const visibleOrder = () => MACHINE_ORDER.filter(grpVisible);
```

- [ ] **Step 2: 初始機器尊重群組**

把：

```js
  const firstOnline = MACHINE_ORDER.find((id) => MACHINE_META[id].status !== "offline") || MACHINE_ORDER[0];
  const state = {
    machine: localStorage.getItem("cockpit-machine") || firstOnline,
    range: localStorage.getItem("cockpit-range") || "24h",
  };
  if (!MACHINE_META[state.machine]) state.machine = firstOnline;
```

改為：

```js
  const firstOnline = visibleOrder().find((id) => MACHINE_META[id].status !== "offline")
    || visibleOrder()[0] || MACHINE_ORDER[0];
  const state = {
    machine: localStorage.getItem("cockpit-machine") || firstOnline,
    range: localStorage.getItem("cockpit-range") || "24h",
  };
  if (!MACHINE_META[state.machine] || !grpVisible(state.machine)) state.machine = firstOnline;
```

- [ ] **Step 3: `renderSwitcher()` 用可見清單**

把函式內：

```js
    $("#m-count").textContent = MACHINE_ORDER.length + " 台";
    const idx = MACHINE_ORDER.indexOf(state.machine);
    $("#m-prev").disabled = idx <= 0;
    $("#m-next").disabled = idx >= MACHINE_ORDER.length - 1;
```

改為：

```js
    const ord = visibleOrder();
    $("#m-count").textContent = ord.length + " 台";
    const idx = ord.indexOf(state.machine);
    $("#m-prev").disabled = idx <= 0;
    $("#m-next").disabled = idx >= ord.length - 1;
```

- [ ] **Step 4: `renderPopoverList()` 從可見清單出發**

把：

```js
    const ids = MACHINE_ORDER.filter((id) => !f || id.toLowerCase().includes(f) || MACHINE_META[id].label.toLowerCase().includes(f));
```

改為：

```js
    const ids = visibleOrder().filter((id) => !f || id.toLowerCase().includes(f) || MACHINE_META[id].label.toLowerCase().includes(f));
```

- [ ] **Step 5: prev / next 用可見清單**

把兩個 click handler：

```js
  $("#m-prev").addEventListener("click", () => { const i = MACHINE_ORDER.indexOf(state.machine); if (i > 0) selectMachine(MACHINE_ORDER[i - 1]); });
  $("#m-next").addEventListener("click", () => { const i = MACHINE_ORDER.indexOf(state.machine); if (i < MACHINE_ORDER.length - 1) selectMachine(MACHINE_ORDER[i + 1]); });
```

改為：

```js
  $("#m-prev").addEventListener("click", () => { const ord = visibleOrder(); const i = ord.indexOf(state.machine); if (i > 0) selectMachine(ord[i - 1]); });
  $("#m-next").addEventListener("click", () => { const ord = visibleOrder(); const i = ord.indexOf(state.machine); if (i < ord.length - 1) selectMachine(ord[i + 1]); });
```

- [ ] **Step 6: 群組切換 → 不在群組內自動跳台**

檔尾 `window.addEventListener("trends:refresh", ...)` 附近加：

```js
  if (window.CockpitGroups) {
    window.CockpitGroups.onChange(() => {
      const ord = visibleOrder();
      if (!ord.includes(state.machine) && ord.length) { selectMachine(ord[0]); return; }
      renderSwitcher();
    });
  }
```

- [ ] **Step 7: Build + commit**

Run: `go build ./...`
Expected: 成功

```bash
git add cockpit_frontend/trends.js
git commit -m "feat(web): machine page switcher scoped to active group, auto-jumps when filtered out"
```

---

### Task 8: `app.js` — 軟體頁（index）群組過濾

**Files:**
- Modify: `cockpit_frontend/app.js`

背景：index 頁只 fetch `/api/installs`，installs 的 `machine` 是 label。需要另外 fetch `/api/systems` 取得 label→effective_group 對照，並用它初始化切換器。

- [ ] **Step 1: 載入 systems 與群組對照**

在 `loadInstalls()` 函式之後加：

```js
  /* ---- 群組：label → effective_group 對照（installs.machine 是 label）---- */
  let EFF_BY_LABEL = {};
  async function loadSystems() {
    try {
      const systems = await api("/api/systems");
      EFF_BY_LABEL = Object.fromEntries(systems.map((s) => [s.label, s.effective_group || ""]));
      if (window.CockpitGroups) {
        const effs = systems.map((s) => s.effective_group || "");
        window.CockpitGroups.init(effs.filter(Boolean), effs.some((g) => !g));
      }
    } catch (_) {
      EFF_BY_LABEL = {};
    }
  }
  const machineVisible = (label) =>
    !window.CockpitGroups || window.CockpitGroups.matches(EFF_BY_LABEL[label] || "");
```

- [ ] **Step 2: `filtered()` 加群組條件**

```js
  function filtered() {
    const { machine, onlyUpdates, q } = state.filters;
    return state.installs.filter((it) => {
      if (!machineVisible(it.machine)) return false;
      if (machine && it.machine !== machine) return false;
      if (onlyUpdates && it.status !== "behind") return false;
      if (q && !it.software.toLowerCase().includes(q)) return false;
      return true;
    });
  }
```

- [ ] **Step 3: 機器篩選下拉只列群組內機器**

`populateMachineFilter()` 改為：

```js
  function populateMachineFilter() {
    const sel = $("#filter-machine");
    // 清除舊選項（除了第一個「全部機器」佔位符）
    while (sel.options.length > 1) sel.remove(1);
    MACHINES.filter(machineVisible).forEach((m) => {
      const o = document.createElement("option");
      o.value = m; o.textContent = m;
      sel.appendChild(o);
    });
    // 目前選中的機器被群組過濾掉 → 重設為全部
    if (state.filters.machine && !machineVisible(state.filters.machine)) {
      state.filters.machine = "";
    }
    sel.value = state.filters.machine || "";
  }
```

- [ ] **Step 4: 摘要列反映可見集合**

`renderSummary()` 第一行：

```js
    const all = state.installs.filter((i) => machineVisible(i.machine));
```

- [ ] **Step 5: 啟動流程載入 systems + 註冊切換**

檔尾啟動 IIFE 內，把：

```js
      await loadInstalls();
      await loadJobs();
```

改為：

```js
      await loadInstalls();
      await loadSystems();
      await loadJobs();
```

並在 `render(); renderRecentJobs();`（IIFE 最後兩行）**之前**加：

```js
    if (window.CockpitGroups) {
      window.CockpitGroups.onChange(() => { populateMachineFilter(); render(); });
    }
```

- [ ] **Step 6: 「立即檢查」流程同步刷新 systems**

`#check-btn` handler 內，把：

```js
    try {
      await loadInstalls();
      state.installs = structuredClone(INSTALLS);
      populateMachineFilter();
    } catch (_) {}
```

改為：

```js
    try {
      await loadInstalls();
      await loadSystems();
      state.installs = structuredClone(INSTALLS);
      populateMachineFilter();
    } catch (_) {}
```

- [ ] **Step 7: Build + commit**

Run: `go build ./...`
Expected: 成功

```bash
git add cockpit_frontend/app.js
git commit -m "feat(web): software list filters installs/machine dropdown/summary by active group"
```

---

### Task 9: `manage.js` — 管理頁群組欄位（inline 編輯 + datalist）

**Files:**
- Modify: `cockpit_frontend/manage.js`

**Spec 調整說明**：spec 寫「編輯 modal 加群組欄位」，但管理頁機器列實際是 **inline 列編輯**（`inline-name` input 改名 + 按鈕），沒有編輯 modal。故依現有 UI 慣例：在機器列加 inline 群組 input（搭配 datalist 可選可輸入），on change 直接 PATCH。管理頁**不過濾**、永遠顯示全部機器。

- [ ] **Step 1: 機器列加群組 input + datalist**

`renderMachines()` 內，row template 的 `${metaFrag}` 之後、`<span style="flex:1;"></span>` 之前插入：

```js
          <input class="inline-name" list="grp-datalist" data-grpedit="${escHtml(m.id)}"
                 value="${escHtml(m.group || "")}"
                 placeholder="${m.kind === "vm" && !m.group && m.effective_group ? "繼承：" + escHtml(m.effective_group) : "群組"}"
                 title="群組（留空 = ${m.kind === "vm" ? "繼承宿主機" : "未分組"}）"
                 style="flex:none;width:104px;font-size:12px;" />
```

並在 `el.innerHTML = SYSTEMS.map(...).join("");` 之後加 datalist（既有群組名供下拉選）：

```js
    const grpNames = [...new Set(SYSTEMS.map((s) => s.group).filter(Boolean))].sort((a, b) => a.localeCompare(b));
    el.insertAdjacentHTML("beforeend",
      `<datalist id="grp-datalist">${grpNames.map((g) => `<option value="${escHtml(g)}">`).join("")}</datalist>`);
```

- [ ] **Step 2: PATCH 函式**

`renameMachine` 函式之後加：

```js
  // ── Set machine group ──────────────────────────────────────────────────────
  async function setMachineGroup(id, grp, inputEl) {
    const m = SYSTEMS.find((s) => s.id === id);
    if (!m) return;
    if (grp === (m.group || "")) return;
    try {
      await api(`/api/systems/${encodeURIComponent(id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ group: grp }),
      });
      toast("ok", grp ? `已設定群組：${grp}` : "已清除群組");
      await loadAll();
    } catch (e) {
      toast("err", "群組更新失敗：" + e.message);
      if (inputEl) inputEl.value = m.group || ""; // revert
    }
  }
```

- [ ] **Step 3: 事件掛接**

現有的 `$("#machine-list").addEventListener("change", ...)` handler 開頭加 grpedit 分支：

```js
  $("#machine-list").addEventListener("change", (e) => {
    const g = e.target.closest("[data-grpedit]");
    if (g) {
      setMachineGroup(g.getAttribute("data-grpedit"), g.value.trim(), g);
      return;
    }
    const r = e.target.closest("[data-rename]");
    if (!r) return;
    const id = r.getAttribute("data-rename");
    const v  = r.value.trim();
    const m  = SYSTEMS.find((s) => s.id === id);
    if (v && m && v !== m.label) renameMachine(id, v, r);
    else if (m) r.value = m.label;
  });
```

現有的 keydown handler 改為同時涵蓋兩種 input：

```js
  $("#machine-list").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.target.closest("[data-rename]") || e.target.closest("[data-grpedit]"))) e.target.blur();
  });
```

- [ ] **Step 4: Build + commit**

Run: `go build ./...`
Expected: 成功

```bash
git add cockpit_frontend/manage.js
git commit -m "feat(web): manage page inline group editing with datalist; vm rows hint inherited group"
```

---

### Task 10: 文件 + 全量驗證 + 手動驗收清單

**Files:**
- Modify: `cockpit_frontend/api-contract.md`（systems 端點欄位說明）

- [ ] **Step 1: 更新 API 契約文件**

`cockpit_frontend/api-contract.md` 內找到 `GET /api/systems` 的欄位說明區塊，補上：

```markdown
- `group`：機器自己存的群組名（空字串 = 未分組；VM 留空 = 繼承宿主機）
- `effective_group`：含 VM 繼承計算後的有效群組（前端過濾一律用這個欄位）

`PATCH /api/systems/{id}` body 另接受 `"group"`（optional string）：trim 後存入，
長度上限 64 字元（rune），空字串 = 清除群組/恢復繼承。
```

（依該文件實際格式融入；若有 systems 回應範例 JSON，同步加上兩個欄位。）

- [ ] **Step 2: 全量測試 + build**

Run: `go test ./... && go build ./...`
Expected: 全部 PASS、build 成功

- [ ] **Step 3: 手動驗收（需瀏覽器；若執行環境無瀏覽器則列為交付後人工驗收）**

啟動：`go run ./cmd/cockpit serve`（或專案慣用啟法；參考 README）→ 開 `http://localhost:8787/`。

驗收清單（來自 spec §4）：

- [ ] 管理頁給兩台機器設不同群組（例：工作 / 個人）→ 三頁（清單/拓樸/機器）header 出現切換器
- [ ] 全部機器清空群組 → 三頁切換器隱藏
- [ ] 在拓樸頁選「工作」→ 切到清單頁、機器頁仍是「工作」（localStorage 同步）
- [ ] 重新整理頁面 → 群組選擇保留
- [ ] 清單頁：群組外機器的軟體列消失、機器下拉只剩群組內、摘要數字同步縮小
- [ ] 拓樸頁：群組外機器/服務/軟體卡消失、連線無 JS error（開 console 確認）
- [ ] 機器頁：下拉只列群組內機器；當前機器被過濾掉時自動跳到群組內第一台
- [ ] VM 覆寫群組（管理頁 VM 列輸入別的群組名）→ 選該群組時 VM 在拓樸頁以頂層卡片出現（host 不在）
- [ ] VM 群組清空 → 提示「繼承：<host群組>」、行為跟隨 host
- [ ] 切換器選中的群組被改名消失 → 重新整理後退回「全部」
- [ ] 30 秒自動刷新後，過濾狀態維持（被過濾機器不會閃回來）

- [ ] **Step 4: Commit**

```bash
git add cockpit_frontend/api-contract.md
git commit -m "docs: api-contract group/effective_group fields + PATCH group semantics"
```

---

## Self-Review 紀錄

- Spec 覆蓋：§1 資料模型（Task 1-3）、§2 切換器與各頁過濾（Task 4-9）、§3 邊緣情境（隱藏切換器 Task 4 groups.js init、消失群組退回全部 Task 4、SSE/輪詢過濾一致 Task 6-8 都在 render 路徑過濾、host 刪除 Task 2 resolver、寬容處理 `|| ""` 全程）、§4 測試（Go TDD Task 1-3、手動清單 Task 10）。
- Spec 調整：管理頁群組編輯由「modal」改為 inline 列編輯（管理頁實際無機器編輯 modal，沿用 inline-name 慣例），已在 Task 9 註明。
- 型別/命名一致性：`grp`（SQL）/ `Grp`（Go）/ `"group"`（JSON）/ `effective_group`（JSON）/ `CockpitGroups`（前端 API）/ `cockpit-machine-group`（localStorage）全文一致。
