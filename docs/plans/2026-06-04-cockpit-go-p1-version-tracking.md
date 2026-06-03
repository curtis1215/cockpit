# Cockpit P1 (backend) — 版本追蹤 port 到 Go Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把現有 Python 版本追蹤器（inventory、版本來源、版本解析、build_update、jobs 佇列、SSE log、changelog 翻譯、collector）port 進統一 Go module，並把 agent 端的版本讀取/更新執行接上 P0 的 enroll/heartbeat 傳輸——讓「偵測落後 → 點更新 → agent 執行 → 即時 log → 完成」端到端在 Go 跑通。

**Architecture:** 延續 P0 的 Go module `github.com/curtis1215/cockpit`。新增 `internal/{version,inventory,sources,translate,jobs,collector,executor}`，擴充 `internal/store`（versions/installs/jobs 表 + machine flags）與 `internal/server`（瀏覽器面版本 API + SSE、agent 面 poll/report/log/result/control）與 `internal/agent`（版本讀取 + 更新執行迴圈）。沿用 agent-daemon 的佇列模型（server 渲染指令、agent 笨執行）。**inventory 本階段為 YAML**（含每機 `agent_token`，與 P0 的 bootstrap enroll 並存——agent_token 由 inventory 提供以做版本 API 的 token→machine 解析）。

**Tech Stack:** Go 1.22+；stdlib `net/http`/`database/sql`/`os/exec`/`regexp`/`encoding/json`；`modernc.org/sqlite`；`gopkg.in/yaml.v3`（inventory 解析）。changelog 翻譯走 `claude -p`（`os/exec`）。

設計依據：`docs/specs/2026-06-03-cockpit-unified-go-design.md`（P1 範圍）。本計畫**不含前端版本頁接線**（另立 P1-frontend）。

---

## 慣例與型別約定（跨任務一致，請勿改名）

- **狀態字串**：install status ∈ `up_to_date|behind|unknown|error`；job status ∈ `queued|running|success|failed|aborted`；update type ∈ `command|agent`；runner ∈ `codex_exec|claude_p|custom`；provider ∈ `npm|github|pypi|brew|claude-plugin|custom`。
- **inventory 型別**（`internal/inventory`）：`Machine{Name,Host,SSHUser,Local,AgentToken}`、`Update{Type,Cmd,Runner,Prompt,Machine,Cwd,Invoke}`、`Install{Machine,CurrentCmd,Update,VersionRegex}`、`Software{Name,Kind,LatestSource,Changelog,Installs}`、`Inventory{Machines map[string]Machine, Software []Software}`。
- **store 新函式**（接 P0 的 `*Store`）：`UpsertInstall`、`ListInstalls`、`GetInstall`、`AddVersion`、`GetVersion`、`LatestVersion`、`LatestVersionMap`、`CreateJobUnique`、`ClaimOldestQueued`、`SetJobDispatch`、`GetJob`、`ListJobs`、`AppendJobLog`、`FinishJob`、`UpsertInstallStatus`、`RequestAbort`、`AbortRequested`、`AddEvent`、`LastError`、`SetCheckRequested`、`TakeCheckRequested`。
- **版本**：`version.Parse(text, regex) string`（回 "" 表無）；`version.Compare(current, latest) (status string, behind int)`。
- **來源**：`sources.SourceResult{Version, ChangelogRaw}`；`sources.FetchLatest(sw Software, hc *http.Client) (SourceResult, error)`。
- **jobs**：`jobs.BuildUpdate(inv, sw, inst, latest, current, changelogZh) (cmd string, machine Machine, err error)`；`jobs.StartJob`、`jobs.ClaimNextJob`、`jobs.RecordResult`、`jobs.RequestAbort`；`ErrActiveJobExists`。
- **指令唯一來源為 inventory**；agent 只跑 server 渲染好的字串。Unix 走 `bash -lc`（Windows 之後 P2/P4 處理）。

## 檔案結構

```
internal/
  version/version.go          # Parse + Compare（port version_parse.py）
  inventory/inventory.go      # YAML 解析 + 型別 + MachineForToken（port inventory.py）
  sources/sources.go          # FetchLatest 分派 + npm + github
  sources/more.go             # pypi/brew/claude_plugin/custom
  translate/translate.go      # claude -p 翻譯（port translate.py）
  executor/executor.go        # bash -lc 串流 + timeout + group-kill（port 自舊 agent/）
  jobs/jobs.go                # BuildUpdate + 佇列（port jobs.py）
  collector/collector.go      # RefreshUpstream + ApplyVersionReport（port collector.py）
  store/
    schema.sql                # +versions/installs/jobs/machine_state（接 P0 的 systems）
    store.go                  # +版本追蹤 CRUD
  server/
    version_api.go            # 瀏覽器面：installs/changelog/jobs/update/abort/check
    sse.go                    # /api/jobs/{id}/log/stream（SSE）
    agent_vt_api.go           # agent 面：installs/poll/report-versions/log/result/control
  agent/agent.go              # +版本讀取迴圈 + job 執行迴圈
  config/config.go            # serve 加 inventory_path、check_hours
cmd/cockpit/serve.go          # 載 inventory、起 collector 排程
```

---

### Task 1: version 套件（Parse + Compare）

**Files:** Create `internal/version/version.go`, Test `internal/version/version_test.go`

- [ ] **Step 1: 寫失敗測試** — `internal/version/version_test.go`:

