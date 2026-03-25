// Package mcp_test MCP SSE+消息全流程集成测试 / MCP SSE+message full handshake integration tests
package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/mcp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoTool 用于集成测试的桩工具 / Stub tool used in integration tests
type echoTool struct{}

// Definition 返回 echo 工具定义 / Return echo tool definition
func (e *echoTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "echo",
		Description: "Echo test tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

// Execute 回显输入参数 / Echo back the input arguments
func (e *echoTool) Execute(_ context.Context, args json.RawMessage) (*mcp.ToolResult, error) {
	return mcp.TextResult("echo: " + string(args)), nil
}

// readSSEFrame 读取单个 SSE 帧，返回 event 和 data 字段 / Read a single SSE frame, returning event and data fields
func readSSEFrame(reader *bufio.Reader) (event, data string, err error) {
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")

		if readErr != nil {
			// Return whatever we have along with the error
			return event, data, readErr
		}

		if line == "" {
			// Blank line = end of SSE frame
			return event, data, nil
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// sseCollector 后台读取 SSE 流，将 data 字段发送到 channel / Background SSE reader that sends data fields to a channel
func sseCollector(reader *bufio.Reader, ch chan<- string, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}
		_, data, err := readSSEFrame(reader)
		if err != nil {
			return
		}
		if data != "" {
			select {
			case ch <- data:
			case <-done:
				return
			}
		}
	}
}

// postJSON 向 URL 发送 JSON-RPC 请求 / POST a JSON-RPC request to the given URL
func postJSON(t *testing.T, client *http.Client, url string, body string) *http.Response {
	t.Helper()
	resp, err := client.Post(url, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	return resp
}

// waitSSEResponse 等待 SSE channel 收到响应，超时则 fatal / Wait for SSE channel to receive a response, fatal on timeout
func waitSSEResponse(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case data := <-ch:
		return data
	case <-time.After(timeout):
		t.Fatal("timeout waiting for SSE response")
		return ""
	}
}

// newIntegrationServer 创建带 echo 工具的完整测试服务器 / Create a full test server with the echo tool registered
func newIntegrationServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	cfg := config.MCPConfig{
		Enabled:        true,
		Port:           0, // unused with httptest
		DefaultTeamID:  "test-team",
		DefaultOwnerID: "test-user",
	}
	reg := mcp.NewRegistry()
	reg.RegisterTool(&echoTool{})
	srv := mcp.NewServer(cfg, reg)
	ts := httptest.NewServer(srv.Handler())
	// No global timeout so the SSE stream stays open across multiple reads
	client := &http.Client{}
	t.Cleanup(ts.Close)
	return ts, client
}

// openSSE 打开 SSE 连接并返回 reader 和 sessionID / Open SSE connection and return reader + sessionID
func openSSE(t *testing.T, client *http.Client, baseURL string) (*http.Response, *bufio.Reader, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/sse", nil)
	require.NoError(t, err)

	sseResp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, sseResp.StatusCode)
	assert.Contains(t, sseResp.Header.Get("Content-Type"), "text/event-stream")

	reader := bufio.NewReader(sseResp.Body)

	// Read the mandatory endpoint event
	event, data, err := readSSEFrame(reader)
	require.NoError(t, err, "failed to read endpoint SSE frame")
	require.Equal(t, "endpoint", event, "first SSE event must be 'endpoint'")
	require.Contains(t, data, "/messages?session=", "endpoint data must contain session URL")

	// Extract session ID from data ("/messages?session=<uuid>")
	parts := strings.SplitN(data, "session=", 2)
	require.Len(t, parts, 2, "endpoint data must contain session= parameter")
	sessionID := parts[1]
	require.NotEmpty(t, sessionID)

	return sseResp, reader, sessionID
}

