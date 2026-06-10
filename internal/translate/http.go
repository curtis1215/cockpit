package translate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Config 為 OpenAI 相容（LM Studio）翻譯端點設定；Endpoint 空字串代表未設定。
type Config struct {
	Endpoint  string
	Model     string
	MaxTokens int
}

const defaultMaxTokens = 4096

// NewDynamic：每次翻譯時呼叫 cfgFn 取得當前設定——端點已設走 HTTP，
// 未設 fallback 到 shell 指令（cmd 空字串用預設 claude -p）。
// 設定改動（WebUI 寫入 DB）即時生效，不需重啟。
func NewDynamic(cfgFn func() Config, cmd string) *Translator {
	shell := NewWithCmd(cmd)
	return &Translator{Run: func(prompt string) (string, error) {
		cfg := cfgFn()
		if strings.TrimSpace(cfg.Endpoint) == "" {
			return shell.Run(prompt)
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
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(BaseURL(cfg.Endpoint)+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("translate endpoint returned %d", resp.StatusCode)
	}
	var cc chatCompletions
	if err := json.NewDecoder(resp.Body).Decode(&cc); err != nil {
		return "", err
	}
	if len(cc.Choices) == 0 {
		return "", errors.New("translate endpoint returned no choices")
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
