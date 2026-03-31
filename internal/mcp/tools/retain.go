// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// MemoryCreator 记忆创建接口 / Interface for creating memories
type MemoryCreator interface {
	Create(ctx context.Context, mem *model.Memory) (*model.Memory, error)
}

// RetainTool iclude_retain 工具 / iclude_retain tool handler
type RetainTool struct{ manager MemoryCreator }

// NewRetainTool 创建 retain 工具 / Create retain tool
func NewRetainTool(manager MemoryCreator) *RetainTool { return &RetainTool{manager: manager} }

// retainArgs iclude_retain 工具参数 / iclude_retain tool arguments
type retainArgs struct {
	Content     string            `json:"content"`
	Scope       string            `json:"scope,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ContextID   string            `json:"context_id,omitempty"`
	SourceType  string            `json:"source_type,omitempty"`
	MessageRole string            `json:"message_role,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *RetainTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_retain",
		Description: "**Call before the session ends** to persist key decisions, facts, and outcomes from this conversation. Also use whenever a notable fact, preference, or decision emerges mid-conversation.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "content":{"type":"string","description":"The memory content to save"},
                "scope":{"type":"string","description":"Namespace scope for organization"},
                "kind":{"type":"string","description":"Memory kind (fact, decision, preference, etc.)"},
                "tags":{"type":"array","items":{"type":"string"},"description":"Optional tags"},
                "metadata":{"type":"object","description":"Optional key-value metadata"},
                "context_id":{"type":"string","description":"Context ID to associate with (e.g. session context)"},
                "source_type":{"type":"string","description":"Source type (manual, hook, conversation, api)"},
                "message_role":{"type":"string","description":"Message role (user, assistant, tool, system)"}
            },
            "required":["content"]
        }`),
	}
}

// Execute 执行记忆保存并返回结果 / Execute memory save and return result
func (t *RetainTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args retainArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.Content == "" {
		return mcp.ErrorResult("content is required"), nil
	}

	id := mcp.IdentityFromContext(ctx)
	mem := &model.Memory{
		Content:     args.Content,
		Scope:       args.Scope,
		Kind:        args.Kind,
		ContextID:   args.ContextID,
		SourceType:  args.SourceType,
		MessageRole: args.MessageRole,
	}
	if id != nil {
		mem.TeamID = id.TeamID
		mem.OwnerID = id.OwnerID
	}

	created, err := t.manager.Create(ctx, mem)
	if err != nil {
		return mcp.ErrorResult("failed to save memory: " + err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"id": created.ID, "content": created.Content})
	return mcp.TextResult(string(out)), nil
}
