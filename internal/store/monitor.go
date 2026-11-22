package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

type MetricRow struct {
	TS                                                      int64
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
	// 保留既有手動/自動連結：以 (host,uuid) 帶過 linked_system_id（新回報不含連結資訊）。
	existing := map[string]string{}
	if cur, err := s.ListVMs(); err == nil {
		for _, v := range cur {
			if v.HostSystemID == hostSystemID && v.LinkedSystemID != "" {
				existing[v.UUID] = v.LinkedSystemID
			}
		}
	}
	for i := range rows {
		if rows[i].LinkedSystemID == "" {
			rows[i].LinkedSystemID = existing[rows[i].UUID]
		}
	}
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

// UnlinkVM：手動取消連結——清除 vms.linked_system_id，把 guest system 還原為 kind=physical + host_id=NULL。
func (s *Store) UnlinkVM(hostSystemID, uuid string) error {
	// Find linked_system_id before clearing it.
	var linkedID sql.NullString
	s.db.QueryRow(`SELECT linked_system_id FROM vms WHERE host_system_id=? AND uuid=?`, hostSystemID, uuid).Scan(&linkedID)
	if _, err := s.db.Exec(`UPDATE vms SET linked_system_id=NULL WHERE host_system_id=? AND uuid=?`, hostSystemID, uuid); err != nil {
		return err
	}
	if linkedID.Valid && linkedID.String != "" {
		_, err := s.db.Exec(`UPDATE systems SET kind='physical', host_id=NULL WHERE id=?`, linkedID.String)
		return err
	}
	return nil
}

// VMByHostAndUUID looks up a single VM row.
func (s *Store) VMByHostAndUUID(hostSystemID, uuid string) (VMRow, error) {
	var r VMRow
	var vmx, gos, link sql.NullString
	var vcpu, ram sql.NullInt64
	err := s.db.QueryRow(`SELECT host_system_id,name,uuid,vmx_path,state,vcpu,ram_mb,guest_os,linked_system_id FROM vms WHERE host_system_id=? AND uuid=?`, hostSystemID, uuid).
		Scan(&r.HostSystemID, &r.Name, &r.UUID, &vmx, &r.State, &vcpu, &ram, &gos, &link)
	if err == sql.ErrNoRows {
		return VMRow{}, ErrNotFound
	}
	if err != nil {
		return VMRow{}, err
	}
	r.VmxPath, r.GuestOS, r.LinkedSystemID = vmx.String, gos.String, link.String
	r.VCPU, r.RamMB = int(vcpu.Int64), int(ram.Int64)
	return r, nil
}

// 聚合層級：dst ← src，bucket 秒數與保留秒數。
var dsLevels = []struct {
	Dst, Src  string
	BucketSec int64
	RetainSec int64 // dst 的保留期（PruneMetrics 用）
}{
	{"10m", "1m", 600, 14 * 3600},
	{"15m", "1m", 900, 26 * 3600},
	{"60m", "15m", 3600, 8 * 24 * 3600},
	{"480m", "60m", 28800, 32 * 24 * 3600},
}

const retain1m = 2 * 3600

// Downsample：把 src 聚合進 dst，依 bucket_start = (ts/BucketSec)*BucketSec 分組。冪等（INSERT OR REPLACE）。
// 下限取來源 type 的 prune 水位 wm（PruneMetrics 記錄），向上取整到桶邊界：水位之前的桶可能已被部分刪除，不重算。
// 無水位時改由 guessWatermark 判斷；都沒有才不設下限，停機超過保留期時舊來源仍會聚合。
// ts < now 保留，最新的未完結桶仍會持續更新（趨勢圖需要）。
func (s *Store) Downsample(now int64) error {
	for _, lv := range dsLevels {
		B := lv.BucketSec
		lb := int64(math.MinInt64)
		var wm sql.NullInt64
		err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM settings WHERE key=?`, "metrics_prune_wm:"+lv.Src).Scan(&wm)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !wm.Valid {
			if wm, err = s.guessWatermark(lv.Src, lv.Dst, B); err != nil {
				return err
			}
		}
		if wm.Valid {
			lb = (wm.Int64 + B - 1) / B * B
		}
		_, err = s.db.Exec(`
			INSERT OR REPLACE INTO metrics (system_id,type,ts,cpu,mem,disk,gpu,net_up,net_down,load,temp)
			SELECT m.system_id, ?, (m.ts/?)*?,
			  AVG(m.cpu), AVG(m.mem), AVG(m.disk), AVG(m.gpu), AVG(m.net_up), AVG(m.net_down), AVG(m.load), AVG(m.temp)
			FROM metrics m
			WHERE m.type=? AND m.ts<? AND m.ts>=?
			GROUP BY m.system_id, m.ts/?`,
			lv.Dst, B, B, lv.Src, now, lb, B)
		if err != nil {
			return err
		}
	}
	return nil
}

// guessWatermark：無水位（升級前已 prune、尚未寫入水位）時的保守下限。
// 來源最早桶若已在 dst 有列，代表它在 prune 前就已聚合，現在可能殘缺，回傳現存最早 ts；否則回 NULL（不設下限）。
// 不會誤傷「從未 prune、首筆不在邊界」：此時 dst 還沒有該桶的列。
// shortcut: 只以「dst 已有該桶列」判斷 prune 痕跡，dst 也被清掉時退回不設下限；有水位後即不再進來。
func (s *Store) guessWatermark(src, dst string, B int64) (sql.NullInt64, error) {
	var minTS sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(ts) FROM metrics WHERE type=?`, src).Scan(&minTS); err != nil {
		return sql.NullInt64{}, err
	}
	if !minTS.Valid {
		return sql.NullInt64{}, nil
	}
	var seen int
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM metrics WHERE type=? AND ts=?)`, dst, minTS.Int64/B*B).Scan(&seen); err != nil {
		return sql.NullInt64{}, err
	}
	if seen == 0 {
		return sql.NullInt64{}, nil
	}
	return minTS, nil
}

// PruneMetrics：刪各 type 超過保留期的列，並記下各 type 的 cutoff 作為水位。
func (s *Store) PruneMetrics(now int64) error {
	if err := s.pruneType("1m", now-retain1m); err != nil {
		return err
	}
	for _, lv := range dsLevels {
		if err := s.pruneType(lv.Dst, now-lv.RetainSec); err != nil {
			return err
		}
	}
	return nil
}

// pruneType 刪 typ 中 ts < cutoff 的列，並把 cutoff 寫入水位 key。水位只增不減，舊的較小 cutoff 不能把它往回拉。
func (s *Store) pruneType(typ string, cutoff int64) error {
	if _, err := s.db.Exec(`DELETE FROM metrics WHERE type=? AND ts < ?`, typ, cutoff); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
		WHERE CAST(excluded.value AS INTEGER) > CAST(settings.value AS INTEGER)`,
		"metrics_prune_wm:"+typ, strconv.FormatInt(cutoff, 10))
	return err
}

// TouchSystem updates last_seen and sets status=online for a system.
func (s *Store) TouchSystem(id string) error {
	_, err := s.db.Exec(`UPDATE systems SET last_seen=datetime('now'), status='online' WHERE id=?`, id)
	return err
}
