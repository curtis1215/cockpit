# Web UI 觸發 Server 自我升級 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 管理頁一鍵升級 cockpit server 到最新 GitHub release，免 SSH。

**Architecture:** 重用 `internal/selfupdate`（原子替換 binary）。新增 `POST /api/server/upgrade` 端點（注入 `upgradeFn`/`exitFn` seam 以利測試），替換成功後行程退出、由 launchd `KeepAlive` / systemd `Restart` 以新 binary 拉起。`GET /api/version` 擴充 `latest`/`update_available`（server 端查 GitHub、記憶體快取 1 小時）。前端管理頁加升級按鈕 + 輪詢恢復偵測。

**Tech Stack:** Go（net/http、`internal/selfupdate`）、vanilla JS（manage.js）。

**Spec:** `docs/specs/2026-06-08-server-self-upgrade-design.md`

**重要背景知識（實作前必讀）：**

1. `selfupdate.Run(hc, base, repo, currentVersion, targetPath)` 回傳 `(replaced bool, err error)`：`(true,nil)`=已替換、`(false,nil)`=已最新、`(false,err)`=失敗（先下載驗證再原子替換，失敗時舊 binary 不動）。`targetPath=""` 表示 `os.Executable()`。
2. `selfupdate.Latest(hc, base, repo)` 回傳 `(tag string, assets map[string]string, err)`，tag 帶 `v` 前綴。
3. repo 慣例：`os.Getenv("COCKPIT_REPO")`，空則 `"curtis1215/cockpit"`（見 `internal/agent/agent.go:103-107` 與 `cmd/cockpit/upgrade.go`）。
4. `/api/version` 既有 handler 在 `internal/server/server.go:89-95`（inline closure，回 `{"version": v}`，version 空時回 `"dev"`）。
5. `Server` struct 在 `internal/server/server.go:15-24`；測試 helper `newTestServer(t)` 在 `internal/server/server_test.go`；HTTP 測試用 `doJSON(t, srv, method, path, body)`（`manage_api_test.go`）。
6. dev build 判斷：`version == "" || version == "0.0.0-dev"`。
7. 前端 toast：`toast("ok"|"warn"|"err", msg)`（manage.js）；server 版本顯示在 `manage.html:86` 的 `<span id="server-ver">`，由 manage.js 檔尾 `api("/api/version").then(...)` 填入。
8. 測試指令在 repo root：`cd /Users/curtis/Dev/cockpit`。

---

## File Structure

- `internal/server/upgrade_api.go`（新）：update-status 快取邏輯 + upgrade 端點 + seam 預設值 — 自我升級的所有 server 邏輯集中一檔
- `internal/server/upgrade_api_test.go`（新）：上述全部測試
- `internal/server/server.go`（改）：Server struct 加欄位、`/api/version` handler 改呼叫新邏輯、路由註冊
- `cmd/cockpit/doctor.go`（改）：serve 段加 binary 可寫性檢查
- `cockpit_frontend/manage.js`（改）：升級按鈕 + 輪詢
- `cockpit_frontend/manage.html`（改）：按鈕容器
- `cockpit_frontend/api-contract.md`（改）：端點文件

---

### Task 1: Server struct 擴充 + `/api/version` 帶 latest（含 1h 快取）

**Files:**
- Create: `internal/server/upgrade_api.go`
- Modify: `internal/server/server.go`（struct、NewWithInventory、/api/version handler）
- Test: `internal/server/upgrade_api_test.go`（新檔）

- [ ] **Step 1: Write the failing test**

建立 `internal/server/upgrade_api_test.go`：

