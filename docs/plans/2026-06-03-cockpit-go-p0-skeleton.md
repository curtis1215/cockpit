# Cockpit P0 — 統一 Go 核心骨架 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立統一 Go cockpit 的核心骨架——單一 binary（`cockpit serve|agent|version`）、內嵌前端、純 Go SQLite、agent enrollment（bootstrap token）+ heartbeat，讓 `cockpit serve` 起得來、前端載入、`cockpit agent` 能連上並讓該機在 `systems` 表由 pending 變 online。

**Architecture:** 單一 Go module `github.com/curtis1215/cockpit`。`cmd/cockpit` 用 stdlib `flag` 做子命令；`internal/{config,store,server,agent,httpx}` 各司一職；`cockpit_frontend/` 由 root `embed.go` 以 `//go:embed` 夾進 binary，server 直接服務。SQLite 用 `modernc.org/sqlite`（純 Go、免 CGO，交叉編譯零摩擦）。HTTP 用 stdlib `net/http`。

**Tech Stack:** Go 1.22+；`modernc.org/sqlite`（純 Go SQLite driver）；stdlib `net/http` / `flag` / `embed` / `database/sql`。

設計依據：`docs/specs/2026-06-03-cockpit-unified-go-design.md`（本計畫只做 §2 的 **P0 核心骨架**）。

---

## 慣例與型別約定（跨任務一致，請勿改名）

- **module path**：`github.com/curtis1215/cockpit`；內部 import 前綴 `github.com/curtis1215/cockpit/internal/...`。
- **System（`internal/store`）**：`type System struct { ID, Label, Role, OS, Arch, Kind, HostID, Status, AgentVersion, AgentStatus, LastSeen, AgentToken string ; Created int64 }`（P0 用得到的欄位；JSON tag 全 snake_case）。
- **enrollment（P0 簡化）**：serve 端持有一個 **bootstrap enroll secret**（serve config `enroll_secret`）。agent 第一次用該 secret 呼叫 `POST /api/agent/enroll`（帶 hostname/os/arch）→ server 建一筆 `online` system 並回 **長期 agent_token**；agent 落地 token 後改用 Bearer 做 `POST /api/agent/heartbeat`。（每機 UI 發 token 是 P3，本階段先用共享 secret。）
- **狀態字串**：system status ∈ `pending|online|warn|offline`；agent_status ∈ `pending|ok|stale|behind`。P0 只用到 `online`/`ok`。
- **HTTP**：人面 `GET /api/health`、`GET /api/systems`；agent 面 `POST /api/agent/enroll`、`POST /api/agent/heartbeat`（Bearer）。靜態前端掛在 `/`。

## 檔案結構

```
go.mod                         # module github.com/curtis1215/cockpit
embed.go                       # package cockpit：//go:embed cockpit_frontend → Frontend embed.FS
cmd/cockpit/main.go            # CLI：serve | agent | version
internal/
  config/config.go            # ServeConfig / AgentConfig + Load*
  store/store.go              # SQLite（modernc）：Open + schema + System CRUD
  store/schema.sql            # systems DDL
  httpx/httpx.go              # 極簡 HTTP client（PostJSON + Bearer）
  server/server.go            # net/http：embedded 前端 + /api/health + /api/systems
  server/agent_api.go         # /api/agent/enroll + /api/agent/heartbeat
  agent/agent.go              # enroll + heartbeat 迴圈
cockpit_frontend/             # （已存在）前端，被 embed
```

> 註：現有 `cockpit/`（Python）與 `agent/`（舊 Go module `cockpit-agent`）暫不動，視為 legacy；P0 在 repo root 建立新統一 module。之後階段再清掉 legacy。

---

### Task 1: Go module + CLI 骨架 + config

