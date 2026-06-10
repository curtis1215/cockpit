package translate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// fakeLM 模擬 LM Studio 的 OpenAI 相容端點，回傳指定 content 並記錄收到的請求。
func fakeLM(t *testing.T, content string, gotReq *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		if gotReq != nil {
			*gotReq = body
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": content}, "finish_reason": "stop"},
			},
		})
	}))
}

func TestHTTPTranslate(t *testing.T) {
	var got map[string]any
	srv := fakeLM(t, "  中文摘要\n", &got)
	defer srv.Close()

	cfg := Config{Endpoint: srv.URL, Model: "google/gemma-4-26b-a4b-qat", MaxTokens: 8192}
	tr := NewDynamic(func() Config { return cfg }, "")
	out := tr.Changelog("## 1.0\n- fix bug")
	if out != "中文摘要" {
		t.Fatalf("got %q", out)
	}
	if got["model"] != "google/gemma-4-26b-a4b-qat" {
		t.Fatalf("model = %v", got["model"])
	}
	if mt, _ := got["max_tokens"].(float64); mt != 8192 {
		t.Fatalf("max_tokens = %v", got["max_tokens"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", got["messages"])
	}
	m0, _ := msgs[0].(map[string]any)
	c, _ := m0["content"].(string)
	if !strings.Contains(c, "fix bug") || !strings.Contains(c, "技術翻譯") {
		t.Fatalf("prompt missing raw/template: %q", c)
	}
}

func TestHTTPMaxTokensDefault(t *testing.T) {
	var got map[string]any
	srv := fakeLM(t, "ok", &got)
	defer srv.Close()

	tr := NewDynamic(func() Config { return Config{Endpoint: srv.URL, Model: "m"} }, "")
	if tr.Changelog("raw") != "ok" {
		t.Fatal("translate failed")
	}
	if mt, _ := got["max_tokens"].(float64); mt != 4096 {
		t.Fatalf("default max_tokens = %v, want 4096", got["max_tokens"])
	}
}

func TestHTTPEndpointNormalize(t *testing.T) {
	srv := fakeLM(t, "ok", nil)
	defer srv.Close()
	for _, ep := range []string{srv.URL, srv.URL + "/", srv.URL + "/v1", srv.URL + "/v1/"} {
		tr := NewDynamic(func() Config { return Config{Endpoint: ep, Model: "m"} }, "")
		if out := tr.Changelog("raw"); out != "ok" {
			t.Fatalf("endpoint %q: got %q", ep, out)
		}
	}
}

func TestHTTPEmptyContent(t *testing.T) {
	// reasoning 模型把 token 吃光時 content 為空——必須回 ""，讓呼叫端記 error event。
	srv := fakeLM(t, "", nil)
	defer srv.Close()
	tr := NewDynamic(func() Config { return Config{Endpoint: srv.URL, Model: "m"} }, "")
	if out := tr.Changelog("raw"); out != "" {
		t.Fatalf("empty content should yield empty, got %q", out)
	}
}

func TestHTTPServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	tr := NewDynamic(func() Config { return Config{Endpoint: srv.URL, Model: "m"} }, "")
	if out := tr.Changelog("raw"); out != "" {
		t.Fatalf("http 500 should yield empty, got %q", out)
	}
}

func TestDynamicFallbackToCmd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	// endpoint 未設定 → 走 shell fallback（cat 原樣回吐 stdin）。
	tr := NewDynamic(func() Config { return Config{} }, "cat")
	out := tr.Changelog("hello-raw")
	if !strings.Contains(out, "hello-raw") {
		t.Fatalf("fallback path: %q", out)
	}
}

func TestDynamicHotSwitch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	srv := fakeLM(t, "via-http", nil)
	defer srv.Close()
	cfg := Config{}
	tr := NewDynamic(func() Config { return cfg }, "cat")
	if out := tr.Changelog("raw1"); !strings.Contains(out, "raw1") {
		t.Fatalf("before switch: %q", out)
	}
	cfg = Config{Endpoint: srv.URL, Model: "m"} // 模擬 WebUI 改設定後即時生效
	if out := tr.Changelog("raw2"); out != "via-http" {
		t.Fatalf("after switch: %q", out)
	}
}
