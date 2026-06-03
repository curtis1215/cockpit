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
		Token:     "",
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