**Files:**
- Create: `go.mod`
- Create: `cmd/cockpit/main.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: 建 module**

Run:
```bash
cd /Users/curtis/Dev/cockpit && go mod init github.com/curtis1215/cockpit && mkdir -p cmd/cockpit internal/config
```
Expected: 產生 `go.mod`（module `github.com/curtis1215/cockpit`, go 1.2x）。

- [ ] **Step 2: 寫失敗測試** — Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServeDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "serve.json")
	os.WriteFile(p, []byte(`{"enroll_secret":"s3cret"}`), 0o600)
	c, err := LoadServe(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8787" || c.DBPath != "cockpit.db" || c.EnrollSecret != "s3cret" {
		t.Fatalf("bad serve defaults: %+v", c)
	}
}

func TestLoadAgentRequired(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.json")
	os.WriteFile(p, []byte(`{"server_url":"https://x"}`), 0o600)
	c, err := LoadAgent(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "https://x" || c.HeartbeatSec != 15 {
		t.Fatalf("bad agent cfg: %+v", c)
	}
	if _, err := LoadAgent(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
```

- [ ] **Step 3: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/config/`
Expected: FAIL（undefined: LoadServe）。

- [ ] **Step 4: 實作 config.go** — Create `internal/config/config.go`:

```go
package config

import (
	"encoding/json"
	"os"
)

type ServeConfig struct {
	Listen       string `json:"listen"`
	DBPath       string `json:"db_path"`
	EnrollSecret string `json:"enroll_secret"`
}

type AgentConfig struct {
	ServerURL    string `json:"server_url"`
	EnrollSecret string `json:"enroll_secret"`
	AgentToken   string `json:"agent_token"`
	HeartbeatSec int    `json:"heartbeat_sec"`
	path         string // 來源檔，供寫回 agent_token 用
}

func LoadServe(path string) (ServeConfig, error) {
	var c ServeConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.DBPath == "" {
		c.DBPath = "cockpit.db"
	}
	return c, nil
}

func LoadAgent(path string) (AgentConfig, error) {
	var c AgentConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.HeartbeatSec == 0 {
		c.HeartbeatSec = 15
	}
	c.path = path
	return c, nil
}

// SaveAgentToken 把 enroll 換得的 agent_token 寫回原 config 檔（保留其它欄位）。
func (c *AgentConfig) SaveAgentToken(token string) error {
	c.AgentToken = token
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o600)
}
```

- [ ] **Step 5: 跑測試確認通過**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/config/`
Expected: PASS（2 passed）。

- [ ] **Step 6: CLI 骨架** — Create `cmd/cockpit/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

var version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "agent":
		runAgent(os.Args[2:])
	case "version":
		fmt.Println("cockpit", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cockpit <serve|agent|version> [flags]")
}
```

> `runServe`/`runAgent` 在 Task 6 實作；本步驟先讓 `cockpit version` 可編譯執行。為了能編譯，**暫時**在 `cmd/cockpit/` 加一個佔位檔 `stubs.go`：

Create `cmd/cockpit/stubs.go`:

```go
package main

func runServe(args []string) { panic("serve not wired yet (Task 6)") }
func runAgent(args []string) { panic("agent not wired yet (Task 6)") }
```

- [ ] **Step 7: 編譯 + version 可跑 + Commit**

Run: `cd /Users/curtis/Dev/cockpit && go build ./... && go run ./cmd/cockpit version`
Expected: 印出 `cockpit 0.0.0-dev`。

```bash
git add go.mod cmd/cockpit/ internal/config/ && git commit -m "feat(go): module, cli skeleton, config"
```

---

### Task 2: SQLite store（modernc）+ systems schema + CRUD

**Files:**
- Create: `internal/store/schema.sql`
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: 取得 driver**

Run: `cd /Users/curtis/Dev/cockpit && go get modernc.org/sqlite@latest`
Expected: `go.mod` 加入 `modernc.org/sqlite`。

