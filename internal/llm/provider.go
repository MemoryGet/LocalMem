// Package llm LLM推理调用抽象层 / LLM inference call abstraction layer
package llm

import "context"

// ChatMessage LLM对话消息 / LLM chat message
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResponseFormat LLM响应格式约束 / LLM response format constraint
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatRequest LLM对话请求 / LLM chat request
type ChatRequest struct {
	Messages       []ChatMessage
	ResponseFormat *ResponseFormat
	Temperature    *float64
	MaxTokens      int
}

// ChatResponse LLM对话响应 / LLM chat response
type ChatResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Provider LLM推理接口 / LLM inference provider interface
type Provider interface {
	// Chat 发送对话请求并返回响应 / Send chat request and return response
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
