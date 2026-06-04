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
var ErrConflict = errors.New("conflict")

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
	EnrollToken  string `json:"-"`
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
// RegisterSystem upserts a system by label. If the label already exists (e.g. an
// agent that lost its token re-enrolling under the same hostname), the existing row
// is reused and its agent_token is rotated (old token invalidated); the existing id
// and a fresh token are returned. A new label inserts a fresh row.
func (s *Store) RegisterSystem(label, osName, arch string) (string, string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	token := "ck_agent_" + randHex(20)

	// Re-enroll path: label exists → rotate token on existing row.
	var existingID string
	err := s.db.QueryRow(`SELECT id FROM systems WHERE label=?`, label).Scan(&existingID)
	if err == nil {
		if _, err := s.db.Exec(
			`UPDATE systems SET agent_token=?, os=?, arch=?, status='online', agent_status='ok', last_seen=? WHERE id=?`,
			token, osName, arch, now, existingID); err != nil {
			return "", "", err
		}
		return existingID, token, nil
	}
	if err != sql.ErrNoRows {
		return "", "", err
	}

	// New label → insert fresh row.
	id := "sys_" + randHex(6)
	_, err = s.db.Exec(
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
	var hostID, agentToken, enrollToken sql.NullString
	err := row.Scan(&s.ID, &s.Label, &s.Role, &s.OS, &s.Arch, &s.Kind, &hostID,
		&s.Status, &s.AgentVersion, &s.AgentStatus, &s.LastSeen, &agentToken, &enrollToken, &s.Created)
	s.HostID = hostID.String
	s.AgentToken = agentToken.String
	s.EnrollToken = enrollToken.String
	return s, err
}

const cols = "id,label,role,os,arch,kind,host_id,status,agent_version,agent_status,last_seen,agent_token,enroll_token,created"

func (s *Store) SystemByID(id string) (System, error) {
	row := s.db.QueryRow("SELECT "+cols+" FROM systems WHERE id=?", id)
	sys, err := scanSystem(row)
	if err == sql.ErrNoRows {
		return System{}, ErrNotFound
	}
	return sys, err
}

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

// HeartbeatByID updates last_seen, status, agent_status, and agent_version for a system by its ID.
func (s *Store) HeartbeatByID(systemID, agentVersion string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`UPDATE systems SET status='online', agent_status='ok', agent_version=?, last_seen=? WHERE id=?`,
		agentVersion, now, systemID)
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

// ── Machine lifecycle management ─────────────────────────────────────────────

// CreateSystemPending creates a pending system with a one-time enroll token.
// Returns (id, enrollToken, error). If label already exists, returns ErrConflict.
func (s *Store) CreateSystemPending(label, role string) (string, string, error) {
	id := "sys_" + randHex(6)
	enrollToken := "ck_enroll_" + randHex(12)
	_, err := s.db.Exec(
		`INSERT INTO systems (id,label,role,kind,status,agent_status,enroll_token,created)
		 VALUES (?,?,?,'physical','pending','pending',?,unixepoch())`,
		id, label, role, enrollToken)
	if err != nil {
		// SQLite unique constraint violation on label or enroll_token
		if isUniqueErr(err) {
			return "", "", ErrConflict
		}
		return "", "", err
	}
	return id, enrollToken, nil
}

// isUniqueErr checks if an error is a SQLite unique constraint violation.
func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "UNIQUE constraint failed") || contains(s, "unique constraint")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// SystemByEnrollToken looks up a system by its enroll token.
func (s *Store) SystemByEnrollToken(token string) (System, error) {
	row := s.db.QueryRow("SELECT "+cols+" FROM systems WHERE enroll_token=?", token)
	sys, err := scanSystem(row)
	if err == sql.ErrNoRows {
		return System{}, ErrNotFound
	}
	return sys, err
}

// ConsumeEnrollToken activates a pending system: sets os/arch/status/agent_token, clears enroll_token.
// Returns the new agent token.
func (s *Store) ConsumeEnrollToken(id, osName, arch string) (string, error) {
	agentToken := "ck_agent_" + randHex(20)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`UPDATE systems SET os=?, arch=?, status='online', agent_status='ok',
		 last_seen=?, agent_token=?, enroll_token=NULL WHERE id=? AND enroll_token IS NOT NULL`,
		osName, arch, now, agentToken, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return agentToken, nil
}

// RegenEnrollToken generates a new enroll token for a system (invalidating the old one).
func (s *Store) RegenEnrollToken(id string) (string, error) {
	newToken := "ck_enroll_" + randHex(12)
	res, err := s.db.Exec(`UPDATE systems SET enroll_token=? WHERE id=?`, newToken, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return newToken, nil
}

// UpdateSystem updates label and/or role of a system. Empty string means keep current value.
func (s *Store) UpdateSystem(id, label, role string) error {
	if label == "" && role == "" {
		return nil
	}
	if label != "" && role != "" {
		res, err := s.db.Exec(`UPDATE systems SET label=?, role=? WHERE id=?`, label, role, id)
		if err != nil {
			if isUniqueErr(err) {
				return ErrConflict
			}
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	}
	if label != "" {
		res, err := s.db.Exec(`UPDATE systems SET label=? WHERE id=?`, label, id)
		if err != nil {
			if isUniqueErr(err) {
				return ErrConflict
			}
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	}
	// role only
	res, err := s.db.Exec(`UPDATE systems SET role=? WHERE id=?`, role, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSystemCascade removes a system and all its associated data.
func (s *Store) DeleteSystemCascade(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM metrics WHERE system_id=?`,
		`DELETE FROM metrics_latest WHERE system_id=?`,
		`DELETE FROM services WHERE system_id=?`,
		`DELETE FROM vms WHERE host_system_id=?`,
		`UPDATE vms SET linked_system_id=NULL WHERE linked_system_id=?`,
		`DELETE FROM systems WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LabelExists checks if any system has the given label (used for unique label enforcement).
func (s *Store) LabelExists(label string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(1) FROM systems WHERE label=?`, label).Scan(&n)
	return n > 0
}

// ── Version tracker types and methods ───────────────────────────────────────

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

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
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

// CreateJobUnique creates a job only if no queued/running job exists for (software,machine).
// Returns the new job ID, or 0 if a duplicate exists.
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
	res, err := s.db.Exec(`UPDATE jobs SET status='running', started_at=datetime('now') WHERE id=? AND status='queued'`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Another caller claimed it between SELECT and UPDATE
		return nil, nil
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
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Job
	for _, id := range ids {
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