- [ ] **Step 2: 寫失敗測試** — Create `internal/store/store_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRegisterAndLookup(t *testing.T) {
	s := open(t)
	id, token, err := s.RegisterSystem("Mac mini", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || token == "" {
		t.Fatalf("empty id/token")
	}
	sys, err := s.SystemByAgentToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if sys.ID != id || sys.Status != "online" || sys.OS != "darwin" {
		t.Fatalf("bad system: %+v", sys)
	}
	if _, err := s.SystemByAgentToken("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestHeartbeatAndList(t *testing.T) {
	s := open(t)
	_, token, _ := s.RegisterSystem("box", "linux", "amd64")
	if err := s.Heartbeat(token, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSystems()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AgentVersion != "0.1.0" || list[0].LastSeen == "" {
		t.Fatalf("bad list: %+v", list)
	}
}
```

- [ ] **Step 3: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/store/`
Expected: FAIL（undefined: Open）。

- [ ] **Step 4: schema.sql** — Create `internal/store/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS systems (
  id            TEXT PRIMARY KEY,
  label         TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT '',
  os            TEXT NOT NULL DEFAULT '',
  arch          TEXT NOT NULL DEFAULT '',
  kind          TEXT NOT NULL DEFAULT 'physical',
  host_id       TEXT,
  status        TEXT NOT NULL DEFAULT 'pending',
  agent_version TEXT NOT NULL DEFAULT '',
  agent_status  TEXT NOT NULL DEFAULT 'pending',
  last_seen     TEXT NOT NULL DEFAULT '',
  agent_token   TEXT UNIQUE,
  created       INTEGER NOT NULL
);
```

- [ ] **Step 5: 實作 store.go** — Create `internal/store/store.go`:

```go
package store

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type System struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Role         string `json:"role"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Kind         string `json:"kind"`
	HostID       string `json:"host_id"`
	Status       string `json:"status"`
	AgentVersion string `json:"agent_version"`
	AgentStatus  string `json:"agent_status"`
	LastSeen     string `json:"last_seen"`
	AgentToken   string `json:"-"`
	Created      int64  `json:"created"`
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// RegisterSystem 建立一筆 online system 並回傳 (id, agent_token)。
func (s *Store) RegisterSystem(label, osName, arch string) (string, string, error) {
	id := "sys_" + randHex(6)
	token := "ck_agent_" + randHex(20)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO systems (id,label,os,arch,kind,status,agent_status,last_seen,agent_token,created)
		 VALUES (?,?,?,?, 'physical','online','ok',?,?,?)`,
		id, label, osName, arch, now, token, time.Now().Unix())
	if err != nil {
		return "", "", err
	}
	return id, token, nil
}

func scanSystem(row interface{ Scan(...any) error }) (System, error) {
	var s System
	var hostID sql.NullString
	err := row.Scan(&s.ID, &s.Label, &s.Role, &s.OS, &s.Arch, &s.Kind, &hostID,
		&s.Status, &s.AgentVersion, &s.AgentStatus, &s.LastSeen, &s.AgentToken, &s.Created)
	s.HostID = hostID.String
	return s, err
}

const cols = "id,label,role,os,arch,kind,host_id,status,agent_version,agent_status,last_seen,agent_token,created"

func (s *Store) SystemByAgentToken(token string) (System, error) {
	row := s.db.QueryRow("SELECT "+cols+" FROM systems WHERE agent_token=?", token)
	sys, err := scanSystem(row)
	if err == sql.ErrNoRows {
		return System{}, ErrNotFound
	}
	return sys, err
}

