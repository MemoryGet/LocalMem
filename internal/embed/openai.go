package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"iclude/internal/store"
)

// 编译期接口实现检查 / Compile-time interface compliance check
var _ store.Embedder = (*OpenAIEmbedder)(nil)

// OpenAIEmbedder OpenAI 向量嵌入客户端 / OpenAI embeddings API adapter
type OpenAIEmbedder struct {
	apiKey string
	model  string
	client *http.Client
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

// NewOpenAIEmbedder 创建 OpenAI 嵌入客户端 / Create a new OpenAI embedding client
func NewOpenAIEmbedder(apiKey, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 30 * time.Second},
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
const maxRetries = 3

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
			// 指数退避: 1s, 2s / Exponential backoff
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		result, statusCode, err := e.doSingleRequest(ctx, bodyBytes)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// 仅对 429(速率限制) 和 5xx(服务端错误) 重试 / Retry only on 429 and 5xx
		if statusCode > 0 && statusCode != http.StatusTooManyRequests && statusCode < 500 {
			return nil, err
		}
	}
	return nil, fmt.Errorf("embedding request failed after %d attempts: %w", maxRetries, lastErr)
}

func (e *OpenAIEmbedder) doSingleRequest(ctx context.Context, bodyBytes []byte) ([][]float32, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiEmbeddingsURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxEmbedResponseSize = 10 << 20 // 10 MB
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedResponseSize))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result openaiEmbedResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, resp.StatusCode, fmt.Errorf("API error: %s", result.Error.Message)
	}

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, resp.StatusCode, nil
}