```go
package version

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]string{"2.1.101": "2.1.101", "claude 2.1.98 (x)": "2.1.98", "v0.9.0": "0.9.0", "no ver": ""}
	for in, want := range cases {
		if got := Parse(in, ""); got != want {
			t.Fatalf("Parse(%q)=%q want %q", in, got, want)
		}
	}
	if got := Parse("image: multica:0.8.2", `multica:([0-9.]+)`); got != "0.8.2" {
		t.Fatalf("custom regex got %q", got)
	}
	if got := Parse("app:1.2.3", `app:[0-9.]+`); got != "app:1.2.3" { // 無 group → 整段
		t.Fatalf("no-group got %q", got)
	}
	if got := Parse("x", `([0-9`); got != "" { // 非法 regex
		t.Fatalf("bad regex got %q", got)
	}
}

func TestCompare(t *testing.T) {
	check := func(cur, lat, wantS string, wantN int) {
		s, n := Compare(cur, lat)
		if s != wantS || n != wantN {
			t.Fatalf("Compare(%q,%q)=(%q,%d) want (%q,%d)", cur, lat, s, n, wantS, wantN)
		}
	}
	check("2.1.98", "2.1.101", "behind", 3)
	check("2.1.101", "2.1.101", "up_to_date", 0)
	check("1.0.0", "0.9.0", "up_to_date", 0)
	check("", "2.1.101", "unknown", 0)
}
```

- [ ] **Step 2: 跑測試確認失敗** — `cd /Users/curtis/Dev/cockpit && go test ./internal/version/` → FAIL（undefined: Parse）。

- [ ] **Step 3: 實作** — `internal/version/version.go`:

```go
package version

import (
	"regexp"
	"strconv"
	"strings"
)

var semver = regexp.MustCompile(`(\d+(?:\.\d+){1,3})`)

// Parse 從文字抽版本：customRegex 為空用預設 semver（group1）；自訂 regex 有 capture group 用 group1、否則整段；非法 regex 回 ""。
func Parse(text, customRegex string) string {
	re := semver
	group := 1
	if customRegex != "" {
		r, err := regexp.Compile(customRegex)
		if err != nil {
			return ""
		}
		re = r
		if re.NumSubexp() == 0 {
			group = 0
		}
	}
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[group]
}

func key(v string) ([]int, bool) {
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// Compare 回 (status, behindCount)。current/latest 任一空或不可解析 → ("unknown",0)；current>=latest → ("up_to_date",0)；否則 ("behind", N)。
func Compare(current, latest string) (string, int) {
	if current == "" || latest == "" {
		return "unknown", 0
	}
	ck, ok1 := key(current)
	lk, ok2 := key(latest)
	if !ok1 || !ok2 {
		return "unknown", 0
	}
	n := len(ck)
	if len(lk) > n {
		n = len(lk)
	}
	pad := func(a []int) []int {
		for len(a) < n {
			a = append(a, 0)
		}
		return a
	}
	ck, lk = pad(ck), pad(lk)
	cmp := 0
	for i := 0; i < n; i++ {
		if ck[i] != lk[i] {
			if ck[i] < lk[i] {
				cmp = -1
			} else {
				cmp = 1
			}
			break
		}
	}
	if cmp >= 0 {
		return "up_to_date", 0
	}
	behind := 1
	eq := true
	for i := 0; i < n-1; i++ {
		if ck[i] != lk[i] {
			eq = false
			break
		}
	}
	if eq {
		behind = lk[n-1] - ck[n-1]
		if behind < 1 {
			behind = 1
		}
	}
	return "behind", behind
}
```

- [ ] **Step 4: 跑測試確認通過** — `go test ./internal/version/` → PASS。
- [ ] **Step 5: Commit** — `git add internal/version/ && git commit -m "feat(go): version parse + compare"`

---

### Task 2: inventory 套件（YAML）

**Files:** Create `internal/inventory/inventory.go`, Test `internal/inventory/inventory_test.go`；新增依賴 `gopkg.in/yaml.v3`。

- [ ] **Step 1: 取得 yaml** — `cd /Users/curtis/Dev/cockpit && go get gopkg.in/yaml.v3@latest`

- [ ] **Step 2: 寫失敗測試** — `internal/inventory/inventory_test.go`:

```go
package inventory

import "testing"

const inv = `
machines:
  mac:  { host: 1.2.3.4, ssh_user: curtis, local: true, agent_token: tok-mac }
  box:  { host: 5.6.7.8, ssh_user: root, agent_token: tok-box }
software:
  - name: cc
    kind: npm
    latest_source: "npm:cc"
    changelog: null
    installs:
      - machine: mac
        current_cmd: "cc --version"
        update: { type: command, cmd: "npm i -g cc@latest" }
  - name: multica
    kind: custom
    latest_source: "github:o/multica"
    changelog: "github:o/multica"
    installs:
      - machine: box
        current_cmd: "docker inspect"
        update: { type: agent, runner: codex_exec, cwd: /srv/multica, prompt: "update to {latest_version}" }
`

func TestLoad(t *testing.T) {
	iv, err := LoadText([]byte(inv))
	if err != nil {
		t.Fatal(err)
	}
	if iv.Machines["mac"].AgentToken != "tok-mac" || !iv.Machines["mac"].Local {
		t.Fatalf("mac: %+v", iv.Machines["mac"])
	}
	if iv.Software[0].Installs[0].Update.Type != "command" || iv.Software[1].Installs[0].Update.Runner != "codex_exec" {
		t.Fatalf("software: %+v", iv.Software)
	}
	if MachineForToken(iv, "tok-box") != "box" || MachineForToken(iv, "nope") != "" {
		t.Fatal("token resolve")
	}
}

func TestValidation(t *testing.T) {
	bad := "machines: { mac: { host: x } }\nsoftware: []\n" // 缺 ssh_user
	if _, err := LoadText([]byte(bad)); err == nil {
		t.Fatal("want error for missing ssh_user")
	}
	badRef := inv[:len(inv)-1] + "\n" // ok base; 另測未知 machine ref
	_ = badRef
	bad2 := `
machines: { mac: { host: x, ssh_user: c } }
software:
  - name: s
    latest_source: "github:o/s"
    installs:
      - machine: ghost
        current_cmd: "x"
        update: { type: command, cmd: "y" }
`
	if _, err := LoadText([]byte(bad2)); err == nil {
		t.Fatal("want error for unknown machine ref")
	}
}
```

- [ ] **Step 3: 跑測試確認失敗** — `go test ./internal/inventory/` → FAIL。

- [ ] **Step 4: 實作** — `internal/inventory/inventory.go`:

```go
package inventory

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Machine struct {
	Name       string
	Host       string
	SSHUser    string
	Local      bool
	AgentToken string
}
type Update struct {
	Type, Cmd, Runner, Prompt, Machine, Cwd, Invoke string
}
type Install struct {
	Machine, CurrentCmd string
	Update              Update
	VersionRegex        string
}
type Software struct {
	Name, Kind, LatestSource, Changelog string
	Installs                            []Install
}
type Inventory struct {
	Machines map[string]Machine
	Software []Software
}

func LoadText(b []byte) (Inventory, error) {
	var raw struct {
		Machines map[string]map[string]any `yaml:"machines"`
		Software []struct {
			Name         string `yaml:"name"`
			Kind         string `yaml:"kind"`
			LatestSource string `yaml:"latest_source"`
			Changelog    string `yaml:"changelog"`
			Installs     []struct {
				Machine      string         `yaml:"machine"`
				CurrentCmd   string         `yaml:"current_cmd"`
				VersionRegex string         `yaml:"version_regex"`
				Update       map[string]any `yaml:"update"`
			} `yaml:"installs"`
		} `yaml:"software"`
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return Inventory{}, fmt.Errorf("yaml: %w", err)
	}
	inv := Inventory{Machines: map[string]Machine{}}
	for name, m := range raw.Machines {
		host, _ := m["host"].(string)
		ssh, _ := m["ssh_user"].(string)
		if host == "" || ssh == "" {
			return Inventory{}, fmt.Errorf("machine %s: need host and ssh_user", name)
		}
		local, _ := m["local"].(bool)
		tok, _ := m["agent_token"].(string)
		inv.Machines[name] = Machine{Name: name, Host: host, SSHUser: ssh, Local: local, AgentToken: tok}
	}
	for _, sw := range raw.Software {
		if sw.Name == "" {
			return Inventory{}, fmt.Errorf("software missing name")
		}
		if sw.LatestSource == "" {
			return Inventory{}, fmt.Errorf("software %s: need latest_source", sw.Name)
		}
		kind := sw.Kind
		if kind == "" {
			kind = "custom"
		}
		out := Software{Name: sw.Name, Kind: kind, LatestSource: sw.LatestSource, Changelog: sw.Changelog}
		for i, inst := range sw.Installs {
			if _, ok := inv.Machines[inst.Machine]; !ok {
				return Inventory{}, fmt.Errorf("software %s install[%d]: unknown machine %q", sw.Name, i, inst.Machine)
			}
			if inst.CurrentCmd == "" {
				return Inventory{}, fmt.Errorf("software %s install[%d]: need current_cmd", sw.Name, i)
			}
			up, err := parseUpdate(inst.Update, fmt.Sprintf("software %s install[%d]", sw.Name, i))
			if err != nil {
				return Inventory{}, err
			}
			out.Installs = append(out.Installs, Install{Machine: inst.Machine, CurrentCmd: inst.CurrentCmd, Update: up, VersionRegex: inst.VersionRegex})
		}
		inv.Software = append(inv.Software, out)
	}
	return inv, nil
}

func parseUpdate(raw map[string]any, ctx string) (Update, error) {
	s := func(k string) string { v, _ := raw[k].(string); return v }
	t := s("type")
	switch t {
	case "command":
		if s("cmd") == "" {
			return Update{}, fmt.Errorf("%s: command update needs cmd", ctx)
		}
		return Update{Type: "command", Cmd: s("cmd")}, nil
	case "agent":
		if s("runner") == "" {
			return Update{}, fmt.Errorf("%s: agent update needs runner", ctx)
		}
		if s("prompt") == "" {
			return Update{}, fmt.Errorf("%s: agent update needs prompt", ctx)
		}
		return Update{Type: "agent", Runner: s("runner"), Prompt: s("prompt"), Machine: s("machine"), Cwd: s("cwd"), Invoke: s("invoke")}, nil
	default:
		return Update{}, fmt.Errorf("%s: unknown update.type %q", ctx, t)
	}
}

func Load(path string) (Inventory, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Inventory{}, err
	}
	return LoadText(b)
}

func MachineForToken(inv Inventory, token string) string {
	if token == "" {
		return ""
	}
	for name, m := range inv.Machines {
		if m.AgentToken != "" && m.AgentToken == token {
			return name
		}
	}
	return ""
}
```

- [ ] **Step 5: 跑測試確認通過** — `go test ./internal/inventory/` → PASS。
- [ ] **Step 6: Commit** — `git add internal/inventory/ go.mod go.sum && git commit -m "feat(go): inventory yaml loader"`

---

### Task 3: store — versions/installs/jobs 表 + CRUD

**Files:** Modify `internal/store/schema.sql`, `internal/store/store.go`; Test `internal/store/vt_test.go`

- [ ] **Step 1: 寫失敗測試** — `internal/store/vt_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func vtOpen(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInstallsAndVersions(t *testing.T) {
	s := vtOpen(t)
	s.AddVersion("cc", "2.1.101", "", "raw", "中文")
	if v, _ := s.GetVersion("cc", "2.1.101"); v.ChangelogZh != "中文" {
		t.Fatalf("version: %+v", v)
	}
	s.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	s.UpsertInstall("cc", "mac", "2.1.101", "up_to_date", "t2") // upsert
	rows, _ := s.ListInstalls()
	if len(rows) != 1 || rows[0].CurrentVersion != "2.1.101" || rows[0].Status != "up_to_date" {
		t.Fatalf("installs: %+v", rows)
	}
	if lv := s.LatestVersionMap()["cc"]; lv != "2.1.101" {
		t.Fatalf("latest map: %v", lv)
	}
}

func TestJobQueue(t *testing.T) {
	s := vtOpen(t)
	jid, err := s.CreateJobUnique("cc", "mac", "command", "")
	if err != nil || jid == 0 {
		t.Fatalf("create: %v %d", err, jid)
	}
	if dup, _ := s.CreateJobUnique("cc", "mac", "command", ""); dup != 0 {
		t.Fatal("want 0 for active duplicate")
	}
	claimed, _ := s.ClaimOldestQueued("mac")
	if claimed == nil || claimed.ID != jid {
		t.Fatalf("claim: %+v", claimed)
	}
	s.SetJobDispatch(jid, "npm i", "", "cc --version", "")
	s.AppendJobLog(jid, "line1")
	s.FinishJob(jid, "success", 0, "2.1.101")
	job, _ := s.GetJob(jid)
	if job.Status != "success" || job.NewVersion != "2.1.101" || job.Cmd != "npm i" || job.Log != "line1\n" {
		t.Fatalf("job: %+v", job)
	}
}

func TestAbortAndCheckFlags(t *testing.T) {
	s := vtOpen(t)
	jid, _ := s.CreateJobUnique("cc", "mac", "command", "")
	s.RequestAbort(jid)
	if !s.AbortRequested(jid) {
		t.Fatal("abort flag")
	}
	s.SetCheckRequested("mac")
	if !s.TakeCheckRequested("mac") || s.TakeCheckRequested("mac") {
		t.Fatal("check flag once")
	}
}
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/store/ -run 'Installs|JobQueue|Abort'` → FAIL。

- [ ] **Step 3: schema.sql 追加** — 在 `internal/store/schema.sql` 末端追加：

```sql
CREATE TABLE IF NOT EXISTS versions (
  software TEXT NOT NULL, version TEXT NOT NULL, released_at TEXT,
  changelog_raw TEXT, changelog_zh TEXT, fetched_at TEXT DEFAULT (datetime('now')),
  PRIMARY KEY (software, version)
);
CREATE TABLE IF NOT EXISTS installs (
  software TEXT NOT NULL, machine TEXT NOT NULL, current_version TEXT,
  status TEXT NOT NULL DEFAULT 'unknown', last_checked TEXT,
  PRIMARY KEY (software, machine)
);
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT, software TEXT NOT NULL, machine TEXT NOT NULL,
  kind TEXT NOT NULL, runner TEXT, status TEXT NOT NULL DEFAULT 'queued',
  started_at TEXT, finished_at TEXT, exit_code INTEGER, new_version TEXT,
  log TEXT NOT NULL DEFAULT '', cmd TEXT, cwd TEXT, current_cmd TEXT, version_regex TEXT,
  abort_requested INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT DEFAULT (datetime('now')),
  type TEXT NOT NULL, software TEXT, machine TEXT, detail TEXT
);
CREATE TABLE IF NOT EXISTS machine_state (
  machine TEXT PRIMARY KEY, check_requested INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT DEFAULT (datetime('now'))
);
```

- [ ] **Step 4: store.go 追加** — 在 `internal/store/store.go` 末端追加（型別 + 函式）：

```go
type Version struct {
	Software, VersionStr, ReleasedAt, ChangelogRaw, ChangelogZh string
}
type Install struct {
	Software, Machine, CurrentVersion, Status, LastChecked string
}
type Job struct {
	ID                                          int64
	Software, Machine, Kind, Runner, Status     string
	StartedAt, FinishedAt                       string
	ExitCode                                    int
	NewVersion, Log, Cmd, Cwd, CurrentCmd, VRgx string
	AbortRequested                              bool
}

func (s *Store) AddVersion(software, ver, released, raw, zh string) error {
	_, err := s.db.Exec(
		`INSERT INTO versions (software,version,released_at,changelog_raw,changelog_zh) VALUES (?,?,?,?,?)
		 ON CONFLICT(software,version) DO UPDATE SET released_at=excluded.released_at,
		   changelog_raw=excluded.changelog_raw,
		   changelog_zh=COALESCE(excluded.changelog_zh, versions.changelog_zh)`,
		software, ver, nullStr(released), raw, nullStr(zh))
	return err
}
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (s *Store) GetVersion(software, ver string) (Version, error) {
	var v Version
	var rel, raw, zh sql.NullString
	err := s.db.QueryRow(`SELECT software,version,released_at,changelog_raw,changelog_zh FROM versions WHERE software=? AND version=?`, software, ver).
		Scan(&v.Software, &v.VersionStr, &rel, &raw, &zh)
	if err == sql.ErrNoRows {
		return Version{}, ErrNotFound
	}
	v.ReleasedAt, v.ChangelogRaw, v.ChangelogZh = rel.String, raw.String, zh.String
	return v, err
}
func (s *Store) LatestVersion(software string) (Version, error) {
	var v Version
	var rel, raw, zh sql.NullString
	err := s.db.QueryRow(`SELECT software,version,released_at,changelog_raw,changelog_zh FROM versions WHERE software=? ORDER BY rowid DESC LIMIT 1`, software).
		Scan(&v.Software, &v.VersionStr, &rel, &raw, &zh)
	if err == sql.ErrNoRows {
		return Version{}, ErrNotFound
	}
	v.ReleasedAt, v.ChangelogRaw, v.ChangelogZh = rel.String, raw.String, zh.String
	return v, err
}
func (s *Store) LatestVersionMap() map[string]string {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT software,version FROM versions ORDER BY rowid`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		rows.Scan(&a, &b)
		out[a] = b
	}
	return out
}
func (s *Store) UpsertInstall(software, machine, cur, status, lastChecked string) error {
	_, err := s.db.Exec(
		`INSERT INTO installs (software,machine,current_version,status,last_checked) VALUES (?,?,?,?,?)
		 ON CONFLICT(software,machine) DO UPDATE SET current_version=excluded.current_version,
		   status=excluded.status, last_checked=excluded.last_checked`,
		software, machine, nullStr(cur), status, lastChecked)
	return err
}
func (s *Store) GetInstall(software, machine string) (Install, error) {
	var i Install
	var cur, lc sql.NullString
	err := s.db.QueryRow(`SELECT software,machine,current_version,status,last_checked FROM installs WHERE software=? AND machine=?`, software, machine).
		Scan(&i.Software, &i.Machine, &cur, &i.Status, &lc)
	if err == sql.ErrNoRows {
		return Install{}, ErrNotFound
	}
	i.CurrentVersion, i.LastChecked = cur.String, lc.String
	return i, err
}
func (s *Store) ListInstalls() ([]Install, error) {
	rows, err := s.db.Query(`SELECT software,machine,current_version,status,last_checked FROM installs ORDER BY software,machine`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Install
	for rows.Next() {
		var i Install
		var cur, lc sql.NullString
		rows.Scan(&i.Software, &i.Machine, &cur, &i.Status, &lc)
		i.CurrentVersion, i.LastChecked = cur.String, lc.String
		out = append(out, i)
	}
	return out, rows.Err()
}

// CreateJobUnique：同 (software,machine) 無 queued/running 才建，回 jobID；有則回 0。
func (s *Store) CreateJobUnique(software, machine, kind, runner string) (int64, error) {
	var existing int
	err := s.db.QueryRow(`SELECT 1 FROM jobs WHERE software=? AND machine=? AND status IN ('queued','running') LIMIT 1`, software, machine).Scan(&existing)
	if err == nil {
		return 0, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO jobs (software,machine,kind,runner) VALUES (?,?,?,?)`, software, machine, kind, nullStr(runner))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (s *Store) ClaimOldestQueued(machine string) (*Job, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM jobs WHERE machine=? AND status='queued' ORDER BY id LIMIT 1`, machine).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE jobs SET status='running', started_at=datetime('now') WHERE id=?`, id); err != nil {
		return nil, err
	}
	j, err := s.GetJob(id)
	return &j, err
}
func (s *Store) SetJobDispatch(id int64, cmd, cwd, currentCmd, vrgx string) error {
	_, err := s.db.Exec(`UPDATE jobs SET cmd=?, cwd=?, current_cmd=?, version_regex=? WHERE id=?`,
		nullStr(cmd), nullStr(cwd), nullStr(currentCmd), nullStr(vrgx), id)
	return err
}
func (s *Store) GetJob(id int64) (Job, error) {
	var j Job
	var runner, started, finished, newv, cmd, cwd, ccmd, vrgx sql.NullString
	var exit sql.NullInt64
	err := s.db.QueryRow(`SELECT id,software,machine,kind,runner,status,started_at,finished_at,exit_code,new_version,log,cmd,cwd,current_cmd,version_regex,abort_requested FROM jobs WHERE id=?`, id).
		Scan(&j.ID, &j.Software, &j.Machine, &j.Kind, &runner, &j.Status, &started, &finished, &exit, &newv, &j.Log, &cmd, &cwd, &ccmd, &vrgx, &j.AbortRequested)
	if err == sql.ErrNoRows {
		return Job{}, ErrNotFound
	}
	j.Runner, j.StartedAt, j.FinishedAt, j.NewVersion = runner.String, started.String, finished.String, newv.String
	j.Cmd, j.Cwd, j.CurrentCmd, j.VRgx, j.ExitCode = cmd.String, cwd.String, ccmd.String, vrgx.String, int(exit.Int64)
	return j, err
}
func (s *Store) ListJobs(limit int) ([]Job, error) {
	rows, err := s.db.Query(`SELECT id FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		j, err := s.GetJob(id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}
func (s *Store) AppendJobLog(id int64, line string) error {
	_, err := s.db.Exec(`UPDATE jobs SET log = log || ? || char(10) WHERE id=?`, line, id)
	return err
}
func (s *Store) FinishJob(id int64, status string, exit int, newVersion string) error {
	_, err := s.db.Exec(`UPDATE jobs SET status=?, exit_code=?, new_version=?, finished_at=datetime('now') WHERE id=?`,
		status, exit, nullStr(newVersion), id)
	return err
}
func (s *Store) RequestAbort(id int64) error {
	_, err := s.db.Exec(`UPDATE jobs SET abort_requested=1 WHERE id=?`, id)
	return err
}
func (s *Store) AbortRequested(id int64) bool {
	var v int
	s.db.QueryRow(`SELECT abort_requested FROM jobs WHERE id=?`, id).Scan(&v)
	return v != 0
}
func (s *Store) AddEvent(typ, software, machine, detail string) error {
	_, err := s.db.Exec(`INSERT INTO events (type,software,machine,detail) VALUES (?,?,?,?)`, typ, nullStr(software), nullStr(machine), detail)
	return err
}
func (s *Store) LastError(software, machine string) string {
	var d sql.NullString
	s.db.QueryRow(`SELECT detail FROM events WHERE type='error' AND software=? AND machine=? ORDER BY id DESC LIMIT 1`, software, machine).Scan(&d)
	return d.String
}
func (s *Store) SetCheckRequested(machine string) error {
	_, err := s.db.Exec(`INSERT INTO machine_state (machine,check_requested,updated_at) VALUES (?,1,datetime('now'))
		ON CONFLICT(machine) DO UPDATE SET check_requested=1, updated_at=datetime('now')`, machine)
	return err
}
func (s *Store) TakeCheckRequested(machine string) bool {
	var v int
	s.db.QueryRow(`SELECT check_requested FROM machine_state WHERE machine=?`, machine).Scan(&v)
	if v != 0 {
		s.db.Exec(`UPDATE machine_state SET check_requested=0, updated_at=datetime('now') WHERE machine=?`, machine)
		return true
	}
	return false
}
```

> 注意：`internal/store/store.go` 既有 `import` 區已含 `database/sql`；本任務新增的程式用到 `sql.NullString`/`sql.NullInt64`，沿用既有 import 即可，無新 import。

- [ ] **Step 5: 跑測試確認通過** — `go test ./internal/store/` → PASS（含 P0 既有 systems 測試 + 新 3 個）。
- [ ] **Step 6: Commit** — `git add internal/store/ && git commit -m "feat(go): store versions/installs/jobs/events/machine_state"`

---

### Task 4: sources — FetchLatest + npm + github

**Files:** Create `internal/sources/sources.go`, Test `internal/sources/sources_test.go`

- [ ] **Step 1: 寫失敗測試**（用自訂 `http.Client` + `httptest`，不打真網路）— `internal/sources/sources_test.go`:

```go
package sources

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
)

func srv(h http.HandlerFunc) (*httptest.Server, *http.Client) {
	s := httptest.NewServer(h)
	return s, s.Client()
}

func TestNpm(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"dist-tags":{"latest":"2.1.101"}}`))
	})
	defer s.Close()
	sw := inventory.Software{Name: "cc", Kind: "npm", LatestSource: "npm:cc"}
	res, err := fetchNpm(sw, "cc", hc, s.URL) // base 注入測試
	if err != nil || res.Version != "2.1.101" {
		t.Fatalf("npm: %+v %v", res, err)
	}
}

func TestGithub(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.9.0","body":"## 0.9.0\n- fix"}`))
	})
	defer s.Close()
	sw := inventory.Software{Name: "m", Kind: "github", LatestSource: "github:o/m", Changelog: "github:o/m"}
	res, err := fetchGithub(sw, "o/m", hc, s.URL)
	if err != nil || res.Version != "0.9.0" || res.ChangelogRaw == "" {
		t.Fatalf("github: %+v %v", res, err)
	}
}
```

> 設計：對外的 `FetchLatest(sw, hc)` 用正式 base URL；內部 `fetchNpm/fetchGithub(sw, locator, hc, base)` 把 base 參數化以便測試注入 httptest。

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/sources/` → FAIL。

