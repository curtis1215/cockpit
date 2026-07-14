package translate

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

const promptTmpl = "你是技術翻譯。把以下軟體 changelog 整理成繁體中文重點摘要，用條列列出重要變更（新功能/修正/安全/破壞性變更），精簡不逐字翻。\n\n---\n%RAW%\n---"

const defaultCmd = "claude -p"

// defaultTimeoutSec is used when Config.TimeoutSec is unset or non-positive.
const defaultTimeoutSec = 300

type Translator struct {
	// Run 把整段 prompt 丟給翻譯引擎、回傳結果；預設用 claude -p。可注入測試。
	Run func(prompt string) (string, error)
}

func New() *Translator { return NewWithCmd("") }

// NewWithCmd：cmd 為翻譯指令（經 bash -lc 執行、prompt 由 stdin 餵入），
// 空字串用預設 "claude -p"。例：serve 以 root 跑時可配
// "sudo -H -u curtis /Users/curtis/.local/bin/claude -p"。
func NewWithCmd(cmd string) *Translator {
	if strings.TrimSpace(cmd) == "" {
		cmd = defaultCmd
	}
	timeout := time.Duration(defaultTimeoutSec) * time.Second
	return &Translator{Run: func(prompt string) (string, error) {
		return shellRun(cmd, prompt, timeout)
	}}
}

// shellRun：prompt 走 stdin（claude -p 支援管線輸入），避免引號與參數長度問題。
func shellRun(cmd, prompt string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = time.Duration(defaultTimeoutSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "bash", "-lc", cmd)
	c.Stdin = strings.NewReader(prompt)
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ChangelogResult 翻譯 raw changelog。空 raw → ("", nil)；失敗 / 空 content → error。
// 過長 raw 會先 TruncateForTranslate 再送模型。
func (t *Translator) ChangelogResult(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	raw = TruncateForTranslate(raw)
	prompt := strings.ReplaceAll(promptTmpl, "%RAW%", raw)
	out, err := t.Run(prompt)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("empty translation content")
	}
	return out, nil
}

// Changelog：空輸入/錯誤回 ""（best-effort，呼叫端降級保留原文）。
func (t *Translator) Changelog(raw string) string {
	out, _ := t.ChangelogResult(raw)
	return out
}
