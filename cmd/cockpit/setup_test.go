package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/curtis1215/cockpit/internal/config"
)

// ── writeServeConfig ───────────────────────────────────────────────────────────

func TestWriteServeConfig_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()

	path, created, err := writeServeConfig(dir, data, "0.0.0.0:8787")
	if err != nil {
		t.Fatalf("writeServeConfig: %v", err)
	}
	if !created {
		t.Error("created should be true for a new file")
	}
	if path != filepath.Join(dir, "serve.json") {
		t.Errorf("path = %q, want %q", path, filepath.Join(dir, "serve.json"))
	}

	// 驗證可用 config.LoadServe 解析
	cfg, err := config.LoadServe(path)
	if err != nil {
		t.Fatalf("LoadServe: %v", err)
	}
	if cfg.Listen != "0.0.0.0:8787" {
		t.Errorf("Listen = %q, want 0.0.0.0:8787", cfg.Listen)
	}
	if cfg.DBPath != filepath.Join(data, "cockpit.db") {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, filepath.Join(data, "cockpit.db"))
	}
	if len(cfg.EnrollSecret) < len("ck_secret_")+1 {
		t.Errorf("EnrollSecret too short: %q", cfg.EnrollSecret)
	}
	if cfg.EnrollSecret[:len("ck_secret_")] != "ck_secret_" {
		t.Errorf("EnrollSecret does not start with ck_secret_: %q", cfg.EnrollSecret)
	}
	if cfg.InventoryPath != filepath.Join(dir, "inventory.yaml") {
		t.Errorf("InventoryPath = %q, want %q", cfg.InventoryPath, filepath.Join(dir, "inventory.yaml"))
	}
	if cfg.CheckHours != 24 {
		t.Errorf("CheckHours = %d, want 24", cfg.CheckHours)
	}
}

func TestWriteServeConfig_DefaultListen(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()

	_, _, err := writeServeConfig(dir, data, "")
	if err != nil {
		t.Fatalf("writeServeConfig: %v", err)
	}
	cfg, err := config.LoadServe(filepath.Join(dir, "serve.json"))
	if err != nil {
		t.Fatalf("LoadServe: %v", err)
	}
	// LoadServe defaults 127.0.0.1:8787 if empty, but our writeServeConfig writes "0.0.0.0:8787"
	if cfg.Listen != "0.0.0.0:8787" {
		t.Errorf("Listen = %q, want 0.0.0.0:8787", cfg.Listen)
	}
}

func TestWriteServeConfig_KeepsExisting(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()

	// 第一次建立
	path, created1, err := writeServeConfig(dir, data, "0.0.0.0:8787")
	if err != nil {
		t.Fatalf("first writeServeConfig: %v", err)
	}
	if !created1 {
		t.Fatal("first call: created should be true")
	}

	// 讀取原始內容
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// 第二次不覆寫
	_, created2, err := writeServeConfig(dir, data, "127.0.0.1:9999")
	if err != nil {
		t.Fatalf("second writeServeConfig: %v", err)
	}
	if created2 {
		t.Error("second call: created should be false (file already exists)")
	}

	// 確認內容未變
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after: %v", err)
	}
	if string(origBytes) != string(afterBytes) {
		t.Error("existing file was overwritten, but should be kept")
	}
}

func TestWriteServeConfig_EnrollSecretUnique(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	data := t.TempDir()

	p1, _, _ := writeServeConfig(dir1, data, "")
	p2, _, _ := writeServeConfig(dir2, data, "")

	var m1, m2 map[string]any
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	_ = json.Unmarshal(b1, &m1)
	_ = json.Unmarshal(b2, &m2)

	s1, _ := m1["enroll_secret"].(string)
	s2, _ := m2["enroll_secret"].(string)
	if s1 == s2 {
		t.Errorf("enroll_secret should be unique per run, got same value: %q", s1)
	}
}

// ── writeAgentConfig ───────────────────────────────────────────────────────────

