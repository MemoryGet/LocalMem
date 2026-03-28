// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"
	"errors"

	"iclude/internal/logger"
	"iclude/internal/mcp"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// MemoryGetter 按 ID 获取记忆接口 / Interface for getting memories by ID
type MemoryGetter interface {
	Get(ctx context.Context, id string) (*model.Memory, error)
}

// FetchResultItem 批量获取结果项 / Batch fetch result item
type FetchResultItem struct {
	Memory *model.Memory `json:"memory"`
}

// FetchTool iclude_fetch MCP 工具 / iclude_fetch MCP tool
type FetchTool struct{ getter MemoryGetter }

// NewFetchTool 创建 FetchTool 实例 / Create a new FetchTool instance
func NewFetchTool(getter MemoryGetter) *FetchTool {
	return &FetchTool{getter: getter}
}

// fetchArgs iclude_fetch 工具参数 / iclude_fetch tool arguments
type fetchArgs struct {
	IDs []string `json:"ids"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *FetchTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_fetch",
		Description: "Fetch full memory content by IDs. Use after iclude_scan to get details for selected items only.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "ids":{"type":"array","items":{"type":"string"},"description":"Memory IDs to fetch","minItems":1,"maxItems":20}
            },
            "required":["ids"]
        }`),
	}
}

// Execute 执行批量记忆获取并返回结果 / Execute batch memory fetch and return results
func (t *FetchTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args fetchArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if len(args.IDs) == 0 {
		return mcp.ErrorResult("ids is required and must not be empty"), nil
	}
	if len(args.IDs) > 20 {
		return mcp.ErrorResult("maximum 20 ids per request"), nil
	}

	items := make([]FetchResultItem, 0, len(args.IDs))
	for _, id := range args.IDs {
		mem, err := t.getter.Get(ctx, id)
		if err != nil {
			if errors.Is(err, model.ErrMemoryNotFound) {
				logger.Debug("iclude_fetch: memory not found, skipping", zap.String("id", id))
				continue
			}
			return mcp.ErrorResult("failed to fetch memory " + id + ": " + err.Error()), nil
		}
		items = append(items, FetchResultItem{Memory: mem})
	}

	out, _ := json.Marshal(items)
	return mcp.TextResult(string(out)), nil
}
