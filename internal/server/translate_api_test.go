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

// 只存端點、暫不選 model 是允許的（前端「拉取模型」會先存端點再拉清單）。
func TestTranslateConfigEndpointWithoutModel(t *testing.T) {
	srv, st := trServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/api/translate/config",
		strings.NewReader(`{"endpoint":"http://h:1234","model":"","max_tokens":4096}`)))
	if rec.Code != 200 {
		t.Fatalf("endpoint without model should be allowed: code %d: %s", rec.Code, rec.Body)
	}
	if st.GetSetting("translate.endpoint") != "http://h:1234" {
		t.Fatal("endpoint not stored")
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

// 只對「已儲存的端點」拉模型——不接受任意 query endpoint（SSRF 防護）。
func TestTranslateModelsUsesSavedEndpoint(t *testing.T) {
	lm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"data":[{"id":"google/gemma-4-26b-a4b-qat"},{"id":"qwen/qwen3.6-35b-a3b"}],"object":"list"}`))
	}))
	defer lm.Close()

	srv, st := trServer(t)
	st.SetSetting("translate.endpoint", lm.URL)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models", nil))
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

// query endpoint 必須被忽略，不得誘導 server 對任意 host 發出請求（SSRF）。
func TestTranslateModelsIgnoresQueryEndpoint(t *testing.T) {
	var probed bool
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probed = true
		w.Write([]byte(`{"data":[{"id":"leaked"}]}`))
	}))
	defer attacker.Close()

	srv, _ := trServer(t) // 沒有已存端點
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models?endpoint="+attacker.URL, nil))
	if rec.Code != 400 {
		t.Fatalf("query endpoint must not be honored; code %d: %s", rec.Code, rec.Body)
	}
	if probed {
		t.Fatal("server made a request to the query-supplied endpoint (SSRF)")
	}
}

func TestTranslateModelsNoSavedEndpoint(t *testing.T) {
	srv, _ := trServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models", nil))
	if rec.Code != 400 {
		t.Fatalf("no saved endpoint should 400: code %d", rec.Code)
	}
}

// 已存端點連不上 → 502（這不是 SSRF：端點是管理員存進設定的）。
func TestTranslateModelsSavedUnreachable(t *testing.T) {
	srv, st := trServer(t)
	st.SetSetting("translate.endpoint", "http://127.0.0.1:1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/translate/models", nil))
	if rec.Code != 502 {
		t.Fatalf("unreachable saved endpoint: code %d", rec.Code)
	}
}