```go
package server

import (
	"encoding/json"
	"testing"
)

// ── /api/version with latest ────────────────────────────────────────────────

func TestVersionWithLatest(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	calls := 0
	srv.latestFn = func() (string, error) { calls++; return "0.3.0", nil }

	rec := doJSON(t, srv, "GET", "/api/version", "")
	if rec.Code != 200 {
		t.Fatalf("version: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["version"] != "0.2.1" || resp["latest"] != "0.3.0" || resp["update_available"] != true {
		t.Fatalf("resp = %v", resp)
	}

	// 快取：再打一次不應重查
	doJSON(t, srv, "GET", "/api/version", "")
	if calls != 1 {
		t.Fatalf("latestFn calls = %d, want 1 (cached)", calls)
	}
}

func TestVersionLatestEqualsCurrent(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.3.0")
	srv.latestFn = func() (string, error) { return "0.3.0", nil }
	rec := doJSON(t, srv, "GET", "/api/version", "")
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["update_available"] != false {
		t.Fatalf("same version should not flag update: %v", resp)
	}
}

func TestVersionLatestFetchFails(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	srv.latestFn = func() (string, error) { return "", errTest }
	rec := doJSON(t, srv, "GET", "/api/version", "")
	if rec.Code != 200 {
		t.Fatalf("fetch failure must degrade, not error: %d", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["latest"] != "" || resp["update_available"] != false {
		t.Fatalf("degraded resp = %v", resp)
	}
}

func TestVersionDevBuildSkipsLatest(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.0.0-dev")
	called := false
	srv.latestFn = func() (string, error) { called = true; return "9.9.9", nil }
	rec := doJSON(t, srv, "GET", "/api/version", "")
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if called || resp["update_available"] != false {
		t.Fatalf("dev build must not query github: called=%v resp=%v", called, resp)
	}
}
```

並在檔案底部加共用測試錯誤（若 server 套件測試已有同名變數則沿用）：

```go
var errTest = json.Unmarshal([]byte("x"), &struct{}{}) // 任何非 nil error 皆可
```

（若覺得彆扭可改 `errors.New("boom")` 並 import `errors`。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run 'TestVersion' -v`
Expected: FAIL（compile error：`srv.latestFn` 未定義）

- [ ] **Step 3: Implement**

3a. `internal/server/server.go` — `Server` struct（line 15-24）加欄位：

```go
type Server struct {
	st           *store.Store
	enrollSecret string
	invMu        sync.RWMutex
	inv          inventory.Inventory
	invPath      string
	onCheck      func()
	mux          *http.ServeMux
	version      string

	// self-upgrade（見 upgrade_api.go；測試可替換）
	latestFn    func() (string, error) // 查 GitHub 最新版（不帶 v 前綴）
	upgradeFn   func() (bool, error)   // 執行自我升級
	exitFn      func()                 // 升級成功後退出行程
	latestMu    sync.Mutex
	latestCache string    // 上次查到的 latest（可為空 = 查失敗）
	latestAt    time.Time // 上次查詢時間
	upgrading   atomic.Bool
}
```

import 加 `"sync/atomic"`、`"time"`。

3b. `NewWithInventory` 內（return 前）設定預設值：

```go
	s.latestFn = defaultLatestFn()
	s.upgradeFn = func() (bool, error) { return defaultUpgrade(s.version) }
	s.exitFn = func() { os.Exit(0) }
```

（依該函式實際結構調整變數名；import 加 `"os"`。）

3c. `/api/version` handler（server.go:89-95）改為：

```go
	s.mux.HandleFunc("/api/version", s.apiVersion)
```

3d. 建立 `internal/server/upgrade_api.go`：

```go
package server

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/selfupdate"
)

const latestCacheTTL = time.Hour

func cockpitRepo() string {
	if r := os.Getenv("COCKPIT_REPO"); r != "" {
		return r
	}
	return "curtis1215/cockpit"
}

// defaultLatestFn 查 GitHub 最新 release tag（去掉 v 前綴）。
func defaultLatestFn() func() (string, error) {
	return func() (string, error) {
		hc := &http.Client{Timeout: 20 * time.Second}
		tag, _, err := selfupdate.Latest(hc, "https://api.github.com", cockpitRepo())
		if err != nil {
			return "", err
		}
		return strings.TrimPrefix(tag, "v"), nil
	}
}

// defaultUpgrade 以 selfupdate 替換自身執行檔。
func defaultUpgrade(current string) (bool, error) {
	hc := &http.Client{Timeout: 60 * time.Second}
	return selfupdate.Run(hc, "https://api.github.com", cockpitRepo(), current, "")
}