- [ ] **Step 3: 實作** — `internal/sources/sources.go`:

```go
package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
)

type SourceResult struct {
	Version      string
	ChangelogRaw string
}

const (
	npmBase    = "https://registry.npmjs.org"
	githubBase = "https://api.github.com"
	pypiBase   = "https://pypi.org"
	brewBase   = "https://formulae.brew.sh"
)

func split(source string) (provider, locator string) {
	i := strings.IndexByte(source, ':')
	if i < 0 {
		return source, ""
	}
	return source[:i], source[i+1:]
}

func FetchLatest(sw inventory.Software, hc *http.Client) (SourceResult, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	provider, locator := split(sw.LatestSource)
	switch provider {
	case "npm":
		return fetchNpm(sw, locator, hc, npmBase)
	case "github":
		return fetchGithub(sw, locator, hc, githubBase)
	case "pypi":
		return fetchPypi(sw, locator, hc, pypiBase)
	case "brew":
		return fetchBrew(sw, locator, hc, brewBase)
	case "claude-plugin":
		return fetchGithub(sw, locator, hc, githubBase)
	case "custom":
		return fetchCustom(sw, locator)
	default:
		return SourceResult{}, fmt.Errorf("unknown provider: %s", provider)
	}
}

func getJSON(hc *http.Client, url string, hdr map[string]string, out any) error {
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("http %d: %s", resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func ghHeaders() map[string]string {
	if t := os.Getenv("COCKPIT_GITHUB_TOKEN"); t != "" {
		return map[string]string{"Authorization": "Bearer " + t}
	}
	return nil
}

func fetchNpm(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		DistTags struct {
			Latest string `json:"latest"`
		} `json:"dist-tags"`
	}
	if err := getJSON(hc, base+"/"+locator, nil, &out); err != nil {
		return SourceResult{}, err
	}
	res := SourceResult{Version: out.DistTags.Latest}
	if strings.HasPrefix(sw.Changelog, "github:") {
		res.ChangelogRaw = githubReleaseBody(strings.TrimPrefix(sw.Changelog, "github:"), res.Version, hc, githubBase)
	}
	return res, nil
}

func fetchGithub(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	if err := getJSON(hc, base+"/repos/"+locator+"/releases/latest", ghHeaders(), &out); err != nil {
		return SourceResult{}, err
	}
	return SourceResult{Version: strings.TrimPrefix(out.TagName, "v"), ChangelogRaw: out.Body}, nil
}

func githubReleaseBody(repo, version string, hc *http.Client, base string) string {
	for _, tag := range []string{"v" + version, version} {
		var out struct {
			Body string `json:"body"`
		}
		if err := getJSON(hc, base+"/repos/"+repo+"/releases/tags/"+tag, ghHeaders(), &out); err == nil {
			return out.Body
		}
	}
	return ""
}
```

