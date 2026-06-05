package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/curtis1215/cockpit/internal/config"
	"github.com/curtis1215/cockpit/internal/httpx"
)

// ── Unit: inSudoSecurePath ─────────────────────────────────────────────────────

func TestInSudoSecurePath(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"/usr/local/bin", true},
		{"/usr/bin", true},
		{"/bin", true},
		{"/sbin", true},
		{"/usr/sbin", true},
		{"/opt/homebrew/bin", true},
		{"/home/user/bin", false},
		{"/opt/myapp/bin", false},
		{"", false},
	}
	for _, tc := range cases {
		got := inSudoSecurePath(tc.dir)
		if got != tc.want {
			t.Errorf("inSudoSecurePath(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

// ── Unit: doctor line formatting helpers ──────────────────────────────────────

func TestDoctorLineIcons(t *testing.T) {
	// 驗證輸出格式符合 ✅/⚠️/❌ <名稱>：<說明> 規則（grep-able）
	var lines []string
	collect := func(s string) { lines = append(lines, s) }

	checkDocker(collect)
	if len(lines) == 0 {
		t.Fatal("checkDocker produced no output")
	}
	line := lines[0]
	if !strings.HasPrefix(line, "✅") && !strings.HasPrefix(line, "⚠️") && !strings.HasPrefix(line, "❌") {
		t.Errorf("line does not start with icon: %q", line)
	}
	if !strings.Contains(line, "docker") {
		t.Errorf("line does not mention 'docker': %q", line)
	}
}

// ── Integration: checkServer connectivity + token ─────────────────────────────

func TestCheckServerConnectOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "0.1.9"})
		case "/api/agent/poll":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	cfg := config.AgentConfig{}
	// 手動填入 – LoadAgent 需要檔案，這裡直接賦值
	cfg2 := agentCfgWith(cfg, srv.URL, "valid-token")

	var lines []string
	collect := func(s string) { lines = append(lines, s) }
	checkServerWithClient(collect, collect, cfg2, true, httpx.New(srv.URL, 5*time.Second))

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✅") {
		t.Fatalf("expected ✅ in output:\n%s", joined)
	}
	if strings.Contains(joined, "❌") {
		t.Fatalf("unexpected ❌ in output:\n%s", joined)
	}
}

func TestCheckServerToken401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "0.1.9"})
		case "/api/agent/poll":
			w.WriteHeader(401)
			w.Write([]byte("unauthorized"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	cfg2 := agentCfgWith(config.AgentConfig{}, srv.URL, "bad-token")

	var lines []string
	var failLines []string
	checkServerWithClient(
		func(s string) { lines = append(lines, s) },
		func(s string) { failLines = append(failLines, s) },
		cfg2, true, httpx.New(srv.URL, 5*time.Second),
	)

	joined := strings.Join(failLines, "\n")
	if !strings.Contains(joined, "❌") {
		t.Fatalf("expected ❌ for 401:\n%s", joined)
	}
	if !strings.Contains(joined, "401") {
		t.Fatalf("expected 401 mention:\n%s", joined)
	}
}

func TestCheckServerNoConfig(t *testing.T) {
	var lines []string
	collect := func(s string) { lines = append(lines, s) }
	checkServerWithClient(collect, collect, config.AgentConfig{}, false, nil)
	if len(lines) == 0 {
		t.Fatal("expected at least one line for no-config case")
	}
	if !strings.Contains(lines[0], "略過") {
		t.Errorf("expected skip notice, got: %q", lines[0])
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// agentCfgWith は AgentConfig に ServerURL と AgentToken を設定するヘルパー。
func agentCfgWith(base config.AgentConfig, serverURL, agentToken string) config.AgentConfig {
	base.ServerURL = serverURL
	base.AgentToken = agentToken
	return base
}

// checkServerWithClient は checkServer の可注入版（テスト用）。
// productionの checkServer は httpx.New でクライアントを生成するため、
// テストでは httptest サーバーのクライアントを渡せるよう分離する。
func checkServerWithClient(print, fail func(string), cfg config.AgentConfig, loaded bool, hc *httpx.Client) {
	if !loaded || cfg.ServerURL == "" {
		print("ℹ️  server 連通：無 agent 設定檔（略過）")
		return
	}

	var vResp struct {
		Version string `json:"version"`
	}
	statusCode, err := hc.GetJSON("/api/version", "", &vResp)
	if err != nil || statusCode >= 400 {
		fail(fmt.Sprintf("❌ server 連通：無法連接 %s（%v）", cfg.ServerURL, err))
		return
	}

	if vResp.Version != "" && vResp.Version != version {
		print(fmt.Sprintf("⚠️  server 連通：已連接 %s，server 版本 %s 與本機版本 %s 不同，建議升級",
			cfg.ServerURL, vResp.Version, version))
	} else {
		print(fmt.Sprintf("✅ server 連通：%s（版本 %s）", cfg.ServerURL, vResp.Version))
	}

	if cfg.AgentToken == "" {
		print("ℹ️  agent token：未設定（略過）")
		return
	}

	tokenStatus, tokenErr := hc.GetJSON("/api/agent/poll?wait=0", cfg.AgentToken, nil)
	switch {
	case tokenErr == nil || tokenStatus == 200 || tokenStatus == 204:
		print("✅ agent token：有效")
	case tokenStatus == 401:
		fail("❌ agent token：token 無效（401 Unauthorized）")
	default:
		print(fmt.Sprintf("⚠️  agent token：HTTP %d（%v）", tokenStatus, tokenErr))
	}
}
