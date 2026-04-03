// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"
	"time"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// TimelineQuerier 时间线查询接口 / Interface for querying the memory timeline
type TimelineQuerier interface {
	Timeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error)
}

// TimelineTool iclude_timeline 工具 / iclude_timeline MCP tool
type TimelineTool struct{ querier TimelineQuerier }

// NewTimelineTool 创建时间线工具 / Create timeline tool
func NewTimelineTool(querier TimelineQuerier) *TimelineTool {
	return &TimelineTool{querier: querier}
}

// timelineArgs iclude_timeline 工具参数 / iclude_timeline tool arguments
type timelineArgs struct {
	Scope     string `json:"scope,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
	After     string `json:"after,omitempty"`  // RFC3339
	Before    string `json:"before,omitempty"` // RFC3339
	Limit     int    `json:"limit,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *TimelineTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_timeline",
		Description: "Retrieve memories in chronological order (timeline view). Useful for recalling what happened over a period of time.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "scope":{"type":"string","description":"Namespace scope filter"},
                "source_ref":{"type":"string","description":"Filter by source reference (e.g. session ID)"},
                "after":{"type":"string","format":"date-time","description":"Return memories after this timestamp (RFC3339)"},
                "before":{"type":"string","format":"date-time","description":"Return memories before this timestamp (RFC3339)"},
                "limit":{"type":"integer","minimum":1,"maximum":100,"default":20}
            }
        }`),
	}
}

// Execute 执行时间线查询并返回结果 / Execute timeline query and return results
func (t *TimelineTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args timelineArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	req := &model.TimelineRequest{
		Scope:     args.Scope,
		SourceRef: args.SourceRef,
		Limit:     limit,
	}

	// 注入身份过滤 / Inject identity filtering
	id := mcp.IdentityFromContext(ctx)
	if id != nil {
		req.TeamID = id.TeamID
		req.OwnerID = id.OwnerID
	}

	if args.After != "" {
		ts, err := time.Parse(time.RFC3339, args.After)
		if err != nil {
			return mcp.ErrorResult("invalid after timestamp: " + err.Error()), nil
		}
		req.After = &ts
	}
	if args.Before != "" {
		ts, err := time.Parse(time.RFC3339, args.Before)
		if err != nil {
			return mcp.ErrorResult("invalid before timestamp: " + err.Error()), nil
		}
		req.Before = &ts
	}

	results, err := t.querier.Timeline(ctx, req)
	if err != nil {
		return mcp.ErrorResult("timeline query failed: " + err.Error()), nil
	}

	out, _ := json.Marshal(results)
	return mcp.TextResult(string(out)), nil
}
