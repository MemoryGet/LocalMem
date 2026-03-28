// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"
	"time"

	"iclude/internal/logger"
	"iclude/internal/mcp"
	"iclude/internal/model"
	"iclude/internal/search"

	"go.uber.org/zap"
)

// TagStoreReader scan 工具需要的标签查询接口 / Tag query interface for scan tool
type TagStoreReader interface {
	GetTagNamesByMemoryIDs(ctx context.Context, ids []string) (map[string][]string, error)
}

// ScanResultItem 轻量扫描结果条目（仅含索引信息）/ Compact scan result item (index metadata only)
type ScanResultItem struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Score         float64    `json:"score"`
	Source        string     `json:"source"`
	Kind          string     `json:"kind,omitempty"`
	Scope         string     `json:"scope,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
	HappenedAt    *time.Time `json:"happened_at,omitempty"`
	TokenEstimate int        `json:"token_estimate"`
}

// ScanTool iclude_scan 工具 / iclude_scan MCP tool
type ScanTool struct {
	retriever MemoryRetriever
	tagStore  TagStoreReader // 可为 nil / may be nil
}

// NewScanTool 创建 ScanTool 实例 / Create a new ScanTool instance
func NewScanTool(retriever MemoryRetriever, tagStore TagStoreReader) *ScanTool {
	return &ScanTool{retriever: retriever, tagStore: tagStore}
}

// scanArgs iclude_scan 工具参数 / iclude_scan tool arguments
type scanArgs struct {
	Query   string         `json:"query"`
	Scope   string         `json:"scope,omitempty"`
	Limit   int            `json:"limit,omitempty"`
	Filters map[string]any `json:"filters,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *ScanTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_scan",
		Description: "**Primary search tool.** Returns compact index (ID, title, score, tags, scope, token estimate) for efficient browsing. Use this FIRST, then iclude_fetch for full content on selected items. Saves ~10x tokens vs iclude_recall.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "query":{"type":"string","description":"Search query text"},
                "scope":{"type":"string","description":"Namespace scope filter"},
                "limit":{"type":"integer","minimum":1,"maximum":50,"default":10},
                "filters":{"type":"object","description":"Structured filters: kind, tags, min_strength, happened_after, include_expired"}
            },
            "required":["query"]
        }`),
	}
}

// Execute 执行轻量扫描并返回紧凑索引 / Execute lightweight scan and return compact index
func (t *ScanTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args scanArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
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
		Query: args.Query,
		Limit: limit,
	}
	if id != nil {
		req.TeamID = id.TeamID
	}

	// 从结构化 map 和 scope 简写构建过滤器 / Build filters from structured map + scope shorthand
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
		return mcp.ErrorResult("retrieval failed: " + err.Error()), nil
	}

	items := make([]ScanResultItem, 0, len(results))
	for _, r := range results {
		if r.Memory == nil {
			continue
		}
		title := r.Memory.Abstract
		if title == "" {
			content := r.Memory.Content
			if len([]rune(content)) > 100 {
				title = string([]rune(content)[:100]) + "..."
			} else {
				title = content
			}
		}
		items = append(items, ScanResultItem{
			ID:            r.Memory.ID,
			Title:         title,
			Score:         r.Score,
			Source:        r.Source,
			Kind:          r.Memory.Kind,
			Scope:         r.Memory.Scope,
			HappenedAt:    r.Memory.HappenedAt,
			TokenEstimate: search.EstimateTokens(r.Memory.Content),
		})
	}

	// 批量查询标签 / Batch query tags
	if t.tagStore != nil && len(items) > 0 {
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.ID
		}
		tagMap, err := t.tagStore.GetTagNamesByMemoryIDs(ctx, ids)
		if err != nil {
			logger.Warn("scan: failed to batch get tags", zap.Error(err))
		} else {
			for i, item := range items {
				if tags, ok := tagMap[item.ID]; ok {
					items[i].Tags = tags
				}
			}
		}
	}

	out, _ := json.Marshal(items)
	return mcp.TextResult(string(out)), nil
}
