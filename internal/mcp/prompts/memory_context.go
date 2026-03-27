// Package prompts MCP 提示模板处理器 / MCP prompt handlers for IClude memory context injection
package prompts

import (
	"context"
	"encoding/json"
	"fmt"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// MemoryRetriever 记忆检索接口（返回包含评分的完整结果）/ Interface for prompt memory retrieval with full ranking metadata
type MemoryRetriever interface {
	Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error)
}

// MemoryContextPrompt memory_context 提示模板处理器 / memory_context prompt handler
type MemoryContextPrompt struct {
	retriever MemoryRetriever
}

// NewMemoryContextPrompt 创建提示模板处理器 / Create memory context prompt handler
func NewMemoryContextPrompt(retriever MemoryRetriever) *MemoryContextPrompt {
	return &MemoryContextPrompt{retriever: retriever}
}

// Definition 返回 MCP prompts/list 定义 / Return MCP prompts/list definition
func (p *MemoryContextPrompt) Definition() mcp.PromptDefinition {
	return mcp.PromptDefinition{
		Name:        "memory_context",
		Description: "Inject relevant memories as context for a question. Returns a system message with retrieved memories followed by the user's question.",
		Arguments: []mcp.PromptArgument{
			{Name: "question", Description: "The question to answer with memory context", Required: true},
			{Name: "scope", Description: "Namespace scope filter", Required: false},
			{Name: "limit", Description: "Maximum number of memories to inject (default 10)", Required: false},
		},
	}
}

// Get 渲染提示模板 / Render the prompt with retrieved memories injected as system context
func (p *MemoryContextPrompt) Get(ctx context.Context, arguments map[string]string) (*mcp.PromptResult, error) {
	question := arguments["question"]
	if question == "" {
		return nil, fmt.Errorf("question argument is required")
	}

	scope := arguments["scope"]

	limit := 10
	if l, ok := arguments["limit"]; ok && l != "" {
		parsed := 0
		if n, err := fmt.Sscanf(l, "%d", &parsed); err == nil && n == 1 && parsed > 0 {
			limit = parsed
		}
	}

	id := mcp.IdentityFromContext(ctx)
	req := &model.RetrieveRequest{
		Query: question,
		Limit: limit,
	}
	if id != nil {
		req.TeamID = id.TeamID
	}
	if scope != "" {
		req.Filters = &model.SearchFilters{Scope: scope}
	}

	results, err := p.retriever.Retrieve(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve memories: %w", err)
	}

	memJSON, _ := json.MarshalIndent(results, "", "  ")
	systemText := fmt.Sprintf(
		"## Relevant memories from IClude\n\n```json\n%s\n```\n\nUse the above memories as context when answering the following question.",
		string(memJSON),
	)

	return &mcp.PromptResult{
		Description: "Memory context for: " + question,
		Messages: []mcp.PromptMessage{
			{Role: "system", Content: mcp.ContentBlock{Type: "text", Text: systemText}},
			{Role: "user", Content: mcp.ContentBlock{Type: "text", Text: question}},
		},
	}, nil
}
