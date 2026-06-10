package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/curtis1215/cockpit/internal/translate"
)

// settings keys（與 internal/translate.Config 對應）
const (
	setTranslateEndpoint  = "translate.endpoint"
	setTranslateModel     = "translate.model"
	setTranslateMaxTokens = "translate.max_tokens"
)

// proxyClient 給 models 代理用：短 timeout、共用連線。
var proxyClient = &http.Client{Timeout: 10 * time.Second}

func (s *Server) registerTranslateAPI() {
	s.mux.HandleFunc("/api/translate/config", s.handleTranslateConfig)
	s.mux.HandleFunc("/api/translate/models", s.handleTranslateModels)
}

// TranslateConfig 讀取目前儲存的翻譯端點設定（供 serve.go 注入 translate.NewDynamic）。
func (s *Server) TranslateConfig() translate.Config {
	maxTokens, _ := strconv.Atoi(s.st.GetSetting(setTranslateMaxTokens))
	return translate.Config{
		Endpoint:  s.st.GetSetting(setTranslateEndpoint),
		Model:     s.st.GetSetting(setTranslateModel),
		MaxTokens: maxTokens,
	}
}

// validEndpointURL 驗證使用者提供的端點是 host 非空的 http(s) URL。
func validEndpointURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *Server) handleTranslateConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.TranslateConfig())
	case http.MethodPut:
		var body translate.Config
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
		// 單一 transaction 寫入：避免中途失敗留下半套設定（endpoint 有、model 沒有），
		// NewDynamic 每次翻譯都即時讀，半套設定會直接打出錯誤請求。
		if err := s.st.SetSettings(map[string]string{
			setTranslateEndpoint:  body.Endpoint,
			setTranslateModel:     body.Model,
			setTranslateMaxTokens: strconv.Itoa(body.MaxTokens),
		}); err != nil {
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
//（SSRF）。前端需先 PUT 儲存端點再呼叫此 API。
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
