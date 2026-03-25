// Package mcp MCP 客户端会话 / Per-client MCP session with identity context and JSON-RPC dispatch
package mcp

import (
	"context"
	"encoding/json"
	"sync"

	"iclude/internal/model"
)

// identityCtxKey 私有 context key，防止与 gin string key 碰撞 / Unexported context key type prevents collisions
type identityCtxKey struct{}

// WithIdentity 注入身份到 context / Inject identity into context
func WithIdentity(ctx context.Context, id *model.Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext 从 context 获取身份 / Get identity from context; nil if absent
func IdentityFromContext(ctx context.Context) *model.Identity {
	id, _ := ctx.Value(identityCtxKey{}).(*model.Identity)
	return id
}

// Session 单个 MCP 客户端会话 / Single MCP client session with identity and JSON-RPC dispatch
type Session struct {
	id       string
	registry *Registry
	identity *model.Identity
	outCh    chan []byte // SSE 输出 channel（未导出） / SSE output channel (unexported)
	once     sync.Once  // 保证 outCh 只关闭一次 / Ensures outCh is closed exactly once
}

// NewSession 创建新的客户端会话 / Create a new client session
func NewSession(id string, registry *Registry, identity *model.Identity) *Session {
	return &Session{
		id:       id,
		registry: registry,
		identity: identity,
		outCh:    make(chan []byte, 64),
	}
}

// ID 返回会话 ID / Return session ID
func (s *Session) ID() string { return s.id }

// Out 返回只读 SSE 输出 channel / Return read-only SSE output channel
func (s *Session) Out() <-chan []byte { return s.outCh }

// Close 安全关闭输出 channel，幂等 / Safely close output channel, idempotent
func (s *Session) Close() {
	s.once.Do(func() { close(s.outCh) })
}

// Dispatch 处理单个原始 JSON-RPC 请求字节 / Handle a single raw JSON-RPC request and send response to Out()
func (s *Session) Dispatch(ctx context.Context, raw []byte) {
	ctx = WithIdentity(ctx, s.identity)

	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.send(errResponse(nil, -32700, "parse error"))
		return
	}

	resp := s.HandleRequest(ctx, &req)
	s.send(resp)
}

// HandleRequest 处理 JSON-RPC 请求，返回响应 / Handle a JSON-RPC request and return response
func (s *Session) HandleRequest(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	// 注入当前会话身份 / Inject session identity into context
	ctx = WithIdentity(ctx, s.identity)

	switch req.Method {
	case MethodInitialize:
		return s.handleInitialize(req)
	case MethodPing:
		return okResponse(req.ID, map[string]any{})
	case MethodToolsList:
		return okResponse(req.ID, map[string]any{"tools": s.registry.Tools()})
	case MethodToolsCall:
		return s.handleToolsCall(ctx, req)
	case MethodResourcesList:
		return okResponse(req.ID, map[string]any{"resources": s.registry.Resources()})
	case MethodResourcesRead:
		return s.handleResourcesRead(ctx, req)
	case MethodPromptsList:
		return okResponse(req.ID, map[string]any{"prompts": s.registry.Prompts()})
	case MethodPromptsGet:
		return s.handlePromptsGet(ctx, req)
	default:
		return errResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Session) handleInitialize(req *JSONRPCRequest) *JSONRPCResponse {
	return okResponse(req.ID, map[string]any{
		"protocolVersion": MCPProtocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"subscribe": false, "listChanged": false},
			"prompts":   map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{"name": "iclude-mcp", "version": "1.0.0"},
	})
}

func (s *Session) handleToolsCall(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	h, ok := s.registry.Tool(params.Name)
	if !ok {
		return errResponse(req.ID, -32601, "unknown tool: "+params.Name)
	}
	result, err := h.Execute(ctx, params.Arguments)
	if err != nil {
		return errResponse(req.ID, -32603, "tool execution error: "+err.Error())
	}
	return okResponse(req.ID, result)
}

func (s *Session) handleResourcesRead(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	var params ReadResourceParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	h, ok := s.registry.Resource(params.URI)
	if !ok {
		return errResponse(req.ID, -32601, "unknown resource: "+params.URI)
	}
	content, err := h.Read(ctx, params.URI)
	if err != nil {
		return errResponse(req.ID, -32603, err.Error())
	}
	return okResponse(req.ID, map[string]any{
		"contents": []map[string]any{
			{"uri": params.URI, "mimeType": "application/json", "text": content},
		},
	})
}

func (s *Session) handlePromptsGet(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	var params GetPromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	h, ok := s.registry.Prompt(params.Name)
	if !ok {
		return errResponse(req.ID, -32601, "unknown prompt: "+params.Name)
	}
	result, err := h.Get(ctx, params.Arguments)
	if err != nil {
		return errResponse(req.ID, -32603, err.Error())
	}
	return okResponse(req.ID, result)
}

// send 序列化并发送响应到输出 channel / Serialize and send response to output channel; drops if full
func (s *Session) send(resp *JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	select {
	case s.outCh <- data:
	default:
		// 丢弃：channel 满时降级 / Drop: graceful degradation when channel is full
	}
}
