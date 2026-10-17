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
- `tailwind.css` 不需重建：HTML/JS 沒有用到 Tailwind 顏色 utility（如 `bg-blue-500`），顏色全走 CSS 變數。

## 反目標（做了等於失敗）

- 任何頁面在任一模式殘留舊的藍色主色（`#2f63ff`、`#5b8cff`）或舊冷灰底（`#0a0c10`、`#f5f6f8`）。
- 某頁漏換，切頁時配色跳來跳去。
- 文字讀不清：淺色主色已為可讀性從樣張的 `#d9939b` 加深為 `#c7707c`（白字對比 3.5:1，樣張色只有 2.4:1）。

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
| `--text-2` | `#7d6e6c` | `#b3a1a5` |
| `--text-3` | `#a39290` | `#857479` |
| `--accent` | `#c7707c` | `#e8a3ad` |
| `--accent-ink` | `#ffffff` | `#1a1517` |
| `--accent-weak` | `#fbe9eb` | `#3a252a` |
| `--term-bg` | `#1f191c` | `#120e10` |
| `--term-text` | `#e2d5d8` | `#e2d5d8` |
| `--ok` / `--ok-bg` / `--ok-bd` | `#4f8a64` / `#e7f2eb` / `#c9e0d1` | `#9fceb0` / `#1c2a22` / `#2f4a3a` |
| `--warn` / `--warn-bg` / `--warn-bd` | `#a8762c` / `#f9efdf` / `#eedcb8` | `#e9c38a` / `#2c2418` / `#54432a` |
| `--err` / `--err-bg` / `--err-bd` | `#c0565b` / `#f9e4e4` / `#efc6c6` | `#f08a8a` / `#311a1c` / `#5c2d31` |
| `--mut` / `--mut-bg` / `--mut-bd` | `#8a7b79` / `#f5eeec` / `#eadfdc` | `#b3a1a5` / `#2d2428` / `#45373c` |

### 單頁額外變數

| 頁面 | 變數 | 淺色 | 深色 |
|---|---|---|---|
| `machine.html` | `--chart-grid` | `#f3e8e5` | `#2a2125` |
| `manage.html` | `--info` / `--info-bg` / `--info-bd` | `#8a78c0` / `#f0ecf8` / `#dcd3ef` | `#b9a7e0` / `#251f33` / `#3e3456` |
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

## 測試

新增 `cockpit_frontend/theme.test.js`（`node:test`、零依賴，風格比照既有 `load-performance.test.js`），執行方式不變：`node --test cockpit_frontend/*.test.js`。至少鎖住：

1. 四個 HTML 的淺色區塊 `--accent` 為 `#c7707c`、深色區塊 `--accent` 為 `#e8a3ad`、深色 `--bg` 為 `#1a1517`。
2. 載入中的前端檔（四個 HTML + `app.js`、`topo.js`、`api-data.js`、`manage.js`、`trends.js`、`groups.js`、`store.js`、`topo-state.js`）不含舊主色與舊底色：`#2f63ff`、`#5b8cff`、`#0a0c10`、`#f5f6f8`、`#07090c`；也不含舊終端機行色 `#4ade80`、`#7dd3fc`、`#c4b5fd`。（`api-data.js` 錯誤橫幅 fallback 的 `#2a1313`、`#f87171`、`#5a2424` 不在禁止清單，以免誤殺。）

注意：HTML 的色票有兩種寫法（`index.html` 是 `--accent:      #2f63ff;` 對齊空白，其他頁是 `--accent:#2f63ff;`），測試解析時要容許空白。

## 留給實作者決定

- 測試如何切出 `:root` / `html.dark` 區塊（regex 或字串切割都可）。
- 是否順手保留 `index.html` 原本的對齊排版風格（建議保留各頁原本寫法，diff 最小）。

## 驗收

- `node --test cockpit_frontend/*.test.js` 全綠；`go test ./...` 不受影響。
- 本機跑起 cockpit，四頁在深色與淺色下目視確認為櫻色，無殘留藍色。
