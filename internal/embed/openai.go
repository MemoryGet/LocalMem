package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"iclude/internal/store"
)

// 编译期接口实现检查 / Compile-time interface compliance check
var _ store.Embedder = (*OpenAIEmbedder)(nil)

// OpenAIEmbedder OpenAI 向量嵌入客户端（兼容任意 OpenAI 格式 API）
// OpenAI embeddings API adapter (compatible with any OpenAI-format API)
type OpenAIEmbedder struct {
	apiKey  string
	model   string
	baseURL string // embeddings endpoint, e.g. https://api.openai.com/v1/embeddings
	client  *http.Client
}

type openaiEmbedRequest struct {
	Input any    `json:"input"`
	Model string `json:"model"`
}

type openaiEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

const openaiEmbeddingsURL = "https://api.openai.com/v1/embeddings"

// maxEmbeddingChars ~8000 tokens, safe for text-embedding-3-small (8191 token limit)
const maxEmbeddingChars = 24000

// truncateForEmbedding 截断超长文本 / Truncate text for embedding API limits
func truncateForEmbedding(text string) string {
	runes := []rune(text)
	if len(runes) > maxEmbeddingChars {
		return string(runes[:maxEmbeddingChars])
	}
	return text
}

// NewOpenAIEmbedder 创建 OpenAI 嵌入客户端（官方端点）/ Create OpenAI embedding client (official endpoint)
func NewOpenAIEmbedder(apiKey, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		apiKey:  apiKey,
		model:   model,
		baseURL: openaiEmbeddingsURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// NewOpenAICompatibleEmbedder 创建兼容 OpenAI 格式的嵌入客户端（自定义 base URL）
// Create OpenAI-compatible embedding client with custom base URL.
// baseURL should be the API root, e.g. "https://my-api.com/v1" — "/embeddings" is appended automatically.
func NewOpenAICompatibleEmbedder(baseURL, apiKey, model string) *OpenAIEmbedder {
	embURL := strings.TrimRight(baseURL, "/") + "/embeddings"
	return &OpenAIEmbedder{
		apiKey:  apiKey,
		model:   model,
		baseURL: embURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed 单条文本向量化 / Embed a single text via OpenAI API
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	text = truncateForEmbedding(text)
	embeddings, err := e.doRequest(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("openai embed: empty response")
	}
	return embeddings[0], nil
}

// EmbedBatch 批量文本向量化 / Embed multiple texts via OpenAI API
func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	truncated := make([]string, len(texts))
	for i, t := range texts {
		truncated[i] = truncateForEmbedding(t)
	}
	embeddings, err := e.doRequest(ctx, truncated)
	if err != nil {
		return nil, fmt.Errorf("openai embed batch: %w", err)
	}
	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("openai embed batch: expected %d embeddings, got %d", len(texts), len(embeddings))
	}
	return embeddings, nil
}

// maxRetries 嵌入请求最大重试次数 / Max retries for embedding requests
const maxRetries = 5

// rateLimitBackoff 429 默认退避基准 / Default backoff base for 429 rate-limit responses
const rateLimitBackoff = 10 * time.Second

func (e *OpenAIEmbedder) doRequest(ctx context.Context, input any) ([][]float32, error) {
	reqBody := openaiEmbedRequest{
		Input: input,
		Model: e.model,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// 指数退避: 1s, 2s for 5xx; 10s, 20s, 40s… for 429 / Backoff: quick for 5xx, slow for rate-limit
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<uint(attempt-1)) * time.Second):
			}
		}

		result, statusCode, retryAfter, err := e.doSingleRequest(ctx, bodyBytes)
		if err == nil {
			return result, nil
		}
		lastErr = err

		switch {
		case statusCode == http.StatusTooManyRequests,
			statusCode == http.StatusOK:
			// 429: 标准限速  / Standard rate-limit.
			// 200 + 非 JSON: 反向代理将限速页以 HTTP 200 返回（HTML body）
			// 200 + non-JSON: reverse-proxy returning a rate-limit HTML page with HTTP 200.
			// Both need a long backoff before retry.
			wait := retryAfter
			if wait <= 0 {
				wait = rateLimitBackoff * time.Duration(1<<uint(attempt))
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		case statusCode > 0 && statusCode < 500:
			// 4xx（非 429/200）不重试 / Other 4xx: do not retry
			return nil, err
		}
	}
	return nil, fmt.Errorf("embedding request failed after %d attempts: %w", maxRetries, lastErr)
}

func (e *OpenAIEmbedder) doSingleRequest(ctx context.Context, bodyBytes []byte) ([][]float32, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// 解析 Retry-After 头（秒数）/ Parse Retry-After header (seconds)
	var retryAfter time.Duration
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, parseErr := strconv.Atoi(ra); parseErr == nil && secs > 0 {
			retryAfter = time.Duration(secs) * time.Second
		}
	}

	const maxEmbedResponseSize = 10 << 20 // 10 MB
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedResponseSize))
	if err != nil {
		return nil, resp.StatusCode, retryAfter, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, retryAfter, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result openaiEmbedResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, resp.StatusCode, retryAfter, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, resp.StatusCode, retryAfter, fmt.Errorf("API error: %s", result.Error.Message)
	}

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, resp.StatusCode, 0, nil
}
