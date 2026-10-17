# cockpit web 配色更換：日系小清新・櫻色

日期：2026-10-17

## 意圖

使用者看膩了現在的冷灰＋藍色配色，想換成「日系小清新」感覺。經樣張比較後選定：

- **深色（預設，使用者平常用）**：D1「夜櫻」——帶梅子紅的暖黑底、櫻花粉主色。
- **淺色**：C「櫻色」——粉白底、櫻花粉主色。

純換色，功能、版面、字體都不動。

## 目標

1. 四個頁面（`index.html`、`machine.html`、`manage.html`、`topology.html`）的 `:root`（淺色）與 `html.dark`（深色）色票全部換成下方新值。
2. JS 裡寫死、會顯示在畫面上的顏色一併換成柔和版（見「JS 寫死顏色」）。
3. 新增一個零依賴 `node:test` 測試鎖住換色結果，防止舊藍色回流。

## 非目標

- 不新增主題選單，也不保留舊藍色主題可切回。
- 不改 favicon、字體、版面、背景格線樣式（格線顏色跟著 `--bg-grid` 變）。
- 預設主題（深色）與切換按鈕行為不變。
- 不把四份重複的色票抽成共用檔（維持現有每頁一份的做法，最小 diff）。
- 不動 `trends-data.js`、`mock-data.js`、`topo-data.js`：已被 `api-data.js` 取代，沒有任何頁面載入（`api-data.js:4` 註解為證）。
- 不動 `api-data.js:391` 錯誤橫幅裡 `var(--err-bg,#2a1313)` 這類 fallback 值——變數一定存在，fallback 不會生效。
- 陰影與遮罩用的 `rgba(0,0,0,…)` 不動。
- `tailwind.css` 不需重建。唯一用到的 Tailwind 顏色 utility 是首頁切換鈕 `#toggle-knob` 的 `bg-white`，刻意保留白色。Tailwind Preflight 的 placeholder 灰色另在首頁覆寫（見「其他寫死顏色」）。

## 反目標（做了等於失敗）

- 任何頁面在任一模式殘留舊的藍色主色（`#2f63ff`、`#5b8cff`）或舊冷灰底（`#0a0c10`、`#f5f6f8`）。
- 某頁漏換，切頁時配色跳來跳去。
- 文字讀不清。所有一般文字（含 `--text-3` 小字、主按鈕白字、淺色狀態標籤字）對實際底色至少 4.5:1。因此淺色主色從樣張的 `#d9939b`（白字僅 2.4:1）加深為 `#b05462`（4.9:1），淺色狀態文字色也比樣張深。深色模式各組合已 ≥4.9:1。

## 新色票

每頁目前的變數名稱不完全相同（各頁有自己的額外變數），**只替換該頁已存在的變數值，不新增、不刪除變數**。

### 共用變數

| 變數 | 淺色 `:root` | 深色 `html.dark` |
|---|---|---|
| `--bg` | `#fbf6f4` | `#1a1517` |
| `--bg-grid` | `#f5ece9` | `#1f191c` |
| `--surface` | `#ffffff` | `#231c1f` |
| `--surface-2` | `#f7eeeb` | `#2d2428` |
| `--surface-3` | `#f0e3df` | `#362b30` |
| `--border` | `#efe1dd` | `#33292d` |
| `--border-2` | `#e3d0cb` | `#45373c` |
| `--text` | `#463c3c` | `#f1e6e8` |
| `--text-2` | `#5f514f` | `#c2b1b5` |
| `--text-3` | `#7a6966` | `#a08f93` |
| `--accent` | `#b05462` | `#e8a3ad` |
| `--accent-ink` | `#ffffff` | `#1a1517` |
| `--accent-weak` | `#fbe9eb` | `#3a252a` |
| `--term-bg` | `#1f191c` | `#120e10` |
| `--term-text` | `#e2d5d8` | `#e2d5d8` |
| `--ok` / `--ok-bg` / `--ok-bd` | `#3f7552` / `#e7f2eb` / `#c9e0d1` | `#9fceb0` / `#1c2a22` / `#2f4a3a` |
| `--warn` / `--warn-bg` / `--warn-bd` | `#8a5d1c` / `#f9efdf` / `#eedcb8` | `#e9c38a` / `#2c2418` / `#54432a` |
| `--err` / `--err-bg` / `--err-bd` | `#a8444a` / `#f9e4e4` / `#efc6c6` | `#f08a8a` / `#311a1c` / `#5c2d31` |
| `--mut` / `--mut-bg` / `--mut-bd` | `#6e5f5d` / `#f5eeec` / `#eadfdc` | `#b3a1a5` / `#2d2428` / `#45373c` |

### 單頁額外變數

| 頁面 | 變數 | 淺色 | 深色 |
|---|---|---|---|
| `machine.html` | `--chart-grid` | `#f3e8e5` | `#2a2125` |
| `manage.html` | `--info` / `--info-bg` / `--info-bd` | `#6c5aa6` / `#f0ecf8` / `#dcd3ef` | `#b9a7e0` / `#251f33` / `#3e3456` |
| `topology.html` | `--edge` / `--edge-dim` | `#dcc9c5` / `#f0e5e2` | `#4a3b40` / `#261e21` |

`index.html` 的 `--row-bd` 是 inline `var(--border)`，不用改。

## JS 寫死顏色

