// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"
	"time"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// ContextCreator 上下文创建接口 / Interface for creating contexts
type ContextCreator interface {
	Create(ctx context.Context, req *model.CreateContextRequest) (*model.Context, error)
}

// CreateSessionTool iclude_create_session 工具 / iclude_create_session tool handler
type CreateSessionTool struct{ creator ContextCreator }

// NewCreateSessionTool 创建 create_session 工具 / Create create_session tool
func NewCreateSessionTool(creator ContextCreator) *CreateSessionTool {
	return &CreateSessionTool{creator: creator}
}

// createSessionArgs iclude_create_session 工具参数 / iclude_create_session tool arguments
type createSessionArgs struct {
	SessionID  string `json:"session_id"`
	ProjectDir string `json:"project_dir,omitempty"`
	Scope      string `json:"scope,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *CreateSessionTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_create_session",
		Description: "Create a session context for Claude Code hook integration. Call this at the start of a coding session (e.g., in PreToolUse hook) to establish a named context that groups memories by session, project directory, and scope.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "session_id":{"type":"string","description":"Unique session identifier (e.g. Claude Code session ID)"},
                "project_dir":{"type":"string","description":"Absolute path to the project directory"},
                "scope":{"type":"string","description":"Optional namespace scope for organizing session memories"}
            },
            "required":["session_id"]
        }`),
	}
}

// Execute 创建会话上下文并返回 context_id / Create session context and return context_id
func (t *CreateSessionTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args createSessionArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.SessionID == "" {
		return mcp.ErrorResult("session_id is required"), nil
	}

	metadata := map[string]any{
		"session_id":  args.SessionID,
		"started_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if args.ProjectDir != "" {
		metadata["project_dir"] = args.ProjectDir
	}

	req := &model.CreateContextRequest{
		Name:        args.SessionID,
		ContextType: "session",
		Scope:    args.Scope,
		Metadata: metadata,
	}

	created, err := t.creator.Create(ctx, req)
	if err != nil {
		return mcp.ErrorResult("failed to create session context: " + err.Error()), nil
	}

	out, _ := json.Marshal(map[string]any{
		"context_id": created.ID,
		"session_id": args.SessionID,
		"context_type": created.ContextType,
		"scope":      created.Scope,
	})
	return mcp.TextResult(string(out)), nil
}
