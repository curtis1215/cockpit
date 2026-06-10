package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/store"
)

func trServer(t *testing.T) (*Server, *store.Store) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	t.Cleanup(func() { st.Close() })
	return New(st, "s3cret"), st
}

func TestTranslateConfigGetDefault(t *testing.T) {
	srv, _ := trServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/config", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Endpoint  string `json:"endpoint"`
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != "" || got.Model != "" || got.MaxTokens != 0 {
		t.Fatalf("default should be empty: %+v", got)
	}
}

func TestTranslateConfigPutAndGet(t *testing.T) {
	srv, st := trServer(t)
	body := `{"endpoint":"http://100.73.202.65:1234","model":"google/gemma-4-26b-a4b-qat","max_tokens":4096}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/api/translate/config", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("put code %d: %s", rec.Code, rec.Body)
	}
	if v := st.GetSetting("translate.endpoint"); v != "http://100.73.202.65:1234" {
		t.Fatalf("endpoint stored %q", v)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/config", nil))
	b := rec.Body.String()
	if !strings.Contains(b, "gemma-4-26b") || !strings.Contains(b, "4096") {
		t.Fatalf("get after put: %s", b)
	}
}

func TestTranslateConfigClear(t *testing.T) {
	srv, st := trServer(t)
	st.SetSetting("translate.endpoint", "http://x:1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/api/translate/config",
		strings.NewReader(`{"endpoint":"","model":"","max_tokens":0}`)))
	if rec.Code != 200 {
		t.Fatalf("clear code %d: %s", rec.Code, rec.Body)
	}
	if v := st.GetSetting("translate.endpoint"); v != "" {
		t.Fatalf("endpoint should be cleared, got %q", v)
	}
}

func TestTranslateConfigValidation(t *testing.T) {
	srv, _ := trServer(t)
	cases := []string{
		`{"endpoint":"not-a-url","model":"m","max_tokens":4096}`,
		`{"endpoint":"ftp://h:1","model":"m","max_tokens":4096}`,
		`{"endpoint":"http://h:1","model":"m","max_tokens":-1}`,
		`{"endpoint":"http://h:1","model":"","max_tokens":4096}`, // 設了端點就必須給模型
		`not json`,
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/api/translate/config", strings.NewReader(c)))
		if rec.Code != 400 {
			t.Fatalf("case %s: code %d", c, rec.Code)
		}
	}
}

func TestTranslateConfigMethodNotAllowed(t *testing.T) {
	srv, _ := trServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/translate/config", nil))
	if rec.Code != 405 {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestTranslateModelsProxy(t *testing.T) {
	lm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"data":[{"id":"google/gemma-4-26b-a4b-qat"},{"id":"qwen/qwen3.6-35b-a3b"}],"object":"list"}`))
	}))
	defer lm.Close()

	srv, _ := trServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models?endpoint="+lm.URL, nil))
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[0] != "google/gemma-4-26b-a4b-qat" {
		t.Fatalf("models: %v", got.Models)
	}
}

func TestTranslateModelsProxySavedEndpoint(t *testing.T) {
	lm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer lm.Close()

	srv, st := trServer(t)
	st.SetSetting("translate.endpoint", lm.URL)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "m1") {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
}

func TestTranslateModelsProxyErrors(t *testing.T) {
	srv, _ := trServer(t)
	// 沒給 endpoint 也沒存設定 → 400
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models", nil))
	if rec.Code != 400 {
		t.Fatalf("no endpoint: code %d", rec.Code)
	}
	// 連不上 → 502
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models?endpoint=http://127.0.0.1:1", nil))
	if rec.Code != 502 {
		t.Fatalf("unreachable: code %d", rec.Code)
	}
}
