package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"iclude/internal/mcp"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdioMockTool 测试用工具 / Mock tool for stdio tests
type stdioMockTool struct {
	def    mcp.ToolDefinition
	result string
}

func (m *stdioMockTool) Definition() mcp.ToolDefinition { return m.def }
func (m *stdioMockTool) Execute(_ context.Context, _ json.RawMessage) (*mcp.ToolResult, error) {
	return mcp.TextResult(m.result), nil
}

// buildStdin 构建 NDJSON 格式的 stdin / Build newline-delimited JSON stdin
func buildStdin(requests ...map[string]any) *bytes.Buffer {
	var buf bytes.Buffer
	for _, req := range requests {
		line, _ := json.Marshal(req)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return &buf
}

// parseResponses 解析 NDJSON 格式的 stdout / Parse newline-delimited JSON responses
func parseResponses(t *testing.T, stdout string) []mcp.JSONRPCResponse {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var resps []mcp.JSONRPCResponse
	for _, line := range lines {
		if line == "" {
			continue
		}
		var resp mcp.JSONRPCResponse
		require.NoError(t, json.Unmarshal([]byte(line), &resp), "failed to parse: %s", line)
		resps = append(resps, resp)
	}
	return resps
}

// handshakeLines 返回标准 MCP 握手行（initialize + notifications/initialized）/ Return standard MCP handshake lines
func handshakeLines() []map[string]any {
	return []map[string]any{
		{
			"jsonrpc": "2.0", "id": 0, "method": "initialize",
			"params": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
			},
		},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
	}
}

func TestStdioTransport_InitializeAndToolsList(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stdioMockTool{
		def: mcp.ToolDefinition{
			Name:        "test_tool",
			Description: "A test tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		result: "ok",
	})
	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	lines := handshakeLines()
	lines = append(lines, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	stdin := buildStdin(lines...)

	var stdout bytes.Buffer
	err := mcp.RunStdio(context.Background(), reg, identity, stdin, &stdout)
	assert.NoError(t, err)

	resps := parseResponses(t, stdout.String())
	// initialize 响应 + tools/list 响应（notifications/initialized 无响应）
	require.Len(t, resps, 2)

	assert.Nil(t, resps[0].Error)
	assert.Nil(t, resps[1].Error)
}

func TestStdioTransport_ToolCall(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stdioMockTool{
		def: mcp.ToolDefinition{
			Name:        "echo",
			Description: "Echo",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		result: "hello-world",
	})
	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	lines := handshakeLines()
	lines = append(lines, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "echo", "arguments": map[string]any{}},
	})
	stdin := buildStdin(lines...)

	var stdout bytes.Buffer
	err := mcp.RunStdio(context.Background(), reg, identity, stdin, &stdout)
	assert.NoError(t, err)

	resps := parseResponses(t, stdout.String())
	require.Len(t, resps, 2)
	assert.Nil(t, resps[1].Error)
}

func TestStdioTransport_InvalidJSON(t *testing.T) {
	reg := mcp.NewRegistry()
	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	stdin := bytes.NewBufferString("not-json\n")
	var stdout bytes.Buffer

	err := mcp.RunStdio(context.Background(), reg, identity, stdin, &stdout)
	assert.NoError(t, err)

	resps := parseResponses(t, stdout.String())
	require.Len(t, resps, 1)
	assert.NotNil(t, resps[0].Error)
	assert.Equal(t, -32700, resps[0].Error.Code)
}

func TestStdioTransport_EmptyLines(t *testing.T) {
	reg := mcp.NewRegistry()
	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	stdin := bytes.NewBufferString("\n\n\n")
	var stdout bytes.Buffer

	err := mcp.RunStdio(context.Background(), reg, identity, stdin, &stdout)
	assert.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(stdout.String()))
}