func (s *Store) Heartbeat(token, agentVersion string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`UPDATE systems SET status='online', agent_status='ok', agent_version=?, last_seen=? WHERE agent_token=?`,
		agentVersion, now, token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListSystems() ([]System, error) {
	rows, err := s.db.Query("SELECT " + cols + " FROM systems ORDER BY created")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []System
	for rows.Next() {
		sys, err := scanSystem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sys)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: 跑測試確認通過**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/store/`
Expected: PASS（2 passed；首次會下載 modernc，稍久）。

- [ ] **Step 7: Commit**

```bash
git add internal/store/ go.mod go.sum && git commit -m "feat(go): sqlite store with systems schema and crud"
```

---

### Task 3: Server core — 內嵌前端 + /api/health + /api/systems

**Files:**
- Create: `embed.go`
- Create: `internal/server/server.go`
- Test: `internal/server/server_test.go`

- [ ] **Step 1: root embed** — Create `embed.go`（repo root，package `cockpit`，只放 embed、不 import 任何 internal 套件以免循環）：

```go
package cockpit

import "embed"

//go:embed all:cockpit_frontend
var Frontend embed.FS
```

- [ ] **Step 2: 寫失敗測試** — Create `internal/server/server_test.go`:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/curtis1215/cockpit/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, "s3cret"), st
}

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != 200 || rec.Body.String() != `{"ok":true}` {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}
}

func TestListSystemsEmptyThenOne(t *testing.T) {
	srv, st := newTestServer(t)
	st.RegisterSystem("Mac mini", "darwin", "arm64")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/systems", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "Mac mini") || !contains(body, `"status":"online"`) {
		t.Fatalf("systems body: %s", body)
	}
}

func TestServesFrontendIndex(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("index status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !contains(ct, "text/html") {
		t.Fatalf("index content-type %q", ct)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}()) }

var _ = http.MethodGet
```

- [ ] **Step 3: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/server/`
Expected: FAIL（undefined: New）。

- [ ] **Step 4: 實作 server.go** — Create `internal/server/server.go`:

```go
package server

import (
	"encoding/json"
	"io/fs"
	"net/http"

	rootpkg "github.com/curtis1215/cockpit"
	"github.com/curtis1215/cockpit/internal/store"
)

type Server struct {
	st           *store.Store
	enrollSecret string
	mux          *http.ServeMux
}

func New(st *store.Store, enrollSecret string) *Server {
	s := &Server{st: st, enrollSecret: enrollSecret, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	s.mux.HandleFunc("/api/systems", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.st.ListSystems()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if list == nil {
			list = []store.System{}
		}
		writeJSON(w, 200, list)
	})
	s.registerAgentAPI() // Task 4

	// 靜態前端（catch-all，需最後註冊；ServeMux 以最長前綴匹配，/api/* 仍優先）
	sub, _ := fs.Sub(rootpkg.Frontend, "cockpit_frontend")
	s.mux.Handle("/", http.FileServer(http.FS(sub)))
}
```

> 註：`registerAgentAPI` 由 Task 4 在 `agent_api.go` 實作（同 package）。本步驟先讓它存在——若 Task 4 尚未做，暫時加一個空實作以便編譯：在 `server.go` 末端**暫時**加 `func (s *Server) registerAgentAPI() {}`，Task 4 會把它移到 `agent_api.go` 並填內容。

為了本任務能單獨編譯/通過，先在 `server.go` 末端加：

```go
func (s *Server) registerAgentAPI() {} // 由 Task 4 取代
```

- [ ] **Step 5: 跑測試確認通過**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/server/`
Expected: PASS（3 passed）。

- [ ] **Step 6: Commit**

```bash
git add embed.go internal/server/ && git commit -m "feat(go): http server with embedded frontend, health, systems list"
```

---

### Task 4: Agent 面 API — enroll + heartbeat

**Files:**
- Create: `internal/server/agent_api.go`
- Modify: `internal/server/server.go`（移除 Task 3 的暫時空 `registerAgentAPI`）
- Test: `internal/server/agent_api_test.go`

- [ ] **Step 1: 寫失敗測試** — Create `internal/server/agent_api_test.go`:

```go
package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnrollThenHeartbeat(t *testing.T) {
	srv, st := newTestServer(t) // enrollSecret = "s3cret"

	// 錯誤 secret → 401
	bad := httptest.NewRecorder()
	srv.Handler().ServeHTTP(bad, httptest.NewRequest("POST", "/api/agent/enroll",
		strings.NewReader(`{"label":"box","os":"linux","arch":"amd64","enroll_secret":"wrong"}`)))
	if bad.Code != 401 {
		t.Fatalf("bad secret want 401 got %d", bad.Code)
	}

	// 正確 secret → 200 + agent_token
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/agent/enroll",
		strings.NewReader(`{"label":"box","os":"linux","arch":"amd64","enroll_secret":"s3cret"}`)))
	if rec.Code != 200 {
		t.Fatalf("enroll want 200 got %d (%s)", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"agent_token":"ck_agent_`) {
		t.Fatalf("no agent_token: %s", rec.Body.String())
	}
	// 取出 token
	tok := extractToken(rec.Body.String())

	// heartbeat 無 token → 401
	noauth := httptest.NewRecorder()
	srv.Handler().ServeHTTP(noauth, httptest.NewRequest("POST", "/api/agent/heartbeat",
		strings.NewReader(`{"agent_version":"0.1.0"}`)))
	if noauth.Code != 401 {
		t.Fatalf("heartbeat noauth want 401 got %d", noauth.Code)
	}

	// heartbeat 帶 Bearer → 204，且 system 變 online + 有版本
	hb := httptest.NewRequest("POST", "/api/agent/heartbeat", strings.NewReader(`{"agent_version":"0.1.0"}`))
	hb.Header.Set("Authorization", "Bearer "+tok)
	hrec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(hrec, hb)
	if hrec.Code != 204 {
		t.Fatalf("heartbeat want 204 got %d", hrec.Code)
	}
	sys, err := st.SystemByAgentToken(tok)
	if err != nil || sys.AgentVersion != "0.1.0" || sys.Status != "online" {
		t.Fatalf("system after hb: %+v err=%v", sys, err)
	}
}

