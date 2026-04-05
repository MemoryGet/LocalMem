package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"iclude/internal/mcp"
)

// MemoryPromoter 记忆晋升接口 / Memory promotion interface
type MemoryPromoter interface {
	PromoteByID(ctx context.Context, memoryID, targetClass string) error
}

// PromoteMemoryTool iclude_promote_memory MCP 工具 / iclude_promote_memory MCP tool
type PromoteMemoryTool struct{ promoter MemoryPromoter }

// NewPromoteMemoryTool 创建 PromoteMemoryTool / Create a new PromoteMemoryTool
func NewPromoteMemoryTool(promoter MemoryPromoter) *PromoteMemoryTool {
	return &PromoteMemoryTool{promoter: promoter}
}

type promoteArgs struct {
	MemoryID    string `json:"memory_id"`
	TargetClass string `json:"target_class"`
}

// Definition 返回工具元数据 / Return tool metadata
func (t *PromoteMemoryTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_promote_memory",
		Description: "Promote a candidate memory to a higher class (semantic, procedural, or core). Use this to confirm that a candidate memory should be permanently elevated. Only memories with a candidate_for marker can be promoted to core.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "memory_id":{"type":"string","description":"ID of the memory to promote"},
                "target_class":{"type":"string","enum":["semantic","procedural","core"],"description":"Target memory class to promote to"}
            },
            "required":["memory_id","target_class"]
        }`),
	}
}

// Execute 执行晋升 / Execute promotion
func (t *PromoteMemoryTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args promoteArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.MemoryID == "" {
		return nil, fmt.Errorf("memory_id is required")
	}
	if args.TargetClass == "" {
		return nil, fmt.Errorf("target_class is required")
	}

	if err := t.promoter.PromoteByID(ctx, args.MemoryID, args.TargetClass); err != nil {
		return nil, fmt.Errorf("promote failed: %w", err)
	}

	return json.Marshal(map[string]any{
		"promoted":     true,
		"memory_id":    args.MemoryID,
		"target_class": args.TargetClass,
	})
}
