# Changelog 翻譯狀態、Timeout 可調、Grok 原文來源設計

日期：2026-07-14

## 背景

版本檢查已能偵測到 `grok` / `openclaw` / `multica` 有新版，但 UI「中文」內容長期空白。

根因（2026-07-14 於 Mac mini 生產 DB `/var/lib/cockpit/cockpit.db` 與 LM Studio 重現）：

| 軟體 | 現象 | 原因 |
|------|------|------|
| multica 0.4.1 | raw ~3KB、zh 空 | `translate failed` event；gemma-4 reasoning 常逼近 120s timeout |
| openclaw 2026.7.1 | raw ~123KB、zh 空 | 全文送翻必然 timeout（>120s） |
| grok 0.2.101 | raw 空 | inventory 無 `changelog`；custom 來源不會抓 release body |

翻譯設定現況：endpoint `http://100.73.202.65:1234`、model `google/gemma-4-26b-a4b-qat`、`max_tokens` 4096、HTTP timeout 固定 120s。失敗只寫 `events`，UI 無法區分「處理中 / 失敗 / 完成」。

Grok Build CLI 官方原文可從 CDN 取得（實測 200）：

```
https://x.ai/cli/changelogs/{version}.external.md
```

（另有 `.external.json`；本設計用 markdown 當 raw。）

## 目標

1. **補翻**：部署後能對既有 raw 空 zh 的版本重跑翻譯（含 multica / openclaw）。
2. **Grok changelog**：inventory + sources 支援 URL 模板，抓到正確 per-version 原文後再翻。
3. **Timeout 可調**：WebUI 翻譯設定新增 `timeout_sec`（預設 300）。
4. **Modal 翻譯狀態**：僅在 changelog modal 呈現（列表不顯示 badge）；處理中自動輪詢；失敗可重試。

## 決策摘要（brainstorming）

| 項目 | 決定 |
|------|------|
| 狀態顯示位置 | 僅 modal（非列表） |
| Timeout | WebUI 可調，預設 300s，clamp 30–900 |
| 大 raw | 翻譯前固定截斷 12KB；DB 存完整 raw |
| Modal 互動 | 自動輪詢 + 失敗可重試 |
| 架構 | 方案 1：versions 加狀態欄位，最小增量（不做背景佇列） |

## 設計

### 1. 資料模型

`versions` 新增：

| 欄位 | 型別 | 含義 |
|------|------|------|
| `translate_status` | TEXT | `none` / `pending` / `translating` / `ready` / `failed` |
| `translate_error` | TEXT | 失敗原因；成功清空 |
| `translate_updated_at` | TEXT | 狀態最後變更（UTC） |

狀態語意：

| 狀態 | 何時 |
|------|------|
| `none` | 無 raw，或無需翻譯 |
| `pending` | 有 raw、zh 空，尚未開始呼叫模型。實作可省略此中間態、直接寫 `translating`；API/UI 仍須接受並顯示 `pending` |
| `translating` | 正在呼叫翻譯端點 |
| `ready` | `changelog_zh` 非空 |
| `failed` | 翻譯失敗；保留 raw |

Migration / 啟動 backfill 既有列：

- zh 非空 → `ready`
- 有 raw、無 zh → `failed`（等 refresh 或手動 retry；error 可填 `needs retranslate`）
- 皆空 → `none`
- 若重啟時卡在 `translating` 且超過 `timeout_sec + 60` → 改 `failed`（`interrupted`）

`AddVersion`（refresh 路徑）維持「不覆蓋既有非空 zh」（`COALESCE`），並同步更新 status 欄位。

**Retry 強制重翻** 必須走獨立 store 路徑（例如 `SetChangelogZh` / `UpdateTranslateResult`），**允許覆寫**既有 `changelog_zh` 與 status；不得依賴 `AddVersion` 的 COALESCE，否則 ready 後重試無效。

Settings 新 key：

