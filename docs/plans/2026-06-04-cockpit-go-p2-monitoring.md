# Cockpit P2 (backend) — 原生監控 + 拓樸資料 + VM 列舉 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** agent 用 gopsutil 收集機器指標（cpu/mem/disk/net/load/temp/uptime/GPU）與 Docker 服務、host agent 列舉 VMware Fusion VM；server 收存（metrics_latest + 1m 時序）、背景降採樣到 30 天（仿 Beszel ~數百筆/機）、提供拓樸/機器頁所需 API（systems enriched、metrics range、services、vms 對帳）。

**Architecture:** 延續既有 module。agent 新增 `internal/collect`（指標）與 `internal/dockerstat`（容器）與 `internal/vmenum`（hypervisor 列舉，首發 VMware Fusion）；server 新增 `report-metrics/report-services/report-vms` 端點與**統一 agent 識別 resolver**（systems token 或 inventory token 皆可；inventory token 自動 find-or-create systems 列，label=machine 名）；store 新增 metrics/metrics_latest/services/vms 表；`internal/downsample` 做聚合+清理。**前端接線另立 P2-frontend plan。**

**Tech Stack:** `github.com/shirou/gopsutil/v4`（cpu/mem/disk/net/load/host/sensors）；docker 用 CLI（`docker ps`/`docker stats --no-stream`，injectable runner、無 SDK 依賴）；`nvidia-smi`（有才用）；`vmrun` + `.vmx` 解析（injectable）。

設計依據：`docs/specs/2026-06-03-cockpit-unified-go-design.md` §5/§6/§9/§9.1/§10。

---

## 慣例（跨任務一致）

- **MetricsReport**（agent→server JSON）：`{ts(unix sec), cpu, mem, disk, gpu(nullable), net_up, net_down(MB/s), load, temp(nullable), uptime(sec)}`，數值 float64、nullable 用 `*float64`。
- **metrics.type** ∈ `1m|10m|15m|60m|480m`；range 對應：`1h→1m, 12h→10m, 24h→15m, 7d→60m, 30d→480m`。
- **保留期**：1m=2h、10m=14h、15m=26h、60m=8d、480m=32d（聚合來源：10m/15m←1m、60m←15m、480m←60m）。
- **status 判定**（讀取時計算）：`offline` last_seen>60s；`warn` cpu>90∥mem>90∥disk>90∥temp>85；否則 `online`（pending 留 P3）。
- **services**：每次回報整批取代該 system 的列。kind ∈ docker|service|daemon|proxy|db|plugin|runtime|bundle。
- **統一 resolver** `agentSystem(r) (systemID string, ok bool)`：先試 P0 systems token（SystemByAgentToken）；失敗試 inventory token（MachineForToken）→ `EnsureSystemForMachine(machineName)`（label=machine 名 find-or-create，os/arch 空、kind=physical）。版本 VT 端點維持原 inventory-token 認證不動。
- store 新函式：`EnsureSystemForMachine`、`UpsertMetricsLatest`（含 spark 維護：append cpu、cap 24）、`InsertMetric`、`QueryMetrics`、`Downsample`、`PruneMetrics`、`ReplaceServices`、`ListServices`、`ListServicesBySystem`、`ReplaceVMs`、`ListVMs`、`LinkVM`、`SystemsWithLatest`。

## 檔案結構

```
internal/
  collect/collect.go         # gopsutil 指標收集（Collector，net rate 需前次狀態）
  collect/gpu.go             # nvidia-smi GPU（injectable exec）
  dockerstat/dockerstat.go   # docker ps + stats 解析（injectable runner）
  vmenum/vmware.go           # VMware Fusion：vmrun list + .vmx 掃描/解析（injectable）
  store/schema.sql           # +metrics/metrics_latest/services/vms
  store/monitor.go           # 監控相關 CRUD + Downsample/Prune（新檔，store package）
  server/monitor_api.go      # agent: report-metrics/services/vms + resolver；browser: systems enriched/metrics/services/vms
  agent/monitor.go           # agent 監控迴圈（15s metrics、60s services、5m vms）
cmd/cockpit/serve.go         # 起降採樣排程 goroutine
```

---

### Task 1: store — 監控表 + CRUD

**Files:** Modify `internal/store/schema.sql`; Create `internal/store/monitor.go`; Test `internal/store/monitor_test.go`

- [ ] **Step 1: 失敗測試** — `internal/store/monitor_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func mOpen(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func f(v float64) *float64 { return &v }

func TestEnsureSystemForMachine(t *testing.T) {
	s := mOpen(t)
	id1, err := s.EnsureSystemForMachine("mac")
	if err != nil || id1 == "" {
		t.Fatalf("ensure: %v %q", err, id1)
	}
	id2, _ := s.EnsureSystemForMachine("mac") // 第二次回同一筆
	if id2 != id1 {
		t.Fatalf("idempotent: %q vs %q", id1, id2)
	}
	list, _ := s.ListSystems()
	if len(list) != 1 || list[0].Label != "mac" {
		t.Fatalf("systems: %+v", list)
	}
}

func TestMetricsLatestAndSpark(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	for i := 0; i < 30; i++ {
		s.UpsertMetricsLatest(id, MetricRow{TS: int64(1000 + i), CPU: f(float64(i)), Mem: f(50), Disk: f(60), NetUp: f(1), NetDown: f(2), Load: f(0.5), Uptime: f(99)})
	}
	rows, _ := s.SystemsWithLatest()
	if len(rows) != 1 || *rows[0].Latest.CPU != 29 {
		t.Fatalf("latest: %+v", rows)
	}
	if n := len(rows[0].Spark); n != 24 { // cap 24
		t.Fatalf("spark len: %d", n)
	}
	if rows[0].Spark[23] != 29 {
		t.Fatalf("spark tail: %v", rows[0].Spark)
	}
}

func TestMetricsInsertQuery(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	for i := 0; i < 5; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	pts, _ := s.QueryMetrics(id, "1m", 0)
	if len(pts) != 5 || *pts[4].CPU != 14 {
		t.Fatalf("query: %d %+v", len(pts), pts)
	}
	pts2, _ := s.QueryMetrics(id, "1m", 120) // since
	if len(pts2) != 3 {
		t.Fatalf("since: %d", len(pts2))
	}
}

func TestServicesReplace(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	s.ReplaceServices(id, []ServiceRow{{Name: "redis", Kind: "docker", Status: "running", CPU: f(1), Mem: f(2), Port: 6379}})
	s.ReplaceServices(id, []ServiceRow{{Name: "caddy", Kind: "docker", Status: "running"}})
	rows, _ := s.ListServices()
	if len(rows) != 1 || rows[0].Name != "caddy" {
		t.Fatalf("replace: %+v", rows)
	}
}

func TestVMsReplaceAndLink(t *testing.T) {
	s := mOpen(t)
	host, _ := s.EnsureSystemForMachine("minihost")
	s.ReplaceVMs(host, []VMRow{{Name: "ubuntu-vm", UUID: "u-1", VmxPath: "/x.vmx", State: "running", VCPU: 4, RamMB: 4096, GuestOS: "ubuntu"}})
	vms, _ := s.ListVMs()
	if len(vms) != 1 || vms[0].HostSystemID != host {
		t.Fatalf("vms: %+v", vms)
	}
	guest, _ := s.EnsureSystemForMachine("ubuntu-vm")
	s.LinkVM(host, "u-1", guest)
	vms2, _ := s.ListVMs()
	if vms2[0].LinkedSystemID != guest {
		t.Fatalf("link: %+v", vms2)
	}
	sys, _ := s.ListSystems()
	for _, x := range sys {
		if x.ID == guest && (x.Kind != "vm" || x.HostID != host) {
			t.Fatalf("guest system not linked: %+v", x)
		}
	}
}
```

- [ ] **Step 2:** `go test ./internal/store/ -run 'Ensure|MetricsLatest|MetricsInsert|ServicesReplace|VMs'` → FAIL。

- [ ] **Step 3: schema.sql 追加：**

