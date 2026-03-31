package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// writeFrame 写入 Content-Length 帧到 buffer / Write a Content-Length framed message to buffer
func writeFrame(buf *bytes.Buffer, msg map[string]any) {
	body, _ := json.Marshal(msg)
	fmt.Fprintf(buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
}

// buildFramedStdin 构建 Content-Length 帧格式的 stdin / Build Content-Length framed stdin
func buildFramedStdin(requests ...map[string]any) *bytes.Buffer {
	var buf bytes.Buffer
	for _, req := range requests {
		writeFrame(&buf, req)
	}
	return &buf
}

// parseFramedResponses 解析 Content-Length 帧格式的 stdout / Parse Content-Length framed responses from stdout
func parseFramedResponses(t *testing.T, stdout string) []mcp.JSONRPCResponse {
	t.Helper()
	var resps []mcp.JSONRPCResponse
	remaining := stdout
	for remaining != "" {
		// 查找 Content-Length header / Find Content-Length header
		idx := strings.Index(remaining, "Content-Length: ")
		if idx == -1 {
			break
		}
		remaining = remaining[idx+len("Content-Length: "):]

		// 读取长度值 / Read length value
		endIdx := strings.Index(remaining, "\r\n\r\n")
		if endIdx == -1 {
			break
		}
		var contentLen int
		fmt.Sscanf(remaining[:endIdx], "%d", &contentLen)
		remaining = remaining[endIdx+4:]

		if len(remaining) < contentLen {
			break
		}

		body := remaining[:contentLen]
		remaining = remaining[contentLen:]

		var resp mcp.JSONRPCResponse
		require.NoError(t, json.Unmarshal([]byte(body), &resp), "failed to parse: %s", body)
		resps = append(resps, resp)
	}
	return resps
}

// handshakeFrames 返回标准 MCP 握手帧（initialize + notifications/initialized）/ Return standard MCP handshake frames
func handshakeFrames(buf *bytes.Buffer) {
	writeFrame(buf, map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	})
	// notifications/initialized（无 ID = 通知）/ No ID = notification
	writeFrame(buf, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})
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

	var stdin bytes.Buffer
	handshakeFrames(&stdin)
	writeFrame(&stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})

	var stdout bytes.Buffer
	err := mcp.RunStdio(context.Background(), reg, identity, &stdin, &stdout)
	assert.NoError(t, err)

	resps := parseFramedResponses(t, stdout.String())
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

	var stdin bytes.Buffer
	handshakeFrames(&stdin)
	writeFrame(&stdin, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "echo", "arguments": map[string]any{}},
	})

	var stdout bytes.Buffer
	err := mcp.RunStdio(context.Background(), reg, identity, &stdin, &stdout)
	assert.NoError(t, err)

	resps := parseFramedResponses(t, stdout.String())
	// initialize 响应 + tools/call 响应
	require.Len(t, resps, 2)
	assert.Nil(t, resps[1].Error)
}

func TestStdioTransport_InvalidJSON(t *testing.T) {
	reg := mcp.NewRegistry()
	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	// 发送一个合法帧但内容不是 JSON / Send a valid frame with non-JSON body
	body := []byte("not-json")
	var stdin bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)

	var stdout bytes.Buffer
	err := mcp.RunStdio(context.Background(), reg, identity, &stdin, &stdout)
	assert.NoError(t, err)

	resps := parseFramedResponses(t, stdout.String())
	require.Len(t, resps, 1)
	assert.NotNil(t, resps[0].Error)
	assert.Equal(t, -32700, resps[0].Error.Code)
}

func TestStdioTransport_EmptyInput(t *testing.T) {
	reg := mcp.NewRegistry()
	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	stdin := bytes.NewBufferString("")
	var stdout bytes.Buffer

	err := mcp.RunStdio(context.Background(), reg, identity, stdin, &stdout)
	assert.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(stdout.String()))
}