（pypi/brew/custom 由 Task 5 補在 `more.go`；本任務先讓 npm/github 測試過。`FetchLatest` 引用的 `fetchPypi/fetchBrew/fetchCustom` 由 Task 5 提供——為了本任務能編譯，Task 5 之前先在 `sources.go` 末端加暫時樁：）

```go
// 暫時樁，Task 5 取代（放在 sources.go 末端）
func fetchPypi(sw inventory.Software, l string, hc *http.Client, base string) (SourceResult, error) {
	return SourceResult{}, fmt.Errorf("pypi not yet")
}
func fetchBrew(sw inventory.Software, l string, hc *http.Client, base string) (SourceResult, error) {
	return SourceResult{}, fmt.Errorf("brew not yet")
}
func fetchCustom(sw inventory.Software, l string) (SourceResult, error) {
	return SourceResult{}, fmt.Errorf("custom not yet")
}
```

- [ ] **Step 4: 跑測試確認通過** — `go test ./internal/sources/` → PASS（2 passed）。
- [ ] **Step 5: Commit** — `git add internal/sources/ && git commit -m "feat(go): sources framework + npm + github"`

---

### Task 5: sources — pypi/brew/claude-plugin/custom

**Files:** Create `internal/sources/more.go`, Modify `internal/sources/sources.go`（移除 Task 4 暫時樁）, Test `internal/sources/more_test.go`

- [ ] **Step 1: 寫失敗測試** — `internal/sources/more_test.go`:

```go
package sources

import (
	"net/http"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
)

func TestPypi(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"info":{"version":"1.4.2"}}`)) })
	defer s.Close()
	res, err := fetchPypi(inventory.Software{Name: "x", LatestSource: "pypi:p"}, "p", hc, s.URL)
	if err != nil || res.Version != "1.4.2" {
		t.Fatalf("pypi: %+v %v", res, err)
	}
}
func TestBrew(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"versions":{"stable":"3.2.1"}}`)) })
	defer s.Close()
	res, err := fetchBrew(inventory.Software{Name: "x", LatestSource: "brew:w"}, "w", hc, s.URL)
	if err != nil || res.Version != "3.2.1" {
		t.Fatalf("brew: %+v %v", res, err)
	}
}
func TestCustom(t *testing.T) {
	res, err := fetchCustom(inventory.Software{Name: "x", LatestSource: "custom:echo 9.9.9"}, "echo 9.9.9")
	if err != nil || res.Version != "9.9.9" {
		t.Fatalf("custom: %+v %v", res, err)
	}
	if _, err := fetchCustom(inventory.Software{}, "exit 7"); err == nil {
		t.Fatal("custom nonzero should error")
	}
}
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/sources/ -run 'Pypi|Brew|Custom'` → FAIL（樁回 error）。

- [ ] **Step 3: 移除 Task 4 暫時樁** — 刪掉 `sources.go` 末端那三個 `fetchPypi/fetchBrew/fetchCustom` 暫時樁。

- [ ] **Step 4: 實作 more.go** — `internal/sources/more.go`:

```go
package sources

import (
	"os/exec"
	"strings"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/version"
)

func fetchPypi(sw inventory.Software, locator string, hc interface{ Do(*reqAlias) (*respAlias, error) }, base string) (SourceResult, error) {
	return SourceResult{}, nil // placeholder replaced below
}
```

> 上面的 `more.go` 第一版只是讓你注意：**pypi/brew 的簽名要與 Task 4 暫時樁一致**（`*http.Client`）。請改用下面這份正式 `more.go`（覆蓋掉上面，勿保留 placeholder）：

```go
package sources

import (
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/version"
)

func fetchPypi(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := getJSON(hc, base+"/pypi/"+locator+"/json", nil, &out); err != nil {
		return SourceResult{}, err
	}
	res := SourceResult{Version: out.Info.Version}
	if strings.HasPrefix(sw.Changelog, "github:") {
		res.ChangelogRaw = githubReleaseBody(strings.TrimPrefix(sw.Changelog, "github:"), res.Version, hc, githubBase)
	}
	return res, nil
}

func fetchBrew(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
	}
	if err := getJSON(hc, base+"/api/formula/"+locator+".json", nil, &out); err != nil {
		return SourceResult{}, err
	}
	return SourceResult{Version: out.Versions.Stable}, nil
}

func fetchCustom(sw inventory.Software, locator string) (SourceResult, error) {
	ctx, cancel := contextTimeout(60 * time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "bash", "-lc", locator)
	b, err := c.CombinedOutput()
	if err != nil {
		return SourceResult{}, err
	}
	out := strings.TrimSpace(string(b))
	v := version.Parse(out, "")
	if v == "" {
		v = out
	}
	return SourceResult{Version: v}, nil
}
```

> 補一個小工具（避免直接 import context 與其它檔重複）——在 `more.go` 末端加：

```go
import "context"

func contextTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
```

> 注意 Go import 規則：把 `"context"` 併入 `more.go` 檔頂的 import 區（不要另起 import 區塊）。最終 `more.go` 的 import 應為：`context`, `net/http`, `os/exec`, `strings`, `time`, `inventory`, `version`。請以此整理，移除上面示意用的重複 import 行。

- [ ] **Step 5: 跑測試確認通過** — `go test ./internal/sources/` → PASS（npm/github/pypi/brew/custom 全過）。
- [ ] **Step 6: Commit** — `git add internal/sources/ && git commit -m "feat(go): pypi/brew/claude-plugin/custom sources"`

---

### Task 6: translate（claude -p）

**Files:** Create `internal/translate/translate.go`, Test `internal/translate/translate_test.go`

- [ ] **Step 1: 寫失敗測試**（用可注入的 runner，不呼叫真 claude）— `internal/translate/translate_test.go`:

```go
package translate

import "testing"

func TestTranslate(t *testing.T) {
	tr := &Translator{Run: func(prompt string) (string, error) { return "中文摘要", nil }}
	if out := tr.Changelog("## 1.0\n- fix {bug}"); out != "中文摘要" {
		t.Fatalf("got %q", out)
	}
	if tr.Changelog("") != "" || tr.Changelog("   ") != "" {
		t.Fatal("empty → empty")
	}
	boom := &Translator{Run: func(string) (string, error) { return "", errFake }}
	if boom.Changelog("notes") != "" {
		t.Fatal("error → empty")
	}
}

var errFake = errBoom{}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/translate/` → FAIL。

- [ ] **Step 3: 實作** — `internal/translate/translate.go`:

```go
package translate

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const promptTmpl = "你是技術翻譯。把以下軟體 changelog 整理成繁體中文重點摘要，用條列列出重要變更（新功能/修正/安全/破壞性變更），精簡不逐字翻。\n\n---\n%RAW%\n---"

type Translator struct {
	// Run 把整段 prompt 丟給翻譯引擎、回傳結果；預設用 claude -p。可注入測試。
	Run func(prompt string) (string, error)
}

func New() *Translator { return &Translator{Run: claudeRun} }

func claudeRun(prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "-p", prompt).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Changelog：空輸入/錯誤回 ""（best-effort，呼叫端降級保留原文）。
func (t *Translator) Changelog(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	prompt := strings.ReplaceAll(promptTmpl, "%RAW%", raw)
	out, err := t.Run(prompt)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
```

- [ ] **Step 4: 跑測試確認通過** — `go test ./internal/translate/` → PASS。
- [ ] **Step 5: Commit** — `git add internal/translate/ && git commit -m "feat(go): changelog translate via claude -p"`

---

### Task 7: executor（bash -lc 串流 + timeout + group-kill）

**Files:** Create `internal/executor/executor.go`, `internal/executor/kill_unix.go`, Test `internal/executor/executor_test.go`

> 直接沿用舊 `agent/internal/executor` 的設計（已驗證 group-kill），放進新 module。Unix 專屬的 `setpgid`/`SIGKILL` 放 build-tag 檔，Windows 之後 P2/P4 補。

- [ ] **Step 1: 寫失敗測試** — `internal/executor/executor_test.go`:

```go
package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStream(t *testing.T) {
	var lines []string
	res := Run(context.Background(), "echo hello && echo world", "", 10*time.Second, func(l string) { lines = append(lines, l) })
	if res.ExitCode != 0 || strings.Join(lines, ",") != "hello,world" {
		t.Fatalf("exit=%d lines=%v", res.ExitCode, lines)
	}
}
func TestNonzero(t *testing.T) {
	if Run(context.Background(), "exit 3", "", 10*time.Second, nil).ExitCode != 3 {
		t.Fatal("exit 3")
	}
}
func TestCancelKills(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	res := Run(ctx, "sleep 5", "", 10*time.Second, nil)
	if time.Since(start) > 3*time.Second || res.ExitCode == 0 {
		t.Fatalf("cancel slow/exit0: %v %d", time.Since(start), res.ExitCode)
	}
}
func TestTimeout(t *testing.T) {
	start := time.Now()
	res := Run(context.Background(), "sleep 5", "", 1*time.Second, nil)
	if time.Since(start) > 3*time.Second || res.ExitCode == 0 {
		t.Fatalf("timeout: %v %d", time.Since(start), res.ExitCode)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/executor/` → FAIL。

- [ ] **Step 3: 實作 executor.go**：

