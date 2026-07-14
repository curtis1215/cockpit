package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config 為 OpenAI 相容（LM Studio）翻譯端點設定；Endpoint 空字串代表未設定。
// json tags 同時作為 /api/translate/config 的線上格式。
type Config struct {
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	MaxTokens  int    `json:"max_tokens"`
	TimeoutSec int    `json:"timeout_sec"`
}

const defaultMaxTokens = 4096

// 共用 client：不設全域 Timeout，由每次 request 的 context 控制 deadline。
// Transport 可 keep-alive 重用，避免每次翻譯丟棄整個 Transport。
var httpClient = &http.Client{}

// effectiveTimeout 回傳設定的 timeout；TimeoutSec <= 0 時預設 300s。
func effectiveTimeout(cfg Config) time.Duration {
	sec := cfg.TimeoutSec
	if sec <= 0 {
		sec = defaultTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

// NewDynamic：每次翻譯時呼叫 cfgFn 取得當前設定——端點已設走 HTTP，
// 未設 fallback 到 shell 指令（cmd 空字串用預設 claude -p）。
// 設定改動（WebUI 寫入 DB）即時生效，不需重啟。
func NewDynamic(cfgFn func() Config, cmd string) *Translator {
	if strings.TrimSpace(cmd) == "" {
		cmd = defaultCmd
	}
	return &Translator{Run: func(prompt string) (string, error) {
		cfg := cfgFn()
		// endpoint 與 model 都備齊才走 HTTP；任一缺（含「只存了端點、還沒選模型」
		// 的中間狀態）一律 fallback 到 shell，不會用空 model 打壞請求。
		if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.Model) == "" {
			return shellRun(cmd, prompt, effectiveTimeout(cfg))
		}
		return httpRun(cfg, prompt)
	}}
}

// chatCompletions 為 OpenAI /v1/chat/completions 回應中本實作需要的欄位。
type chatCompletions struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func httpRun(cfg Config, prompt string) (string, error) {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	body, err := json.Marshal(map[string]any{
		"model":       cfg.Model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.3,
		"max_tokens":  maxTokens,
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), effectiveTimeout(cfg))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL(cfg.Endpoint)+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("translate endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var cc chatCompletions
	if err := json.NewDecoder(resp.Body).Decode(&cc); err != nil {
		return "", err
	}
	if len(cc.Choices) == 0 {
		return "", errors.New("translate endpoint returned no choices")
	}
	// 被 max_tokens 截斷的輸出是半句話，存進 DB 比沒翻更糟——當錯誤處理，
	// 走呼叫端的 error event（reasoning 模型 max_tokens 太小時就會這樣）。
	if cc.Choices[0].FinishReason == "length" {
		return "", fmt.Errorf("translation truncated by max_tokens=%d (raise it; reasoning models need headroom)", maxTokens)
	}
	return cc.Choices[0].Message.Content, nil
}

// BaseURL 正規化使用者輸入的端點：去尾斜線與尾端 /v1
//（接受 http://host:1234、http://host:1234/、http://host:1234/v1 等形式）。
func BaseURL(endpoint string) string {
	base := strings.TrimSpace(endpoint)
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/v1")
	return base
}