func extractToken(body string) string {
	const key = `"agent_token":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	j := strings.IndexByte(rest, '"')
	return rest[:j]
}
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/server/ -run TestEnrollThenHeartbeat`
Expected: FAIL（enroll 404/未實作）。

- [ ] **Step 3: 移除 Task 3 暫時空實作** — 編輯 `internal/server/server.go`，刪掉末端那行 `func (s *Server) registerAgentAPI() {} // 由 Task 4 取代`。

- [ ] **Step 4: 實作 agent_api.go** — Create `internal/server/agent_api.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/curtis1215/cockpit/internal/store"
)

func (s *Server) registerAgentAPI() {
	s.mux.HandleFunc("/api/agent/enroll", s.handleEnroll)
	s.mux.HandleFunc("/api/agent/heartbeat", s.handleHeartbeat)
}

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var body struct {
		Label, OS, Arch, EnrollSecret string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if s.enrollSecret == "" || body.EnrollSecret != s.enrollSecret {
		writeJSON(w, 401, map[string]string{"error": "invalid enroll secret"})
		return
	}
	label := body.Label
	if label == "" {
		label = "unnamed"
	}
	id, token, err := s.st.RegisterSystem(label, body.OS, body.Arch)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"system_id": id, "agent_token": token})
}

func (s *Server) bearer(r *http.Request) (store.System, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return store.System{}, false
	}
	sys, err := s.st.SystemByAgentToken(strings.TrimSpace(h[len("Bearer "):]))
	if err != nil {
		return store.System{}, false
	}
	return sys, true
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	sys, ok := s.bearer(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		AgentVersion string `json:"agent_version"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := s.st.Heartbeat(sys.AgentToken, body.AgentVersion); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(204)
}
```

- [ ] **Step 5: 跑測試確認通過**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/server/`
Expected: PASS（全 server 測試含 enroll/heartbeat）。

- [ ] **Step 6: Commit**

```bash
git add internal/server/ && git commit -m "feat(go): agent enroll + heartbeat api with bearer auth"
```