```go
package executor

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"time"
)

type Result struct{ ExitCode int }

func Run(ctx context.Context, cmd, cwd string, timeout time.Duration, onLine func(string)) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.Command("bash", "-lc", cmd)
	if cwd != "" {
		c.Dir = cwd
	}
	setPgid(c)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1}
	}
	c.Stderr = c.Stdout
	if err := c.Start(); err != nil {
		return Result{ExitCode: -1}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killGroup(c)
		case <-done:
		}
	}()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if onLine != nil {
			onLine(line)
		}
	}
	err = c.Wait()
	close(done)
	return Result{ExitCode: exitCode(err)}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
```

- [ ] **Step 4: 實作 kill_unix.go**（build tag）：

```go
//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

func setPgid(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(c *exec.Cmd) {
	if c.Process != nil {
		syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
```

> 註：`ee.ExitCode()` 對被 signal 殺死的程序回 -1，足夠表達「非零/失敗」。Windows 版 `setPgid/killGroup`（`//go:build windows`，用 taskkill/JobObject）留 P2/P4。

- [ ] **Step 5: 跑測試確認通過** — `go test ./internal/executor/` → PASS（cancel/timeout 各約 0.2s/1s）。
- [ ] **Step 6: Commit** — `git add internal/executor/ && git commit -m "feat(go): streaming executor with timeout + group kill (unix)"`

---

### Task 8: jobs（BuildUpdate + 佇列）

**Files:** Create `internal/jobs/jobs.go`, Test `internal/jobs/jobs_test.go`

- [ ] **Step 1: 寫失敗測試** — `internal/jobs/jobs_test.go`:

```go
package jobs

import (
	"path/filepath"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

func invCmd() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{"mac": {Name: "mac", Host: "x", SSHUser: "c", Local: true}},
		Software: []inventory.Software{{Name: "cc", Kind: "npm", LatestSource: "npm:cc",
			Installs: []inventory.Install{{Machine: "mac", CurrentCmd: "cc --version",
				Update: inventory.Update{Type: "command", Cmd: "npm i -g cc@latest"}}}}},
	}
}
func seed(t *testing.T) *store.Store {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	t.Cleanup(func() { s.Close() })
	s.AddVersion("cc", "2.1.101", "", "raw", "中文")
	s.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	return s
}

func TestBuildCommand(t *testing.T) {
	iv := invCmd()
	cmd, m, err := BuildUpdate(iv, iv.Software[0], iv.Software[0].Installs[0], "2.1.101", "2.1.98", "")
	if err != nil || cmd != "npm i -g cc@latest" || m.Name != "mac" {
		t.Fatalf("build cmd: %q %v %v", cmd, m, err)
	}
}
func TestBuildAgentCodex(t *testing.T) {
	up := inventory.Update{Type: "agent", Runner: "codex_exec", Cwd: "/srv/x", Prompt: "update to {latest_version}"}
	inst := inventory.Install{Machine: "mac", CurrentCmd: "x", Update: up}
	iv := inventory.Inventory{Machines: map[string]inventory.Machine{"mac": {Name: "mac"}}, Software: []inventory.Software{{Name: "x", Installs: []inventory.Install{inst}}}}
	cmd, _, err := BuildUpdate(iv, iv.Software[0], inst, "0.9.0", "0.8.0", "")
	if err != nil || !contains(cmd, "codex exec --cd ") || !contains(cmd, "update to 0.9.0") {
		t.Fatalf("agent cmd: %q %v", cmd, err)
	}
}
func TestClaimAndRecord(t *testing.T) {
	s := seed(t)
	iv := invCmd()
	jid, err := StartJob(s, iv, "cc", "mac")
	if err != nil || jid == 0 {
		t.Fatalf("start: %v %d", err, jid)
	}
	claimed, _ := ClaimNextJob(s, iv, "mac")
	if claimed == nil || claimed.ShellCmd != "npm i -g cc@latest" || claimed.CurrentCmd != "cc --version" {
		t.Fatalf("claim: %+v", claimed)
	}
	RecordResult(s, jid, "success", 0, "2.1.101")
	job, _ := s.GetJob(jid)
	inst, _ := s.GetInstall("cc", "mac")
	if job.Status != "success" || inst.CurrentVersion != "2.1.101" || inst.Status != "up_to_date" {
		t.Fatalf("record: job=%+v inst=%+v", job, inst)
	}
}
func TestRequestAbort(t *testing.T) {
	s := seed(t)
	iv := invCmd()
	jid, _ := StartJob(s, iv, "cc", "mac")        // queued
	job, _ := RequestAbort(s, jid)
	if job.Status != "aborted" {
		t.Fatalf("queued abort: %+v", job)
	}
	jid2, _ := StartJob(s, iv, "cc", "mac")
	ClaimNextJob(s, iv, "mac")
	job2, _ := RequestAbort(s, jid2)
	if job2.Status != "running" || !s.AbortRequested(jid2) {
		t.Fatalf("running abort: %+v", job2)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/jobs/` → FAIL。

- [ ] **Step 3: 實作** — `internal/jobs/jobs.go`:

```go
package jobs

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

var ErrActiveJobExists = errors.New("active job exists")

func find(inv inventory.Inventory, software, machine string) (inventory.Software, inventory.Install, error) {
	for _, sw := range inv.Software {
		if sw.Name == software {
			for _, inst := range sw.Installs {
				if inst.Machine == machine {
					return sw, inst, nil
				}
			}
		}
	}
	return inventory.Software{}, inventory.Install{}, fmt.Errorf("install not found: %s@%s", software, machine)
}

func render(tmpl string, vars map[string]string) string {
	out := tmpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

// shellQuote 單引號包裹（POSIX）：把 ' 換成 '\'' 。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func BuildUpdate(inv inventory.Inventory, sw inventory.Software, inst inventory.Install, latest, current, changelogZh string) (string, inventory.Machine, error) {
	up := inst.Update
	target := up.Machine
	if target == "" {
		target = inst.Machine
	}
	machine := inv.Machines[target]
	if up.Type == "command" {
		return up.Cmd, machine, nil
	}
	prompt := render(up.Prompt, map[string]string{
		"name": sw.Name, "machine": target, "current_version": current,
		"latest_version": latest, "changelog_zh": changelogZh, "cwd": up.Cwd,
	})
	switch up.Runner {
	case "codex_exec":
		cd := ""
		if up.Cwd != "" {
			cd = "--cd " + shellQuote(up.Cwd) + " "
		}
		return "codex exec " + cd + shellQuote(prompt), machine, nil
	case "claude_p":
		cd := ""
		if up.Cwd != "" {
			cd = "cd " + shellQuote(up.Cwd) + " && "
		}
		return cd + "claude -p " + shellQuote(prompt), machine, nil
	case "custom":
		cwdq := ""
		if up.Cwd != "" {
			cwdq = shellQuote(up.Cwd)
		}
		return render(up.Invoke, map[string]string{"prompt": shellQuote(prompt), "cwd": cwdq}), machine, nil
	default:
		return "", machine, fmt.Errorf("unknown runner: %s", up.Runner)
	}
}

func StartJob(s *store.Store, inv inventory.Inventory, software, machine string) (int64, error) {
	_, inst, err := find(inv, software, machine)
	if err != nil {
		return 0, err
	}
	jid, err := s.CreateJobUnique(software, machine, inst.Update.Type, inst.Update.Runner)
	if err != nil {
		return 0, err
	}
	if jid == 0 {
		return 0, ErrActiveJobExists
	}
	return jid, nil
}

type Claimed struct {
	ID                                   int64
	Software, Machine, ShellCmd, Cwd     string
	CurrentCmd, VersionRegex             string
}

func ClaimNextJob(s *store.Store, inv inventory.Inventory, machine string) (*Claimed, error) {
	row, err := s.ClaimOldestQueued(machine)
	if err != nil || row == nil {
		return nil, err
	}
	sw, inst, err := find(inv, row.Software, row.Machine)
	if err != nil {
		return nil, err
	}
	latest, _ := s.LatestVersion(sw.Name)
	cur, _ := s.GetInstall(sw.Name, inst.Machine)
	cmd, _, err := BuildUpdate(inv, sw, inst, latest.VersionStr, cur.CurrentVersion, latest.ChangelogZh)
	if err != nil {
		return nil, err
	}
	cwd := ""
	if inst.Update.Type == "agent" {
		cwd = inst.Update.Cwd
	}
	s.SetJobDispatch(row.ID, cmd, cwd, inst.CurrentCmd, inst.VersionRegex)
	return &Claimed{ID: row.ID, Software: sw.Name, Machine: inst.Machine, ShellCmd: cmd, Cwd: cwd, CurrentCmd: inst.CurrentCmd, VersionRegex: inst.VersionRegex}, nil
}

func RecordResult(s *store.Store, jobID int64, status string, exit int, newVersion string) error {
	job, err := s.GetJob(jobID)
	if err != nil {
		return err
	}
	if status == "success" && newVersion != "" {
		s.UpsertInstall(job.Software, job.Machine, newVersion, "up_to_date", time.Now().UTC().Format(time.RFC3339))
	}
	s.FinishJob(jobID, status, exit, newVersion)
	s.AddEvent("update", job.Software, job.Machine, fmt.Sprintf("job %d %s exit=%d new=%s", jobID, status, exit, newVersion))
	return nil
}

func RequestAbort(s *store.Store, jobID int64) (store.Job, error) {
	job, err := s.GetJob(jobID)
	if err != nil {
		return store.Job{}, err
	}
	switch job.Status {
	case "queued":
		s.FinishJob(jobID, "aborted", -1, "")
		s.AddEvent("update", job.Software, job.Machine, fmt.Sprintf("job %d aborted (queued)", jobID))
	case "running":
		s.RequestAbort(jobID)
	}
	return s.GetJob(jobID)
}
```

- [ ] **Step 4: 跑測試確認通過** — `go test ./internal/jobs/` → PASS。
- [ ] **Step 5: Commit** — `git add internal/jobs/ && git commit -m "feat(go): build_update + job queue (claim/record/abort)"`

---

### Task 9: collector（RefreshUpstream + ApplyVersionReport）

**Files:** Create `internal/collector/collector.go`, Test `internal/collector/collector_test.go`

- [ ] **Step 1: 寫失敗測試** — `internal/collector/collector_test.go`:

