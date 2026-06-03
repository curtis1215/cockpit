package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnrollThenHeartbeat(t *testing.T) {
	srv, st := newTestServer(t) // enrollSecret = "s3cret"

	bad := httptest.NewRecorder()
	srv.Handler().ServeHTTP(bad, httptest.NewRequest("POST", "/api/agent/enroll",
		strings.NewReader(`{"label":"box","os":"linux","arch":"amd64","enroll_secret":"wrong"}`)))
	if bad.Code != 401 {
		t.Fatalf("bad secret want 401 got %d", bad.Code)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/agent/enroll",
		strings.NewReader(`{"label":"box","os":"linux","arch":"amd64","enroll_secret":"s3cret"}`)))
	if rec.Code != 200 {
		t.Fatalf("enroll want 200 got %d (%s)", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"agent_token":"ck_agent_`) {
		t.Fatalf("no agent_token: %s", rec.Body.String())
	}
	tok := extractToken(rec.Body.String())

	noauth := httptest.NewRecorder()
	srv.Handler().ServeHTTP(noauth, httptest.NewRequest("POST", "/api/agent/heartbeat",
		strings.NewReader(`{"agent_version":"0.1.0"}`)))
	if noauth.Code != 401 {
		t.Fatalf("heartbeat noauth want 401 got %d", noauth.Code)
	}

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
