# Changelog 翻譯狀態 / Timeout / Grok URL 來源 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 changelog 翻譯可觀測（modal 顯示處理中/失敗/完成）、timeout WebUI 可調（預設 300s）、大 raw 截斷後可翻成功，並讓 grok 從官方 `url:` changelog 抓到原文。

**Architecture:** 在 `versions` 加 `translate_status` / `translate_error` / `translate_updated_at`；refresh 與非同步 retry 共用單飛鎖；翻譯送模型前固定截斷 12KB；`changelog: url:…/{version}…` 與既有 `github:` 並列；前端只在 modal 輪詢與重試。

**Tech Stack:** Go、SQLite（modernc）、vanilla JS WebUI、`go test`、node 無強制前端測試。

**設計文件：** `docs/superpowers/specs/2026-07-14-changelog-translate-status-design.md`

## Global Constraints

- 狀態枚舉：`none` | `pending` | `translating` | `ready` | `failed`（可省略寫入 `pending`，直接 `translating`）
- Timeout 預設 **300** 秒；WebUI clamp **30–900**；settings key `translate.timeout_sec`
- 截斷常數 **12000** bytes（rune 安全）；DB 存完整 raw
- Retry 覆寫 zh 必須走獨立 store API，**禁止** `AddVersion` COALESCE
- 列表 `/api/installs` **不**帶翻譯狀態
- 單飛：同一 `software@version` 不可並行翻譯
- 生產 grok changelog：`url:https://x.ai/cli/changelogs/{version}.external.md`

---

## File Structure

| 檔案 | 職責 |
|------|------|
| `internal/store/schema.sql` | versions 新欄位（新 DB） |
| `internal/store/store.go` | Version 型別、migration、CRUD、UpdateTranslateResult、backfill |
| `internal/store/vt_test.go`（或新 `translate_status_test.go`） | store 測試 |
| `internal/translate/truncate.go` | TruncateForTranslate |
| `internal/translate/translate.go` | ChangelogResult、shell timeout |
| `internal/translate/http.go` | Config.TimeoutSec、per-call timeout |
| `internal/translate/*_test.go` | 截斷 / timeout / error |
| `internal/sources/sources.go` + `more.go` | `url:` changelog fetch |
| `internal/sources/*_test.go` | URL 模板測試 |
| `internal/collector/collector.go` | 狀態機 + TranslateFunc 回 error |
| `internal/collector/collector_test.go` | ready/failed/status |
| `internal/server/translate_api.go` | timeout_sec config |
| `internal/server/version_api.go` | GET 擴充 + POST retry |
| `internal/server/server.go` | 單飛鎖 / retry 依賴注入 translator |
| `cmd/cockpit/serve.go` | 接線 |
| `cockpit_frontend/{app.js,index.html,manage.js,manage.html}` | modal + 設定 UI |
| `inventory.example.yaml` | grok 範例 |
| `docs/api-contract.md` / `cockpit_frontend/api-contract.md` | 契約 |

---

### Task 1: Store — 欄位、migration、UpdateTranslateResult

**Files:**
- Modify: `internal/store/schema.sql`
- Modify: `internal/store/store.go`
- Test: `internal/store/translate_status_test.go`（create）

**Interfaces:**
- Produces:
  - `Version` 含 `TranslateStatus`, `TranslateError`, `TranslateUpdatedAt string`
  - `func (s *Store) UpdateTranslateResult(software, ver, zh, status, errMsg string) error` — **覆寫** zh 與 status
  - `func (s *Store) SetTranslateStatus(software, ver, status, errMsg string) error` — 只改狀態（translating 時可不清 zh）
  - `func (s *Store) BackfillTranslateStatus() error` — Open 時呼叫
  - `AddVersion` 簽名可擴充或內部推導 status：有 zh→ready；raw 空→none；有 raw 無 zh→由呼叫端再 SetTranslateStatus

- [ ] **Step 1: 寫失敗測試**