```go
package collector

import (
	"path/filepath"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/sources"
	"github.com/curtis1215/cockpit/internal/store"
)

func iv() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{"mac": {Name: "mac"}},
		Software: []inventory.Software{{Name: "cc", Kind: "npm", LatestSource: "npm:cc", Changelog: "github:o/cc",
			Installs: []inventory.Install{{Machine: "mac", CurrentCmd: "cc --version", Update: inventory.Update{Type: "command", Cmd: "x"}}}}},
	}
}

func TestRefreshUpstream(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	fetch := func(sw inventory.Software) (sources.SourceResult, error) {
		return sources.SourceResult{Version: "2.1.101", ChangelogRaw: "## notes"}, nil
	}
	tr := func(raw string) string { return "中文摘要" }
	RefreshUpstream(s, iv(), fetch, tr)
	if v, _ := s.GetVersion("cc", "2.1.101"); v.ChangelogZh != "中文摘要" {
		t.Fatalf("version: %+v", v)
	}
}

func TestApplyReport(t *testing.T) {
	s, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer s.Close()
	s.AddVersion("cc", "2.1.101", "", "raw", "中文")
	n := ApplyVersionReport(s, "mac", []Report{{Software: "cc", CurrentVersion: "2.1.98"}})
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	inst, _ := s.GetInstall("cc", "mac")
	if inst.CurrentVersion != "2.1.98" || inst.Status != "behind" {
		t.Fatalf("inst: %+v", inst)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/collector/` → FAIL。

- [ ] **Step 3: 實作** — `internal/collector/collector.go`:

```go
package collector

import (
	"fmt"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/sources"
	"github.com/curtis1215/cockpit/internal/store"
	"github.com/curtis1215/cockpit/internal/version"
)

type FetchFunc func(inventory.Software) (sources.SourceResult, error)
type TranslateFunc func(raw string) string
type Report struct {
	Software       string `json:"software"`
	CurrentVersion string `json:"current_version"`
}

func DefaultFetch(sw inventory.Software) (sources.SourceResult, error) {
	return sources.FetchLatest(sw, nil)
}

func RefreshUpstream(s *store.Store, inv inventory.Inventory, fetch FetchFunc, translate TranslateFunc) {
	for _, sw := range inv.Software {
		latest, err := fetch(sw)
		if err != nil {
			s.AddEvent("error", sw.Name, "", "fetch failed: "+err.Error())
			continue
		}
		zh := ""
		if existing, e := s.GetVersion(sw.Name, latest.Version); e == nil {
			zh = existing.ChangelogZh
		}
		if zh == "" && latest.ChangelogRaw != "" {
			zh = translate(latest.ChangelogRaw)
		}
		s.AddVersion(sw.Name, latest.Version, "", latest.ChangelogRaw, zh)
	}
}

func ApplyVersionReport(s *store.Store, machine string, reports []Report) int {
	now := time.Now().UTC().Format(time.RFC3339)
	applied := 0
	for _, r := range reports {
		if r.Software == "" {
			continue
		}
		latest, _ := s.LatestVersion(r.Software)
		status, _ := version.Compare(r.CurrentVersion, latest.VersionStr)
		s.UpsertInstall(r.Software, machine, r.CurrentVersion, status, now)
		s.AddEvent("check", r.Software, machine, fmt.Sprintf("current=%s latest=%s status=%s", r.CurrentVersion, latest.VersionStr, status))
		applied++
	}
	return applied
}
```

- [ ] **Step 4: 跑測試確認通過** — `go test ./internal/collector/` → PASS。
- [ ] **Step 5: Commit** — `git add internal/collector/ && git commit -m "feat(go): collector refresh_upstream + apply_version_report"`

---

### Task 10: server — 瀏覽器面版本 API + SSE

**Files:** Create `internal/server/version_api.go`, `internal/server/sse.go`, Modify `internal/server/server.go`（New 簽名加 inv + 註冊新路由）, Test `internal/server/version_api_test.go`

- [ ] **Step 1: 寫失敗測試** — `internal/server/version_api_test.go`:

```go
package server

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

func vtInv() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{"mac": {Name: "mac", AgentToken: "tok-mac"}},
		Software: []inventory.Software{{Name: "cc", Kind: "npm", LatestSource: "npm:cc",
			Installs: []inventory.Install{{Machine: "mac", CurrentCmd: "cc --version", Update: inventory.Update{Type: "command", Cmd: "x"}}}}},
	}
}
func vtServer(t *testing.T) (*Server, *store.Store) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	t.Cleanup(func() { st.Close() })
	st.AddVersion("cc", "2.1.101", "2026-04-10", "raw", "中文")
	st.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	return NewWithInventory(st, "s3cret", vtInv()), st
}

func TestInstallsEnriched(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/installs", nil))
	b := rec.Body.String()
	if !strings.Contains(b, `"id":"cc::mac"`) || !strings.Contains(b, `"behind_count":3`) ||
		!strings.Contains(b, `"update_kind":"command"`) || !strings.Contains(b, `"status":"behind"`) {
		t.Fatalf("installs: %s", b)
	}
}
func TestChangelog(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/changelog/cc/2.1.101", nil))
	if !strings.Contains(rec.Body.String(), `"changelog_zh":"中文"`) {
		t.Fatalf("changelog: %s", rec.Body.String())
	}
}
func TestTriggerAndConflict(t *testing.T) {
	srv, st := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/installs/cc/mac/update", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"job_id"`) {
		t.Fatalf("trigger: %d %s", rec.Code, rec.Body.String())
	}
	// 第二次 → 409（已有 active）
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("POST", "/api/installs/cc/mac/update", nil))
	if rec2.Code != 409 {
		t.Fatalf("conflict want 409 got %d", rec2.Code)
	}
	_ = st
}
func TestSSEEndsOnDone(t *testing.T) {
	srv, st := vtServer(t)
	jid, _ := st.CreateJobUnique("cc", "mac", "command", "")
	st.AppendJobLog(jid, "line A")
	st.FinishJob(jid, "success", 0, "2.1.101")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+itoa(jid)+"/log/stream", nil))
	b := rec.Body.String()
	if !strings.Contains(b, "line A") || !strings.Contains(b, "event: done") {
		t.Fatalf("sse: %s", b)
	}
}
func itoa(n int64) string { return strings.TrimSpace(sprintInt(n)) }
func sprintInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		d = append([]byte{'-'}, d...)
	}
	return string(d)
}
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/server/ -run 'Installs|Changelog|Trigger|SSE'` → FAIL（undefined: NewWithInventory）。

- [ ] **Step 3: 改 server.go — New 保留、加 NewWithInventory 與 inv 欄位**。修改 `internal/server/server.go`：

把 `Server` struct 加一欄 `inv inventory.Inventory`；新增 `NewWithInventory`，並讓既有 `New` 轉呼叫它（空 inventory）：

```go
// 在 import 加：
//   "github.com/curtis1215/cockpit/internal/inventory"

// Server struct 加欄位 inv inventory.Inventory（放在 enrollSecret 後）。

func New(st *store.Store, enrollSecret string) *Server {
	return NewWithInventory(st, enrollSecret, inventory.Inventory{})
}

func NewWithInventory(st *store.Store, enrollSecret string, inv inventory.Inventory) *Server {
	s := &Server{st: st, enrollSecret: enrollSecret, inv: inv, mux: http.NewServeMux()}
	s.routes()
	return s
}
```

並在 `routes()` 內、static mount **之前**加：
```go
	s.registerVersionAPI() // version_api.go
	s.registerSSE()        // sse.go
```

- [ ] **Step 4: 實作 version_api.go**：

```go
package server

import (
	"net/http"
	"strings"

	"github.com/curtis1215/cockpit/internal/jobs"
	"github.com/curtis1215/cockpit/internal/store"
	"github.com/curtis1215/cockpit/internal/version"
)

func (s *Server) registerVersionAPI() {
	s.mux.HandleFunc("/api/installs", s.handleInstalls)              // GET
	s.mux.HandleFunc("/api/changelog/", s.handleChangelog)          // GET /api/changelog/{sw}/{ver}
	s.mux.HandleFunc("/api/jobs", s.handleJobs)                     // GET ?limit=
	s.mux.HandleFunc("/api/installs/", s.handleInstallSub)          // POST /api/installs/{sw}/{m}/update
	s.mux.HandleFunc("/api/jobs/", s.handleJobSub)                  // GET /api/jobs/{id}; POST /api/jobs/{id}/abort（/log/stream 由 sse.go）
	s.mux.HandleFunc("/api/check", s.handleCheck)                   // POST
}

func (s *Server) handleInstalls(w http.ResponseWriter, r *http.Request) {
	latest := s.st.LatestVersionMap()
	kindOf := map[string]string{}
	updKind := map[string]string{}
	for _, sw := range s.inv.Software {
		kindOf[sw.Name] = sw.Kind
		for _, ins := range sw.Installs {
			updKind[sw.Name+"::"+ins.Machine] = ins.Update.Type
		}
	}
	rows, _ := s.st.ListInstalls()
	out := []map[string]any{}
	for _, in := range rows {
		lv := latest[in.Software]
		liveStatus, behind := version.Compare(in.CurrentVersion, lv)
		status := liveStatus
		if in.Status == "error" {
			status = "error"
		}
		var errMsg any
		if status == "error" {
			errMsg = s.st.LastError(in.Software, in.Machine)
		}
		out = append(out, map[string]any{
			"id": in.Software + "::" + in.Machine, "software": in.Software, "machine": in.Machine,
			"kind": kindOf[in.Software], "current_version": in.CurrentVersion, "latest_version": nilIfEmpty(lv),
			"status": status, "behind_count": behind, "update_kind": updKind[in.Software+"::"+in.Machine],
			"error": errMsg, "last_checked": in.LastChecked,
		})
	}
	writeJSON(w, 200, out)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Server) handleChangelog(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/changelog/"), "/")
	if len(parts) != 2 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	v, err := s.st.GetVersion(parts[0], parts[1])
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "version not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"software": parts[0], "version": parts[1],
		"changelog_zh": v.ChangelogZh, "changelog_raw": v.ChangelogRaw, "released_at": v.ReleasedAt})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	list, _ := s.st.ListJobs(limit)
	out := []map[string]any{}
	for _, j := range list {
		out = append(out, jobMap(j))
	}
	writeJSON(w, 200, out)
}

func jobMap(j store.Job) map[string]any {
	return map[string]any{"id": j.ID, "software": j.Software, "machine": j.Machine, "kind": j.Kind,
		"runner": j.Runner, "status": j.Status, "started_at": j.StartedAt, "finished_at": j.FinishedAt,
		"exit_code": j.ExitCode, "new_version": j.NewVersion, "log": j.Log, "cmd": j.Cmd}
}

