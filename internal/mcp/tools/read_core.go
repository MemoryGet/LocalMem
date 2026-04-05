package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// CoreReader 核心记忆读取接口 / Core memory reader interface
type CoreReader interface {
	GetCoreBlocksMultiScope(ctx context.Context, scopes []string, identity *model.Identity) ([]*model.Memory, error)
}

// ReadCoreTool iclude_read_core MCP 工具 / iclude_read_core MCP tool
type ReadCoreTool struct{ reader CoreReader }

// NewReadCoreTool 创建 ReadCoreTool / Create a new ReadCoreTool
func NewReadCoreTool(reader CoreReader) *ReadCoreTool {
	return &ReadCoreTool{reader: reader}
}

type readCoreArgs struct {
	Scope string `json:"scope"`
}

// Definition 返回工具元数据 / Return tool metadata
func (t *ReadCoreTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_read_core",
		Description: "Read core memory blocks for a given scope. Core memories are stable, high-value information (user profile, preferences, goals, project state, operating rules) that persist across sessions. Returns up to 5 core blocks.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "scope":{"type":"string","description":"Scope to read core blocks from (e.g. 'user/alice', 'project/myapp')"}
            },
            "required":["scope"]
        }`),
	}
}

// Execute 执行读取核心记忆 / Execute read core blocks
func (t *ReadCoreTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args readCoreArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Scope == "" {
		return nil, fmt.Errorf("scope is required")
	}

	if err := ValidateScope(args.Scope); err != nil {
		return nil, err
	}

	identity := mcp.IdentityFromContext(ctx)
	blocks, err := t.reader.GetCoreBlocksMultiScope(ctx, []string{args.Scope}, identity)
	if err != nil {
		return nil, fmt.Errorf("read core blocks: %w", err)
	}

	type coreBlock struct {
		ID      string `json:"id"`
		SubKind string `json:"sub_kind,omitempty"`
		Content string `json:"content"`
		Scope   string `json:"scope"`
		Excerpt string `json:"excerpt,omitempty"`
	}

	result := make([]coreBlock, 0, len(blocks))
	for _, m := range blocks {
		result = append(result, coreBlock{
			ID:      m.ID,
			SubKind: m.SubKind,
			Content: m.Content,
			Scope:   m.Scope,
			Excerpt: m.Excerpt,
		})
	}

	return json.Marshal(map[string]any{
		"scope":  args.Scope,
		"blocks": result,
		"count":  len(result),
	})
}
