# 櫻色主題 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 cockpit web 四頁的深／淺色票與 JS 寫死顏色換成 spec 定義的櫻色主題，並用 `theme.test.js` 鎖住。

**Architecture:** 純值替換。每頁 `<style>` 內 `:root`（淺色）與 `html.dark`（深色）區塊只改既有變數的值；JS 只改寫死的色碼字串。新增一支 `node:test` 測試檔，用一份色票表逐頁逐模式比對。

**Tech Stack:** 原生 HTML/CSS/JS、`node:test`（零依賴）。

**Spec:** `docs/superpowers/specs/2026-10-17-sakura-theme-design.md`（色票與替換表以 spec 為準，本 plan 不重抄完整表）

## Global Constraints

- 只改既有變數的值，**不新增、不刪除 CSS 變數**；保留各頁原本排版（`index.html` 對齊空白、其他頁緊貼寫法）。
- 不動：`trends-data.js`、`mock-data.js`、`topo-data.js`、`store.js`、`favicon.svg`、`tailwind.css`、`rgba(0,0,0,…)` 陰影、`#toggle-knob` 的 `bg-white`、`api-data.js:391` 錯誤橫幅的 fallback 值。
- 測試執行：`node --test cockpit_frontend/*.test.js`（在 repo 根目錄）。現有 8 個測試必須保持綠燈。
- hex 比對一律不分大小寫。
- 每個 task 一個 commit，訊息結尾加 `Co-Authored-By` 行（依執行者自己的身分）。

## Review Focus

1. 使用者切換深／淺色時，四頁都必須換色——每頁兩個區塊都要被測試覆蓋（Task 1、2 的逐頁逐模式斷言）。
2. 拓樸頁 VM 節點標籤在兩種模式都不能殘留藍色（Task 4 斷言 `.vm-tag` 兩組新值）。
3. 更新軟體時終端機輸出：`app.js` 與 `topo.js` 兩份行色必須一致換掉，不能只換一份（Task 3 對兩檔都斷言）。
4. 機器頁趨勢圖提示框在淺色模式下數值要讀得清（Task 4 斷言 `class="tv"` 用 `var(--text)`）。
5. 舊色以大寫或 URL 編碼形式殘留（`%238B93A0`）也要抓到（Task 5 禁止清單不分大小寫並比對 `%23`）。

---

## 第 1 輪：色票（Task 1–2）

### Task 1: 測試骨架＋`index.html` 色票

**Files:**
- Create: `cockpit_frontend/theme.test.js`
- Modify: `cockpit_frontend/index.html:19-62`（`:root` 與 `html.dark` 區塊）

**Interfaces:**
- Produces（於 `theme.test.js` 內，後續 task 會擴充同一檔）：
  - `const PALETTE = { light: {...}, dark: {...} }`：spec「共用變數」表的全部 25 個變數（含 `--term-bg`、`--term-text`，以及 ok/warn/err/mut 各三個），值為小寫 hex。
  - `const PAGE_EXTRA = { "machine.html": {light:{...},dark:{...}}, "manage.html": ..., "topology.html": ... }`：spec「單頁額外變數」表。
  - `function block(html, selector) -> string`：回傳 `selector {` 到其對應 `}` 之間的文字；selector 為 `":root"` 或 `"html.dark"`。
  - `function vars(css) -> Map<string,string>`：解析 `--name: <ws>* #hex` 為 `name → 小寫 hex`（容許冒號後任意空白，一行多個變數）。
  - `function assertPalette(file)`：對 `file` 的兩個區塊，凡 `vars()` 取出的變數，若在 `PALETTE[mode]` 或 `PAGE_EXTRA[file][mode]` 有定義，就 `assert.equal` 新值；另斷言區塊內**每個**變數都在表中有定義（防止漏換未知變數）。

- [ ] **Step 1: 寫失敗測試**

```js
test("index.html 兩種模式色票為櫻色", () => assertPalette("index.html"));
```
另加一個健全性測試，確保解析器真的有抓到東西：
```js
test("色票解析器在 index.html 深色區塊抓到 --accent", () => {
  const v = vars(block(read("index.html"), "html.dark"));
  assert.ok(v.size >= 20);
  assert.ok(v.has("--accent"));
});
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `node --test cockpit_frontend/theme.test.js`
Expected: `index.html 兩種模式色票為櫻色` FAIL（例如 `--bg` 實際為 `#f5f6f8`、預期 `#fbf6f4`）；健全性測試 PASS。

- [ ] **Step 3: 依 spec 共用表替換 `index.html` 兩個區塊的值**

保留對齊空白排版。

- [ ] **Step 4: 跑全部測試**

Run: `node --test cockpit_frontend/*.test.js`
Expected: 全部 PASS（原 8 個＋新 2 個）。

- [ ] **Step 5: Commit** — `feat(web): index 頁改為櫻色色票並新增主題測試`

### Task 2: 其餘三頁色票

**Files:**
- Modify: `cockpit_frontend/machine.html:14-33`、`cockpit_frontend/manage.html:14-29`、`cockpit_frontend/topology.html:15-34`
- Test: `cockpit_frontend/theme.test.js`

**Interfaces:**
- Consumes: Task 1 的 `assertPalette`、`PAGE_EXTRA`。

- [ ] **Step 1: 寫失敗測試**

