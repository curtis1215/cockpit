# 管理頁全 agent 一鍵升級 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 管理頁加「升級全部 agent」按鈕，一次對所有過時 agent 派送升級，免逐台點擊。

**Architecture:** 純前端：重用既有 `POST /api/systems/{id}/upgrade-agent` 端點，`Promise.allSettled` 對所有「`agent_version` 非空且 ≠ server 版本」的機器派送，彙整 toast。零後端變更。

**Tech Stack:** vanilla JS（manage.js / manage.html）。

**設計依據**（無獨立 spec，設計已在對話中核可）：

- 按鈕只在至少一台機器過時（`agent_version` 非空且 ≠ `serverVersion`，與單台「⬆ 升級 agent」按鈕同條件 — 見 manage.js:204-205）時顯示，標示台數
- 自動跳過 pending / 未回報版本 / 已同步的機器
- confirm 列出目標機器名 → `Promise.allSettled` 派送 → 彙整 toast（成功台數 + 個別失敗機器名）→ 35 秒後 `loadAll()`（沿用單台 upgradeAgent 的節奏，manage.js:629-637）
- YAGNI：不加後端端點、不做逐台進度、不自動重試

**重要背景知識：**

1. `SYSTEMS` 是 manage.js 的模組變數（`loadAll()` 後為 `/api/systems` 回傳陣列），每台有 `id`/`label`/`agent_version`
2. `serverVersion` 模組變數由檔尾 `refreshServerVersion()` 填入（v0.3.0 起）
3. `renderMachines()` 重繪 `#machine-list`；「新增機器」按鈕是 `manage.html:110` 的 `#add-machine`，位於機器區塊標題列
4. toast 用法：`toast("ok"|"warn"|"err", msg)` — **沒有 "info" 分支，勿用**
5. 前端無測試框架；驗證 = `go build ./...` + 手動驗收
6. 測試指令在 repo root：`cd /Users/curtis/Dev/cockpit`

---

### Task 1: 全 agent 升級按鈕

**Files:**
- Modify: `cockpit_frontend/manage.html`（`#add-machine` 旁加按鈕）
- Modify: `cockpit_frontend/manage.js`（顯示邏輯 + 批次派送）

- [ ] **Step 1: manage.html 加按鈕**

`manage.html:110` 的 `#add-machine` button **之前**插入（同一個容器 div 內）：

```html
        <button id="upgrade-all-agents" class="btn" style="display:none;color:var(--warn);"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7"/></svg>升級全部 agent</button>
```

- [ ] **Step 2: manage.js 加過時清單 helper 與按鈕刷新**

在 `renderMachines()` 函式**之前**加：

```js
  // ── 全 agent 升級 ────────────────────────────────────────────────────────
  // 過時 = 有回報 agent_version 且 ≠ server 版本（與單台升級按鈕同條件）。
  function outdatedAgents() {
    if (!serverVersion) return [];
    return SYSTEMS.filter((m) => m.agent_version && m.agent_version !== serverVersion);
  }

  function refreshUpgradeAllBtn() {
    const btn = $("#upgrade-all-agents");
    if (!btn) return;
    const list = outdatedAgents();
    if (list.length > 1) {
      btn.style.display = "";
      btn.innerHTML = btn.innerHTML.replace(/升級全部 agent.*$/, `升級全部 agent（${list.length} 台）`);
    } else {
      btn.style.display = "none";
    }
  }
```

注意：`innerHTML.replace` 針對含 svg 的按鈕不可靠 — 改用更穩的寫法：按鈕文字部分包一個 span。**因此 Step 1 的按鈕 HTML 改為**（svg 後加 span）：

```html
        <button id="upgrade-all-agents" class="btn" style="display:none;color:var(--warn);"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7"/></svg><span id="upgrade-all-label">升級全部 agent</span></button>
```

`refreshUpgradeAllBtn` 對應改為：

