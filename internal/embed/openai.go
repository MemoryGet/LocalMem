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
	embeddings, err := e.doRequest(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("openai embed batch: %w", err)
	}
	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("openai embed batch: expected %d embeddings, got %d", len(texts), len(embeddings))
	}
	return embeddings, nil
}

func (e *OpenAIEmbedder) doRequest(ctx context.Context, input any) ([][]float32, error) {
	reqBody := openaiEmbedRequest{
		Input: input,
		Model: e.model,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiEmbeddingsURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result openaiEmbedResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("API error: %s", result.Error.Message)
	}

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, nil
}