func TestWriteAgentConfig_CreatesFileWithToken(t *testing.T) {
	dir := t.TempDir()

	path, created, err := writeAgentConfig(dir, "http://cockpit.local:8787", "tok_abc123", "")
	if err != nil {
		t.Fatalf("writeAgentConfig: %v", err)
	}
	if !created {
		t.Error("created should be true for a new file")
	}
	if path != filepath.Join(dir, "agent.json") {
		t.Errorf("path = %q, want %q", path, filepath.Join(dir, "agent.json"))
	}

	cfg, err := config.LoadAgent(path)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if cfg.ServerURL != "http://cockpit.local:8787" {
		t.Errorf("ServerURL = %q", cfg.ServerURL)
	}
	if cfg.EnrollToken != "tok_abc123" {
		t.Errorf("EnrollToken = %q, want tok_abc123", cfg.EnrollToken)
	}
	if cfg.EnrollSecret != "" {
		t.Errorf("EnrollSecret should be empty, got %q", cfg.EnrollSecret)
	}
}

func TestWriteAgentConfig_CreatesFileWithSecret(t *testing.T) {
	dir := t.TempDir()

	_, _, err := writeAgentConfig(dir, "http://cockpit.local:8787", "", "mysecret")
	if err != nil {
		t.Fatalf("writeAgentConfig: %v", err)
	}
	cfg, err := config.LoadAgent(filepath.Join(dir, "agent.json"))
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if cfg.EnrollSecret != "mysecret" {
		t.Errorf("EnrollSecret = %q, want mysecret", cfg.EnrollSecret)
	}
	if cfg.EnrollToken != "" {
		t.Errorf("EnrollToken should be empty, got %q", cfg.EnrollToken)
	}
}

func TestWriteAgentConfig_KeepsExisting(t *testing.T) {
	dir := t.TempDir()

	path, created1, err := writeAgentConfig(dir, "http://original.local", "tok_orig", "")
	if err != nil {
		t.Fatalf("first writeAgentConfig: %v", err)
	}
	if !created1 {
		t.Fatal("first call: created should be true")
	}

	origBytes, _ := os.ReadFile(path)

	_, created2, err := writeAgentConfig(dir, "http://other.local", "tok_other", "")
	if err != nil {
		t.Fatalf("second writeAgentConfig: %v", err)
	}
	if created2 {
		t.Error("second call: created should be false")
	}

	afterBytes, _ := os.ReadFile(path)
	if string(origBytes) != string(afterBytes) {
		t.Error("existing file was overwritten")
	}
}

// ── writeInventoryIfMissing ───────────────────────────────────────────────────

func TestWriteInventoryIfMissing_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "inventory.yaml")

	if err := writeInventoryIfMissing(inv); err != nil {
		t.Fatalf("writeInventoryIfMissing: %v", err)
	}
	b, err := os.ReadFile(inv)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "machines: {}\nsoftware: []\n" {
		t.Errorf("inventory content = %q", string(b))
	}
}

func TestWriteInventoryIfMissing_KeepsExisting(t *testing.T) {
	dir := t.TempDir()
	inv := filepath.Join(dir, "inventory.yaml")

	orig := "machines:\n  myhost: {}\nsoftware: []\n"
	if err := os.WriteFile(inv, []byte(orig), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := writeInventoryIfMissing(inv); err != nil {
		t.Fatalf("writeInventoryIfMissing: %v", err)
	}
	b, _ := os.ReadFile(inv)
	if string(b) != orig {
		t.Errorf("inventory was changed: got %q", string(b))
	}
}

// ── buildUIURL ─────────────────────────────────────────────────────────────────

func TestBuildUIURL_WithHostname(t *testing.T) {
	url := buildUIURL("myhost:8787")
	if url != "http://myhost:8787/" {
		t.Errorf("buildUIURL = %q, want http://myhost:8787/", url)
	}
}

func TestBuildUIURL_AllInterface(t *testing.T) {
	url := buildUIURL("0.0.0.0:8787")
	// 應把 0.0.0.0 替換為 hostname 或 localhost
	if url == "http://0.0.0.0:8787/" {
		t.Error("buildUIURL should replace 0.0.0.0 with a real hostname")
	}
}
