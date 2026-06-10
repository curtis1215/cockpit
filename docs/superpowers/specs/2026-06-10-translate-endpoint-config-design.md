# WebUI 可配置翻譯端點（OpenAI 相容 / LM Studio）設計

日期：2026-06-10

## 背景

changelog 繁中翻譯由 server（`cmd/cockpit/serve.go`）透過 `translate_cmd`（shell 指令，預設 `claude -p`，生產配 `codex exec`）執行。`codex exec` 走 ChatGPT OAuth，與同機 openclaw 的 codex auth 互搶 refresh token（rotation 互踢），token 頻繁失效 → 翻譯回空字串。

解法：翻譯改打自架 LM Studio 的 OpenAI 相容 API（`http://100.73.202.65:1234`，模型 `google/gemma-4-26b-a4b-qat`），並讓端點可由 WebUI 配置。

實測已知坑：該模型強制 reasoning 且關不掉，`max_tokens` 太小（如 500）會被思考 token 吃光，`content` 回空字串。`max_tokens` 必須 ≥ 4096。

## 設計

### 1. 持久化：SQLite `settings` 表

```sql
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
```

Keys：`translate.endpoint`、`translate.model`、`translate.max_tokens`。
Store 新增 `GetSetting(key) string` / `SetSetting(key, value) error`。

選 DB 而非回寫 `serve.json` 的理由：serve.json 位於 `/etc/cockpit/`（權限風險）、程式僅有 Load 無 Save、DB 寫入即時生效免重啟。

### 2. translate package：HTTP 模式 + fallback

- `Translator` 每次 `Changelog()` 呼叫時動態解析設定（callback 注入，WebUI 改完即時生效）：
  - `translate.endpoint` 已設 → `POST {base}/v1/chat/completions`（OpenAI 規格）：`model`、`max_tokens`（未設預設 4096）、`temperature: 0.3`、沿用現有繁中摘要 prompt。timeout 維持 120s。
  - 未設 → 沿用現行 `translate_cmd` shell 路徑（完全向後相容）。
- 回應只讀 `choices[0].message.content`；空值（reasoning 吃光額度）→ 回 `""`，由呼叫端既有機制（#12）記 error event。
- URL 正規化：接受 `http://host:1234`、`http://host:1234/`、`http://host:1234/v1` 等形式。

### 3. Server API（`internal/server/translate_api.go`）

| Method | Path | 行為 |
|---|---|---|
| GET | `/api/translate/config` | 回 `{endpoint, model, max_tokens}` |
| PUT | `/api/translate/config` | 驗證（URL 格式、max_tokens > 0）後寫 settings |
| GET | `/api/translate/models` | server 代理**已儲存端點**的 `/v1/models`（避 CORS，兼連線測試），回模型 id 清單。不接受任意 query endpoint——避免 SSRF；前端「拉取模型」會先 PUT 儲存端點再呼叫 |

### 4. WebUI（manage 頁「翻譯設定」區塊）

- 端點 URL 輸入框；輸入後可拉模型清單。
- 模型下拉（由 models API 填充；拉不到可手動輸入）。
- Max tokens 數字欄，預設 4096，註記：「建議 ≥ 4096——reasoning 模型會先消耗思考 token，設太小會導致翻譯輸出為空」。
- 儲存 → PUT config，成功/失敗提示。

### 5. 測試

- store：settings 讀寫。
- translate：httptest 模擬 LM Studio——正常翻譯、content 為空、HTTP error、URL 正規化。
- server：config GET/PUT 驗證、models 代理錯誤路徑。

### 不做（YAGNI）

API key 欄位、retry、多 profile、temperature 可調、串流。

## 部署備忘

合併部署後在 WebUI 填：endpoint `http://100.73.202.65:1234`、model `google/gemma-4-26b-a4b-qat`、max_tokens `4096`。`serve.json` 的 `translate_cmd` 可保留不動（僅作 fallback）。
