// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// MemoryRetriever 记忆检索接口（返回包含评分的完整结果）/ Interface for retrieving memories with full ranking metadata
type MemoryRetriever interface {
	Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error)
}

// RecallTool iclude_recall 工具 / iclude_recall MCP tool
type RecallTool struct{ retriever MemoryRetriever }

// NewRecallTool 创建 RecallTool 实例 / Create a new RecallTool instance
func NewRecallTool(retriever MemoryRetriever) *RecallTool {
	return &RecallTool{retriever: retriever}
}

// recallArgs iclude_recall 工具参数 / iclude_recall tool arguments
type recallArgs struct {
	Query      string         `json:"query"`
	Scope      string         `json:"scope,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Filters    map[string]any `json:"filters,omitempty"`
	MmrEnabled *bool          `json:"mmr_enabled,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *RecallTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_recall",
		Description: "Retrieve full memory content via semantic + full-text search. **High token cost** — prefer iclude_scan + iclude_fetch for MCP workflows. Use only when you need all results with full content in one call.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "query":{"type":"string","description":"Search query text"},
                "scope":{"type":"string","description":"Namespace scope filter"},
                "limit":{"type":"integer","minimum":1,"maximum":50,"default":10},
                "filters":{"type":"object","description":"Structured filters: kind, tags, min_strength, happened_after, include_expired"},
                "mmr_enabled":{"type":"boolean","description":"Enable MMR diversity re-ranking"}
            },
            "required":["query"]
        }`),
	}
}

// Execute 执行记忆检索并返回结果 / Execute memory retrieval and return results
func (t *RecallTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args recallArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return toolInputError("invalid arguments")
	}
	if args.Query == "" {
		return mcp.ErrorResult("query is required"), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	id := mcp.IdentityFromContext(ctx)
	req := &model.RetrieveRequest{
		Query:      args.Query,
		Limit:      limit,
		MmrEnabled: args.MmrEnabled,
	}
	if id != nil {
		req.TeamID = id.TeamID
		req.OwnerID = id.OwnerID
	}

	// Build filters from structured map + scope shorthand
	if len(args.Filters) > 0 {
		raw, _ := json.Marshal(args.Filters)
		var sf model.SearchFilters
		_ = json.Unmarshal(raw, &sf)
		req.Filters = &sf
	}
	if args.Scope != "" {
		if req.Filters == nil {
			req.Filters = &model.SearchFilters{}
		}
		if req.Filters.Scope == "" {
			req.Filters.Scope = args.Scope
		}
	}

	results, err := t.retriever.Retrieve(ctx, req)
	if err != nil {
		return toolError("recall", err)
	}

	out, _ := json.Marshal(results)
	return mcp.TextResult(string(out)), nil
}
