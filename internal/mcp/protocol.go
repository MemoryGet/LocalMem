// Package mcp MCP 协议层 / MCP protocol layer — JSON-RPC 2.0 types and MCP method constants
package mcp

import "encoding/json"

// MCP method constants
const (
	MethodInitialize    = "initialize"
	MethodPing          = "ping"
	MethodToolsList     = "tools/list"
	MethodToolsCall     = "tools/call"
	MethodResourcesList = "resources/list"
	MethodResourcesRead = "resources/read"
	MethodPromptsList   = "prompts/list"
	MethodPromptsGet    = "prompts/get"
)

// MCPProtocolVersion MCP 协议版本 / MCP protocol version for SSE transport
const MCPProtocolVersion = "2024-11-05"

// JSONRPCRequest JSON-RPC 2.0 请求 / JSON-RPC 2.0 request envelope
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse JSON-RPC 2.0 响应 / JSON-RPC 2.0 response envelope
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError JSON-RPC 错误体 / JSON-RPC error object
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ContentBlock 内容块 / Content block (text type)
type ContentBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text,omitempty"`
}

// ToolResult 工具调用结果 / Tool call result
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ToolDefinition MCP tools/list 响应项 / MCP tools/list response item
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ResourceDefinition MCP resources/list 响应项 / MCP resources/list response item
type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// PromptDefinition MCP prompts/list 响应项 / MCP prompts/list response item
type PromptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument 提示模板参数定义 / Prompt argument definition
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptMessage 提示模板消息 / Prompt message
type PromptMessage struct {
	Role    string       `json:"role"`
	Content ContentBlock `json:"content"`
}

// PromptResult 提示模板渲染结果 / Prompt template render result
type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// CallToolParams tools/call 参数 / tools/call params
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ReadResourceParams resources/read 参数 / resources/read params
type ReadResourceParams struct {
	URI string `json:"uri"`
}

// GetPromptParams prompts/get 参数 / prompts/get params
type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// TextResult 创建文本内容结果 / Create text content result
func TextResult(text string) *ToolResult {
	return &ToolResult{Content: []ContentBlock{{Type: "text", Text: text}}}
}

// ErrorResult 创建错误结果 / Create error result
func ErrorResult(msg string) *ToolResult {
	return &ToolResult{Content: []ContentBlock{{Type: "text", Text: msg}}, IsError: true}
}

// okResponse 构建成功 JSON-RPC 响应 / Build successful JSON-RPC response
func okResponse(id json.RawMessage, result any) *JSONRPCResponse {
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// errResponse 构建错误 JSON-RPC 响应 / Build error JSON-RPC response
func errResponse(id json.RawMessage, code int, message string) *JSONRPCResponse {
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &JSONRPCError{Code: code, Message: message}}
}
