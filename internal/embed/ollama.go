package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"iclude/internal/store"
)

// 编译期接口实现检查 / Compile-time interface compliance check
var _ store.Embedder = (*OllamaEmbedder)(nil)

// OllamaEmbedder Ollama 向量嵌入客户端 / Ollama embeddings API adapter
type OllamaEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

// NewOllamaEmbedder 创建 Ollama 嵌入客户端 / Create a new Ollama embedding client
func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	return &OllamaEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed 单条文本向量化 / Embed a single text via Ollama API
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.doRequest(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("ollama embed: empty response")
	}
	return embeddings[0], nil
}

// EmbedBatch 批量文本向量化 / Embed multiple texts via Ollama API
func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	embeddings, err := e.doRequest(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("ollama embed batch: %w", err)
	}
	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed batch: expected %d embeddings, got %d", len(texts), len(embeddings))
	}
	return embeddings, nil
}

func (e *OllamaEmbedder) doRequest(ctx context.Context, input any) ([][]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model: e.model,
		Input: input,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := e.baseURL + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

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

	var result ollamaEmbedResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("API error: %s", result.Error)
	}

	return result.Embeddings, nil
}
