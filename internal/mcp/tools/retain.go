// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// MemoryCreator 记忆创建接口 / Interface for creating memories
type MemoryCreator interface {
	Create(ctx context.Context, mem *model.Memory) (*model.Memory, error)
}

// RetainPolicyChecker scope 策略检查接口（避免直接依赖 memory 包）/ Scope policy checker interface
type RetainPolicyChecker interface {
	GetByScope(ctx context.Context, scope string) (*model.ScopePolicy, error)
}

// RetainTool iclude_retain 工具 / iclude_retain tool handler
type RetainTool struct {
	manager       MemoryCreator
	policyChecker RetainPolicyChecker // 可为 nil / may be nil
}

// NewRetainTool 创建 retain 工具 / Create retain tool
func NewRetainTool(manager MemoryCreator, policyChecker RetainPolicyChecker) *RetainTool {
	return &RetainTool{manager: manager, policyChecker: policyChecker}
}

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
	MemoryClass string            `json:"memory_class,omitempty"`
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
                "scope":{"type":"string","description":"Scope rules: user preferences/habits → 'user/{owner_id}'; project knowledge/decisions/architecture → use project scope from session context; uncertain → omit and system auto-derives from session"},
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
		return toolInputError("invalid arguments")
	}
	if args.Content == "" {
		return mcp.ErrorResult("content is required"), nil
	}
	if err := ValidateWritePolicy(args.MemoryClass, args.SourceType); err != nil {
		return toolError("retain:validate_policy", err)
	}
	if err := ValidateScope(args.Scope); err != nil {
		return toolError("retain:validate_scope", err)
	}

	id := mcp.IdentityFromContext(ctx)

	// scope 自动推导 / Auto-derive scope when empty
	scope := args.Scope
	if scope == "" {
		// 优先从 session 获取项目 scope / Prefer project scope from session context
		if ps := mcp.ProjectScopeFromContext(ctx); ps != "" {
			scope = ps
		} else if id != nil && id.OwnerID != "" {
			scope = "user/" + id.OwnerID
		}
	}

	// scope 降级检查 / Scope downgrade check
	var downgraded bool
	var requestedScope, downgradeReason string
	if t.policyChecker != nil && id != nil {
		requestedScope = scope
		var actualScope string
		actualScope, downgraded, downgradeReason = checkAndDowngradeScope(ctx, t.policyChecker, scope, id.OwnerID)
		if downgraded {
			scope = actualScope
		}
	}

	mem := &model.Memory{
		Content:     args.Content,
		Scope:       scope,
		Kind:        args.Kind,
		ContextID:   args.ContextID,
		SourceType:  args.SourceType,
		MessageRole: args.MessageRole,
		MemoryClass: args.MemoryClass,
	}
	if id != nil {
		mem.TeamID = id.TeamID
		mem.OwnerID = id.OwnerID
	}
	if downgraded {
		mem.Visibility = model.VisibilityPrivate
	}

	created, err := t.manager.Create(ctx, mem)
	if err != nil {
		return toolError("retain", err)
	}

	resp := map[string]any{"id": created.ID, "content": created.Content}
	if downgraded {
		resp["scope_downgraded"] = true
		resp["requested_scope"] = requestedScope
		resp["actual_scope"] = scope
		resp["reason"] = downgradeReason
	}
	out, _ := json.Marshal(resp)
	return mcp.TextResult(string(out)), nil
}

// checkAndDowngradeScope 检查 scope 写入权限 / Check scope write permission and downgrade if denied
func checkAndDowngradeScope(ctx context.Context, checker RetainPolicyChecker, scope, ownerID string) (string, bool, string) {
	if !strings.HasPrefix(scope, "project/") {
		return scope, false, ""
	}
	policy, err := checker.GetByScope(ctx, scope)
	if err != nil {
		return scope, false, ""
	}
	if policy.CanWrite(ownerID) {
		return scope, false, ""
	}
	downgradedScope := "user/" + ownerID
	return downgradedScope, true, fmt.Sprintf("not in allowed_writers for %s", scope)
}