- `translate.timeout_sec` — 預設 300，clamp 30–900

### 2. 翻譯流程（collector）

`RefreshUpstream` 對每個 software（維持順序同步）：

```
fetch latest
  ├─ error → event "fetch failed…"；跳過
  └─ ok
       完整 raw 寫入 DB
       若既有 zh 非空 → status=ready，不重翻
       否則若 raw 空 → status=none
       否則：
         status=translating
         zh = translate(Truncate(raw))
         成功 → ready, error=""
         失敗 → failed, error=可讀原因, event "translate failed…"
```

`translate` package：

- 錯誤必須 **propagate**（不再只靜默回 `""`）。可新增 `ChangelogEx(raw) (string, error)` 或讓現有路徑回 error；collector 負責寫 status。
- `Config` 加 `TimeoutSec`；HTTP 與 shell fallback 皆用該 timeout（預設 300）。
- 不再使用固定 120s 的 package 級 client 當唯一 timeout；改 `context.WithTimeout` 或 per-call `http.Client`。

截斷（程式常數，WebUI 不暴露）：

- `maxTranslateRawBytes = 12_000`（UTF-8；在 rune 邊界切）
- 超長時取前綴 + 附加：`\n\n…(truncated for translation, full raw retained)`
- **僅送模型前截斷**；modal 原文仍顯示完整 `changelog_raw`

### 3. Grok / 通用 URL changelog 來源

inventory 例：

```yaml
- name: grok
  kind: native
  latest_source: custom:curl -fsSL https://x.ai/cli/stable
  changelog: url:https://x.ai/cli/changelogs/{version}.external.md
```

- 前綴 `url:`；locator 支援 `{version}` / `{ver}` 佔位符。
- 在 version 已知後 GET；200 且 body 非空 → `changelog_raw`。
- 非 200 / 空 body → raw 空、status `none`（避免刷 error event 噪音；可 log）。
- 掛在既有會處理 `changelog:` 的 source 路徑（npm / custom / brew / pypi 等），與 `github:` 並列。

生產 inventory（`/etc/cockpit/inventory.yaml`）部署時補上 grok 的 `changelog` 行。

### 4. API

#### `GET /api/changelog/{software}/{version}`（擴充）

既有欄位外新增：

```json
{
  "software": "openclaw",
  "version": "2026.7.1",
  "changelog_zh": "",
  "changelog_raw": "...",
  "released_at": "",
  "translate_status": "failed",
  "translate_error": "translate timeout after 300s",
  "translate_updated_at": "2026-07-14T12:28:01Z"
}
```

#### `POST /api/changelog/{software}/{version}/retry`（新）

| 情況 | 回應 |
|------|------|
| 接受 | `200 { "ok": true, "translate_status": "translating" }` |
| 無 version | `404` |
| 無 raw | `400 { "error": "no changelog raw" }` |
| 已在 translating | `409 { "error": "translation already in progress" }` |
| 已 ready | **允許**強制重翻（以獨立 store API **覆寫** zh，見 §1） |

- **非同步**：handler 標 `translating` 後立刻 200，goroutine 內完成 ready/failed。
- 單飛：process 內 `map[software@version]` + mutex；與 refresh 路徑共用，避免雙重翻譯。
- 完成時用 `UpdateTranslateResult`（名稱可調整）寫入 zh + status，**不**走 `AddVersion` 的 COALESCE。

#### `GET/PUT /api/translate/config`（擴充）

```json
{
  "endpoint": "...",
  "model": "...",
  "max_tokens": 4096,
  "timeout_sec": 300
}
```

- 省略 / 0 → 當 300；PUT clamp 30–900。
- manage 頁新增 Timeout（秒）欄位。

#### 不改

- `GET /api/installs` 不帶翻譯狀態（列表無 badge）。

### 5. 前端（changelog modal only）

列表「中文」按鈕維持：有 `latest_version` 即可點。