```sql
CREATE TABLE IF NOT EXISTS metrics (
  system_id TEXT NOT NULL, type TEXT NOT NULL, ts INTEGER NOT NULL,
  cpu REAL, mem REAL, disk REAL, gpu REAL, net_up REAL, net_down REAL, load REAL, temp REAL,
  PRIMARY KEY (system_id, type, ts)
);
CREATE TABLE IF NOT EXISTS metrics_latest (
  system_id TEXT PRIMARY KEY, ts INTEGER NOT NULL,
  cpu REAL, mem REAL, disk REAL, gpu REAL, net_up REAL, net_down REAL, load REAL, temp REAL, uptime REAL,
  spark TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS services (
  id INTEGER PRIMARY KEY AUTOINCREMENT, system_id TEXT NOT NULL,
  name TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'running',
  cpu REAL, mem REAL, port INTEGER, software_ids TEXT, depends TEXT
);
CREATE TABLE IF NOT EXISTS vms (
  host_system_id TEXT NOT NULL, name TEXT NOT NULL, uuid TEXT NOT NULL,
  vmx_path TEXT, state TEXT NOT NULL, vcpu INTEGER, ram_mb INTEGER, guest_os TEXT,
  linked_system_id TEXT, last_seen TEXT DEFAULT (datetime('now')),
  PRIMARY KEY (host_system_id, uuid)
);
```

- [ ] **Step 4: 建 `internal/store/monitor.go`：**

```go
package store

import (
	"database/sql"
	"encoding/json"
)

type MetricRow struct {
	TS                                          int64
	CPU, Mem, Disk, GPU, NetUp, NetDown, Load, Temp, Uptime *float64
}
type ServiceRow struct {
	SystemID, Name, Kind, Status string
	CPU, Mem                     *float64
	Port                         int
	SoftwareIDs, Depends         string // json 字串（可空）
}
type VMRow struct {
	HostSystemID, Name, UUID, VmxPath, State, GuestOS, LinkedSystemID string
	VCPU, RamMB                                                       int
}
type SystemWithLatest struct {
	System
	Latest MetricRow
	Spark  []float64
}

// EnsureSystemForMachine：依 label find-or-create（inventory token 走監控時自動建 systems 列）。
func (s *Store) EnsureSystemForMachine(label string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM systems WHERE label=?`, label).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = "sys_" + randHex(6)
	_, err = s.db.Exec(`INSERT INTO systems (id,label,os,arch,kind,status,agent_status,last_seen,created)
		VALUES (?,?,?,?,'physical','online','ok',datetime('now'),unixepoch())`, id, label, "", "")
	return id, err
}

func (s *Store) UpsertMetricsLatest(systemID string, m MetricRow) error {
	var sparkJSON string
	err := s.db.QueryRow(`SELECT spark FROM metrics_latest WHERE system_id=?`, systemID).Scan(&sparkJSON)
	if err == sql.ErrNoRows {
		sparkJSON = "[]"
	} else if err != nil {
		return err
	}
	var spark []float64
	json.Unmarshal([]byte(sparkJSON), &spark)
	if m.CPU != nil {
		spark = append(spark, *m.CPU)
		if len(spark) > 24 {
			spark = spark[len(spark)-24:]
		}
	}
	sb, _ := json.Marshal(spark)
	_, err = s.db.Exec(`INSERT INTO metrics_latest (system_id,ts,cpu,mem,disk,gpu,net_up,net_down,load,temp,uptime,spark)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(system_id) DO UPDATE SET ts=excluded.ts,cpu=excluded.cpu,mem=excluded.mem,disk=excluded.disk,
		  gpu=excluded.gpu,net_up=excluded.net_up,net_down=excluded.net_down,load=excluded.load,
		  temp=excluded.temp,uptime=excluded.uptime,spark=excluded.spark`,
		systemID, m.TS, fv(m.CPU), fv(m.Mem), fv(m.Disk), fv(m.GPU), fv(m.NetUp), fv(m.NetDown), fv(m.Load), fv(m.Temp), fv(m.Uptime), string(sb))
	return err
}

func fv(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Store) InsertMetric(systemID, typ string, m MetricRow) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO metrics (system_id,type,ts,cpu,mem,disk,gpu,net_up,net_down,load,temp)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		systemID, typ, m.TS, fv(m.CPU), fv(m.Mem), fv(m.Disk), fv(m.GPU), fv(m.NetUp), fv(m.NetDown), fv(m.Load), fv(m.Temp))
	return err
}

