// Package tools MCP 工具实现层 / MCP tool implementations
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// Reflector 反思推理接口 / Interface for reflect engine
type Reflector interface {
	Reflect(ctx context.Context, req *model.ReflectRequest) (*model.ReflectResponse, error)
}

// ReflectTool iclude_reflect 工具 / iclude_reflect tool handler
type ReflectTool struct{ engine Reflector }

// NewReflectTool 创建 reflect 工具实例 / Create a new ReflectTool instance
func NewReflectTool(engine Reflector) *ReflectTool {
	return &ReflectTool{engine: engine}
}

// reflectArgs iclude_reflect 工具参数 / iclude_reflect tool arguments
type reflectArgs struct {
	Question  string `json:"question"`
	Scope     string `json:"scope,omitempty"`
	MaxRounds int    `json:"max_rounds,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *ReflectTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_reflect",
		Description: "Run multi-round LLM reasoning over stored memories. **Use when the question requires synthesizing multiple memories or cross-referencing facts** that a single search cannot answer. Returns a structured conclusion with reasoning trace and source memory IDs.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "question":{"type":"string","description":"The question or topic to reason about"},
                "scope":{"type":"string","description":"Namespace scope to restrict memory retrieval"},
                "max_rounds":{"type":"integer","minimum":1,"maximum":10,"description":"Maximum reasoning rounds (default: engine config)"}
            },
            "required":["question"]
        }`),
	}
}

// reflectOutput iclude_reflect 响应负载 / iclude_reflect response payload
type reflectOutput struct {
	Result     string   `json:"result"`
	RoundsUsed int      `json:"rounds_used"`
	Sources    []string `json:"sources"`
}

// Execute 执行反思推理并返回结论 / Execute reflect reasoning and return conclusion
func (t *ReflectTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args reflectArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.Question == "" {
		return mcp.ErrorResult("question is required"), nil
	}

	req := &model.ReflectRequest{
		Question:  args.Question,
		Scope:     args.Scope,
		MaxRounds: args.MaxRounds,
	}

	// 注入身份信息（如有）/ Inject identity when available
	if id := mcp.IdentityFromContext(ctx); id != nil {
		req.TeamID = id.TeamID
		req.OwnerID = id.OwnerID
	}

	resp, err := t.engine.Reflect(ctx, req)
	if err != nil {
		return mcp.ErrorResult("reflect failed: " + err.Error()), nil
	}

	out := reflectOutput{
		Result:     resp.Result,
		RoundsUsed: resp.Metadata.RoundsUsed,
		Sources:    resp.Sources,
	}
	raw, _ := json.Marshal(out)
	return mcp.TextResult(string(raw)), nil
}
