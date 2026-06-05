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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}

func TestVersionEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	// default: empty version → "dev"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/version", nil))
	if rec.Code != 200 || !contains(rec.Body.String(), `"dev"`) {
		t.Fatalf("version (default): %d %s", rec.Code, rec.Body.String())
	}

	// set version → returned verbatim
	srv.SetVersion("v0.1.5")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("GET", "/api/version", nil))
	if rec2.Code != 200 || !contains(rec2.Body.String(), "v0.1.5") {
		t.Fatalf("version (set): %d %s", rec2.Code, rec2.Body.String())
	}
}

var _ = http.MethodGet
