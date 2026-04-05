package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
	"iclude/internal/runtime"
)

// SessionFinalizer 会话终结接口 / Interface for finalizing sessions
type SessionFinalizer interface {
	Finalize(ctx context.Context, req *runtime.FinalizeRequest, identity *model.Identity) (*runtime.FinalizeResponse, error)
}

// FinalizeSessionTool iclude_finalize_session 工具 / iclude_finalize_session MCP tool
type FinalizeSessionTool struct{ finalizer SessionFinalizer }

// NewFinalizeSessionTool 创建 finalize_session 工具 / Create finalize_session tool
func NewFinalizeSessionTool(finalizer SessionFinalizer) *FinalizeSessionTool {
	return &FinalizeSessionTool{finalizer: finalizer}
}

// finalizeSessionArgs iclude_finalize_session 工具参数 / iclude_finalize_session tool arguments
type finalizeSessionArgs struct {
	SessionID      string `json:"session_id"`
	ContextID      string `json:"context_id,omitempty"`
	ToolName       string `json:"tool_name"`
	IdempotencyKey string `json:"idempotency_key"`
	Summary        string `json:"summary,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *FinalizeSessionTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_finalize_session",
		Description: "Finalize a coding session: generate summary, mark session closed, and ensure idempotent completion. Call this when a session ends (e.g., in Stop hook) instead of manually retaining a summary.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "session_id":{"type":"string","description":"Session identifier to finalize"},
                "context_id":{"type":"string","description":"LocalMem context ID bound to this session"},
                "tool_name":{"type":"string","description":"Host tool name (claude-code, codex, cursor, cline)"},
                "idempotency_key":{"type":"string","description":"Unique key for idempotent finalization (e.g., finalize:{tool}:{session}:v1)"},
                "summary":{"type":"string","description":"Optional pre-generated summary from adapter side"}
            },
            "required":["session_id","tool_name","idempotency_key"]
        }`),
	}
}

// Execute 执行会话终结 / Execute session finalization
func (t *FinalizeSessionTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args finalizeSessionArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return toolInputError("invalid arguments")
	}
	if args.SessionID == "" {
		return mcp.ErrorResult("session_id is required"), nil
	}
	if args.IdempotencyKey == "" {
		return mcp.ErrorResult("idempotency_key is required"), nil
	}

	identity := mcp.IdentityFromContext(ctx)

	req := &runtime.FinalizeRequest{
		SessionID:      args.SessionID,
		ContextID:      args.ContextID,
		ToolName:       args.ToolName,
		IdempotencyKey: args.IdempotencyKey,
		Summary:        args.Summary,
	}

	resp, err := t.finalizer.Finalize(ctx, req, identity)
	if err != nil {
		return toolError("finalize_session", err)
	}

	out, _ := json.Marshal(resp)
	return mcp.TextResult(string(out)), nil
}