func (s *Server) handleInstallSub(w http.ResponseWriter, r *http.Request) {
	// POST /api/installs/{sw}/{m}/update
	rest := strings.TrimPrefix(r.URL.Path, "/api/installs/")
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[2] != "update" || r.Method != http.MethodPost {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	jid, err := jobs.StartJob(s.st, s.inv, parts[0], parts[1])
	if err == jobs.ErrActiveJobExists {
		writeJSON(w, 409, map[string]string{"error": "update already in progress"})
		return
	}
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "install not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"job_id": jid})
}

func (s *Server) handleJobSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.Split(rest, "/")
	id := parseInt64(parts[0])
	if len(parts) == 1 { // GET /api/jobs/{id}
		j, err := s.st.GetJob(id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "job not found"})
			return
		}
		writeJSON(w, 200, jobMap(j))
		return
	}
	if len(parts) == 2 && parts[1] == "abort" && r.Method == http.MethodPost {
		job, err := jobs.RequestAbort(s.st, id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "job not found"})
			return
		}
		writeJSON(w, 200, jobMap(job))
		return
	}
	// /log/stream 由 sse.go 的獨立 handler 處理（不同前綴註冊）
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	// 觸發背景刷新 + 對各機設 check 旗標。RefreshUpstream 由 serve 端注入（避免 server 直接相依 collector）。
	if s.onCheck != nil {
		go s.onCheck()
	}
	for name := range s.inv.Machines {
		s.st.SetCheckRequested(name)
	}
	writeJSON(w, 200, map[string]bool{"started": true})
}

func parseInt64(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
```

> `s.onCheck func()` 是一個可選回呼（serve 端注入 `RefreshUpstream`）。在 `Server` struct 加欄位 `onCheck func()` 與一個 setter：
```go
// server.go 加：
func (s *Server) OnCheck(f func()) { s.onCheck = f }
```

- [ ] **Step 5: 實作 sse.go**：

```go
package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) registerSSE() {
	// 用更長前綴搶在 handleJobSub 之前：/api/jobs/{id}/log/stream
	// 注意：Go ServeMux 以最長 pattern 匹配，這個前綴比 "/api/jobs/" 更長，會優先。
	s.mux.HandleFunc("/api/jobs/", s.dispatchJobsWithSSE)
}

// 因為 "/api/jobs/" 只能註冊一次，這裡統一分派：log/stream 走 SSE，其餘交給 handleJobSub。
func (s *Server) dispatchJobsWithSSE(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/log/stream") {
		s.streamLog(w, r)
		return
	}
	s.handleJobSub(w, r)
}

func (s *Server) streamLog(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	id := parseInt64(strings.TrimSuffix(rest, "/log/stream"))
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	sent := 0
	for {
		job, err := s.st.GetJob(id)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: job not found\n\n")
			return
		}
		log := job.Log
		var ready []string
		if strings.HasSuffix(log, "\n") {
			ready = strings.Split(strings.TrimSuffix(log, "\n"), "\n")
			if log == "" {
				ready = nil
			}
		} else if log != "" {
			ready = strings.Split(log, "\n")
		}
		for ; sent < len(ready); sent++ {
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", ready[sent])
		}
		if job.Status == "success" || job.Status == "failed" || job.Status == "aborted" {
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", job.Status)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		// 測試環境（httptest + 已完成 job）首輪即 done 結束；運行時每 0.5s 輪詢
		time.Sleep(500 * time.Millisecond)
	}
}
```

> ⚠️ 路由衝突修正：`version_api.go` 的 `registerVersionAPI` 也註冊了 `"/api/jobs/"`。**兩處不能都註冊同一 pattern**（panic）。解法：把 `version_api.go` 裡的 `s.mux.HandleFunc("/api/jobs/", s.handleJobSub)` 這行**刪掉**，改由 `sse.go` 的 `registerSSE` 統一註冊 `"/api/jobs/"` → `dispatchJobsWithSSE`（它再分派到 streamLog 或 handleJobSub）。請在 Step 4 完成後、Step 5 時一併把 version_api.go 的 `/api/jobs/` 註冊行移除。

- [ ] **Step 6: 跑測試確認通過** — `go test ./internal/server/` → PASS（P0 既有 + 新版本 API/SSE）。
- [ ] **Step 7: Commit** — `git add internal/server/ && git commit -m "feat(go): browser version api (installs/changelog/jobs/update/abort/check) + sse"`

---

### Task 11: server — agent 面版本 API（installs/poll/report-versions/log/result/control）

**Files:** Create `internal/server/agent_vt_api.go`, Modify `internal/server/server.go`（routes 加 `s.registerAgentVT()`）, Test `internal/server/agent_vt_test.go`

> 沿用 P0 的 `s.bearer(r)`（解析 token→system）——但版本 API 的 token→machine 解析要走 **inventory 的 agent_token**（每機 token）。本任務的 agent 認證用 `inventory.MachineForToken(s.inv, token)`，與 P0 的 systems agent_token 並存（P0 是 enroll 後的 system token；版本追蹤用 inventory 的 agent_token）。後續 P3 收斂；本階段以 inventory token 為版本 API 認證。

- [ ] **Step 1: 寫失敗測試** — `internal/server/agent_vt_test.go`:

```go
package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func auth(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func TestAgentVTInstallsAndPoll(t *testing.T) {
	srv, st := vtServer(t) // inventory mac agent_token=tok-mac, install cc
	// installs
	r := httptest.NewRequest("GET", "/api/agent/installs", nil)
	r.Header.Set("Authorization", "Bearer tok-mac")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"current_cmd":"cc --version"`) {
		t.Fatalf("installs: %d %s", rec.Code, rec.Body.String())
	}
	// 無 token → 401
	bad := httptest.NewRecorder()
	srv.Handler().ServeHTTP(bad, httptest.NewRequest("GET", "/api/agent/installs", nil))
	if bad.Code != 401 {
		t.Fatalf("noauth want 401 got %d", bad.Code)
	}
	// 建 queued job → poll 取得渲染指令
	st.CreateJobUnique("cc", "mac", "command", "")
	pr := httptest.NewRequest("GET", "/api/agent/poll?wait=0", nil)
	pr.Header.Set("Authorization", "Bearer tok-mac")
	prec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prec, pr)
	if !strings.Contains(prec.Body.String(), `"type":"job"`) || !strings.Contains(prec.Body.String(), `"shell_cmd":"x"`) {
		t.Fatalf("poll: %s", prec.Body.String())
	}
}

func TestAgentVTReportAndResult(t *testing.T) {
	srv, st := vtServer(t)
	// report-versions
	rr := httptest.NewRequest("POST", "/api/agent/report-versions", strings.NewReader(`[{"software":"cc","current_version":"2.1.98"}]`))
	rr.Header.Set("Authorization", "Bearer tok-mac")
	rrec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrec, rr)
	if rrec.Code != 200 {
		t.Fatalf("report: %d", rrec.Code)
	}
	// 建+claim job，post log/result
	jid, _ := st.CreateJobUnique("cc", "mac", "command", "")
	st.ClaimOldestQueued("mac")
	lr := httptest.NewRequest("POST", "/api/agent/jobs/"+itoa(jid)+"/log", strings.NewReader(`{"lines":["added 1 package"]}`))
	lr.Header.Set("Authorization", "Bearer tok-mac")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), lr)
	res := httptest.NewRequest("POST", "/api/agent/jobs/"+itoa(jid)+"/result", strings.NewReader(`{"status":"success","exit_code":0,"new_version":"2.1.101"}`))
	res.Header.Set("Authorization", "Bearer tok-mac")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), res)
	job, _ := st.GetJob(jid)
	if job.Status != "success" || !strings.Contains(job.Log, "added 1 package") {
		t.Fatalf("job: %+v", job)
	}
}
```

- [ ] **Step 2: 跑測試確認失敗** — `go test ./internal/server/ -run AgentVT` → FAIL（404）。

- [ ] **Step 3: 實作 agent_vt_api.go**：

```go
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/collector"
	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/jobs"
)

func (s *Server) registerAgentVT() {
	s.mux.HandleFunc("/api/agent/installs", s.vtInstalls)
	s.mux.HandleFunc("/api/agent/poll", s.vtPoll)
	s.mux.HandleFunc("/api/agent/report-versions", s.vtReportVersions)
	// /api/agent/jobs/{id}/{log|result|control} 由單一前綴分派
	s.mux.HandleFunc("/api/agent/jobs/", s.vtJobSub)
}

func (s *Server) vtMachine(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	m := inventory.MachineForToken(s.inv, strings.TrimSpace(h[len("Bearer "):]))
	return m, m != ""
}

func (s *Server) vtInstalls(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.vtMachine(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	out := []map[string]any{}
	for _, sw := range s.inv.Software {
		for _, ins := range sw.Installs {
			if ins.Machine == machine {
				out = append(out, map[string]any{"software": sw.Name, "current_cmd": ins.CurrentCmd, "version_regex": nilIfEmpty(ins.VersionRegex)})
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) vtPoll(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.vtMachine(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	waitSec := 0.0
	if v := r.URL.Query().Get("wait"); v != "" {
		// 簡化：只支援整數秒
		for _, c := range v {
			if c >= '0' && c <= '9' {
				waitSec = waitSec*10 + float64(c-'0')
			}
		}
	}
	if waitSec > 25 {
		waitSec = 25
	}
	waited := 0.0
	for {
		claimed, _ := jobs.ClaimNextJob(s.st, s.inv, machine)
		if claimed != nil {
			writeJSON(w, 200, map[string]any{"type": "job", "job": map[string]any{
				"id": claimed.ID, "software": claimed.Software, "machine": claimed.Machine,
				"shell_cmd": claimed.ShellCmd, "cwd": claimed.Cwd, "current_cmd": claimed.CurrentCmd,
				"version_regex": nilIfEmpty(claimed.VersionRegex)}})
			return
		}
		if s.st.TakeCheckRequested(machine) {
			writeJSON(w, 200, map[string]string{"type": "check"})
			return
		}
		if waited >= waitSec {
			w.WriteHeader(204)
			return
		}
		time.Sleep(500 * time.Millisecond)
		waited += 0.5
	}
}

func (s *Server) vtReportVersions(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.vtMachine(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var reports []collector.Report
	json.NewDecoder(r.Body).Decode(&reports)
	n := collector.ApplyVersionReport(s.st, machine, reports)
	writeJSON(w, 200, map[string]int{"applied": n})
}

func (s *Server) vtJobSub(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.vtMachine(r); !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/agent/jobs/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	id := parseInt64(parts[0])
	switch parts[1] {
	case "log":
		var body struct {
			Lines []string `json:"lines"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		for _, line := range body.Lines {
			s.st.AppendJobLog(id, line)
		}
		w.WriteHeader(204)
	case "result":
		var body struct {
			Status     string `json:"status"`
			ExitCode   int    `json:"exit_code"`
			NewVersion string `json:"new_version"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		jobs.RecordResult(s.st, id, body.Status, body.ExitCode, body.NewVersion)
		j, _ := s.st.GetJob(id)
		writeJSON(w, 200, jobMap(j))
	case "control":
		writeJSON(w, 200, map[string]bool{"abort": s.st.AbortRequested(id)})
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}
```

- [ ] **Step 4: routes 註冊** — 在 `server.go` 的 `routes()`、static mount 之前加 `s.registerAgentVT()`（在 P0 的 `s.registerAgentAPI()` 之後）。

> ⚠️ 路由前綴衝突：P0 的 `registerAgentAPI` 註冊 `/api/agent/enroll` 與 `/api/agent/heartbeat`（精確路徑），本任務註冊 `/api/agent/installs`、`/api/agent/poll`、`/api/agent/report-versions`（精確）與 `/api/agent/jobs/`（前綴）。彼此不衝突（不同精確路徑 + 一個 jobs 前綴）。確認沒有重複註冊同一字串即可。

- [ ] **Step 5: 跑測試確認通過** — `go test ./internal/server/` → PASS。
- [ ] **Step 6: Commit** — `git add internal/server/ && git commit -m "feat(go): agent version-tracking api (installs/poll/report/log/result/control)"`

---

### Task 12: agent runtime（版本讀取 + job 執行）+ serve 接線 + 整合

**Files:** Modify `internal/agent/agent.go`（加版本讀取 + job 執行迴圈）, Modify `internal/config/config.go`（serve 加 InventoryPath/CheckHours）, Modify `cmd/cockpit/serve.go`（載 inventory + 注入 OnCheck + 排程 RefreshUpstream）, Test `internal/agent/vt_test.go`

- [ ] **Step 1: config 加欄位** — 在 `internal/config/config.go` 的 `ServeConfig` 加：
```go
	InventoryPath string `json:"inventory_path"`
	CheckHours    int    `json:"check_hours"`
```
（`LoadServe` 不需預設這兩個；空字串表示無 inventory→版本功能停用。）

- [ ] **Step 2: 寫失敗測試**（agent 版本流程對 httptest）— `internal/agent/vt_test.go`:

```go
package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReportVersionsOnce(t *testing.T) {
	var reported int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{{"software": "cc", "current_cmd": "echo cc 2.1.98", "version_regex": nil}})
		case "/api/agent/report-versions":
			atomic.AddInt32(&reported, 1)
			w.Write([]byte(`{"applied":1}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.ReportVersions(10 * time.Second)
	if atomic.LoadInt32(&reported) != 1 {
		t.Fatalf("reported=%d", reported)
	}
}

func TestRunJobOnce(t *testing.T) {
	var result map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{})
		case endsWith(r.URL.Path, "/log"):
			w.WriteHeader(204)
		case endsWith(r.URL.Path, "/control"):
			w.Write([]byte(`{"abort":false}`))
		case endsWith(r.URL.Path, "/result"):
			json.NewDecoder(r.Body).Decode(&result)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.RunJob(Job{ID: 7, ShellCmd: "echo added", CurrentCmd: "echo cc 2.1.101"}, 2*time.Second, 10*time.Second)
	if result["status"] != "success" || result["new_version"] != "2.1.101" {
		t.Fatalf("result=%v", result)
	}
}

func endsWith(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }
```

- [ ] **Step 3: 跑測試確認失敗** — `go test ./internal/agent/ -run 'ReportVersions|RunJob'` → FAIL。

- [ ] **Step 4: 實作 agent 版本/ job 邏輯** — 在 `internal/agent/agent.go` 追加（沿用既有 `c()` httpx client；新增用到 `executor`、`version`、`collector` types）：

```go
// 檔頂 import 補：
//   "context"
//   "fmt"
//   "github.com/curtis1215/cockpit/internal/executor"
//   "github.com/curtis1215/cockpit/internal/version"

type installDef struct {
	Software     string `json:"software"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}
type Job struct {
	ID           int64  `json:"id"`
	Software     string `json:"software"`
	Machine      string `json:"machine"`
	ShellCmd     string `json:"shell_cmd"`
	Cwd          string `json:"cwd"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}

func (a *Agent) ReportVersions(execTimeout time.Duration) {
	var defs []installDef
	if _, err := a.c().GetJSON("/api/agent/installs", a.Token, &defs); err != nil {
		return
	}
	var reports []map[string]string
	for _, d := range defs {
		cur := ""
		executor.Run(context.Background(), d.CurrentCmd, "", execTimeout, func(l string) {
			if v := version.Parse(l, d.VersionRegex); v != "" && cur == "" {
				cur = v
			}
		})
		reports = append(reports, map[string]string{"software": d.Software, "current_version": cur})
	}
	if len(reports) > 0 {
		a.c().PostJSON("/api/agent/report-versions", a.Token, reports, nil)
	}
}

func (a *Agent) RunJob(job Job, controlInterval, execTimeout time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aborted := make(chan struct{})
	go func() {
		tk := time.NewTicker(controlInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				var ctrl struct {
					Abort bool `json:"abort"`
				}
				if _, err := a.c().GetJSON(fmt.Sprintf("/api/agent/jobs/%d/control", job.ID), a.Token, &ctrl); err == nil && ctrl.Abort {
					close(aborted)
					cancel()
					return
				}
			}
		}
	}()
	post := func(line string) {
		a.c().PostJSON(fmt.Sprintf("/api/agent/jobs/%d/log", job.ID), a.Token, map[string]any{"lines": []string{line}}, nil)
	}
	res := executor.Run(ctx, job.ShellCmd, job.Cwd, execTimeout, post)
	select {
	case <-aborted:
		post("■ 已由使用者中止")
		a.report(job.ID, "aborted", res.ExitCode, "")
		return
	default:
	}
	if res.ExitCode != 0 {
		a.report(job.ID, "failed", res.ExitCode, "")
		return
	}
	newVer := ""
	executor.Run(context.Background(), job.CurrentCmd, job.Cwd, execTimeout, func(l string) {
		if v := version.Parse(l, job.VersionRegex); v != "" && newVer == "" {
			newVer = v
		}
	})
	a.report(job.ID, "success", res.ExitCode, newVer)
}

