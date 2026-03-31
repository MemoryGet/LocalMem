// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// ConversationIngester 对话摄取接口 / Interface for ingesting conversations
type ConversationIngester interface {
	IngestConversation(ctx context.Context, req *model.IngestConversationRequest, identity *model.Identity) (string, []*model.Memory, error)
}

// IngestConversationTool iclude_ingest_conversation 工具 / iclude_ingest_conversation MCP tool
type IngestConversationTool struct{ manager ConversationIngester }

// NewIngestConversationTool 创建对话摄取工具 / Create ingest conversation tool
func NewIngestConversationTool(manager ConversationIngester) *IngestConversationTool {
	return &IngestConversationTool{manager: manager}
}

// ingestArgs iclude_ingest_conversation 工具参数 / iclude_ingest_conversation tool arguments
type ingestArgs struct {
	Messages   []model.ConversationMessage `json:"messages"`
	Provider   string                      `json:"provider,omitempty"`
	ExternalID string                      `json:"external_id,omitempty"`
	Scope      string                      `json:"scope,omitempty"`
	ContextID  string                      `json:"context_id,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *IngestConversationTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_ingest_conversation",
		Description: "**Call at the end of a session** to save the full conversation for future recall. Ingests an array of role/content messages into IClude memory, creating memories for each turn grouped under a session context.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "messages":{
                    "type":"array",
                    "items":{
                        "type":"object",
                        "properties":{
                            "role":{"type":"string","description":"Message role (user/assistant/system)"},
                            "content":{"type":"string","description":"Message content"}
                        },
                        "required":["role","content"]
                    },
                    "description":"Conversation messages to ingest"
                },
                "provider":{"type":"string","description":"LLM provider identifier (openai/claude/generic)"},
                "external_id":{"type":"string","description":"External thread or conversation identifier"},
                "scope":{"type":"string","description":"Namespace scope for organization"},
                "context_id":{"type":"string","description":"Existing context ID to reuse"}
            },
            "required":["messages"]
        }`),
	}
}

// Execute 执行对话摄取并返回结果 / Execute conversation ingest and return result
func (t *IngestConversationTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args ingestArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if len(args.Messages) == 0 {
		return mcp.ErrorResult("messages is required and must not be empty"), nil
	}

	req := &model.IngestConversationRequest{
		Messages:   args.Messages,
		Provider:   args.Provider,
		ExternalID: args.ExternalID,
		Scope:      args.Scope,
		ContextID:  args.ContextID,
	}

	ctxID, mems, err := t.manager.IngestConversation(ctx, req, mcp.IdentityFromContext(ctx))
	if err != nil {
		return mcp.ErrorResult("ingest failed: " + err.Error()), nil
	}

	out, _ := json.Marshal(map[string]any{"context_id": ctxID, "saved": len(mems)})
	return mcp.TextResult(string(out)), nil
}