### 終端機輸出行色（`app.js:688-692`、`topo.js:777-781`，兩處相同）

終端機底在兩種模式都是深色，所以一套即可：

| 行首 | 舊 | 新 |
|---|---|---|
| `✓` | `#4ade80` | `#a5d8bb` |
| `✗` | `#f87171` | `#f2a0a0` |
| `■` | `#9aa4b2` | `#b3a1a5` |
| `▶` | `#7dd3fc` | `#a9cbe8` |
| `→` | `#c4b5fd` | `#c9b3e6` |

### 拓樸頁軟體更新終端機（`topo.js:797`）

`#sw-term` inline 寫死 `background:#07090c; color:#cdd3db`，改為 `background:#120e10; color:#e2d5d8`（與深色 `--term-bg`/`--term-text` 一致；`topology.html` 沒有 term 變數，故維持寫死）。

### 趨勢圖線色（`api-data.js:71-79` 的 `metricBase`）

`cpu` 與 default 已是 `var(--accent)`，自動變粉。其他改為中間調、深淺兩種底都看得到的柔和色：

| 指標 | 舊 | 新 |
|---|---|---|
| `mem` | `#a78bfa` | `#a48fd6`（藤紫） |
| `disk` | `#34d399` | `#6fb58c`（薄荷） |
| `gpu` | `#f472b6` | `#e58fb4`（桃粉） |
| `netUp` | `#38bdf8` | `#73aed6`（水色） |
| `netDown` | `#22d3ee` | `#6cc3bd`（淺青） |
| `load` | `#fbbf24` | `#d9a95b`（奶油黃） |
| `temp` | `#fb923c` | `#e59a6f`（杏橘） |

曲線屬圖形元素，柔和色在深色底（預設）清楚；淺色白底對比約 2–3:1，與換色前舊線色相當，接受。
**但提示框數值是文字**：`trends.js:275` 的 `<span class="tv" style="color:${s.color};">` 改為 `color:var(--text)`，旁邊的色塊（`background:${s.color}`）保留，用來對應曲線。

### 其他寫死顏色

| 位置 | 舊 | 新 |
|---|---|---|
| `topology.html:174-177` `.vm-tag`（深色） | `background:#22304d; color:#9fb3da` | `background:#2e2640; color:#c9b3e6`（藤紫，與 `--info` 同色系） |
| `topology.html:177` `html:not(.dark) .vm-tag`（淺色） | `background:#dce8f8; color:#2755a0` | `background:#ece6f6; color:#6552a0` |
| `manage.html:47` `select.field` 下拉箭頭 SVG data URL | `stroke='%238b93a0'` | `stroke='%237a6966'`（淺色 `--text-3`；兩模式共用，深色底上仍可見） |
| `index.html` `.field` 樣式旁（約 97-102 行） | 無（吃 Tailwind Preflight 的 `#9ca3af`） | 新增 `.field::placeholder { color: var(--text-3); }`，比照 `machine.html` 既有寫法 |

## 測試

新增 `cockpit_frontend/theme.test.js`（`node:test`、零依賴，風格比照既有 `load-performance.test.js`），執行方式不變：`node --test cockpit_frontend/*.test.js`。

1. **色票正向斷言**：在測試內放一份本 spec 的色票表（共用表＋單頁額外變數），逐頁、逐模式，對該頁 `:root` / `html.dark` 區塊中**已存在的每個變數**斷言值等於表上的新值。比對 hex 不分大小寫，容許 `--x:#abc` 與 `--x:   #abc` 兩種空白寫法（`index.html` 是對齊空白，其他頁是緊貼）。
2. **JS 正向斷言**：`app.js`、`topo.js` 各含五個新終端機行色；`topo.js` 的 `#sw-term` 含 `#120e10` 與 `#e2d5d8`；`api-data.js` 的 `metricBase` 含七個新趨勢色；`trends.js` 提示框數值用 `var(--text)`。
3. **其他寫死顏色正向斷言**：`topology.html` 的 `.vm-tag` 新值、`manage.html` 的 `%237a6966`、`index.html` 的 `.field::placeholder`。
4. **禁止清單（補充）**：正式載入的前端檔——四個 HTML 與 `app.js`、`topo.js`、`api-data.js`、`manage.js`、`trends.js`、`groups.js`、`topo-state.js`——不含舊色（不分大小寫，也比對 URL 編碼的 `%23xxxxxx`）：`#2f63ff`、`#5b8cff`、`#0a0c10`、`#f5f6f8`、`#07090c`、`#8b93a0`、`#4ade80`、`#f87171`、`#9aa4b2`、`#7dd3fc`、`#c4b5fd`、`#22304d`、`#2755a0`。唯一豁免：`api-data.js` 錯誤橫幅那一行的 `var(--err,#f87171)` 等 fallback——測試先把該行移除再檢查，不要整檔放行。

## 留給實作者決定

- 測試如何切出 `:root` / `html.dark` 區塊（regex 或字串切割都可）。
- 是否順手保留 `index.html` 原本的對齊排版風格（建議保留各頁原本寫法，diff 最小）。

## 驗收

- `node --test cockpit_frontend/*.test.js` 全綠；`go test ./...` 不受影響。
- 本機跑起 cockpit，四頁在深色與淺色下目視確認為櫻色，無殘留藍色（含拓樸頁 VM 節點標籤）。
