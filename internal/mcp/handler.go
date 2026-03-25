// Package mcp MCP 处理器接口定义 / MCP handler interface definitions
package mcp

import (
	"context"
	"encoding/json"
)

// ToolHandler MCP 工具处理器接口 / MCP tool handler interface
type ToolHandler interface {
	// Definition 返回工具定义 / Returns the tool definition
	Definition() ToolDefinition
	// Execute 执行工具调用 / Executes a tool call with given arguments
	Execute(ctx context.Context, arguments json.RawMessage) (*ToolResult, error)
}

// ResourceHandler MCP 资源处理器接口 / MCP resource handler interface
type ResourceHandler interface {
	// Definition 返回资源定义 / Returns the resource definition
	Definition() ResourceDefinition
	// Read 读取资源内容 / Reads the resource content at the given URI
	Read(ctx context.Context, uri string) (string, error)
}

// PromptHandler MCP 提示模板处理器接口 / MCP prompt handler interface
type PromptHandler interface {
	// Definition 返回提示模板定义 / Returns the prompt template definition
	Definition() PromptDefinition
	// Get 渲染提示模板 / Renders the prompt template with given arguments
	Get(ctx context.Context, arguments map[string]string) (*PromptResult, error)
}
