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
