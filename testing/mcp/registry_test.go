// Package mcp_test registry 单元测试 / Registry unit tests
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp"
)

// --- stub implementations ---

// stubTool 测试用工具存根 / Tool stub for testing
type stubTool struct {
	name string
	desc string
}

func (s *stubTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        s.name,
		Description: s.desc,
		InputSchema: json.RawMessage(`{}`),
	}
}

func (s *stubTool) Execute(_ context.Context, _ json.RawMessage) (*mcp.ToolResult, error) {
	return mcp.TextResult("ok"), nil
}

// stubResource 测试用资源存根 / Resource stub for testing
type stubResource struct {
	uri  string
	name string
}

func (s *stubResource) Definition() mcp.ResourceDefinition {
	return mcp.ResourceDefinition{URI: s.uri, Name: s.name}
}

func (s *stubResource) Read(_ context.Context, _ string) (string, error) {
	return "content", nil
}

// stubPrompt 测试用提示模板存根 / Prompt stub for testing
type stubPrompt struct {
	name string
	desc string
}

func (s *stubPrompt) Definition() mcp.PromptDefinition {
	return mcp.PromptDefinition{Name: s.name, Description: s.desc}
}

func (s *stubPrompt) Get(_ context.Context, _ map[string]string) (*mcp.PromptResult, error) {
	return &mcp.PromptResult{Messages: []mcp.PromptMessage{}}, nil
}

// --- tests ---

// TestRegistry_Tool_RegisterAndLookup 注册和查找工具 / Register and lookup tool handler
func TestRegistry_Tool_RegisterAndLookup(t *testing.T) {
	cases := []struct {
		name      string
		register  string
		lookup    string
		wantFound bool
	}{
		{name: "found", register: "search_memory", lookup: "search_memory", wantFound: true},
		{name: "not_found", register: "search_memory", lookup: "missing_tool", wantFound: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mcp.NewRegistry()
			r.RegisterTool(&stubTool{name: tc.register, desc: "desc"})

			h, ok := r.Tool(tc.lookup)
			if ok != tc.wantFound {
				t.Fatalf("Tool(%q) found=%v, want %v", tc.lookup, ok, tc.wantFound)
			}
			if tc.wantFound && h.Definition().Name != tc.register {
				t.Errorf("Definition().Name=%q, want %q", h.Definition().Name, tc.register)
			}
		})
	}
}

// TestRegistry_Tool_ListAll 注册多个工具并列出全部 / Register multiple tools and list all definitions
func TestRegistry_Tool_ListAll(t *testing.T) {
	r := mcp.NewRegistry()
	tools := []string{"tool_a", "tool_b", "tool_c"}
	for _, name := range tools {
		r.RegisterTool(&stubTool{name: name, desc: name + " desc"})
	}

	defs := r.Tools()
	if len(defs) != len(tools) {
		t.Fatalf("Tools() returned %d definitions, want %d", len(defs), len(tools))
	}

	// 验证所有名称都存在 / Verify all names are present
	nameSet := make(map[string]bool, len(defs))
	for _, d := range defs {
		nameSet[d.Name] = true
	}
	for _, name := range tools {
		if !nameSet[name] {
			t.Errorf("missing tool definition for %q", name)
		}
	}
}

// TestRegistry_Resource_RegisterAndLookup 注册和查找资源 / Register and lookup resource handler
func TestRegistry_Resource_RegisterAndLookup(t *testing.T) {
	cases := []struct {
		name      string
		registerURI string
		lookupURI string
		wantFound bool
	}{
		{name: "found", registerURI: "mem://memories", lookupURI: "mem://memories", wantFound: true},
		{name: "not_found", registerURI: "mem://memories", lookupURI: "mem://other", wantFound: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mcp.NewRegistry()
			r.RegisterResource(&stubResource{uri: tc.registerURI, name: "memories"})

			h, ok := r.Resource(tc.lookupURI)
			if ok != tc.wantFound {
				t.Fatalf("Resource(%q) found=%v, want %v", tc.lookupURI, ok, tc.wantFound)
			}
			if tc.wantFound && h.Definition().URI != tc.registerURI {
				t.Errorf("Definition().URI=%q, want %q", h.Definition().URI, tc.registerURI)
			}
		})
	}
}

// TestRegistry_Resource_ListAll 注册多个资源并列出全部 / Register multiple resources and list all definitions
func TestRegistry_Resource_ListAll(t *testing.T) {
	r := mcp.NewRegistry()
	uris := []string{"mem://a", "mem://b", "mem://c"}
	for _, uri := range uris {
		r.RegisterResource(&stubResource{uri: uri, name: uri})
	}

	defs := r.Resources()
	if len(defs) != len(uris) {
		t.Fatalf("Resources() returned %d definitions, want %d", len(defs), len(uris))
	}

	uriSet := make(map[string]bool, len(defs))
	for _, d := range defs {
		uriSet[d.URI] = true
	}
	for _, uri := range uris {
		if !uriSet[uri] {
			t.Errorf("missing resource definition for %q", uri)
		}
	}
}

// TestRegistry_Prompt_RegisterAndLookup 注册和查找提示模板 / Register and lookup prompt handler
func TestRegistry_Prompt_RegisterAndLookup(t *testing.T) {
	cases := []struct {
		name      string
		register  string
		lookup    string
		wantFound bool
	}{
		{name: "found", register: "recall_context", lookup: "recall_context", wantFound: true},
		{name: "not_found", register: "recall_context", lookup: "unknown_prompt", wantFound: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mcp.NewRegistry()
			r.RegisterPrompt(&stubPrompt{name: tc.register, desc: "desc"})

			h, ok := r.Prompt(tc.lookup)
			if ok != tc.wantFound {
				t.Fatalf("Prompt(%q) found=%v, want %v", tc.lookup, ok, tc.wantFound)
			}
			if tc.wantFound && h.Definition().Name != tc.register {
				t.Errorf("Definition().Name=%q, want %q", h.Definition().Name, tc.register)
			}
		})
	}
}

// TestRegistry_Prompt_ListAll 注册多个提示模板并列出全部 / Register multiple prompts and list all definitions
func TestRegistry_Prompt_ListAll(t *testing.T) {
	r := mcp.NewRegistry()
	prompts := []string{"prompt_x", "prompt_y"}
	for _, name := range prompts {
		r.RegisterPrompt(&stubPrompt{name: name, desc: name + " desc"})
	}

	defs := r.Prompts()
	if len(defs) != len(prompts) {
		t.Fatalf("Prompts() returned %d definitions, want %d", len(defs), len(prompts))
	}

	nameSet := make(map[string]bool, len(defs))
	for _, d := range defs {
		nameSet[d.Name] = true
	}
	for _, name := range prompts {
		if !nameSet[name] {
			t.Errorf("missing prompt definition for %q", name)
		}
	}
}

// TestRegistry_ConcurrentAccess 并发注册和查找 / Concurrent register and lookup (race detector)
func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := mcp.NewRegistry()
	done := make(chan struct{})

	// 并发注册 / Concurrent registration
	go func() {
		for i := 0; i < 50; i++ {
			r.RegisterTool(&stubTool{name: "tool_concurrent", desc: "desc"})
		}
		close(done)
	}()

	// 并发查找 / Concurrent lookup
	go func() {
		for i := 0; i < 50; i++ {
			r.Tool("tool_concurrent")
		}
	}()

	<-done
}
