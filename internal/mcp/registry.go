// Package mcp MCP 处理器注册表 / MCP handler registry with thread-safe dispatch
package mcp

import "sync"

// Registry 线程安全的处理器注册表 / Thread-safe registry for tool, resource, and prompt handlers
type Registry struct {
	mu        sync.RWMutex
	tools     map[string]ToolHandler
	resources map[string]ResourceHandler
	prompts   map[string]PromptHandler
}

// NewRegistry 创建新的处理器注册表 / Creates a new empty handler registry
func NewRegistry() *Registry {
	return &Registry{
		tools:     make(map[string]ToolHandler),
		resources: make(map[string]ResourceHandler),
		prompts:   make(map[string]PromptHandler),
	}
}

// RegisterTool 注册工具处理器 / Registers a tool handler keyed by its definition name
func (r *Registry) RegisterTool(h ToolHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[h.Definition().Name] = h
}

// RegisterResource 注册资源处理器 / Registers a resource handler keyed by its definition URI
func (r *Registry) RegisterResource(h ResourceHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resources[h.Definition().URI] = h
}

// RegisterPrompt 注册提示模板处理器 / Registers a prompt handler keyed by its definition name
func (r *Registry) RegisterPrompt(h PromptHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts[h.Definition().Name] = h
}

// Tool 按名称查找工具处理器 / Looks up a tool handler by name; returns false if not found
func (r *Registry) Tool(name string) (ToolHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.tools[name]
	return h, ok
}

// Resource 按 URI 查找资源处理器 / Looks up a resource handler by URI; returns false if not found
func (r *Registry) Resource(uri string) (ResourceHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.resources[uri]
	return h, ok
}

// Prompt 按名称查找提示模板处理器 / Looks up a prompt handler by name; returns false if not found
func (r *Registry) Prompt(name string) (PromptHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.prompts[name]
	return h, ok
}

// Tools 返回所有已注册工具的定义列表 / Returns definitions of all registered tool handlers
func (r *Registry) Tools() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, h := range r.tools {
		defs = append(defs, h.Definition())
	}
	return defs
}

// Resources 返回所有已注册资源的定义列表 / Returns definitions of all registered resource handlers
func (r *Registry) Resources() []ResourceDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]ResourceDefinition, 0, len(r.resources))
	for _, h := range r.resources {
		defs = append(defs, h.Definition())
	}
	return defs
}

// Prompts 返回所有已注册提示模板的定义列表 / Returns definitions of all registered prompt handlers
func (r *Registry) Prompts() []PromptDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]PromptDefinition, 0, len(r.prompts))
	for _, h := range r.prompts {
		defs = append(defs, h.Definition())
	}
	return defs
}