// TestMCPIntegration_FullHandshake 完整的 SSE+消息握手流程测试
// Tests the complete MCP SSE+message handshake: initialize → tools/list → ping
func TestMCPIntegration_FullHandshake(t *testing.T) {
	ts, client := newIntegrationServer(t)

	// Step 1: Open SSE connection and extract session ID
	sseResp, reader, sessionID := openSSE(t, client, ts.URL)
	defer sseResp.Body.Close()

	msgURL := ts.URL + "/messages?session=" + sessionID

	// Start background SSE collector
	sseCh := make(chan string, 16)
	done := make(chan struct{})
	defer close(done)
	go sseCollector(reader, sseCh, done)

	// Step 2: Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`
	initResp := postJSON(t, client, msgURL, initReq)
	defer initResp.Body.Close()
	assert.Equal(t, http.StatusAccepted, initResp.StatusCode)

	// Step 3: Read SSE response for initialize — verify protocolVersion
	initData := waitSSEResponse(t, sseCh, 5*time.Second)
	require.NotEmpty(t, initData)

	var initRPC mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal([]byte(initData), &initRPC), "initialize response must be valid JSON-RPC")
	require.Nil(t, initRPC.Error, "initialize must not return an error")

	// Verify protocolVersion in result
	resultBytes, err := json.Marshal(initRPC.Result)
	require.NoError(t, err)
	var resultMap map[string]interface{}
	require.NoError(t, json.Unmarshal(resultBytes, &resultMap))
	assert.Equal(t, "2024-11-05", resultMap["protocolVersion"], "protocolVersion must be 2024-11-05")

	// Step 4: Send tools/list request
	toolsReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	toolsResp := postJSON(t, client, msgURL, toolsReq)
	defer toolsResp.Body.Close()
	assert.Equal(t, http.StatusAccepted, toolsResp.StatusCode)

	// Step 5: Read SSE response for tools/list — verify echo tool is present
	toolsData := waitSSEResponse(t, sseCh, 5*time.Second)
	require.NotEmpty(t, toolsData)

	var toolsRPC mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal([]byte(toolsData), &toolsRPC), "tools/list response must be valid JSON-RPC")
	require.Nil(t, toolsRPC.Error, "tools/list must not return an error")

	toolsResultBytes, err := json.Marshal(toolsRPC.Result)
	require.NoError(t, err)
	var toolsResultMap map[string]interface{}
	require.NoError(t, json.Unmarshal(toolsResultBytes, &toolsResultMap))

	toolsList, ok := toolsResultMap["tools"].([]interface{})
	require.True(t, ok, "tools/list result must contain 'tools' array")
	require.Len(t, toolsList, 1, "exactly one tool must be registered")

	firstTool, ok := toolsList[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "echo", firstTool["name"], "registered tool must be named 'echo'")

	// Step 6: Send ping request
	pingReq := `{"jsonrpc":"2.0","id":3,"method":"ping"}`
	pingResp := postJSON(t, client, msgURL, pingReq)
	defer pingResp.Body.Close()
	assert.Equal(t, http.StatusAccepted, pingResp.StatusCode)

	// Step 7: Read SSE response for ping — verify empty result
	pingData := waitSSEResponse(t, sseCh, 5*time.Second)
	require.NotEmpty(t, pingData)

	var pingRPC mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal([]byte(pingData), &pingRPC), "ping response must be valid JSON-RPC")
	require.Nil(t, pingRPC.Error, "ping must not return an error")

	// Ping returns an empty object {}
	pingResultBytes, err := json.Marshal(pingRPC.Result)
	require.NoError(t, err)
	var pingResult map[string]interface{}
	require.NoError(t, json.Unmarshal(pingResultBytes, &pingResult))
	assert.Empty(t, pingResult, "ping result must be an empty object")

	// Verify ID round-trip (id=3)
	var idVal float64
	require.NoError(t, json.Unmarshal(pingRPC.ID, &idVal))
	assert.Equal(t, float64(3), idVal)
}

// TestMCPIntegration_UnknownSession POST /messages?session=bad 返回 404
// POST /messages with an unknown session ID returns 404
func TestMCPIntegration_UnknownSession(t *testing.T) {
	cfg := config.MCPConfig{
		Enabled:        true,
		Port:           0,
		DefaultTeamID:  "test-team",
		DefaultOwnerID: "test-user",
	}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	resp, err := http.Post(ts.URL+"/messages?session=bad-session-id", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
