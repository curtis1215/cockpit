# Cockpit 版本追蹤器 — 前端設計 Prompt（交付 claude design）

> 把以下整段貼給 claude design 即可。這是一份自包含的前端 prototype 設計任務。

---

## 你要做什麼

幫一個自架的 homelab 控制台 **cockpit** 做「軟體版本追蹤器」這一頁的前端。這是給單一使用者（開發者本人）用的 ops 工具，不是對外產品。請產出**高品質、可直接在瀏覽器開啟的靜態 prototype**（HTML + Tailwind + 少量 vanilla JS + mock 資料），之後會由 FastAPI 後端接上（htmx + SSE 友善）。**不需要真後端**。

UI 文案**全部繁體中文**。

## 這頁在做的事

追蹤散在多台機器（mac、ubuntu_llm、gcp、vps…）上的軟體版本：顯示目前版 vs 上游最新版、把 changelog 翻成繁中、並讓使用者在 UI 內按鈕觸發「更新」。更新會以 **job** 形式背景執行，log **即時串流**在 UI 裡（更新可能是一條指令、也可能是委派 AI agent 做多步任務如「同步上游→重 build 鏡像→重新部署」，耗時數分鐘）。

## 畫面結構

### 1. 頂部 Header
- 左：產品名 `cockpit` + 子標題「版本追蹤器」
- 右：`立即檢查` 按鈕（旁顯示「上次檢查：3 分鐘前」）；深/淺色切換
- 篩選列：機器下拉（全部 / mac / ubuntu_llm / …）、`只看有更新` toggle、搜尋框（依軟體名）

### 2. 主清單（核心）
每列 = 一個「軟體 × 機器」的安裝。欄位：
- **軟體名** + kind 小標籤（npm / github / pypi / brew / claude-plugin / custom）
- **機器**
- **目前版**（等寬字體）
- **最新版**（等寬字體）
- **狀態徽章**：`最新`(綠) / `落後 N 版`(橘) / `未知`(灰) / `錯誤`(紅)
- **changelog**：點「中文」連結開 modal
- **操作**：`[更新]` 按鈕（只有落後時可按；最新時 disabled）
- 桌面用表格、窄螢幕自動轉卡片式。資訊密度高、好掃讀。

### 3. Changelog Modal
點 changelog 開啟，顯示某版本：
- 版本號 + 發布日期
- **繁中重點摘要**（條列式，markdown 渲染）
- 可展開的「原文 changelog」（raw，摺疊）
- Esc 關閉

### 4. 更新 Job 面板（右側 drawer 或底部 panel）
按 `[更新]` 後滑出，顯示這個 job：
- 標頭：軟體 / 機器 / 型態徽章（`指令` 或 `Agent`）；agent 型再顯示 runner（codex exec / claude -p）與「所用 prompt」（可摺疊）
- **狀態**：排隊中 / 執行中 / 成功 / 失敗
- **即時 log**：終端機風格、等寬字、深底、新行從底部 append、自動捲動到底（prototype 用 `setInterval` 逐行模擬串流）
- 完成：成功顯示新版本號（並讓主清單該列目前版更新、徽章轉綠）；失敗顯示錯誤輸出
- 面板內含「最近工作」清單，可點回看歷史 job 的 log

### 5. 狀態
- 空狀態（全部最新時的友善畫面）
- loading / 檢查中
- 錯誤列的呈現

## 互動細節
- 篩選即時生效（純前端過濾 mock 資料）
- `[更新]` → job 面板滑出 → log 逐行串流 → 完成後更新該列狀態
- changelog 連結 → modal
- 鍵盤：Esc 關 modal / drawer
- 動畫克制，重清晰與掃讀效率，不要花俏

## 視覺方向
- **開發者 / ops 控制台**風格：乾淨、資訊密度高、狀態色彩語意明確
- 以**深色為主**（ops 工具慣例），提供深/淺切換
- 字體：介面用 **Inter** 或 **Space Grotesk**；版本號與 log 用 **JetBrains Mono**（等寬）
- 徽章/狀態色：綠=最新、橘=有更新、紅=錯誤、灰=未知
- 不走 generic AI 模板感，要精緻、像真的在用的工具

## Mock 資料形狀（請用真實感的範例填充）

```js
// installs：主清單
{ software: "claude-code", kind: "npm", machine: "mac",
  current_version: "2.1.98", latest_version: "2.1.101", status: "behind", behind_count: 3 }
{ software: "claude-code", kind: "npm", machine: "ubuntu_llm",
  current_version: "2.1.101", latest_version: "2.1.101", status: "up_to_date", behind_count: 0 }
{ software: "super-telegram", kind: "claude-plugin", machine: "mac",
  current_version: "1.3.1", latest_version: "1.4.0", status: "behind", behind_count: 1 }
{ software: "multica", kind: "custom", machine: "macmini",
  current_version: "0.8.2", latest_version: "0.9.0", status: "behind", behind_count: 1 }   // agent 更新型
{ software: "ollama", kind: "github", machine: "ubuntu_llm",
  current_version: "0.5.4", latest_version: "0.5.4", status: "up_to_date", behind_count: 0 }

// versions：changelog（modal 用）
{ software: "claude-code", version: "2.1.101", released_at: "2026-04-10",
  changelog_zh: "- 新增 /team-onboarding 指令…\n- 修正擴展工作階段的記憶體洩漏…\n- 安全性：修補 which fallback 的指令注入",
  changelog_raw: "## 2.1.101\n- Added /team-onboarding…" }

// jobs：更新工作（job 面板用）
{ id: "job_01", software: "multica", machine: "macmini", kind: "agent",
  runner: "codex exec", prompt: "multica 上游有新版 0.9.0（目前 0.8.2）。請：1. 同步上游…",
  status: "running",
  log: ["▶ 啟動 codex exec @ /srv/multica",
        "→ git fetch upstream && git merge…",
        "→ docker build -t multica:0.9.0 .",
        "  Step 7/14 : RUN pnpm install…"] }
{ id: "job_00", software: "claude-code", machine: "mac", kind: "command",
  status: "success", new_version: "2.1.101",
  log: ["▶ npm i -g @anthropic-ai/claude-code@latest", "added 1 package", "✓ claude 2.1.101"] }
```

請涵蓋：有更新 / 最新 / agent 型 / 多機器同軟體 等情境，讓畫面有真實感。

## 技術約束
- 交付**可直接開啟的靜態檔**：`index.html` + Tailwind（CDN 可）+ `app.js` + `mock-data.js`（或合理拆檔）
- 即時 log 用 **SSE 思維**：prototype 用計時器模擬，但預留 `EventSource` 接入點與註解（之後接 FastAPI SSE）
- 在「之後要接後端 API」的地方標註清楚（哪裡填真實資料、哪裡發 fetch / 開 SSE）
- htmx 友善：DOM 結構與 partial 切換方式盡量讓後端可用 htmx 接管（但 prototype 本身用 vanilla JS 即可）
- 不要引入重型框架（除非有強理由）；不要實作真的後端 / SSH / 翻譯邏輯

## 完整脈絡（選讀）
此頁屬於 cockpit 專案子系統 2，後端設計見同 repo `docs/specs/2026-06-03-cockpit-version-tracker-design.md`。授權由 Cloudflare Access 處理（前端不需做登入）。