Create `internal/store/translate_status_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func TestUpdateTranslateResultOverwritesZh(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddVersion("cc", "1.0.0", "", "## raw", "舊中文"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTranslateResult("cc", "1.0.0", "新中文", "ready", ""); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVersion("cc", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if v.ChangelogZh != "新中文" || v.TranslateStatus != "ready" || v.TranslateError != "" {
		t.Fatalf("%+v", v)
	}
}

func TestBackfillTranslateStatus(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 直接 insert 模擬舊列（若 AddVersion 已自動設 status，改用 raw SQL 寫空 status）
	s.db.Exec(`INSERT INTO versions(software,version,changelog_raw,changelog_zh,translate_status) VALUES('a','1','raw','中文','')`)
	s.db.Exec(`INSERT INTO versions(software,version,changelog_raw,changelog_zh,translate_status) VALUES('b','1','raw','','')`)
	s.db.Exec(`INSERT INTO versions(software,version,changelog_raw,changelog_zh,translate_status) VALUES('c','1','','','')`)
	if err := s.BackfillTranslateStatus(); err != nil {
		t.Fatal(err)
	}
	va, _ := s.GetVersion("a", "1")
	vb, _ := s.GetVersion("b", "1")
	vc, _ := s.GetVersion("c", "1")
	if va.TranslateStatus != "ready" || vb.TranslateStatus != "failed" || vc.TranslateStatus != "none" {
		t.Fatalf("a=%q b=%q c=%q", va.TranslateStatus, vb.TranslateStatus, vc.TranslateStatus)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `go test ./internal/store/ -run 'TestUpdateTranslateResult|TestBackfill' -count=1`
Expected: FAIL（method undefined 或欄位不存在）

- [ ] **Step 3: 實作 schema + store**

`schema.sql` 的 `versions`：

```sql
CREATE TABLE IF NOT EXISTS versions (
  software TEXT NOT NULL, version TEXT NOT NULL, released_at TEXT,
  changelog_raw TEXT, changelog_zh TEXT,
  translate_status TEXT NOT NULL DEFAULT 'none',
  translate_error TEXT NOT NULL DEFAULT '',
  translate_updated_at TEXT NOT NULL DEFAULT '',
  fetched_at TEXT DEFAULT (datetime('now')),
  PRIMARY KEY (software, version)
);
```

`Open` 內 defensive migration（與既有 `ALTER TABLE` 模式相同）：

```go
for _, q := range []string{
	`ALTER TABLE versions ADD COLUMN translate_status TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE versions ADD COLUMN translate_error TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE versions ADD COLUMN translate_updated_at TEXT NOT NULL DEFAULT ''`,
} {
	if _, err := db.Exec(q); err != nil && !contains(err.Error(), "duplicate column name") {
		return nil, err
	}
}
if err := (&Store{db: db}).BackfillTranslateStatus(); err != nil {
	return nil, err
}
```

`BackfillTranslateStatus`：

```sql
UPDATE versions SET translate_status='ready', translate_error=''
 WHERE COALESCE(changelog_zh,'') != '' AND COALESCE(translate_status,'') = '';
UPDATE versions SET translate_status='failed', translate_error='needs retranslate'
 WHERE COALESCE(changelog_raw,'') != '' AND COALESCE(changelog_zh,'') = '' AND COALESCE(translate_status,'') = '';
UPDATE versions SET translate_status='none'
 WHERE COALESCE(changelog_raw,'') = '' AND COALESCE(changelog_zh,'') = '' AND COALESCE(translate_status,'') = '';