func scanMetric(rows *sql.Rows) (MetricRow, error) {
	var m MetricRow
	var cpu, mem, disk, gpu, up, down, load, temp sql.NullFloat64
	err := rows.Scan(&m.TS, &cpu, &mem, &disk, &gpu, &up, &down, &load, &temp)
	m.CPU, m.Mem, m.Disk, m.GPU = nf(cpu), nf(mem), nf(disk), nf(gpu)
	m.NetUp, m.NetDown, m.Load, m.Temp = nf(up), nf(down), nf(load), nf(temp)
	return m, err
}
func nf(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func (s *Store) QueryMetrics(systemID, typ string, sinceTS int64) ([]MetricRow, error) {
	rows, err := s.db.Query(`SELECT ts,cpu,mem,disk,gpu,net_up,net_down,load,temp FROM metrics
		WHERE system_id=? AND type=? AND ts>=? ORDER BY ts`, systemID, typ, sinceTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricRow
	for rows.Next() {
		m, err := scanMetric(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SystemsWithLatest() ([]SystemWithLatest, error) {
	sys, err := s.ListSystems()
	if err != nil {
		return nil, err
	}
	out := make([]SystemWithLatest, 0, len(sys))
	for _, x := range sys {
		row := SystemWithLatest{System: x, Spark: []float64{}}
		var cpu, mem, disk, gpu, up, down, load, temp, uptime sql.NullFloat64
		var sparkJSON string
		err := s.db.QueryRow(`SELECT ts,cpu,mem,disk,gpu,net_up,net_down,load,temp,uptime,spark FROM metrics_latest WHERE system_id=?`, x.ID).
			Scan(&row.Latest.TS, &cpu, &mem, &disk, &gpu, &up, &down, &load, &temp, &uptime, &sparkJSON)
		if err == nil {
			row.Latest.CPU, row.Latest.Mem, row.Latest.Disk, row.Latest.GPU = nf(cpu), nf(mem), nf(disk), nf(gpu)
			row.Latest.NetUp, row.Latest.NetDown, row.Latest.Load, row.Latest.Temp, row.Latest.Uptime = nf(up), nf(down), nf(load), nf(temp), nf(uptime)
			json.Unmarshal([]byte(sparkJSON), &row.Spark)
		} else if err != sql.ErrNoRows {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Store) ReplaceServices(systemID string, rows []ServiceRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM services WHERE system_id=?`, systemID); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO services (system_id,name,kind,status,cpu,mem,port,software_ids,depends)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			systemID, r.Name, r.Kind, r.Status, fv(r.CPU), fv(r.Mem), zeroNil(r.Port), nullStr(r.SoftwareIDs), nullStr(r.Depends)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func zeroNil(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func (s *Store) listServicesWhere(where string, args ...any) ([]ServiceRow, error) {
	rows, err := s.db.Query(`SELECT system_id,name,kind,status,cpu,mem,port,software_ids,depends FROM services `+where+` ORDER BY system_id,name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceRow
	for rows.Next() {
		var r ServiceRow
		var cpu, mem sql.NullFloat64
		var port sql.NullInt64
		var sw, dep sql.NullString
		if err := rows.Scan(&r.SystemID, &r.Name, &r.Kind, &r.Status, &cpu, &mem, &port, &sw, &dep); err != nil {
			return nil, err
		}
		r.CPU, r.Mem, r.Port = nf(cpu), nf(mem), int(port.Int64)
		r.SoftwareIDs, r.Depends = sw.String, dep.String
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) ListServices() ([]ServiceRow, error) { return s.listServicesWhere("") }
func (s *Store) ListServicesBySystem(systemID string) ([]ServiceRow, error) {
	return s.listServicesWhere("WHERE system_id=?", systemID)
}

func (s *Store) ReplaceVMs(hostSystemID string, rows []VMRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM vms WHERE host_system_id=?`, hostSystemID); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO vms (host_system_id,name,uuid,vmx_path,state,vcpu,ram_mb,guest_os,linked_system_id)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			hostSystemID, r.Name, r.UUID, nullStr(r.VmxPath), r.State, r.VCPU, r.RamMB, nullStr(r.GuestOS), nullStr(r.LinkedSystemID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListVMs() ([]VMRow, error) {
	rows, err := s.db.Query(`SELECT host_system_id,name,uuid,vmx_path,state,vcpu,ram_mb,guest_os,linked_system_id FROM vms ORDER BY host_system_id,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VMRow
	for rows.Next() {
		var r VMRow
		var vmx, gos, link sql.NullString
		var vcpu, ram sql.NullInt64
		if err := rows.Scan(&r.HostSystemID, &r.Name, &r.UUID, &vmx, &r.State, &vcpu, &ram, &gos, &link); err != nil {
			return nil, err
		}
		r.VmxPath, r.GuestOS, r.LinkedSystemID = vmx.String, gos.String, link.String
		r.VCPU, r.RamMB = int(vcpu.Int64), int(ram.Int64)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LinkVM：對帳成功——vms 列指到 guest system，guest system 標 kind=vm + host_id。
func (s *Store) LinkVM(hostSystemID, uuid, guestSystemID string) error {
	if _, err := s.db.Exec(`UPDATE vms SET linked_system_id=? WHERE host_system_id=? AND uuid=?`, guestSystemID, hostSystemID, uuid); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE systems SET kind='vm', host_id=? WHERE id=?`, hostSystemID, guestSystemID)
	return err
}
```

> 註：`randHex`、`nullStr`、`System`、`ListSystems` 已存在於 store package。`System` struct 需有 `Kind`/`HostID`/`Label` 欄位（P0 已建，含 kind/host_id 欄）——以實際 struct 為準，若 `HostID` 未匯出對應 db 欄 `host_id`，補上 scan。

- [ ] **Step 5:** `go test ./internal/store/` → PASS（含 P0/P1 既有）。
- [ ] **Step 6: Commit** — `git add internal/store/ && git commit -m "feat(go): store monitor tables (metrics/latest+spark/services/vms) + crud"`

---

### Task 2: store — Downsample + Prune

**Files:** Modify `internal/store/monitor.go`; Test 追加 `internal/store/monitor_test.go`

- [ ] **Step 1: 失敗測試**（追加）：

```go
func TestDownsampleAndPrune(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	// 寫 20 筆 1m（ts 0..1140，每 60s），cpu = 10..29
	for i := 0; i < 20; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	// 聚合（now=1200）：10m 桶 = [0,600) 與 [600,1200) → cpu 平均 14.5 / 24.5
	if err := s.Downsample(1200); err != nil {
		t.Fatal(err)
	}
	pts, _ := s.QueryMetrics(id, "10m", 0)
	if len(pts) != 2 {
		t.Fatalf("10m buckets: %d", len(pts))
	}
	if *pts[0].CPU != 14.5 || *pts[1].CPU != 24.5 {
		t.Fatalf("10m avg: %v %v", *pts[0].CPU, *pts[1].CPU)
	}
	// 15m 桶 = [0,900) 平均 17, [900,1200) 平均 26.5
	pts15, _ := s.QueryMetrics(id, "15m", 0)
	if len(pts15) != 2 || *pts15[0].CPU != 17 {
		t.Fatalf("15m: %+v", pts15)
	}
	// Prune：now 拉到 1m 保留期(2h)之後 → 1m 全清，10m 還在
	if err := s.PruneMetrics(60*1140 + 7200 + 1); err == nil {
		one, _ := s.QueryMetrics(id, "1m", 0)
		ten, _ := s.QueryMetrics(id, "10m", 0)
		if len(one) != 0 || len(ten) == 0 {
			t.Fatalf("prune: 1m=%d 10m=%d", len(one), len(ten))
		}
	} else {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2:** `go test ./internal/store/ -run Downsample` → FAIL。

- [ ] **Step 3: 實作**（monitor.go 追加）：

```go
// 聚合層級：dst ← src，bucket 秒數與保留秒數。
var dsLevels = []struct {
	Dst, Src      string
	BucketSec     int64
	RetainSec     int64 // dst 的保留期（PruneMetrics 用）
}{
	{"10m", "1m", 600, 14 * 3600},
	{"15m", "1m", 900, 26 * 3600},
	{"60m", "15m", 3600, 8 * 24 * 3600},
	{"480m", "60m", 28800, 32 * 24 * 3600},
}

const retain1m = 2 * 3600

// Downsample：把 src 聚合進 dst（只聚合「已完結」的桶：bucket_end <= now）。冪等（INSERT OR REPLACE）。
func (s *Store) Downsample(now int64) error {
	for _, lv := range dsLevels {
		_, err := s.db.Exec(`
			INSERT OR REPLACE INTO metrics (system_id,type,ts,cpu,mem,disk,gpu,net_up,net_down,load,temp)
			SELECT system_id, ?, (ts/?)*?,
			  AVG(cpu), AVG(mem), AVG(disk), AVG(gpu), AVG(net_up), AVG(net_down), AVG(load), AVG(temp)
			FROM metrics WHERE type=? AND (ts/?)*? + ? <= ?
			GROUP BY system_id, ts/?`,
			lv.Dst, lv.BucketSec, lv.BucketSec, lv.Src, lv.BucketSec, lv.BucketSec, lv.BucketSec, now, lv.BucketSec)
		if err != nil {
			return err
		}
	}
	return nil
}

// PruneMetrics：刪各 type 超過保留期的列。
func (s *Store) PruneMetrics(now int64) error {
	if _, err := s.db.Exec(`DELETE FROM metrics WHERE type='1m' AND ts < ?`, now-retain1m); err != nil {
		return err
	}
	for _, lv := range dsLevels {
		if _, err := s.db.Exec(`DELETE FROM metrics WHERE type=? AND ts < ?`, lv.Dst, now-lv.RetainSec); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4:** `go test ./internal/store/` → PASS。
- [ ] **Step 5: Commit** — `git add internal/store/ && git commit -m "feat(go): metrics downsample (1m→10m/15m/60m/480m) + retention prune"`

---

### Task 3: collect — gopsutil 指標收集器

**Files:** Create `internal/collect/collect.go`, `internal/collect/gpu.go`; Test `internal/collect/collect_test.go`

- [ ] **Step 1:** `go get github.com/shirou/gopsutil/v4@latest`

- [ ] **Step 2: 失敗測試：**

```go
package collect

import "testing"

func TestCollectReal(t *testing.T) {
	c := New()
	m1 := c.Collect()
	if m1.TS == 0 {
		t.Fatal("ts")
	}
	if m1.CPU == nil || *m1.CPU < 0 || *m1.CPU > 100 {
		t.Fatalf("cpu: %v", m1.CPU)
	}
	if m1.Mem == nil || *m1.Mem <= 0 || *m1.Mem > 100 {
		t.Fatalf("mem: %v", m1.Mem)
	}
	if m1.Disk == nil || *m1.Disk <= 0 || *m1.Disk > 100 {
		t.Fatalf("disk: %v", m1.Disk)
	}
	if m1.Uptime == nil || *m1.Uptime <= 0 {
		t.Fatalf("uptime: %v", m1.Uptime)
	}
	// 第二次收集才有 net rate（首輪 nil）
	m2 := c.Collect()
	if m2.NetUp == nil || m2.NetDown == nil || *m2.NetUp < 0 {
		t.Fatalf("net rate: %v %v", m2.NetUp, m2.NetDown)
	}
}

func TestGPUParse(t *testing.T) {
	out := "37, 54\n"
	util, temp, ok := parseNvidiaSmi(out)
	if !ok || util != 37 || temp != 54 {
		t.Fatalf("gpu parse: %v %v %v", util, temp, ok)
	}
	if _, _, ok := parseNvidiaSmi("garbage"); ok {
		t.Fatal("garbage should fail")
	}
}
```

- [ ] **Step 3:** `go test ./internal/collect/` → FAIL。

- [ ] **Step 4: 實作 collect.go：**

```go
package collect

import (
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"
)

// Metrics 與 server MetricsReport JSON 對齊。
type Metrics struct {
	TS      int64    `json:"ts"`
	CPU     *float64 `json:"cpu"`
	Mem     *float64 `json:"mem"`
	Disk    *float64 `json:"disk"`
	GPU     *float64 `json:"gpu"`
	NetUp   *float64 `json:"net_up"`
	NetDown *float64 `json:"net_down"`
	Load    *float64 `json:"load"`
	Temp    *float64 `json:"temp"`
	Uptime  *float64 `json:"uptime"`
}

type Collector struct {
	prevSent, prevRecv uint64
	prevAt             time.Time
	gpuQuery           func() (util, temp float64, ok bool) // injectable（gpu.go 預設 nvidia-smi）
}

func New() *Collector { return &Collector{gpuQuery: nvidiaSmiQuery} }

func pf(v float64) *float64 { return &v }

func (c *Collector) Collect() Metrics {
	m := Metrics{TS: time.Now().Unix()}
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		m.CPU = pf(round1(pcts[0]))
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		m.Mem = pf(round1(vm.UsedPercent))
	}
	root := "/"
	if runtime.GOOS == "windows" {
		root = "C:\\"
	}
	if du, err := disk.Usage(root); err == nil {
		m.Disk = pf(round1(du.UsedPercent))
	}
	if up, err := host.Uptime(); err == nil {
		m.Uptime = pf(float64(up))
	}
	if la, err := load.Avg(); err == nil {
		m.Load = pf(round1(la.Load1))
	}
	if temps, err := sensors.SensorsTemperatures(); err == nil {
		max := 0.0
		for _, t := range temps {
			if t.Temperature > max && t.Temperature < 150 {
				max = t.Temperature
			}
		}
		if max > 0 {
			m.Temp = pf(round1(max))
		}
	}
	if io, err := gnet.IOCounters(false); err == nil && len(io) > 0 {
		now := time.Now()
		if !c.prevAt.IsZero() {
			dt := now.Sub(c.prevAt).Seconds()
			if dt > 0 {
				up := float64(io[0].BytesSent-c.prevSent) / dt / 1024 / 1024
				down := float64(io[0].BytesRecv-c.prevRecv) / dt / 1024 / 1024
				if up >= 0 && down >= 0 { // counter reset 防護
					m.NetUp, m.NetDown = pf(round2(up)), pf(round2(down))
				}
			}
		}
		c.prevSent, c.prevRecv, c.prevAt = io[0].BytesSent, io[0].BytesRecv, now
	}
	if util, temp, ok := c.gpuQuery(); ok {
		m.GPU = pf(round1(util))
		if m.Temp == nil || temp > *m.Temp {
			m.Temp = pf(round1(temp))
		}
	}
	return m
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
```

> ⚠️ gopsutil v4 的 sensors 套件名以實際為準：v4 是 `github.com/shirou/gopsutil/v4/sensors`，函式 `SensorsTemperatures()`；若編譯錯誤改用 `TemperaturesWithContext(context.Background())`（查 go doc 確認簽名後擇一）。mac/容器內常回 error 或空 → Temp 留 nil，**不可 fatal**。

- [ ] **Step 5: 實作 gpu.go：**

```go
package collect

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// nvidiaSmiQuery：有 nvidia-smi 才有 GPU；任何失敗 → ok=false。
func nvidiaSmiQuery() (float64, float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=utilization.gpu,temperature.gpu", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, 0, false
	}
	return parseNvidiaSmi(string(out))
}

func parseNvidiaSmi(out string) (float64, float64, bool) {
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	parts := strings.Split(line, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	util, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	temp, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return util, temp, true
}
```

- [ ] **Step 6:** `go test ./internal/collect/` → PASS（真機收集：mac 上 cpu/mem/disk/uptime 有值；temp/gpu 可 nil）。
- [ ] **Step 7: Commit** — `git add internal/collect/ go.mod go.sum && git commit -m "feat(go): gopsutil metrics collector + nvidia-smi gpu"`

---

### Task 4: dockerstat — 容器服務收集

**Files:** Create `internal/dockerstat/dockerstat.go`; Test `internal/dockerstat/dockerstat_test.go`

- [ ] **Step 1: 失敗測試：**

```go
package dockerstat

import "testing"

const psOut = `{"Names":"redis","State":"running","Ports":"0.0.0.0:6379->6379/tcp"}
{"Names":"caddy","State":"running","Ports":"0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp"}
`
const statsOut = `{"Name":"redis","CPUPerc":"1.25%","MemPerc":"0.80%"}
{"Name":"caddy","CPUPerc":"0.10%","MemPerc":"2.00%"}
`

func TestParse(t *testing.T) {
	svcs := parse(psOut, statsOut)
	if len(svcs) != 2 {
		t.Fatalf("n=%d", len(svcs))
	}
	r := svcs[0]
	if r.Name != "redis" || r.Kind != "docker" || r.Status != "running" || r.Port != 6379 {
		t.Fatalf("redis: %+v", r)
	}
	if r.CPU == nil || *r.CPU != 1.25 || *r.Mem != 0.8 {
		t.Fatalf("redis stats: %+v", r)
	}
}

func TestCollectNoDocker(t *testing.T) {
	c := &Collector{Run: func(args ...string) (string, error) { return "", errNo{} }}
	if svcs := c.Collect(); svcs != nil {
		t.Fatalf("no docker → nil, got %+v", svcs)
	}
}

type errNo struct{}

func (errNo) Error() string { return "docker not found" }
```

- [ ] **Step 2:** `go test ./internal/dockerstat/` → FAIL。

- [ ] **Step 3: 實作：**

```go
package dockerstat

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Status string   `json:"status"`
	CPU    *float64 `json:"cpu"`
	Mem    *float64 `json:"mem"`
	Port   int      `json:"port"`
}

type Collector struct {
	Run func(args ...string) (string, error) // injectable；預設真 docker
}

func New() *Collector { return &Collector{Run: dockerRun} }

func dockerRun(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	return string(out), err
}

// Collect：docker 不存在/失敗 → nil（該機沒有容器層）。
func (c *Collector) Collect() []Service {
	ps, err := c.Run("ps", "--format", "{{json .}}")
	if err != nil {
		return nil
	}
	stats, _ := c.Run("stats", "--no-stream", "--format", "{{json .}}") // stats 失敗仍回 ps 清單
	return parse(ps, stats)
}

func parse(psOut, statsOut string) []Service {
	type psRow struct{ Names, State, Ports string }
	type stRow struct{ Name, CPUPerc, MemPerc string }
	stats := map[string]stRow{}
	for _, line := range strings.Split(strings.TrimSpace(statsOut), "\n") {
		var r stRow
		if json.Unmarshal([]byte(line), &r) == nil && r.Name != "" {
			stats[r.Name] = r
		}
	}
	var out []Service
	for _, line := range strings.Split(strings.TrimSpace(psOut), "\n") {
		var p psRow
		if json.Unmarshal([]byte(line), &p) != nil || p.Names == "" {
			continue
		}
		svc := Service{Name: p.Names, Kind: "docker", Status: normState(p.State), Port: firstPort(p.Ports)}
		if st, ok := stats[p.Names]; ok {
			if v, err := strconv.ParseFloat(strings.TrimSuffix(st.CPUPerc, "%"), 64); err == nil {
				svc.CPU = &v
			}
			if v, err := strconv.ParseFloat(strings.TrimSuffix(st.MemPerc, "%"), 64); err == nil {
				svc.Mem = &v
			}
		}
		out = append(out, svc)
	}
	return out
}

func normState(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// firstPort：取第一個 host port（"0.0.0.0:6379->6379/tcp, ..." → 6379）。
func firstPort(ports string) int {
	for _, seg := range strings.Split(ports, ",") {
		seg = strings.TrimSpace(seg)
		if i := strings.Index(seg, "->"); i > 0 {
			hostPart := seg[:i]
			if j := strings.LastIndexByte(hostPart, ':'); j >= 0 {
				if n, err := strconv.Atoi(hostPart[j+1:]); err == nil {
					return n
				}
			}
		}
	}
	return 0
}
```

- [ ] **Step 4:** `go test ./internal/dockerstat/` → PASS。
- [ ] **Step 5: Commit** — `git add internal/dockerstat/ && git commit -m "feat(go): docker services collector (ps + stats, injectable)"`

---

### Task 5: vmenum — VMware Fusion VM 列舉

**Files:** Create `internal/vmenum/vmware.go`; Test `internal/vmenum/vmware_test.go`

- [ ] **Step 1: 失敗測試：**

```go
package vmenum

import "testing"

const vmx = `numvcpus = "4"
memsize = "4096"
guestOS = "ubuntu-64"
displayName = "ubuntu-vm"
uuid.bios = "56 4d aa bb cc dd ee ff-00 11 22 33 44 55 66 77"
`

func TestParseVmx(t *testing.T) {
	vm := parseVmx("/p/ubuntu-vm.vmx", vmx)
	if vm.Name != "ubuntu-vm" || vm.VCPU != 4 || vm.RamMB != 4096 || vm.GuestOS != "ubuntu-64" {
		t.Fatalf("vmx: %+v", vm)
	}
	if vm.UUID != "564daabbccddeeff-0011223344556677" {
		t.Fatalf("uuid: %q", vm.UUID)
	}
}

func TestEnumerate(t *testing.T) {
	e := &Enumerator{
		RunVmrun: func() (string, error) {
			return "Total running VMs: 1\n/p/ubuntu-vm.vmx\n", nil
		},
		Glob:     func() []string { return []string{"/p/ubuntu-vm.vmx", "/p/win.vmx"} },
		ReadFile: func(p string) (string, error) {
			if p == "/p/win.vmx" {
				return `displayName = "win"` + "\n" + `uuid.bios = "11 22-33 44"` + "\n", nil
			}
			return vmx, nil
		},
	}
	vms := e.Enumerate()
	if len(vms) != 2 {
		t.Fatalf("n=%d %+v", len(vms), vms)
	}
	byName := map[string]VM{}
	for _, v := range vms {
		byName[v.Name] = v
	}
	if byName["ubuntu-vm"].State != "running" || byName["win"].State != "stopped" {
		t.Fatalf("states: %+v", byName)
	}
}

func TestEnumerateNoVmware(t *testing.T) {
	e := &Enumerator{
		RunVmrun: func() (string, error) { return "", errNo{} },
		Glob:     func() []string { return nil },
		ReadFile: func(string) (string, error) { return "", errNo{} },
	}
	if vms := e.Enumerate(); vms != nil {
		t.Fatalf("no vmware → nil, got %+v", vms)
	}
}

type errNo struct{}

func (errNo) Error() string { return "no vmware" }
```

- [ ] **Step 2:** `go test ./internal/vmenum/` → FAIL。

- [ ] **Step 3: 實作：**

```go
package vmenum

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type VM struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	VmxPath string `json:"vmx_path"`
	State   string `json:"state"` // running|stopped
	VCPU    int    `json:"vcpu"`
	RamMB   int    `json:"ram_mb"`
	GuestOS string `json:"guest_os"`
}

type Enumerator struct {
	RunVmrun func() (string, error)          // injectable：vmrun list
	Glob     func() []string                 // injectable：VM library 掃描
	ReadFile func(path string) (string, error)
}

func New() *Enumerator {
	return &Enumerator{RunVmrun: vmrunList, Glob: fusionGlob, ReadFile: readFile}
}

func vmrunList() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vmrun", "list").Output()
	return string(out), err
}

func fusionGlob() []string {
	home, _ := os.UserHomeDir()
	pats := []string{
		filepath.Join(home, "Virtual Machines.localized", "*", "*.vmx"),
		filepath.Join(home, "Virtual Machines", "*", "*.vmx"),
	}
	var out []string
	for _, p := range pats {
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	return out
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

// Enumerate：vmrun + library 掃描合併；完全沒有 VMware → nil（該機不是 hypervisor host）。
func (e *Enumerator) Enumerate() []VM {
	runningOut, vmrunErr := e.RunVmrun()
	running := map[string]bool{}
	if vmrunErr == nil {
		for _, line := range strings.Split(runningOut, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasSuffix(line, ".vmx") {
				running[line] = true
			}
		}
	}
	paths := map[string]bool{}
	for _, p := range e.Glob() {
		paths[p] = true
	}
	for p := range running {
		paths[p] = true
	}
	if vmrunErr != nil && len(paths) == 0 {
		return nil
	}
	var out []VM
	for p := range paths {
		content, err := e.ReadFile(p)
		if err != nil {
			continue
		}
		vm := parseVmx(p, content)
		if running[p] {
			vm.State = "running"
		} else {
			vm.State = "stopped"
		}
		out = append(out, vm)
	}
	return out
}

// parseVmx：key = "value" 格式。
func parseVmx(path, content string) VM {
	vm := VM{VmxPath: path, Name: strings.TrimSuffix(filepath.Base(path), ".vmx")}
	for _, line := range strings.Split(content, "\n") {
		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.Trim(strings.TrimSpace(line[i+1:]), `"`)
		switch key {
		case "displayName":
			vm.Name = val
		case "numvcpus":
			vm.VCPU, _ = strconv.Atoi(val)
		case "memsize":
			vm.RamMB, _ = strconv.Atoi(val)
		case "guestOS":
			vm.GuestOS = val
		case "uuid.bios":
			vm.UUID = strings.ReplaceAll(strings.ReplaceAll(val, " ", ""), "—", "-")
		}
	}
	return vm
}
```

> 註：uuid.bios 格式 `"56 4d ... ff-00 11 ..."` → 去空白保留 `-` → `564d...ff-0011...`。測試已定此格式。

- [ ] **Step 4:** `go test ./internal/vmenum/` → PASS。
- [ ] **Step 5: Commit** — `git add internal/vmenum/ && git commit -m "feat(go): vmware fusion vm enumerator (vmrun + vmx scan, injectable)"`

---

### Task 6: server — 監控 agent 端點 + 統一 resolver + 對帳

**Files:** Create `internal/server/monitor_api.go`; Modify `internal/server/server.go`（routes 加 `s.registerMonitorAPI()`）; Test `internal/server/monitor_api_test.go`

- [ ] **Step 1: 失敗測試：**

```go
package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func postJSON(t *testing.T, srv *Server, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func TestReportMetricsWithInventoryToken(t *testing.T) {
	srv, st := vtServer(t) // inventory: machine mac, token tok-mac
	rec := postJSON(t, srv, "/api/agent/report-metrics", "tok-mac",
		`{"ts":1000,"cpu":42.5,"mem":61.0,"disk":70.1,"net_up":1.5,"net_down":3.2,"load":0.7,"uptime":3600}`)
	if rec.Code != 200 {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	rows, _ := st.SystemsWithLatest()
	if len(rows) != 1 || rows[0].Label != "mac" || *rows[0].Latest.CPU != 42.5 {
		t.Fatalf("latest: %+v", rows)
	}
	// 同 token 再報 → 同一 system（不重複建）
	postJSON(t, srv, "/api/agent/report-metrics", "tok-mac", `{"ts":1060,"cpu":43.0}`)
	rows2, _ := st.SystemsWithLatest()
	if len(rows2) != 1 {
		t.Fatalf("dup system: %+v", rows2)
	}
	pts, _ := st.QueryMetrics(rows2[0].ID, "1m", 0)
	if len(pts) != 2 {
		t.Fatalf("1m rows: %d", len(pts))
	}
	// 無 token → 401
	if rec := postJSON(t, srv, "/api/agent/report-metrics", "", `{}`); rec.Code != 401 {
		t.Fatalf("noauth: %d", rec.Code)
	}
}

func TestReportServices(t *testing.T) {
	srv, st := vtServer(t)
	rec := postJSON(t, srv, "/api/agent/report-services", "tok-mac",
		`[{"name":"redis","kind":"docker","status":"running","cpu":1.2,"mem":0.8,"port":6379}]`)
	if rec.Code != 200 {
		t.Fatalf("services: %d", rec.Code)
	}
	rows, _ := st.ListServices()
	if len(rows) != 1 || rows[0].Name != "redis" || rows[0].Port != 6379 {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestReportVMsAndReconcile(t *testing.T) {
	srv, st := vtServer(t)
	// guest 先以 system 存在（label=ubuntu-vm，模擬 VM 內 agent 已回報過）
	guestID, _ := st.EnsureSystemForMachine("ubuntu-vm")
	rec := postJSON(t, srv, "/api/agent/report-vms", "tok-mac",
		`[{"name":"ubuntu-vm","uuid":"u-1","vmx_path":"/x.vmx","state":"running","vcpu":4,"ram_mb":4096,"guest_os":"ubuntu-64"},
		  {"name":"ghost-vm","uuid":"u-2","state":"stopped"}]`)
	if rec.Code != 200 {
		t.Fatalf("vms: %d %s", rec.Code, rec.Body.String())
	}
	vms, _ := st.ListVMs()
	if len(vms) != 2 {
		t.Fatalf("vms rows: %+v", vms)
	}
	byUUID := map[string]string{}
	for _, v := range vms {
		byUUID[v.UUID] = v.LinkedSystemID
	}
	if byUUID["u-1"] != guestID || byUUID["u-2"] != "" {
		t.Fatalf("reconcile: %+v", byUUID)
	}
}
```

- [ ] **Step 2:** `go test ./internal/server/ -run 'ReportMetrics|ReportServices|ReportVMs'` → FAIL。

- [ ] **Step 3: 實作 monitor_api.go：**

```go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

func (s *Server) registerMonitorAPI() {
	s.mux.HandleFunc("/api/agent/report-metrics", s.reportMetrics)
	s.mux.HandleFunc("/api/agent/report-services", s.reportServices)
	s.mux.HandleFunc("/api/agent/report-vms", s.reportVMs)
}

// agentSystem：統一識別——先試 systems token（enroll 取得），再試 inventory token（自動 find-or-create systems 列）。
func (s *Server) agentSystem(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) <= len(p) || h[:len(p)] != p {
		return "", false
	}
	tok := h[len(p):]
	if sys, err := s.st.SystemByAgentToken(tok); err == nil {
		return sys.ID, true
	}
	if m := inventory.MachineForToken(s.inv, tok); m != "" {
		id, err := s.st.EnsureSystemForMachine(m)
		return id, err == nil
	}
	return "", false
}

type metricsReport struct {
	TS                                                     int64    `json:"ts"`
	CPU, Mem, Disk, GPU, NetUp, NetDown, Load, Temp, Uptime *float64 `json:"-"`
}

// 用顯式 struct 帶 json tag（上面的 `json:"-"` 佔位是錯的——實際用下面這個）：
type metricsBody struct {
	TS      int64    `json:"ts"`
	CPU     *float64 `json:"cpu"`
	Mem     *float64 `json:"mem"`
	Disk    *float64 `json:"disk"`
	GPU     *float64 `json:"gpu"`
	NetUp   *float64 `json:"net_up"`
	NetDown *float64 `json:"net_down"`
	Load    *float64 `json:"load"`
	Temp    *float64 `json:"temp"`
	Uptime  *float64 `json:"uptime"`
}

func (s *Server) reportMetrics(w http.ResponseWriter, r *http.Request) {
	sysID, ok := s.agentSystem(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var b metricsBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	m := store.MetricRow{TS: b.TS, CPU: b.CPU, Mem: b.Mem, Disk: b.Disk, GPU: b.GPU,
		NetUp: b.NetUp, NetDown: b.NetDown, Load: b.Load, Temp: b.Temp, Uptime: b.Uptime}
	s.st.UpsertMetricsLatest(sysID, m)
	s.st.InsertMetric(sysID, "1m", m)
	s.st.TouchSystem(sysID) // last_seen=now（store 若無此函式，Task 內補：UPDATE systems SET last_seen=datetime('now'), status='online' WHERE id=?）
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) reportServices(w http.ResponseWriter, r *http.Request) {
	sysID, ok := s.agentSystem(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body []struct {
		Name        string   `json:"name"`
		Kind        string   `json:"kind"`
		Status      string   `json:"status"`
		CPU         *float64 `json:"cpu"`
		Mem         *float64 `json:"mem"`
		Port        int      `json:"port"`
		SoftwareIDs []string `json:"software_ids"`
		Depends     []string `json:"depends"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	rows := make([]store.ServiceRow, 0, len(body))
	for _, x := range body {
		sw, _ := json.Marshal(x.SoftwareIDs)
		dep, _ := json.Marshal(x.Depends)
		swS, depS := string(sw), string(dep)
		if x.SoftwareIDs == nil {
			swS = ""
		}
		if x.Depends == nil {
			depS = ""
		}
		rows = append(rows, store.ServiceRow{Name: x.Name, Kind: x.Kind, Status: x.Status,
			CPU: x.CPU, Mem: x.Mem, Port: x.Port, SoftwareIDs: swS, Depends: depS})
	}
	s.st.ReplaceServices(sysID, rows)
	writeJSON(w, 200, map[string]int{"applied": len(rows)})
}

func (s *Server) reportVMs(w http.ResponseWriter, r *http.Request) {
	sysID, ok := s.agentSystem(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body []struct {
		Name    string `json:"name"`
		UUID    string `json:"uuid"`
		VmxPath string `json:"vmx_path"`
		State   string `json:"state"`
		VCPU    int    `json:"vcpu"`
		RamMB   int    `json:"ram_mb"`
		GuestOS string `json:"guest_os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	rows := make([]store.VMRow, 0, len(body))
	for _, x := range body {
		rows = append(rows, store.VMRow{Name: x.Name, UUID: x.UUID, VmxPath: x.VmxPath,
			State: x.State, VCPU: x.VCPU, RamMB: x.RamMB, GuestOS: x.GuestOS})
	}
	s.st.ReplaceVMs(sysID, rows)
	// 對帳：label==vm name 的 system → LinkVM
	systems, _ := s.st.ListSystems()
	byLabel := map[string]string{}
	for _, x := range systems {
		byLabel[x.Label] = x.ID
	}
	linked := 0
	for _, vm := range rows {
		if gid, ok := byLabel[vm.Name]; ok && gid != sysID {
			s.st.LinkVM(sysID, vm.UUID, gid)
			linked++
		}
	}
	writeJSON(w, 200, map[string]int{"applied": len(rows), "linked": linked})
}
```

> ⚠️ 上面 `metricsReport`（含 `json:"-"`）是說明殘渣，**不要寫進檔案**——只用 `metricsBody`。
> `TouchSystem(id)`：若 store 沒有，順手在 monitor.go 加：
> ```go
> func (s *Store) TouchSystem(id string) error {
> 	_, err := s.db.Exec(`UPDATE systems SET last_seen=datetime('now'), status='online' WHERE id=?`, id)
> 	return err
> }
> ```
> 對帳規則 P2 先用 label==vm.Name（hostname 對 displayName）；uuid 對應留 P3（需 agent 報 machine uuid）。

- [ ] **Step 4:** routes() 加 `s.registerMonitorAPI()`（registerAgentVT 之後）。確認無重複 pattern。
- [ ] **Step 5:** `go test ./internal/server/` → ALL PASS。
- [ ] **Step 6: Commit** — `git add internal/server/ internal/store/ && git commit -m "feat(go): agent monitor endpoints (metrics/services/vms) + unified token resolver + vm reconcile"`

---

### Task 7: server — 瀏覽器監控 API（systems enriched / metrics range / services / vms）

**Files:** Modify `internal/server/monitor_api.go`、`internal/server/server.go`; Test 追加 `internal/server/monitor_api_test.go`

- [ ] **Step 1: 失敗測試**（追加）：

```go
func TestSystemsEnrichedAndStatus(t *testing.T) {
	srv, st := vtServer(t)
	postJSON(t, srv, "/api/agent/report-metrics", "tok-mac", `{"ts":1000,"cpu":42.5,"mem":95.0,"disk":70.1}`)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/systems", nil))
	b := rec.Body.String()
	if !strings.Contains(b, `"cpu":42.5`) || !strings.Contains(b, `"spark":[42.5]`) {
		t.Fatalf("enriched: %s", b)
	}
	if !strings.Contains(b, `"status":"warn"`) { // mem 95 → warn（last_seen 剛 touch，online 但 warn 優先）
		t.Fatalf("warn: %s", b)
	}
	_ = st
}

func TestMetricsRange(t *testing.T) {
	srv, st := vtServer(t)
	id, _ := st.EnsureSystemForMachine("mac")
	for i := 0; i < 3; i++ {
		st.InsertMetric(id, "1m", store.MetricRow{TS: int64(60 * i), CPU: fpt(float64(i))})
		st.InsertMetric(id, "10m", store.MetricRow{TS: int64(600 * i), CPU: fpt(float64(100 + i))})
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/systems/"+id+"/metrics?range=12h", nil))
	b := rec.Body.String()
	if !strings.Contains(b, `"cpu":100`) || strings.Contains(b, `"cpu":0`) {
		t.Fatalf("range type: %s", b)
	}
	// 未知 system → 404
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("GET", "/api/systems/nope/metrics?range=1h", nil))
	if rec2.Code != 404 {
		t.Fatalf("missing sys: %d", rec2.Code)
	}
}

func fpt(v float64) *float64 { return &v }

func TestServicesAndVMsAPI(t *testing.T) {
	srv, st := vtServer(t)
	id, _ := st.EnsureSystemForMachine("mac")
	st.ReplaceServices(id, []store.ServiceRow{{Name: "redis", Kind: "docker", Status: "running"}})
	st.ReplaceVMs(id, []store.VMRow{{Name: "v1", UUID: "u", State: "running"}})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/services", nil))
	if !strings.Contains(rec.Body.String(), `"redis"`) {
		t.Fatalf("services: %s", rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("GET", "/api/vms", nil))
	if !strings.Contains(rec2.Body.String(), `"v1"`) {
		t.Fatalf("vms: %s", rec2.Body.String())
	}
}
```

（測試檔需 import `"github.com/curtis1215/cockpit/internal/store"`。）

- [ ] **Step 2:** 跑 → FAIL。

- [ ] **Step 3: 實作**（monitor_api.go 追加；registerMonitorAPI 加路由）：

```go
// registerMonitorAPI 追加：
//   s.mux.HandleFunc("/api/services", s.apiServices)
//   s.mux.HandleFunc("/api/vms", s.apiVMs)
//   s.mux.HandleFunc("/api/systems/", s.apiSystemSub)   // GET /api/systems/{id} 與 /{id}/metrics
// 並把 P0 在 server.go 註冊的 "/api/systems"（精確路徑，list）改造：handler 換成 s.apiSystemsEnriched（取代原 ListSystems 輸出）。

func (s *Server) apiSystemsEnriched(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.SystemsWithLatest()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := []map[string]any{}
	for _, x := range rows {
		out = append(out, systemMap(x))
	}
	writeJSON(w, 200, out)
}

func systemMap(x store.SystemWithLatest) map[string]any {
	st := liveStatus(x)
	return map[string]any{
		"id": x.ID, "label": x.Label, "role": x.Role, "os": x.OS, "arch": x.Arch,
		"kind": x.Kind, "host_id": x.HostID, "status": st,
		"agent_version": x.AgentVersion, "agent_status": x.AgentStatus, "last_seen": x.LastSeen,
		"cpu": fv2(x.Latest.CPU), "mem": fv2(x.Latest.Mem), "disk": fv2(x.Latest.Disk),
		"gpu": fv2(x.Latest.GPU), "net_up": fv2(x.Latest.NetUp), "net_down": fv2(x.Latest.NetDown),
		"load": fv2(x.Latest.Load), "temp": fv2(x.Latest.Temp), "uptime": fv2(x.Latest.Uptime),
		"spark": x.Spark,
	}
}

func fv2(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// liveStatus：offline（last_seen 逾 60s）> warn（門檻）> online。
func liveStatus(x store.SystemWithLatest) string {
	if t, err := time.Parse("2006-01-02 15:04:05", x.LastSeen); err == nil {
		if time.Since(t.UTC()) > 60*time.Second {
			return "offline"
		}
	}
	over := func(p *float64, th float64) bool { return p != nil && *p > th }
	if over(x.Latest.CPU, 90) || over(x.Latest.Mem, 90) || over(x.Latest.Disk, 90) || over(x.Latest.Temp, 85) {
		return "warn"
	}
	return "online"
}

var rangeType = map[string]struct {
	Typ      string
	WindowSec int64
}{
	"1h": {"1m", 3600}, "12h": {"10m", 12 * 3600}, "24h": {"15m", 24 * 3600},
	"7d": {"60m", 7 * 24 * 3600}, "30d": {"480m", 30 * 24 * 3600},
}

func (s *Server) apiSystemSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/systems/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	rows, err := s.st.SystemsWithLatest()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	var found *store.SystemWithLatest
	for i := range rows {
		if rows[i].ID == id {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		writeJSON(w, 404, map[string]string{"error": "system not found"})
		return
	}
	if len(parts) == 1 { // GET /api/systems/{id}
		writeJSON(w, 200, systemMap(*found))
		return
	}
	if len(parts) == 2 && parts[1] == "metrics" {
		rt, ok := rangeType[r.URL.Query().Get("range")]
		if !ok {
			rt = rangeType["24h"]
		}
		since := time.Now().Unix() - rt.WindowSec
		pts, err := s.st.QueryMetrics(id, rt.Typ, since)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		out := []map[string]any{}
		for _, m := range pts {
			out = append(out, map[string]any{"t": m.TS, "cpu": fv2(m.CPU), "mem": fv2(m.Mem),
				"disk": fv2(m.Disk), "gpu": fv2(m.GPU), "net_up": fv2(m.NetUp),
				"net_down": fv2(m.NetDown), "load": fv2(m.Load), "temp": fv2(m.Temp)})
		}
		writeJSON(w, 200, out)
		return
	}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

func (s *Server) apiServices(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.st.ListServices()
	out := []map[string]any{}
	for _, x := range rows {
		m := map[string]any{"system_id": x.SystemID, "name": x.Name, "kind": x.Kind,
			"status": x.Status, "cpu": fv2(x.CPU), "mem": fv2(x.Mem), "port": x.Port}
		var sw, dep []string
		if x.SoftwareIDs != "" {
			json.Unmarshal([]byte(x.SoftwareIDs), &sw)
		}
		if x.Depends != "" {
			json.Unmarshal([]byte(x.Depends), &dep)
		}
		m["software_ids"], m["depends"] = sw, dep
		out = append(out, m)
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiVMs(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.st.ListVMs()
	out := []map[string]any{}
	for _, x := range rows {
		out = append(out, map[string]any{"host_system_id": x.HostSystemID, "name": x.Name,
			"uuid": x.UUID, "vmx_path": x.VmxPath, "state": x.State, "vcpu": x.VCPU,
			"ram_mb": x.RamMB, "guest_os": x.GuestOS, "linked_system_id": nilIfEmpty(x.LinkedSystemID)})
	}
	writeJSON(w, 200, out)
}
```

> 路由整理（避免重複 pattern panic）：P0 server.go 原本就註冊 `"/api/systems"`（精確）——把它的 handler 改指到 `s.apiSystemsEnriched`；本任務新增 `"/api/systems/"`（prefix）→ `apiSystemSub`。`metricsReport` 殘渣勿入。`time`/`strings`/`encoding/json` import 補齊。`QueryMetrics` 的 since 用真實時鐘——測試裡 ts 用小整數，since = now-window 為負或很小，皆 ≥ 條件可過（12h 窗遠大於測試 ts）。注意 last_seen 在 P0 store 以 `datetime('now')` 寫入、ListSystems scan 出字串格式 `2006-01-02 15:04:05`（確認實際格式，liveStatus parse 失敗一律不判 offline）。

- [ ] **Step 4:** `go test ./internal/server/` → ALL PASS；`go vet ./...`。
- [ ] **Step 5: Commit** — `git add internal/server/ && git commit -m "feat(go): browser monitor api (systems enriched/metrics range/services/vms) + live status"`

---

### Task 8: agent — 監控迴圈 + serve 降採樣排程

**Files:** Create `internal/agent/monitor.go`; Modify `internal/agent/agent.go`（Run 啟動監控迴圈）、`cmd/cockpit/serve.go`（降採樣排程）; Test `internal/agent/monitor_test.go`

- [ ] **Step 1: 失敗測試：**

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

func TestMonitorOnce(t *testing.T) {
	var gotMetrics, gotServices int32
	var lastMetrics map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/report-metrics":
			atomic.AddInt32(&gotMetrics, 1)
			json.NewDecoder(r.Body).Decode(&lastMetrics)
			w.Write([]byte(`{"ok":true}`))
		case "/api/agent/report-services":
			atomic.AddInt32(&gotServices, 1)
			w.Write([]byte(`{"applied":0}`))
		}
	}))
	defer srv.Close()
	a := &Agent{ServerURL: srv.URL, Token: "tok", Version: "0.1.0"}
	a.MonitorOnce()
	if atomic.LoadInt32(&gotMetrics) != 1 {
		t.Fatalf("metrics=%d", gotMetrics)
	}
	if lastMetrics["cpu"] == nil || lastMetrics["ts"] == nil {
		t.Fatalf("payload: %v", lastMetrics)
	}
	a.ServicesOnce()
	// docker 可能不存在 → 不送或送空都可；只要不 panic。本測試僅驗證呼叫安全。
	_ = gotServices
	_ = time.Second
}
```

- [ ] **Step 2:** `go test ./internal/agent/ -run MonitorOnce` → FAIL。

- [ ] **Step 3: 實作 `internal/agent/monitor.go`：**

```go
package agent

import (
	"time"

	"github.com/curtis1215/cockpit/internal/collect"
	"github.com/curtis1215/cockpit/internal/dockerstat"
	"github.com/curtis1215/cockpit/internal/vmenum"
)

// 監控收集器（lazy 單例，掛在 Agent 上）
func (a *Agent) collector() *collect.Collector {
	if a.col == nil {
		a.col = collect.New()
	}
	return a.col
}

// MonitorOnce：收集一次指標並回報。
func (a *Agent) MonitorOnce() {
	m := a.collector().Collect()
	a.c().PostJSON("/api/agent/report-metrics", a.Token, m, nil)
}

// ServicesOnce：收集容器服務並回報（無 docker → 跳過）。
func (a *Agent) ServicesOnce() {
	if a.docker == nil {
		a.docker = dockerstat.New()
	}
	svcs := a.docker.Collect()
	if svcs == nil {
		return
	}
	a.c().PostJSON("/api/agent/report-services", a.Token, svcs, nil)
}

// VMsOnce：host 列舉 VM 並回報（非 hypervisor host → 跳過）。
func (a *Agent) VMsOnce() {
	if a.vmenum == nil {
		a.vmenum = vmenum.New()
	}
	vms := a.vmenum.Enumerate()
	if vms == nil {
		return
	}
	a.c().PostJSON("/api/agent/report-vms", a.Token, vms, nil)
}

// monitorLoop：15s 指標、60s 服務、5m VM。
func (a *Agent) monitorLoop() {
	a.MonitorOnce() // 先暖機一次（net rate 基準）
	tickM := time.NewTicker(15 * time.Second)
	tickS := time.NewTicker(60 * time.Second)
	tickV := time.NewTicker(5 * time.Minute)
	defer tickM.Stop()
	defer tickS.Stop()
	defer tickV.Stop()
	a.ServicesOnce()
	a.VMsOnce()
	for {
		select {
		case <-tickM.C:
			a.MonitorOnce()
		case <-tickS.C:
			a.ServicesOnce()
		case <-tickV.C:
			a.VMsOnce()
		}
	}
}
```

Agent struct（agent.go）加未匯出欄位：
```go
	col    *collect.Collector
	docker *dockerstat.Collector
	vmenum *vmenum.Enumerator
```
`Run()` 在 heartbeat goroutine 之後加 `go a.monitorLoop()`。

- [ ] **Step 4: serve.go 加降採樣排程**（runServe 內、ListenAndServe 前）：

```go
	go func() {
		for {
			now := time.Now().Unix()
			if err := st.Downsample(now); err != nil {
				log.Printf("downsample: %v", err)
			}
			if err := st.PruneMetrics(now); err != nil {
				log.Printf("prune: %v", err)
			}
			time.Sleep(5 * time.Minute)
		}
	}()
```

- [ ] **Step 5:** `go test ./... && go vet ./... && go build -o /tmp/cockpit ./cmd/cockpit` → 全綠。
- [ ] **Step 6: Commit** — `git add internal/agent/ cmd/cockpit/serve.go && git commit -m "feat(go): agent monitor loop (metrics/services/vms) + serve downsample schedule"`

---

### Task 9: OrbStack VM 端到端驗收（監控）

**Files:** 無程式碼變更（驗收）。⚠️ 被測 serve/agent 只跑在 OrbStack VM `cockpit-test` 內。

- [ ] **Step 1:** mac 上交叉編譯 + 複製進 VM：
```bash
GOOS=linux GOARCH=arm64 go build -o /tmp/orbtest/cockpit-linux ./cmd/cockpit
orb -m cockpit-test sh -c 'pkill cockpit-linux 2>/dev/null; cp /mnt/mac/tmp/orbtest/cockpit-linux /tmp/orbtest/ && chmod +x /tmp/orbtest/cockpit-linux && rm -f /tmp/ck.db && echo ok'
```
- [ ] **Step 2:** VM 內起 serve + agent（沿用 /tmp/orbtest/serve.json、agent.json；mac 端背景 orb 指令保活）。
- [ ] **Step 3:** 等 ~35s 後驗證：
```bash
curl -s http://cockpit-test.orb.local:8787/api/systems | python3 -m json.tool
```
預期：machine `vm1`（inventory token 自動建立）有 cpu/mem/disk/load/uptime 真值、spark 漸長、status online。
- [ ] **Step 4:** `curl -s "http://cockpit-test.orb.local:8787/api/systems/<id>/metrics?range=1h"` → 1m 點陣 ≥2 筆，cpu/mem 合理。
- [ ] **Step 5:**（VM 內若有 docker）`orb -m cockpit-test sh -c 'docker run -d --name p2redis -p 6379:6379 redis:alpine'` 後等 70s → `curl /api/services` 應出現 p2redis。沒 docker 則跳過此步並註記。
- [ ] **Step 6:** 降採樣驗證：等到 10 分鐘窗完結（或暫時把 serve 的排程睡眠調短重 build——不必，直接等）→ `curl "…/metrics?range=12h"` 出現 10m 點。若驗收時間不允許等待，改在 mac 上用 go test 既有 Downsample 單元測試佐證即可，並註記。
- [ ] **Step 7:** 收尾 pkill + 移除測試容器；記錄結果。

---

## Self-Review（已執行）

1. **Spec 覆蓋（§5/§6/§9/§9.1/§10 的 P2 部分）**：metrics/metrics_latest/services/vms 表→T1；降採樣+保留→T2；gopsutil+GPU→T3；docker→T4；VMware Fusion 列舉→T5；report-metrics/services/vms + 統一 resolver + 對帳→T6；systems enriched/metrics range/services/vms 瀏覽器 API + status 判定→T7；agent 迴圈+serve 排程→T8；VM e2e→T9。**前端接線（topology/machine/trends）另立 P2-frontend**；enrollment 管理（§11）屬 P3；`GET /api/machines` 留 P2-frontend 視需要。
2. **Placeholder**：T6 內標明「metricsReport 殘渣勿寫入」與 TouchSystem 補充函式皆有完整碼；無 TBD。
3. **型別一致**：store.MetricRow/ServiceRow/VMRow/SystemsWithLatest 與 server/agent 用法一致；collect.Metrics json tag 與 metricsBody 對齊；vmenum.VM json tag 與 reportVMs body 對齊；dockerstat.Service json tag 與 reportServices body 對齊（port/cpu/mem/name/kind/status；software_ids/depends agent 端不送、server 容忍缺）。
4. **風險註記**：gopsutil sensors API 名稱以實際版本為準（T3 內註明 fallback）；mac 上 temp/gpu 常 nil 屬正常；OrbStack VM 內 load/temp 可能空。對帳 P2 僅 label==vm.Name。