---

### Task 5: HTTP client + Agent runtime（enroll + heartbeat 迴圈）

**Files:**
- Create: `internal/httpx/httpx.go`
- Create: `internal/agent/agent.go`
- Test: `internal/agent/agent_test.go`

- [ ] **Step 1: httpx client** — Create `internal/httpx/httpx.go`:

```go
package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string, timeout time.Duration) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: timeout}}
}

// PostJSON 送 body（JSON），可選 bearer；status>=400 回 error，否則把回應解進 out（out 可為 nil）。
func (c *Client) PostJSON(path, bearer string, body, out any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", c.base+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return resp.StatusCode, fmt.Errorf("http %d: %s", resp.StatusCode, msg)
	}
	if out != nil && resp.StatusCode != 204 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
```

- [ ] **Step 2: 寫失敗測試** — Create `internal/agent/agent_test.go`:

```go
package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEnrollThenHeartbeatOnce(t *testing.T) {
	var enrolled, beats int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/enroll":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["enroll_secret"] != "s3cret" {
				w.WriteHeader(401)
				return
			}
			atomic.AddInt32(&enrolled, 1)
			json.NewEncoder(w).Encode(map[string]string{"system_id": "sys_x", "agent_token": "ck_agent_tok"})
		case "/api/agent/heartbeat":
			if r.Header.Get("Authorization") != "Bearer ck_agent_tok" {
				w.WriteHeader(401)
				return
			}
			atomic.AddInt32(&beats, 1)
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()

	var savedToken string
	a := &Agent{
		ServerURL: srv.URL,
		Secret:    "s3cret",
		Token:     "", // 尚未 enroll
		Version:   "0.1.0",
		SaveToken: func(tok string) error { savedToken = tok; return nil },
	}
	if err := a.ensureEnrolled(); err != nil {
		t.Fatal(err)
	}
	if savedToken != "ck_agent_tok" || a.Token != "ck_agent_tok" || atomic.LoadInt32(&enrolled) != 1 {
		t.Fatalf("enroll failed: token=%q saved=%q n=%d", a.Token, savedToken, enrolled)
	}
	if err := a.heartbeat(); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&beats) != 1 {
		t.Fatalf("want 1 beat got %d", beats)
	}
}
```

- [ ] **Step 3: 跑測試確認失敗**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/agent/`
Expected: FAIL（undefined: Agent）。

- [ ] **Step 4: 實作 agent.go** — Create `internal/agent/agent.go`:

```go
package agent

import (
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/curtis1215/cockpit/internal/httpx"
)

type Agent struct {
	ServerURL string
	Secret    string
	Token     string
	Version   string
	HeartbeatSec int
	SaveToken func(string) error // 把 enroll 換得的 token 落地
	client    *httpx.Client
}

func (a *Agent) c() *httpx.Client {
	if a.client == nil {
		a.client = httpx.New(a.ServerURL, 20*time.Second)
	}
	return a.client
}

func hostLabel() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unnamed"
	}
	return h
}

// ensureEnrolled：若無 token，用 secret 換 token 並落地。
func (a *Agent) ensureEnrolled() error {
	if a.Token != "" {
		return nil
	}
	if a.Secret == "" {
		return errors.New("agent: no agent_token and no enroll_secret")
	}
	var out struct {
		AgentToken string `json:"agent_token"`
	}
	_, err := a.c().PostJSON("/api/agent/enroll", "", map[string]string{
		"label": hostLabel(), "os": runtime.GOOS, "arch": runtime.GOARCH, "enroll_secret": a.Secret,
	}, &out)
	if err != nil {
		return err
	}
	if out.AgentToken == "" {
		return errors.New("agent: enroll returned empty token")
	}
	a.Token = out.AgentToken
	if a.SaveToken != nil {
		return a.SaveToken(a.Token)
	}
	return nil
}

