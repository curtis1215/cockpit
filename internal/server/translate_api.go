package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/translate"
)

// settings keys（與 internal/translate.Config 對應）
const (
	setTranslateEndpoint   = "translate.endpoint"
	setTranslateModel      = "translate.model"
	setTranslateMaxTokens  = "translate.max_tokens"
	setTranslateTimeoutSec = "translate.timeout_sec"
	setTranslateAPIKey     = "translate.api_key"
)

// proxyClient 給 models 代理用：短 timeout、共用連線。
var proxyClient = &http.Client{Timeout: 10 * time.Second}

func (s *Server) registerTranslateAPI() {
	s.mux.HandleFunc("/api/translate/config", s.handleTranslateConfig)
	s.mux.HandleFunc("/api/translate/models", s.handleTranslateModels)
}

// clampTimeout 將 timeout_sec clamp 到 [30, 900]；<=0 視為預設 300。
func clampTimeout(sec int) int {
	if sec <= 0 {
		return 300
	}
	if sec < 30 {
		return 30
	}
	if sec > 900 {
		return 900
	}
	return sec
}

// TranslateConfig 讀取目前儲存的翻譯端點設定（供 serve.go 注入 translate.NewDynamic）。
// TimeoutSec 未設定或 0 時回傳 effective 300。含 ApiKey 明文（僅 server 內部使用）。
func (s *Server) TranslateConfig() translate.Config {
	maxTokens, _ := strconv.Atoi(s.st.GetSetting(setTranslateMaxTokens))
	timeoutSec, _ := strconv.Atoi(s.st.GetSetting(setTranslateTimeoutSec))
	return translate.Config{
		Endpoint:   s.st.GetSetting(setTranslateEndpoint),
		Model:      s.st.GetSetting(setTranslateModel),
		MaxTokens:  maxTokens,
		TimeoutSec: clampTimeout(timeoutSec),
		ApiKey:     s.st.GetSetting(setTranslateAPIKey),
	}
}

// translateConfigPublic 是 GET /api/translate/config 的線上格式；不含 API key 明文。
type translateConfigPublic struct {
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	MaxTokens  int    `json:"max_tokens"`
	TimeoutSec int    `json:"timeout_sec"`
	APIKeySet  bool   `json:"api_key_set"`
}

// translateConfigPut 是 PUT body。ApiKey 用指標：
//   - nil（欄位省略）→ 保留既有 key
//   - 非空字串 → 覆寫
//   - "" → 清除
type translateConfigPut struct {
	Endpoint   string  `json:"endpoint"`
	Model      string  `json:"model"`
	MaxTokens  int     `json:"max_tokens"`
	TimeoutSec int     `json:"timeout_sec"`
	ApiKey     *string `json:"api_key"`
}

// validEndpointURL 驗證使用者提供的端點是 host 非空的 http(s) URL。
func validEndpointURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *Server) handleTranslateConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.TranslateConfig()
		writeJSON(w, 200, translateConfigPublic{
			Endpoint:   cfg.Endpoint,
			Model:      cfg.Model,
			MaxTokens:  cfg.MaxTokens,
			TimeoutSec: cfg.TimeoutSec,
			APIKeySet:  strings.TrimSpace(cfg.ApiKey) != "",
		})
	case http.MethodPut:
		var body translateConfigPut
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad json"})
			return
		}
		if body.MaxTokens < 0 {
			writeJSON(w, 400, map[string]string{"error": "max_tokens must be >= 0"})
			return
		}
		if body.Endpoint != "" && !validEndpointURL(body.Endpoint) {
			writeJSON(w, 400, map[string]string{"error": "endpoint must be a http(s) URL"})
			return
		}
		// model 可暫空（前端「拉取模型」會先存端點再拉清單）；翻譯端在 model 空時
		// 走 fallback，不會用空 model 打壞請求。
		// 單一 transaction 寫入：避免中途失敗留下半套設定。
		timeoutSec := clampTimeout(body.TimeoutSec)
		kv := map[string]string{
			setTranslateEndpoint:   body.Endpoint,
			setTranslateModel:      body.Model,
			setTranslateMaxTokens:  strconv.Itoa(body.MaxTokens),
			setTranslateTimeoutSec: strconv.Itoa(timeoutSec),
		}
		if body.ApiKey != nil {
			kv[setTranslateAPIKey] = strings.TrimSpace(*body.ApiKey)
		}
		if err := s.st.SetSettings(kv); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleTranslateModels 代理查詢 OpenAI 相容端點的 /v1/models（避免瀏覽器 CORS，兼連線測試）。
// 只對「已儲存的端點」拉取——不接受任意 query endpoint，避免 server 被誘導探測任意內網 host
//（SSRF）。前端需先 PUT 儲存端點再呼叫此 API。有 api_key 時帶 Bearer。
func (s *Server) handleTranslateModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	endpoint := s.st.GetSetting(setTranslateEndpoint)
	if endpoint == "" {
		writeJSON(w, 400, map[string]string{"error": "no saved endpoint; save config first"})
		return
	}
	if !validEndpointURL(endpoint) {
		writeJSON(w, 400, map[string]string{"error": "saved endpoint is not a valid http(s) URL"})
		return
	}
	// 綁 request context：瀏覽器取消/離開頁面時，對端點的探測立即中止，
	// 不會把 dial 撐滿 10 秒 timeout。
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, translate.BaseURL(endpoint)+"/v1/models", nil)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	translate.SetAuthHeader(req, s.st.GetSetting(setTranslateAPIKey))
	resp, err := proxyClient.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "endpoint unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, 502, map[string]string{"error": "endpoint returned " + resp.Status})
		return
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		writeJSON(w, 502, map[string]string{"error": "bad models response"})
		return
	}
	models := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		models = append(models, m.ID)
	}
	writeJSON(w, 200, map[string]any{"models": models})
}