func (s *Server) isDevBuild() bool {
	return s.version == "" || s.version == "0.0.0-dev"
}

// latestCached 回傳快取的 latest（TTL 內直接用，過期重查；查失敗回空字串並快取）。
func (s *Server) latestCached() string {
	s.latestMu.Lock()
	defer s.latestMu.Unlock()
	if time.Since(s.latestAt) < latestCacheTTL && !s.latestAt.IsZero() {
		return s.latestCache
	}
	v, err := s.latestFn()
	if err != nil {
		v = ""
	}
	s.latestCache, s.latestAt = v, time.Now()
	return v
}

// apiVersion handles GET /api/version.
func (s *Server) apiVersion(w http.ResponseWriter, r *http.Request) {
	v := s.version
	if v == "" {
		v = "dev"
	}
	latest := ""
	if !s.isDevBuild() {
		latest = s.latestCached()
	}
	writeJSON(w, 200, map[string]any{
		"version":          v,
		"latest":           latest,
		"update_available": latest != "" && latest != s.version,
	})
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/server/ -v`
Expected: 全部 PASS（注意既有 `TestHealth` 等不受影響；若有既有測試斷言 `/api/version` 回應形狀（grep `api/version` 既有測試），同步更新斷言）

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/upgrade_api.go internal/server/upgrade_api_test.go
git commit -m "feat(api): /api/version reports latest release + update_available (1h cache)"
```

---

### Task 2: `POST /api/server/upgrade` 端點

**Files:**
- Modify: `internal/server/upgrade_api.go`、`internal/server/server.go`（路由）
- Test: `internal/server/upgrade_api_test.go`（追加）

- [ ] **Step 1: Write the failing test**

`upgrade_api_test.go` 追加：

```go
import 區補 "sync" 與 "time"（若未有）

// ── POST /api/server/upgrade ────────────────────────────────────────────────

func TestServerUpgradeSuccess(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	exited := make(chan struct{})
	srv.upgradeFn = func() (bool, error) { return true, nil }
	srv.exitFn = func() { close(exited) }

	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 202 {
		t.Fatalf("upgrade: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("exitFn not called within 3s")
	}
}

func TestServerUpgradeUpToDate(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	srv.upgradeFn = func() (bool, error) { return false, nil }
	srv.exitFn = func() { t.Fatal("must not exit when up to date") }
	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 200 {
		t.Fatalf("up-to-date: %d %s", rec.Code, rec.Body.String())
	}
	// 鎖須釋放：可再次呼叫
	rec = doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 200 {
		t.Fatalf("second call after release: %d", rec.Code)
	}
}

func TestServerUpgradeError(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	srv.upgradeFn = func() (bool, error) { return false, errBoom }
	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 500 {
		t.Fatalf("error: %d %s", rec.Code, rec.Body.String())
	}
	// 失敗後鎖須釋放
	srv.upgradeFn = func() (bool, error) { return false, nil }
	if rec := doJSON(t, srv, "POST", "/api/server/upgrade", ""); rec.Code != 200 {
		t.Fatalf("after error, lock must be released: %d", rec.Code)
	}
}

func TestServerUpgradeConcurrent(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	block := make(chan struct{})
	srv.upgradeFn = func() (bool, error) { <-block; return false, nil }

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); doJSON(t, srv, "POST", "/api/server/upgrade", "") }()
	time.Sleep(100 * time.Millisecond) // 讓第一個請求先取得鎖
	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 409 {
		t.Fatalf("concurrent: %d, want 409", rec.Code)
	}
	close(block)
	wg.Wait()
}

func TestServerUpgradeDevBuild(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.0.0-dev")
	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 400 {
		t.Fatalf("dev build: %d, want 400", rec.Code)
	}
}

func TestServerUpgradeMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doJSON(t, srv, "GET", "/api/server/upgrade", "")
	if rec.Code != 405 {
		t.Fatalf("GET: %d, want 405", rec.Code)
	}
}
```

`errBoom` 定義（檔頂 var 區）：

```go
var errBoom = errors.New("boom")
```

（import `errors`；Task 1 的 `errTest` 若已寫成 `errors.New` 形式則共用一個即可。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run 'TestServerUpgrade' -v`
Expected: FAIL（404，路由不存在）

- [ ] **Step 3: Implement**

3a. `server.go` 路由區（`/api/version` 那行附近）加：

```go
	s.mux.HandleFunc("/api/server/upgrade", s.apiServerUpgrade)
```

3b. `upgrade_api.go` 追加：

```go
// apiServerUpgrade handles POST /api/server/upgrade：自我升級並重啟。
// 流程：互斥 → selfupdate 原子替換 → 記 event → 202 → 延遲退出（launchd/systemd 拉起新版）。
func (s *Server) apiServerUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if s.isDevBuild() {
		writeJSON(w, 400, map[string]string{"error": "dev build cannot self-upgrade"})
		return
	}
	if !s.upgrading.CompareAndSwap(false, true) {
		writeJSON(w, 409, map[string]string{"error": "upgrade already in progress"})
		return
	}

	// binary 可寫性 pre-check：降權安裝（如 plist UserName）會踩到，給出可操作的錯誤。
	if exe, err := os.Executable(); err == nil {
		if f, err := os.OpenFile(exe, os.O_WRONLY, 0); err != nil {
			s.upgrading.Store(false)
			writeJSON(w, 500, map[string]string{
				"error": "binary not writable by server process; run: sudo chown $(whoami) " + exe,
			})
			return
		} else {
			f.Close()
		}
	}

	replaced, err := s.upgradeFn()
	if err != nil {
		s.upgrading.Store(false)
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !replaced {
		s.upgrading.Store(false)
		writeJSON(w, 200, map[string]string{"status": "up_to_date"})
		return
	}
	s.st.AddEvent("upgrade", "", "server", "self-upgrade triggered from web ui (from "+s.version+")")
	writeJSON(w, 202, map[string]string{"status": "restarting"})
	go func() {
		time.Sleep(1 * time.Second)
		s.exitFn()
	}()
}
```

注意：升級成功（202）這條路**不**釋放 `upgrading` — 行程即將退出，留鎖避免退場前再觸發。

3c. 測試環境的 pre-check：`newTestServer` 跑的是 `go test` 編譯出的測試 binary（自己擁有、可寫），pre-check 會通過，不影響測試。若在某些 CI 環境 pre-check 意外失敗，把 pre-check 抽成 `writableCheckFn func() error` seam 並在測試 stub 掉（預設實作同上）。

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/server/ -v && go test -race ./internal/server/ -run 'TestServerUpgrade'`
Expected: 全部 PASS（含 -race）

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/server/upgrade_api.go internal/server/upgrade_api_test.go
git commit -m "feat(api): POST /api/server/upgrade — self-upgrade via selfupdate + service-manager restart"
```

---

### Task 3: doctor 加 binary 可寫性檢查

**Files:**
- Modify: `cmd/cockpit/doctor.go`（serve 段，`macPlistUserWarning` 附近）
- Test: `cmd/cockpit/doctor_test.go`（若該檔已有可單測的純函式模式則加；否則以手動驗證為準）

- [ ] **Step 1: 閱讀現有結構**

讀 `cmd/cockpit/doctor.go:185-225`（`macPlistUserWarning` 一帶），確認 serve 模式檢查的輸出格式（warning 字串以 `\n   ⚠️ ` 開頭）。

- [ ] **Step 2: 實作純函式 + 接線**

`doctor.go` 加：

```go
// binaryWritableWarning：serve 行程對自身 binary 不可寫時提示（self-upgrade 需要）。
// 以「目前 doctor 行程的 euid 能否寫 exePath」近似判斷——doctor 通常與服務同身份執行。
func binaryWritableWarning(exePath string) string {
	f, err := os.OpenFile(exePath, os.O_WRONLY, 0)
	if err != nil {
		return "\n   ⚠️  server binary 不可寫，Web UI 自我升級會失敗（sudo chown <service-user> " + exePath + "）"
	}
	f.Close()
	return ""
}
```

在 serve 模式的檢查輸出處（與 `macPlistUserWarning` 呼叫同一段）接上：

```go
	if exe, err := os.Executable(); err == nil {
		out += binaryWritableWarning(exe)
	}
```

（變數名依該段實際程式碼調整；維持既有輸出風格。）

- [ ] **Step 3: 單測（若 doctor_test.go 有純函式測試慣例）**

```go
func TestBinaryWritableWarning(t *testing.T) {
	// 可寫：自己建的暫存檔
	f := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if w := binaryWritableWarning(f); w != "" {
		t.Fatalf("writable file should not warn: %q", w)
	}
	// 不可寫：唯讀檔
	ro := filepath.Join(t.TempDir(), "ro")
	os.WriteFile(ro, []byte("x"), 0o444)
	if w := binaryWritableWarning(ro); w == "" {
		t.Fatal("read-only file should warn")
	}
}
```

（root 跑測試時唯讀檔仍可寫，case 會誤判 — 比照該檔既有測試慣例，必要時 `if os.Geteuid() == 0 { t.Skip("root can write anything") }`。）

- [ ] **Step 4: Run + Commit**

Run: `go test ./cmd/cockpit/ -run TestBinaryWritableWarning -v && go build ./...`
Expected: PASS、build 成功

```bash
git add cmd/cockpit/doctor.go cmd/cockpit/doctor_test.go
git commit -m "feat(doctor): warn when serve binary is not writable (self-upgrade prereq)"
```

---

### Task 4: 前端 — 管理頁升級按鈕與輪詢

**Files:**
- Modify: `cockpit_frontend/manage.html`（header）
- Modify: `cockpit_frontend/manage.js`（檔尾 /api/version 區塊）

前端無測試框架；以 `go build ./...` + Task 6 手動驗收為準。

- [ ] **Step 1: manage.html 加按鈕容器**

`manage.html:86` 的 `<span id="server-ver" ...></span>` 之後（同一個 div 內）加：

```html
<button id="server-upgrade-btn" class="btn btn-xs" style="display:none;margin-left:.5rem;font-size:10px;padding:2px 8px;"></button>
```

- [ ] **Step 2: manage.js 改版本載入 + 按鈕邏輯**

把檔尾的：

```js
  api("/api/version").then((vr) => {
    const el = document.getElementById("server-ver");
    if (vr && vr.version) {
      serverVersion = vr.version;
      if (el) el.textContent = vr.version;
    }
  });
```

改為：

```js
  function refreshServerVersion() {
    return api("/api/version").then((vr) => {
      const el = document.getElementById("server-ver");
      if (vr && vr.version) {
        serverVersion = vr.version;
        if (el) el.textContent = vr.version;
      }
      const btn = document.getElementById("server-upgrade-btn");
      if (btn) {
        if (vr && vr.update_available) {
          btn.textContent = `↑ 升級 Server 到 v${vr.latest}`;
          btn.style.display = "";
        } else {
          btn.style.display = "none";
        }
      }
      return vr;
    });
  }
  refreshServerVersion();

  // ── Server 自我升級 ──────────────────────────────────────────────────────
  document.getElementById("server-upgrade-btn").addEventListener("click", async (e) => {
    const btn = e.currentTarget;
    if (!confirm("升級會重啟 server（約 10–30 秒），確定？")) return;
    btn.disabled = true;
    try {
      const res = await api("/api/server/upgrade", { method: "POST" });
      if (res && res.status === "up_to_date") {
        toast("ok", "已是最新版");
        await refreshServerVersion();
        return;
      }
      // 202 restarting → 輪詢直到版本變更
      toast("ok", "升級中，server 重啟約 10–30 秒…");
      const oldVer = serverVersion;
      const deadline = Date.now() + 90_000;
      const poll = async () => {
        if (Date.now() > deadline) {
          toast("warn", "升級逾時，請手動檢查 server 狀態");
          btn.disabled = false;
          return;
        }
        try {
          const vr = await api("/api/version");
          if (vr && vr.version && vr.version !== oldVer) {
            toast("ok", `已升級到 v${vr.version}`);
            await refreshServerVersion();
            await loadAll();
            return;
          }
        } catch (_) { /* 重啟中，連線失敗屬預期 */ }
        setTimeout(poll, 3000);
      };
      setTimeout(poll, 3000);
    } catch (err) {
      if (err.status === 409) toast("warn", "升級已在進行");
      else toast("err", "升級失敗：" + err.message);
      btn.disabled = false;
    }
  });
```

- [ ] **Step 3: Build + Commit**

Run: `go build ./...`
Expected: 成功

```bash
git add cockpit_frontend/manage.html cockpit_frontend/manage.js
git commit -m "feat(web): manage page server self-upgrade button with restart polling"
```

---

### Task 5: 文件

**Files:**
- Modify: `cockpit_frontend/api-contract.md`

- [ ] **Step 1: 補端點文件**

在 api-contract.md 的 version/系統相關段落補：

```markdown
### GET /api/version

回應：`{"version":"0.2.1","latest":"0.2.2","update_available":true}`

- `latest`：GitHub 最新 release（server 端查詢、記憶體快取 1 小時；查詢失敗回空字串）
- `update_available`：`latest` 非空且 ≠ `version`；dev build 一律 false

### POST /api/server/upgrade

觸發 server 自我升級（selfupdate 原子替換 + 服務管理器重啟）。

- `202 {"status":"restarting"}`：已替換 binary，約 1 秒後行程退出重啟
- `200 {"status":"up_to_date"}`：已是最新
- `400`：dev build；`409`：升級已在進行；`500`：失敗（含 binary 不可寫，錯誤訊息附 chown 指令）

前提：serve 行程須對自身 binary 可寫（預設 root 安裝天然滿足；手動降權的服務需一次性 `sudo chown <user> <binary>`）。
```

- [ ] **Step 2: Commit**

```bash
git add cockpit_frontend/api-contract.md
git commit -m "docs: api-contract for /api/version latest fields + POST /api/server/upgrade"
```

---

### Task 6: 全量驗證 + 手動驗收清單

- [ ] **Step 1: 全量測試 + build**

Run: `go test ./... && go build ./...`
Expected: 全部 PASS、build 成功

- [ ] **Step 2: 手動驗收（需在 mac-mini 部署後執行；無環境時明確標注待人工驗證）**

前置（一次性，手動降權過的服務才需要）：

```sh
sudo chown curtis /usr/local/bin/cockpit
```

清單：

- [ ] 發新版後，管理頁 server 版本旁出現「↑ 升級 Server 到 vX.Y.Z」按鈕
- [ ] 已是最新版時按鈕不顯示
- [ ] 點擊 → confirm → toast「升級中…」→ 約 10–30 秒後 toast「已升級到 vX.Y.Z」、版本字樣更新、按鈕消失
- [ ] chown 前點擊（可選驗證）→ 500 錯誤 toast 內含 chown 指令
- [ ] `cockpit doctor`（以 service user 跑）在 binary 不可寫時印警告
- [ ] 升級期間重複點擊 → 「升級已在進行」

---

## Self-Review 紀錄

- Spec 覆蓋：§1 兩端點（Task 1-2）、§2 前端（Task 4）、§3 pre-check + doctor（Task 2 步驟 3b pre-check、Task 3 doctor）、§4 邊緣情境（dev build / 併發 / 失敗釋放鎖 / 輪詢逾時都有測試或實作）、§5 測試（Task 1-3 TDD、Task 6 手動清單）
- 型別一致性：`latestFn func() (string, error)`、`upgradeFn func() (bool, error)`、`exitFn func()`、`upgrading atomic.Bool` 在 Task 1 struct 定義與 Task 2 使用一致；前端 `refreshServerVersion()` 定義與呼叫一致
- 已知取捨：202 路徑不釋放鎖（行程將亡）已註明；pre-check 在測試環境可寫（測試 binary 自有）已註明 fallback seam