```

`UpdateTranslateResult`：

```go
func (s *Store) UpdateTranslateResult(software, ver, zh, status, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE versions SET changelog_zh=?, translate_status=?, translate_error=?,
		 translate_updated_at=datetime('now') WHERE software=? AND version=?`,
		nullStr(zh), status, errMsg, software, ver)
	return err
}
```

`SetTranslateStatus`：只更新 status/error/updated_at。

擴充 `GetVersion` / `LatestVersion` 的 SELECT/Scan 讀三個新欄位。

`AddVersion`：INSERT/UPSERT 時若傳入 zh 非空可一併寫 `ready`；zh 空且 raw 非空由 collector 後續 SetTranslateStatus（或 AddVersion 寫 `none`/`pending` 後由 collector 更新）。

- [ ] **Step 4: 跑測試確認通過**

Run: `go test ./internal/store/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/schema.sql internal/store/store.go internal/store/translate_status_test.go
git commit -m "feat(store): versions translate_status fields + UpdateTranslateResult"
```

---

### Task 2: Translate — 截斷、TimeoutSec、錯誤回傳

**Files:**
- Create: `internal/translate/truncate.go`
- Modify: `internal/translate/translate.go`
- Modify: `internal/translate/http.go`
- Modify: `internal/translate/http_test.go`, `translate_test.go`（必要時）
- Create: `internal/translate/truncate_test.go`

**Interfaces:**
- Produces:
  - `const MaxTranslateRawBytes = 12000`
  - `func TruncateForTranslate(raw string) string`
  - `Config.TimeoutSec int` json:`timeout_sec`
  - `func (t *Translator) ChangelogResult(raw string) (string, error)` — 空 raw → `("", nil)`；失敗 → error
  - `Changelog(raw string) string` 保留相容：`out, _ := ChangelogResult(raw); return out`

- [ ] **Step 1: 寫失敗測試**

`truncate_test.go`:

```go
func TestTruncateForTranslate(t *testing.T) {
	raw := strings.Repeat("a", MaxTranslateRawBytes+100)
	out := TruncateForTranslate(raw)
	if len(out) <= MaxTranslateRawBytes {
		t.Fatalf("expected prefix+suffix longer note, len=%d", len(out))
	}
	if !strings.Contains(out, "truncated for translation") {
		t.Fatalf("%q", out[:80])
	}
	// rune 安全：含多位元組字元不 panic、不破壞 UTF-8
	raw2 := strings.Repeat("中", 5000)
	out2 := TruncateForTranslate(raw2)
	if !utf8.ValidString(out2) {
		t.Fatal("invalid utf8")
	}
}
```

`http_test.go` 新增：

```go
func TestHTTPTimeoutFromConfig(t *testing.T) {
	// server sleep 2s；TimeoutSec=1 → ChangelogResult 必須 error
}
func TestChangelogResultEmptyContentError(t *testing.T) {
	// content "" → error 含 empty
}
```

- [ ] **Step 2: 跑測確認紅**

Run: `go test ./internal/translate/ -run 'Truncate|Timeout|EmptyContent' -count=1`
Expected: FAIL

- [ ] **Step 3: 實作**

`truncate.go`：若 `len(raw) <= MaxTranslateRawBytes` 原樣回傳；否則從 0 累加 rune 直到 bytes 上限，再 append `\n\n…(truncated for translation, full raw retained)`。

`Config`：

```go
type Config struct {
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	MaxTokens  int    `json:"max_tokens"`
	TimeoutSec int    `json:"timeout_sec"`
}
```

`effectiveTimeout(cfg) time.Duration`：`sec := cfg.TimeoutSec; if sec <= 0 { sec = 300 }; return time.Duration(sec)*time.Second`

`httpRun`：用 `context.WithTimeout` + `http.NewRequestWithContext`；勿共用固定 120s client 當唯一 deadline（可保留 Transport，但 Request 帶 ctx）。

`shellRun`：同樣用 timeout 參數（從 Dynamic 讀 cfg 或 ChangelogResult 閉包）。

`ChangelogResult`：對 raw 先 `TruncateForTranslate`；Run 失敗 / 空 content / finish_reason length → 回 error。

- [ ] **Step 4: 全 package 綠**

Run: `go test ./internal/translate/ -count=1`
Expected: PASS（更新舊測試若依賴 120s 固定行為）

- [ ] **Step 5: Commit**

```bash
git add internal/translate/
git commit -m "feat(translate): truncate raw, configurable timeout, ChangelogResult errors"
```

---

### Task 3: Sources — `url:` changelog 模板

**Files:**
- Modify: `internal/sources/sources.go`
- Modify: `internal/sources/more.go`
- Modify: `internal/sources/more_test.go` 或 `sources_test.go`

**Interfaces:**
- Produces: `func fetchChangelog(sw inventory.Software, version string, hc *http.Client) string`
  - `github:…` → 既有 `githubReleaseBody`
  - `url:https://…/{version}…` → GET 替換後 URL，200 回 body string
  - 其他 / 失敗 → `""`

- [ ] **Step 1: 寫失敗測試**

```go
func TestURLChangelogTemplate(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte("# 0.2.101\n- feature"))
	}))
	defer srv.Close()
	sw := inventory.Software{
		Name: "grok", LatestSource: "custom:echo 0.2.101",
		Changelog: "url:" + srv.URL + "/changelogs/{version}.external.md",
	}
	res, err := fetchCustom(sw, "echo 0.2.101", srv.Client())
	if err != nil || res.Version != "0.2.101" {
		t.Fatalf("%+v %v", res, err)
	}
	if res.ChangelogRaw == "" || !strings.Contains(res.ChangelogRaw, "feature") {
		t.Fatalf("raw=%q path=%q", res.ChangelogRaw, gotPath)
	}
	if !strings.Contains(gotPath, "0.2.101") {
		t.Fatalf("path %q missing version", gotPath)
	}
}
```

（Windows skip bash 同 `TestCustomChangelog`。）

- [ ] **Step 2: 跑測紅**

Run: `go test ./internal/sources/ -run TestURLChangelogTemplate -count=1`
Expected: FAIL

- [ ] **Step 3: 實作**

抽出共用：

```go
func fillChangelog(sw inventory.Software, version string, hc *http.Client) string {
	cl := strings.TrimSpace(sw.Changelog)
	switch {
	case strings.HasPrefix(cl, "github:"):
		return githubReleaseBody(strings.TrimPrefix(cl, "github:"), version, hc, githubBase)
	case strings.HasPrefix(cl, "url:"):
		u := strings.TrimPrefix(cl, "url:")
		u = strings.ReplaceAll(u, "{version}", version)
		u = strings.ReplaceAll(u, "{ver}", version)
		return httpGetBody(hc, u) // 非 200 或錯回 ""
	default:
		return ""
	}
}
```

`fetchNpm` / `fetchCustom` / `fetchBrew` / `fetchPypi`：把 `if github:` 換成 `res.ChangelogRaw = fillChangelog(sw, res.Version, hc)`。  
`fetchGithub` 已有 body，可選：若另設 `changelog: url:` 則覆蓋——YAGNI，github 路徑維持 release body。

- [ ] **Step 4: 綠**

Run: `go test ./internal/sources/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sources/
git commit -m "feat(sources): url: changelog template with {version}"
```

---

### Task 4: Collector — 狀態機 + TranslateFunc error

**Files:**
- Modify: `internal/collector/collector.go`
- Modify: `internal/collector/collector_test.go`
- Modify: `cmd/cockpit/serve.go`（接線 ChangelogResult）

**Interfaces:**
- Consumes: `store.UpdateTranslateResult`, `store.SetTranslateStatus`, `translate.TruncateForTranslate`（由 TranslateFunc 內部做截斷亦可）
- Produces:
  - `type TranslateFunc func(raw string) (string, error)`
  - Refresh：無 zh 且有 raw → SetTranslateStatus(translating) → 翻 → UpdateTranslateResult

- [ ] **Step 1: 更新/新增測試**

```go
func TestRefreshUpstream_SetsReadyStatus(t *testing.T) {
	// tr 回 ("中文", nil) → GetVersion.TranslateStatus == "ready"
}
func TestRefreshUpstream_SetsFailedStatus(t *testing.T) {
	// tr 回 ("", errors.New("timeout after 300s")) → failed + error event 含 timeout
}
func TestRefreshUpstream_SkipsWhenZhExists(t *testing.T) {
	// 先 AddVersion 有 zh；tr 若被呼叫 t.Fatal；status 仍 ready
}
```

把既有 `tr := func(raw string) string` 改成 `func(raw string) (string, error)`。

- [ ] **Step 2: 紅燈**

Run: `go test ./internal/collector/ -count=1`
Expected: FAIL（簽名或 status 斷言）

- [ ] **Step 3: 實作 `RefreshUpstream`**

```go
type TranslateFunc func(raw string) (string, error)

func RefreshUpstream(s *store.Store, inv inventory.Inventory, fetch FetchFunc, translate TranslateFunc) {
	for _, sw := range inv.Software {
		latest, err := fetch(sw)
		if err != nil {
			s.AddEvent("error", sw.Name, "", "fetch failed: "+err.Error())
			continue
		}
		existing, _ := s.GetVersion(sw.Name, latest.Version)
		zh := existing.ChangelogZh
		if zh != "" {
			s.AddVersion(sw.Name, latest.Version, "", latest.ChangelogRaw, zh)
			s.SetTranslateStatus(sw.Name, latest.Version, "ready", "")
			continue
		}
		s.AddVersion(sw.Name, latest.Version, "", latest.ChangelogRaw, "")
		if strings.TrimSpace(latest.ChangelogRaw) == "" {
			s.SetTranslateStatus(sw.Name, latest.Version, "none", "")
			continue
		}
		s.SetTranslateStatus(sw.Name, latest.Version, "translating", "")
		out, terr := translate(latest.ChangelogRaw)
		if terr != nil || strings.TrimSpace(out) == "" {
			msg := "empty translation"
			if terr != nil {
				msg = terr.Error()
			}
			s.UpdateTranslateResult(sw.Name, latest.Version, "", "failed", msg)
			s.AddEvent("error", sw.Name, "", fmt.Sprintf("translate failed (raw %d bytes): %s", len(latest.ChangelogRaw), msg))
			continue
		}
		s.UpdateTranslateResult(sw.Name, latest.Version, out, "ready", "")
	}
}
```

`serve.go`：

```go
refresh := func() {
	collector.RefreshUpstream(st, srv.Inventory(), collector.DefaultFetch, tr.ChangelogResult)
}
```

（確保 `ChangelogResult` 內部已 Truncate。）

- [ ] **Step 4: 綠**

Run: `go test ./internal/collector/ ./cmd/cockpit/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/collector/ cmd/cockpit/serve.go
git commit -m "feat(collector): persist translate_status ready/failed on refresh"
```

---

### Task 5: Server API — config timeout + changelog GET/retry

**Files:**
- Modify: `internal/server/translate_api.go`, `translate_api_test.go`
- Modify: `internal/server/version_api.go`, `version_api_test.go`（或新檔）
- Modify: `internal/server/server.go`（translator 注入 + 單飛 map）

**Interfaces:**
- Consumes: Store 狀態 API、`Translator.ChangelogResult`
- Produces:
  - Config JSON 含 `timeout_sec`
  - GET changelog 含 status 三欄
  - `POST /api/changelog/{sw}/{ver}/retry`

- [ ] **Step 1: 寫 API 測試**

`translate_api_test.go`：

```go
// PUT timeout_sec=10 → stored "10"；GET 回 10
// PUT timeout_sec=5 → clamp 成 30（或 400——選 clamp 與 spec 一致：clamp 30–900）
// PUT timeout_sec=0 → 視為 300 存儲或 GET 時 effective 300（實作選：存 0、Get 回傳 effective 300 亦可；測試鎖一種：PUT 0 後 Get TimeoutSec==300）
```

`version_api_test.go`：

```go
func TestChangelogIncludesTranslateStatus(t *testing.T) { ... }
func TestChangelogRetryNoRaw400(t *testing.T) { ... }
func TestChangelogRetrySuccess(t *testing.T) {
	// 注入 fake translator；POST retry → 200 translating；等→ Get ready
}
func TestChangelogRetryConflict409(t *testing.T) {
	// translator block on channel；二次 retry → 409
}
```

Server 需要可注入 translate 函式，例如：

```go
// server.go
type Server struct {
	// ...
	translateMu sync.Mutex
	translateInflight map[string]struct{} // key software@version
	TranslateFn func(raw string) (string, error) // 測試可塞
}
```

- [ ] **Step 2: 紅**

Run: `go test ./internal/server/ -run 'TranslateConfig|Changelog' -count=1`
Expected: FAIL

- [ ] **Step 3: 實作**

`TranslateConfig` 讀寫 `translate.timeout_sec`；PUT clamp：

```go
func clampTimeout(sec int) int {
	if sec <= 0 {
		return 300
	}
	if sec < 30 {
		return 30
	}
	if sec > 900 {
		return 900
	}
	return sec
}
```

`handleChangelog` GET 回傳新欄位。

註冊 `POST`：path 解析 `…/retry`。  
邏輯：

1. GetVersion；404 if missing  
2. raw 空 → 400  
3. tryAcquire(key) false → 409  
4. SetTranslateStatus(translating)；200 `{ok, translate_status:translating}`  
5. goroutine：`out, err := s.TranslateFn(raw)` → UpdateTranslateResult；release key  

`serve.go`：`srv.TranslateFn = tr.ChangelogResult`

單飛與 refresh：refresh 為同步迴圈，可在 RefreshUpstream 外層不強制；retry 與 refresh 並行時，UpdateTranslateResult 最後寫入勝出可接受。可選：collector 也 tryAcquire——若複雜則文件註明「手動 retry 期間 scheduled refresh 可能重入」；**最低要求 retry 自衝突 409**。

- [ ] **Step 4: 綠**

Run: `go test ./internal/server/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/ cmd/cockpit/serve.go
git commit -m "feat(api): translate timeout_sec + changelog status + retry"
```

---

### Task 6: WebUI — manage timeout + modal 狀態/輪詢/重試

**Files:**
- Modify: `cockpit_frontend/manage.html`, `manage.js`
- Modify: `cockpit_frontend/app.js`, `index.html`（若需 retry 按鈕容器）
- Modify: `cockpit_frontend/api-contract.md`, `docs/api-contract.md`（若存在）
- Modify: `inventory.example.yaml`（grok changelog 行）

**Interfaces:**
- Consumes: GET/PUT config `timeout_sec`；GET changelog status；POST retry

- [ ] **Step 1: manage 加 Timeout 欄**

`manage.html` 在 max_tokens 旁加：

```html
<div>
  <label for="tr-timeout-sec">Timeout（秒）</label>
  <input id="tr-timeout-sec" class="field mono" type="number" min="30" max="900" step="30" value="300" />
</div>
```

小字：預設 300；reasoning 模型建議 ≥ 180。

`manage.js`：load/save 帶 `timeout_sec`；`trFormBody()` 納入 `timeout_sec: Number(...)`。

- [ ] **Step 2: app.js modal 狀態機**

擴充 `openChangelog`：

```js
let modalPollTimer = null;
let modalKey = null;

function stopModalPoll() {
  if (modalPollTimer) clearInterval(modalPollTimer);
  modalPollTimer = null;
}

function renderChangelogBody(v) {
  const st = v.translate_status || (v.changelog_zh ? "ready" : (v.changelog_raw ? "failed" : "none"));
  $("#modal-raw").textContent = v.changelog_raw || "";
  if (st === "ready" && v.changelog_zh) {
    $("#modal-zh").innerHTML = mdToHtml(v.changelog_zh);
    return st;
  }
  if (st === "pending" || st === "translating") {
    $("#modal-zh").innerHTML = `<p style="color:var(--text-3);">翻譯中…完成後會自動更新</p>`;
    return st;
  }
  if (st === "failed") {
    const err = (v.translate_error || "unknown").replace(/</g, "&lt;");
    $("#modal-zh").innerHTML = `
      <p style="color:var(--err);">翻譯失敗</p>
      <p class="mono text-[12px]" style="color:var(--text-3);">${err}</p>
      <button id="modal-retry" class="btn btn-primary btn-xs mt-2">重試翻譯</button>`;
    $("#modal-retry")?.addEventListener("click", () => retryTranslate(v.software, v.version));
    return st;
  }
  $("#modal-zh").innerHTML = `<p style="color:var(--text-3);">尚無 changelog 原文</p>`;
  return st;
}

async function openChangelog(key) {
  stopModalPoll();
  modalKey = key;
  // …開 modal、載入中…
  const v = await api(`/api/changelog/...`);
  const st = renderChangelogBody(v);
  if (st === "pending" || st === "translating") {
    modalPollTimer = setInterval(async () => {
      if (modalKey !== key) return;
      try {
        const nv = await api(`/api/changelog/...`);
        const nst = renderChangelogBody(nv);
        if (nst !== "pending" && nst !== "translating") stopModalPoll();
      } catch (_) {}
    }, 2500);
  }
}

async function retryTranslate(sw, ver) {
  try {
    await api(`/api/changelog/${encodeURIComponent(sw)}/${encodeURIComponent(ver)}/retry`, { method: "POST" });
    renderChangelogBody({ software: sw, version: ver, translate_status: "translating", changelog_raw: $("#modal-raw").textContent });
    // 重啟 poll（同 openChangelog 邏輯）
  } catch (e) {
    toast?.("err", e.message || "重試失敗"); // 若無 toast 用 alert 或既有模式
  }
}
```

`closeModal` 呼叫 `stopModalPoll()`。

- [ ] **Step 3: inventory.example.yaml**

```yaml
- name: grok
  ...
  changelog: url:https://x.ai/cli/changelogs/{version}.external.md
```

- [ ] **Step 4: 更新 api-contract 文件**（GET changelog 欄位 + retry + timeout_sec）

- [ ] **Step 5: 手動煙測清單（寫在 commit message body 或 PR）**

1. manage 存 timeout 300，GET config 可見  
2. 開 failed modal → 見錯誤 + 重試  
3. 重試後「翻譯中」→ 完成變中文  

- [ ] **Step 6: Commit**

```bash
git add cockpit_frontend/ inventory.example.yaml docs/
git commit -m "feat(web): changelog modal translate status, poll, retry; timeout setting"
```

---

### Task 7: 生產 inventory + 補翻驗收（部署步驟，可同一 PR 文件）

**Files:**
- 生產：`/etc/cockpit/inventory.yaml`（部署機，非 repo 亦可；若 repo 有 inventory 範本則改範本）
- Docs：spec §8 已描述；plan 此 task 為 runbook

- [ ] **Step 1: 部署 binary 後改 inventory grok changelog 行**

```yaml
changelog: url:https://x.ai/cli/changelogs/{version}.external.md
```

- [ ] **Step 2: 觸發補翻**

```bash
# 對 failed 版本
curl -sS -X POST "http://127.0.0.1:8787/api/changelog/multica/0.4.1/retry"
curl -sS -X POST "http://127.0.0.1:8787/api/changelog/openclaw/2026.7.1/retry"
# 等數分鐘後
curl -sS "http://127.0.0.1:8787/api/changelog/multica/0.4.1" | python3 -m json.tool
curl -sS "http://127.0.0.1:8787/api/changelog/openclaw/2026.7.1" | python3 -m json.tool
# 立即檢查讓 grok 抓 raw
curl -sS -X POST "http://127.0.0.1:8787/api/check"
```

Expected：`translate_status=ready` 且 `changelog_zh` 非空；grok raw 非空。

- [ ] **Step 3: 不 commit 生產 secret；僅確認**

---

## Self-Review（plan vs spec）

| Spec 要求 | Task |
|-----------|------|
| versions 三欄位 + migration/backfill | T1 |
| Truncate 12KB + timeout 可調 + error propagate | T2 |
| grok `url:` changelog | T3 + T7 |
| Refresh 狀態機 | T4 |
| GET status + POST retry 非同步 + config timeout | T5 |
| Modal 僅狀態、輪詢、重試、manage UI | T6 |
| 補翻 multica/openclaw | T7 |
| 列表不帶 status | T5/T6 不改 installs |
| Retry 覆寫 zh 獨立 API | T1 `UpdateTranslateResult` |
| 單飛 409 | T5 |
| 不做 worker/列表 badge/可調截斷 | 無對應 task ✓ |

無 TBD 佔位；介面名稱跨 task 一致：`UpdateTranslateResult`、`ChangelogResult`、`fillChangelog` / `url:`。

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-14-changelog-translate-status.md`.

**兩種執行方式：**

1. **Subagent-Driven（建議）** — 每 task 新 subagent，task 間 review  
2. **Inline Execution** — 本 session 依序實作，checkpoint 暫停  

要哪一種？