func (a *Agent) report(jobID int64, status string, exit int, newVersion string) {
	a.c().PostJSON(fmt.Sprintf("/api/agent/jobs/%d/result", jobID), a.Token,
		map[string]any{"status": status, "exit_code": exit, "new_version": newVersion}, nil)
}
```

> 註：`httpx.Client.GetJSON` 目前不存在（P0 只有 PostJSON）。在 `internal/httpx/httpx.go` 補 `GetJSON(path, bearer string, out any) (int, error)`：

```go
func (c *Client) GetJSON(path, bearer string, out any) (int, error) {
	req, err := http.NewRequest("GET", c.base+path, nil)
	if err != nil {
		return 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return 204, nil
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("http %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
```
（`httpx.go` 檔頂 import 補 `"encoding/json"`、`"fmt"` 若缺。）

- [ ] **Step 5: 跑測試確認通過** — `go test ./internal/agent/ ./internal/httpx/` → PASS。

- [ ] **Step 6: serve 接線** — 修改 `cmd/cockpit/serve.go`：載 inventory（若 `InventoryPath` 非空）、用 `server.NewWithInventory`、注入 `OnCheck`（背景 RefreshUpstream）、起一個簡單排程 goroutine 每 `CheckHours` 跑 RefreshUpstream。完整 `runServe`：

```go
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/curtis1215/cockpit/internal/collector"
	"github.com/curtis1215/cockpit/internal/config"
	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/server"
	"github.com/curtis1215/cockpit/internal/store"
	"github.com/curtis1215/cockpit/internal/translate"
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

	inv := inventory.Inventory{}
	if cfg.InventoryPath != "" {
		inv, err = inventory.Load(cfg.InventoryPath)
		if err != nil {
			log.Fatalf("inventory: %v", err)
		}
	}
	srv := server.NewWithInventory(st, cfg.EnrollSecret, inv)

	tr := translate.New()
	refresh := func() { collector.RefreshUpstream(st, inv, collector.DefaultFetch, tr.Changelog) }
	srv.OnCheck(refresh)
	if len(inv.Software) > 0 {
		hours := cfg.CheckHours
		if hours <= 0 {
			hours = 24
		}
		go func() {
			for {
				refresh()
				time.Sleep(time.Duration(hours) * time.Hour)
			}
		}()
	}

	log.Printf("cockpit serve on http://%s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 7: 跑全部測試 + build** — `cd /Users/curtis/Dev/cockpit && go test ./... && go vet ./... && go build -o /tmp/cockpit ./cmd/cockpit`
Expected: 全綠、vet clean、build OK。

- [ ] **Step 8: Commit** — `git add internal/agent/ internal/httpx/ internal/config/ cmd/cockpit/serve.go && git commit -m "feat(go): agent version-read + job-run runtime; serve loads inventory + collector schedule"`

---

## Self-Review（已執行）

**1. Spec coverage（P1 backend）：** version_parse→T1；inventory(+agent_token)→T2；store versions/installs/jobs→T3；版本來源 npm/github→T4，pypi/brew/claude-plugin/custom→T5；translate→T6；executor→T7；build_update + jobs 佇列(claim/record/abort)→T8；collector(refresh+report)→T9；瀏覽器面 installs/changelog/jobs/update/abort/check + SSE→T10；agent 面 installs/poll/report/log/result/control→T11；agent runtime + serve 接線 + 整合→T12。**前端版本頁接線不在本計畫（P1-frontend 另立）。**

**2. Placeholder scan：** 無 TODO/TBD。過渡墊片二處明確標示並於後續任務移除：T4 的 pypi/brew/custom 樁（T5 Step 3 移除）；T10 的 `/api/jobs/` 註冊衝突（明示 sse.go 統一註冊、移除 version_api 的該行）。T5 Step 4 的「示意用 placeholder more.go」已明確要求用正式版覆蓋。

**3. Type consistency：** `store.Job/Install/Version`、`inventory.{Machine,Software,Install,Update,Inventory}`、`sources.SourceResult/FetchLatest/fetch*`、`version.Parse/Compare`、`jobs.{BuildUpdate,StartJob,ClaimNextJob,Claimed,RecordResult,RequestAbort,ErrActiveJobExists}`、`collector.{RefreshUpstream,ApplyVersionReport,Report,FetchFunc,TranslateFunc,DefaultFetch}`、`translate.Translator`、`executor.Run/Result`、`server.{NewWithInventory,OnCheck,registerVersionAPI,registerSSE,registerAgentVT}`、`agent.{ReportVersions,RunJob,Job}`、`httpx.GetJSON` 跨任務一致。`/api/jobs/` 與 `/api/agent/jobs/` 各由單一前綴 handler 分派、無重複註冊。

**4. 已知邊界（後續）：** Windows executor（P2/P4 build-tag）、SSE 首行/空 log 邊界（T10 的 ready 切片已處理 log=="" 與無尾換行）、inventory token 與 P0 systems token 的收斂（P3）、前端接線（P1-frontend）。