```js
for (const f of ["machine.html", "manage.html", "topology.html"])
  test(`${f} 兩種模式色票為櫻色`, () => assertPalette(f));
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `node --test cockpit_frontend/theme.test.js`
Expected: 三個新測試 FAIL。

- [ ] **Step 3: 依 spec 共用表與單頁額外表替換三頁兩個區塊的值**（`--chart-grid`、`--info*`、`--edge*` 也要換）

- [ ] **Step 4: 跑全部測試** — Run: `node --test cockpit_frontend/*.test.js`，Expected: 全 PASS。

- [ ] **Step 5: Commit** — `feat(web): machine/manage/topology 頁改為櫻色色票`

---

## 第 2 輪：寫死顏色與禁止清單（Task 3–5）

### Task 3: 終端機顏色

**Files:**
- Modify: `cockpit_frontend/app.js:688-692`、`cockpit_frontend/topo.js:777-781,797`
- Test: `cockpit_frontend/theme.test.js`

- [ ] **Step 1: 寫失敗測試**

```js
const TERM_LINE = ["#a5d8bb", "#f2a0a0", "#b3a1a5", "#a9cbe8", "#c9b3e6"]; // ✓ ✗ ■ ▶ →
for (const f of ["app.js", "topo.js"])
  test(`${f} 終端機行色為櫻色版`, () => {
    const js = read(f).toLowerCase();
    for (const c of TERM_LINE) assert.ok(js.includes(`color:${c}`), `${f} 缺 ${c}`);
  });
test("topo.js #sw-term 底色與字色", () => {
  const js = read("topo.js").toLowerCase();
  assert.match(js, /id="sw-term" style="background:#120e10; color:#e2d5d8;/);
});
```

- [ ] **Step 2: 確認失敗** — `node --test cockpit_frontend/theme.test.js`，三個新測試 FAIL。
- [ ] **Step 3: 依 spec「終端機輸出行色」與「拓樸頁軟體更新終端機」替換**
- [ ] **Step 4: 全部測試 PASS**
- [ ] **Step 5: Commit** — `feat(web): 終端機輸出改為櫻色柔和色`

### Task 4: 趨勢圖、VM 標籤、下拉箭頭、placeholder

**Files:**
- Modify: `cockpit_frontend/api-data.js:72-78`、`cockpit_frontend/trends.js:275`、`cockpit_frontend/topology.html:174-177`、`cockpit_frontend/manage.html:47`、`cockpit_frontend/index.html`（`.field` 規則之後）
- Test: `cockpit_frontend/theme.test.js`

- [ ] **Step 1: 寫失敗測試**

```js
test("api-data.js metricBase 趨勢色", () => {
  const js = read("api-data.js").toLowerCase();
  const want = { mem: "#a48fd6", disk: "#6fb58c", gpu: "#e58fb4", netup: "#73aed6", netdown: "#6cc3bd", load: "#d9a95b", temp: "#e59a6f" };
  for (const [k, c] of Object.entries(want))
    assert.match(js, new RegExp(`case "${k}":[^\\n]*color: "${c}"`), k);
});
test("trends.js 提示框數值用 var(--text)", () => {
  assert.match(read("trends.js"), /class="tv" style="color:var\(--text\);"/);
});
test("topology.html .vm-tag 為藤紫", () => {
  const h = read("topology.html").toLowerCase();
  assert.match(h, /\.vm-tag \{[^}]*background:#2e2640; color:#c9b3e6;/);
  assert.match(h, /html:not\(\.dark\) \.vm-tag \{ background:#ece6f6; color:#6552a0; \}/);
});
test("manage.html 下拉箭頭用新灰", () => {
  assert.ok(read("manage.html").toLowerCase().includes("stroke='%237a6966'"));
});
test("index.html 搜尋框 placeholder 用 --text-3", () => {
  assert.match(read("index.html"), /\.field::placeholder\s*\{\s*color:\s*var\(--text-3\);?\s*\}/);
});
```

- [ ] **Step 2: 確認失敗** — 五個新測試 FAIL。
- [ ] **Step 3: 依 spec「趨勢圖線色」與「其他寫死顏色」替換；`trends.js:275` 只改 `class="tv"` 的 color，色塊 `background:${s.color}` 保留**
- [ ] **Step 4: 全部測試 PASS**
- [ ] **Step 5: Commit** — `feat(web): 趨勢圖、VM 標籤、下拉箭頭與 placeholder 改為櫻色`

### Task 5: 舊色禁止清單

**Files:**
- Test: `cockpit_frontend/theme.test.js`（只加測試；若失敗代表前面漏換，回頭修對應檔案）

- [ ] **Step 1: 寫測試**

```js
const LIVE = ["index.html", "machine.html", "manage.html", "topology.html",
  "app.js", "topo.js", "api-data.js", "manage.js", "trends.js", "groups.js", "topo-state.js"];
const BANNED = ["2f63ff", "5b8cff", "0a0c10", "f5f6f8", "07090c", "8b93a0", "4ade80",
  "f87171", "9aa4b2", "7dd3fc", "c4b5fd", "22304d", "2755a0"];
for (const f of LIVE)
  test(`${f} 不含舊色`, () => {
    let src = read(f).toLowerCase();
    if (f === "api-data.js") src = src.split("\n").filter((l) => !l.includes('id="api-err"')).join("\n");
    for (const c of BANNED) {
      assert.ok(!src.includes(`#${c}`), `${f} 仍有 #${c}`);
      assert.ok(!src.includes(`%23${c}`), `${f} 仍有 %23${c}`);
    }
  });
```

- [ ] **Step 2: 跑測試** — `node --test cockpit_frontend/*.test.js`。Expected：全 PASS（Task 1–4 完成後應已乾淨）。**若有 FAIL，表示有 spec 沒列到的殘留，停下回報，不要自行挑新色。**
- [ ] **Step 3: Commit** — `test(web): 鎖住舊配色不得回流`
