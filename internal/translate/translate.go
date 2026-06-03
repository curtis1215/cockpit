package translate

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const promptTmpl = "你是技術翻譯。把以下軟體 changelog 整理成繁體中文重點摘要，用條列列出重要變更（新功能/修正/安全/破壞性變更），精簡不逐字翻。\n\n---\n%RAW%\n---"

type Translator struct {
	// Run 把整段 prompt 丟給翻譯引擎、回傳結果；預設用 claude -p。可注入測試。
	Run func(prompt string) (string, error)
}

func New() *Translator { return &Translator{Run: claudeRun} }

func claudeRun(prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "-p", prompt).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Changelog：空輸入/錯誤回 ""（best-effort，呼叫端降級保留原文）。
func (t *Translator) Changelog(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	prompt := strings.ReplaceAll(promptTmpl, "%RAW%", raw)
	out, err := t.Run(prompt)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
