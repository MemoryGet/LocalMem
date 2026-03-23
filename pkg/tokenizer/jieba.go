package tokenizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// JiebaTokenizer 结巴分词 HTTP 客户端 / Jieba tokenizer HTTP client
// 调用独立的 jieba HTTP 微服务进行中文分词
type JiebaTokenizer struct {
	baseURL    string
	httpClient *http.Client
	cutAll     bool // true=全模式, false=精确模式
}

// JiebaOption jieba 配置选项 / Jieba configuration option
type JiebaOption func(*JiebaTokenizer)

// WithCutAll 设置全模式分词 / Enable full-mode tokenization
func WithCutAll(cutAll bool) JiebaOption {
	return func(t *JiebaTokenizer) {
		t.cutAll = cutAll
	}
}

// WithTimeout 设置 HTTP 超时 / Set HTTP timeout
func WithTimeout(d time.Duration) JiebaOption {
	return func(t *JiebaTokenizer) {
		t.httpClient.Timeout = d
	}
}

// NewJiebaTokenizer 创建 jieba 分词器 / Create a jieba tokenizer
// baseURL: jieba HTTP 微服务地址，如 http://localhost:8866
func NewJiebaTokenizer(baseURL string, opts ...JiebaOption) *JiebaTokenizer {
	t := &JiebaTokenizer{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		cutAll: false,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// jiebaRequest jieba 请求体
type jiebaRequest struct {
	Text   string `json:"text"`
	CutAll bool   `json:"cut_all"`
}

// jiebaResponse jieba 响应体
type jiebaResponse struct {
	Tokens []string `json:"tokens"`
}

// Tokenize 调用 jieba HTTP 服务分词 / Call jieba HTTP service for tokenization
func (t *JiebaTokenizer) Tokenize(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", nil
	}

	body, err := json.Marshal(jiebaRequest{
		Text:   text,
		CutAll: t.cutAll,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal jieba request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/tokenize", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create jieba request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("jieba HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("jieba returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result jiebaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode jieba response: %w", err)
	}

	// 过滤空白 token
	var filtered []string
	for _, tok := range result.Tokens {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			filtered = append(filtered, tok)
		}
	}

	return JoinTokens(filtered), nil
}

// Name 返回分词器名称 / Return tokenizer name
func (t *JiebaTokenizer) Name() string {
	return "jieba"
}