Modal `#modal-zh` 依 `translate_status`：

| status | UI |
|--------|-----|
| `ready` | `mdToHtml(changelog_zh)` |
| `pending` / `translating` | spinner +「翻譯中…」+「完成後會自動更新」 |
| `failed` | 「翻譯失敗」+ `translate_error` + **[重試翻譯]** |
| `none` | 「尚無 changelog 原文」 |
| 請求中 | 「載入中…」 |

輪詢：

- modal 開著且 status ∈ `{pending, translating}` → 每 2.5s GET changelog
- 終態或關 modal / 換 key → 清 timer；過期回應以 key guard 丟棄

重試：

- POST retry → 200 則本地切 translating 並輪詢；409/4xx toast

Manage 翻譯設定：

- `#tr-timeout-sec`，min 30 max 900，預設顯示 300
- 提示：reasoning 模型建議 timeout ≥ 180

本版不做 topo 頁完整狀態機（有 zh 才渲染 changelog 維持現狀）。

### 6. 錯誤處理

| 情境 | 行為 |
|------|------|
| HTTP/shell timeout | `failed`，error 含實際秒數 |
| HTTP 非 200 / no choices / finish_reason=length | `failed`，短錯誤 |
| 空翻譯 | `failed`，`empty translation` |
| URL changelog 抓失敗 | raw 空、`none` |
| GitHub rate limit | 既有 fetch failed event |
| retry 撞 translating | 409 |
| 重啟卡 translating | 逾時改 `failed` / `interrupted` |

events 仍可寫 `translate failed (raw N bytes)` 作稽核；UI 以 version 狀態為準。

### 7. 測試

後端：

- store：新欄位、migration backfill
- collector：成功 ready / 失敗 failed+event / 既有 zh 不重翻
- translate：timeout 從 config、截斷 rune 安全、錯誤 propagate
- sources：`url:` 模板 mock HTTP
- API：GET 含 status；retry 200/400/404/409；config timeout clamp

前端：手動驗收 modal 輪詢、重試、manage 欄位。

### 8. 上線與補翻

1. 部署新 serve binary。
2. inventory 為 grok 加上 `changelog: url:https://x.ai/cli/changelogs/{version}.external.md`。
3. WebUI timeout 設 300（或依 LM 負載調整）。
4. 補翻：
   - migration 後有 raw 無 zh → `failed`
   - 對 `multica@0.4.1`、`openclaw@2026.7.1`（及任意 failed）POST retry，或觸發「立即檢查」讓 refresh 重試
5. 驗收：modal ready 有中文；失敗可見 error 且可重試；grok 有 raw 與中文。

### 9. 不做（YAGNI）

- 列表翻譯 badge
- 背景翻譯 worker / 佇列
- 截斷長度 WebUI 可調
- topo 完整翻譯狀態機
- 多 software 並行翻譯（refresh 仍順序；僅 retry 單飛）

## 成功標準

- openclaw 大 raw 截斷後能在 timeout 內產出 zh
- multica 可補出中文
- grok latest 有 raw 且可翻中文
- modal 可區分 翻譯中 / 失敗+重試 / 完成
- timeout 在 manage 可調且即時生效

## 實作落點（參考）

| 區域 | 路徑 |
|------|------|
| schema / store | `internal/store/schema.sql`, `store.go` |
| collector | `internal/collector/collector.go` |
| translate | `internal/translate/{translate,http}.go` |
| sources | `internal/sources/{sources,more}.go` |
| API | `internal/server/{version_api,translate_api}.go` |
| serve 注入 | `cmd/cockpit/serve.go` |
| UI | `cockpit_frontend/{app.js,index.html,manage.js,manage.html}` |
| 契約 | `docs/api-contract.md` / `cockpit_frontend/api-contract.md` |
| inventory | 生產 `/etc/cockpit/inventory.yaml`（及 example） |
