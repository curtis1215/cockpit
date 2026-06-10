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

func (s *Server) handleTranslateConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.TranslateConfig()
		writeJSON(w, 200, map[string]any{
			"endpoint":   cfg.Endpoint,
			"model":      cfg.Model,
			"max_tokens": cfg.MaxTokens,
		})
	case http.MethodPut:
		var body struct {
			Endpoint  string `json:"endpoint"`
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad json"})
			return
		}
		if body.MaxTokens < 0 {
			writeJSON(w, 400, map[string]string{"error": "max_tokens must be >= 0"})
			return
		}
		if body.Endpoint != "" {
			u, err := url.Parse(body.Endpoint)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				writeJSON(w, 400, map[string]string{"error": "endpoint must be a http(s) URL"})
				return
			}
			if body.Model == "" {
				writeJSON(w, 400, map[string]string{"error": "model required when endpoint is set"})
				return
			}
		}
		for k, v := range map[string]string{
			setTranslateEndpoint:  body.Endpoint,
			setTranslateModel:     body.Model,
			setTranslateMaxTokens: strconv.Itoa(body.MaxTokens),
		} {
			if err := s.st.SetSetting(k, v); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleTranslateModels 代理查詢 OpenAI 相容端點的 /v1/models（避免瀏覽器 CORS，兼連線測試）。
// endpoint 取 query 參數，未給則用已儲存設定。
func (s *Server) handleTranslateModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	endpoint := r.URL.Query().Get("endpoint")
	if endpoint == "" {
		endpoint = s.st.GetSetting(setTranslateEndpoint)
	}
	if endpoint == "" {
		writeJSON(w, 400, map[string]string{"error": "endpoint required"})
		return
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, 400, map[string]string{"error": "endpoint must be a http(s) URL"})
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(translate.BaseURL(endpoint) + "/v1/models")
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
