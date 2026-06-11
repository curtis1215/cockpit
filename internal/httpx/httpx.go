package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string, timeout time.Duration) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: timeout}}
}

// Timeout 回傳底層 http.Client 的整體請求逾時。
func (c *Client) Timeout() time.Duration { return c.http.Timeout }

// GetJSON 發 GET，可選 bearer；204 直接回；status>=400 回 error；否則解進 out（out 可為 nil）。
func (c *Client) GetJSON(path, bearer string, out any) (int, error) {
	req, err := http.NewRequest("GET", c.base+path, nil)
	if err != nil {
		return 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return 204, nil
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("http %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// PostJSON 送 body（JSON），可選 bearer；status>=400 回 error，否則把回應解進 out（out 可為 nil）。
func (c *Client) PostJSON(path, bearer string, body, out any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", c.base+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return resp.StatusCode, fmt.Errorf("http %d: %s", resp.StatusCode, msg)
	}
	if out != nil && resp.StatusCode != 204 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