func (a *Agent) heartbeat() error {
	_, err := a.c().PostJSON("/api/agent/heartbeat", a.Token,
		map[string]string{"agent_version": a.Version}, nil)
	return err
}

// Run：enroll（必要時）後進入 heartbeat 迴圈，直到 process 結束。
func (a *Agent) Run() error {
	if err := a.ensureEnrolled(); err != nil {
		return err
	}
	interval := a.HeartbeatSec
	if interval <= 0 {
		interval = 15
	}
	for {
		if err := a.heartbeat(); err != nil {
			// 失敗就退避重試（簡化：固定 sleep）
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
}
```

- [ ] **Step 5: 跑測試確認通過**

Run: `cd /Users/curtis/Dev/cockpit && go test ./internal/agent/ ./internal/httpx/`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/httpx/ internal/agent/ && git commit -m "feat(go): http client and agent runtime (enroll + heartbeat)"
```

---

### Task 6: CLI 接線（serve/agent）+ 整合測試 + build

**Files:**
- Create: `cmd/cockpit/serve.go`
- Create: `cmd/cockpit/agentcmd.go`
- Delete: `cmd/cockpit/stubs.go`
- Test: `cmd/cockpit/integration_test.go`

- [ ] **Step 1: serve 子命令** — Create `cmd/cockpit/serve.go`:

```go
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/curtis1215/cockpit/internal/config"
	"github.com/curtis1215/cockpit/internal/server"
	"github.com/curtis1215/cockpit/internal/store"
)

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/cockpit/serve.json", "serve config json")
	fs.Parse(args)

	cfg, err := config.LoadServe(*cfgPath)
	if err != nil {
		log.Fatalf("serve config: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	srv := server.New(st, cfg.EnrollSecret)
	log.Printf("cockpit serve on http://%s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 2: agent 子命令** — Create `cmd/cockpit/agentcmd.go`:

```go
package main

import (
	"flag"
	"log"

	"github.com/curtis1215/cockpit/internal/agent"
	"github.com/curtis1215/cockpit/internal/config"
)

func runAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/cockpit/agent.json", "agent config json")
	fs.Parse(args)

	cfg, err := config.LoadAgent(*cfgPath)
	if err != nil {
		log.Fatalf("agent config: %v", err)
	}
	a := &agent.Agent{
		ServerURL:    cfg.ServerURL,
		Secret:       cfg.EnrollSecret,
		Token:        cfg.AgentToken,
		Version:      version,
		HeartbeatSec: cfg.HeartbeatSec,
		SaveToken:    cfg.SaveAgentToken,
	}
	if err := a.Run(); err != nil {
		log.Fatalf("agent: %v", err)
	}
}
```

- [ ] **Step 3: 刪除 stub**

Run: `cd /Users/curtis/Dev/cockpit && rm cmd/cockpit/stubs.go`

- [ ] **Step 4: 整合測試**（in-process server + agent enroll + heartbeat → systems online）— Create `cmd/cockpit/integration_test.go`:

```go
package main

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/curtis1215/cockpit/internal/agent"
	"github.com/curtis1215/cockpit/internal/server"
	"github.com/curtis1215/cockpit/internal/store"
)

