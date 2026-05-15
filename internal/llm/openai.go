package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider OpenAI兼容LLM实现 / OpenAI-compatible LLM provider
type OpenAIProvider struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIProvider 创建OpenAI兼容LLM客户端 / Create OpenAI-compatible LLM client
func NewOpenAIProvider(baseURL, apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 180 * time.Second,
		},
	}
}

// ProviderBaseURL 返回 API 基础地址，实现 ModelInfoProvider / Returns API base URL (implements ModelInfoProvider)
func (p *OpenAIProvider) ProviderBaseURL() string { return p.baseURL }

// ProviderModel 返回模型名称，实现 ModelInfoProvider / Returns model name (implements ModelInfoProvider)
func (p *OpenAIProvider) ProviderModel() string { return p.model }

type openaiChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_completion_tokens,omitempty"`
}

type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat 发送对话请求 / Send chat completion request
func (p *OpenAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	reqBody := openaiChatRequest{
		Model:          p.model,
		Messages:       req.Messages,
		ResponseFormat: req.ResponseFormat,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("llm chat: marshal request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("llm chat: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm chat: request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxLLMResponseSize = 10 << 20 // 10 MB
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, fmt.Errorf("llm chat: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm chat: API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result openaiChatResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("llm chat: unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("llm chat: API error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("llm chat: empty choices in response")
	}

	return &ChatResponse{
		Content:          result.Choices[0].Message.Content,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
	}, nil
}

// openaiStreamRequest 流式请求体 / Streaming request body
type openaiStreamRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	Stream         bool            `json:"stream"`
	StreamOptions  *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

// openaiStreamChunk SSE 事件块 / SSE event chunk
type openaiStreamChunk struct {
	Choices []struct {
		Delta        struct{ Content string `json:"content"` } `json:"delta"`
		FinishReason string                                    `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct{ TotalTokens int `json:"total_tokens"` } `json:"usage"`
}

// ChatStream 流式对话：首个 token 快速到达，彻底避免 awaiting-headers 超时
// Streaming chat: first token arrives quickly, eliminates awaiting-headers timeout
func (p *OpenAIProvider) ChatStream(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	body := openaiStreamRequest{
		Model:       p.model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		Stream:      true,
		StreamOptions: &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true},
	}
	// json_schema strict 与流式不兼容，降级为 json_object / json_schema strict incompatible with streaming, downgrade to json_object
	if req.ResponseFormat != nil {
		if req.ResponseFormat.Type == "json_schema" {
			body.ResponseFormat = &ResponseFormat{Type: "json_object"}
		} else {
			body.ResponseFormat = req.ResponseFormat
		}
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm stream: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("llm stream: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm stream: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("llm stream: API status %d: %s", resp.StatusCode, string(b))
	}

	var sb strings.Builder
	totalTokens := 0
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			sb.WriteString(chunk.Choices[0].Delta.Content)
		}
		if chunk.Usage != nil {
			totalTokens = chunk.Usage.TotalTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("llm stream: read SSE: %w", err)
	}

	return &ChatResponse{Content: sb.String(), TotalTokens: totalTokens}, nil
}