```js
  function refreshUpgradeAllBtn() {
    const btn = $("#upgrade-all-agents");
    if (!btn) return;
    const list = outdatedAgents();
    if (list.length > 1) {
      btn.style.display = "";
      $("#upgrade-all-label").textContent = `升級全部 agent（${list.length} 台）`;
    } else {
      btn.style.display = "none";
    }
  }
```

（只有 1 台過時時隱藏 — 單台用列上既有按鈕即可；0 台自然隱藏。）

- [ ] **Step 3: 接到 render 流程**

`renderMachines()` 函式的最後（與 datalist 渲染同層級、函式結尾處）加一行：

```js
    refreshUpgradeAllBtn();
```

並在檔尾 `refreshServerVersion()` 的 `.then` 鏈完成後也會因 `loadAll()` 重繪而更新 — 確認檔尾初始化順序是 `refreshServerVersion()` 先於 `loadAll()`（serverVersion 先就位）；若現況相反，調整為：

```js
  refreshServerVersion().then(() => loadAll());
```

（`refreshServerVersion` 已回傳 promise — 見該函式實作；若無回傳值則補 `return`。）

- [ ] **Step 4: 批次派送 handler**

`upgradeAgent` 函式之後加：

```js
  async function upgradeAllAgents() {
    const targets = outdatedAgents();
    if (targets.length === 0) return;
    const names = targets.map((m) => m.label).join("、");
    if (!confirm(`將升級 ${targets.length} 台 agent 至 v${serverVersion}：${names}，確定？`)) return;
    const btn = $("#upgrade-all-agents");
    if (btn) btn.disabled = true;
    const results = await Promise.allSettled(
      targets.map((m) =>
        api(`/api/systems/${encodeURIComponent(m.id)}/upgrade-agent`, { method: "POST" })
      )
    );
    const failed = results
      .map((r, i) => (r.status === "rejected" ? targets[i].label : null))
      .filter(Boolean);
    const okCount = targets.length - failed.length;
    if (failed.length === 0) {
      toast("ok", `已派送 ${okCount} 台升級（macOS 約 30 秒；Linux 視服務設定最長 2 分鐘）`);
    } else {
      toast("warn", `已派送 ${okCount} 台；失敗：${failed.join("、")}`);
    }
    if (btn) btn.disabled = false;
    setTimeout(loadAll, 35000);
  }

  $("#upgrade-all-agents").addEventListener("click", upgradeAllAgents);
```

- [ ] **Step 5: Build 驗證**

Run: `go build ./...`
Expected: 成功（前端經 embed 打包）

- [ ] **Step 6: Commit**

```bash
git add cockpit_frontend/manage.html cockpit_frontend/manage.js
git commit -m "feat(web): manage page bulk agent upgrade button (skips current/unreported agents)"
```

---

### Task 2: 手動驗收清單

- [ ] **Step 1: 全量驗證**

Run: `go test ./... && go build ./...`
Expected: 全部 PASS、build 成功

- [ ] **Step 2: 手動驗收（需 production 或本地多機環境；做不到的項目明確標注未驗證）**

- [ ] 多台 agent 過時時：按鈕顯示「升級全部 agent（N 台）」，N 正確（排除 pending/未回報/已同步）
- [ ] 0 或 1 台過時：按鈕隱藏
- [ ] 點擊 → confirm 列出機器名 → 確認 → toast「已派送 N 台升級…」
- [ ] 35 秒後列表自動刷新，各台 agent_version 陸續變新
- [ ] 個別派送失敗（可模擬：confirm 後手動刪一台機器）→ warn toast 列出失敗機器名

---

## Self-Review 紀錄

- 設計覆蓋：按鈕條件/台數標示（Task 1 Step 2）、跳過規則（outdatedAgents 過濾）、confirm 列名/allSettled/彙整 toast/35s 刷新（Step 4）皆與核可設計一致
- 型別一致：`outdatedAgents()` 回傳 SYSTEMS 元素（有 `.id`/`.label`）；`refreshUpgradeAllBtn` 與 `upgradeAllAgents` 共用它
- 已知取捨：只有 1 台過時時隱藏批次按鈕（避免與單台按鈕重複）已註明；toast 無 "info" 分支的坑寫進背景知識