func TestEndToEndEnrollHeartbeat(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewServer(server.New(st, "s3cret").Handler())
	defer ts.Close()

	a := &agent.Agent{ServerURL: ts.URL, Secret: "s3cret", Version: "9.9.9",
		SaveToken: func(string) error { return nil }}
	// 模擬一輪：enroll + 一次 heartbeat（不進無窮迴圈）
	if err := a.RunOnce(); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListSystems()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != "online" || list[0].AgentVersion != "9.9.9" {
		t.Fatalf("system not online with version: %+v", list)
	}
	_ = time.Second
}
```

> 整合測試用 `RunOnce`（enroll + 單次 heartbeat，不進迴圈）。在 `internal/agent/agent.go` 加：

```go
// RunOnce：enroll（必要時）+ 一次 heartbeat。供測試/驗證用。
func (a *Agent) RunOnce() error {
	if err := a.ensureEnrolled(); err != nil {
		return err
	}
	return a.heartbeat()
}
```

- [ ] **Step 5: 跑全部測試 + build**

Run:
```bash
cd /Users/curtis/Dev/cockpit && go test ./... && go vet ./... && go build -o /tmp/cockpit ./cmd/cockpit
```
Expected: 全 package PASS、vet clean、build 出 `/tmp/cockpit`。

- [ ] **Step 6: 手動煙霧驗證（選做但建議）**

```bash
mkdir -p /tmp/ck && printf '{"listen":"127.0.0.1:8799","db_path":"/tmp/ck/c.db","enroll_secret":"s3cret"}' > /tmp/ck/serve.json
/tmp/cockpit serve -config /tmp/ck/serve.json &   # 背景起 server
sleep 1
curl -s http://127.0.0.1:8799/api/health           # → {"ok":true}
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8799/   # → 200（前端 index）
printf '{"server_url":"http://127.0.0.1:8799","enroll_secret":"s3cret"}' > /tmp/ck/agent.json
/tmp/cockpit agent -config /tmp/ck/agent.json &     # agent enroll + heartbeat
sleep 2
curl -s http://127.0.0.1:8799/api/systems           # → 一筆 online system
# 清理：kill 背景的 cockpit
kill %1 %2 2>/dev/null
```
Expected: health ok、`/` 回 200、`/api/systems` 有一筆 `"status":"online"`。

- [ ] **Step 7: Commit**

```bash
cd /Users/curtis/Dev/cockpit && git add cmd/cockpit/ internal/agent/agent.go && git rm cmd/cockpit/stubs.go && git commit -m "feat(go): wire serve/agent cli + end-to-end enroll/heartbeat"
```

---

## Self-Review（已執行）

**1. Spec coverage（對 P0 範圍）：**
- 單一 binary `cockpit serve|agent|version` → Task 1/6 ✅（`upgrade` 屬 P4，本階段不做）
- config（serve/agent）→ Task 1 ✅
- 內嵌前端（`//go:embed`）服務 → Task 3 ✅
- 純 Go SQLite + systems schema → Task 2 ✅
- agent enrollment（token）+ 傳輸骨架（Bearer）+ heartbeat → Task 4/5 ✅
- 驗收「serve 起得來、前端載入、agent enroll、systems pending→online」→ Task 6 整合測試 + 煙霧 ✅
  （P0 用 bootstrap enroll_secret；每機 UI 發 token 是 P3，spec §11 已述。）

**2. Placeholder scan：** 無 TODO/TBD。唯一「暫時」碼是 Task 3 的空 `registerAgentAPI`，Task 4 Step 3 明確移除——非 placeholder，是過渡編譯墊片，已標清楚。

**3. Type consistency：** `store.System`、`store.Open/RegisterSystem/SystemByAgentToken/Heartbeat/ListSystems`、`server.New(st, enrollSecret)`、`server.Handler()`、`httpx.New/PostJSON`、`agent.Agent{ServerURL,Secret,Token,Version,HeartbeatSec,SaveToken}` 與 `ensureEnrolled/heartbeat/Run/RunOnce`、`config.LoadServe/LoadAgent/SaveAgentToken` 跨任務一致 ✅。`registerAgentAPI` 同 package（server）定義一次（Task 4），Task 3 的墊片於 Task 4 移除，不重複定義 ✅。

**4. 已知範圍邊界（非缺漏，後續階段）：** 版本追蹤 API（P1）、指標/服務/VM 回報（P2）、UI 接線（P1–P3）、`cockpit upgrade` + GoReleaser + service 安裝（P4）。前端目前仍走自身 mock（`store.js`），P0 只負責「能被 server 服務 + 能載入」，逐頁接真 API 在 P1–P3。
