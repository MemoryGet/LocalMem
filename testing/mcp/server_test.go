// Package mcp_test MCP 服务器集成测试 / MCP server integration tests
package mcp_test

import (
	"bufio"
	"bytes"
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

// newTestServer 创建用于测试的 MCP 服务器 / Create MCP server for testing
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.MCPConfig{
		DefaultTeamID:  "test-team",
		DefaultOwnerID: "test-owner",
	}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)
	return httptest.NewServer(srv.Handler())
}

// TestServer_SSE_opensStream GET /sse 返回 200 和 text/event-stream，并发送 endpoint 事件
// GET /sse returns 200 with text/event-stream content type and endpoint event
func TestServer_SSE_opensStream(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Use a client with no timeout so we can read the first SSE event
	client := &http.Client{Timeout: 3 * time.Second}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/sse", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	// Read first event (endpoint event)
	scanner := bufio.NewScanner(resp.Body)
	var eventType, eventData string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		} else if line == "" && eventType != "" {
			break // end of event block
		}
	}

	assert.Equal(t, "endpoint", eventType)
	assert.Contains(t, eventData, "/messages?session=")
}

// TestServer_SSE_rejectsNonGET GET 以外的方法返回 405 / Non-GET methods return 405
func TestServer_SSE_rejectsNonGET(t *testing.T) {
	cfg := config.MCPConfig{DefaultTeamID: "t", DefaultOwnerID: "u"}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)

	req := httptest.NewRequest(http.MethodPost, "/sse", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestServer_Messages_missingSession POST /messages 没有 session 参数返回 400
// POST /messages without session param returns 400
func TestServer_Messages_missingSession(t *testing.T) {
	cfg := config.MCPConfig{DefaultTeamID: "t", DefaultOwnerID: "u"}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req := httptest.NewRequest(http.MethodPost, "/messages", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestServer_Messages_unknownSession POST /messages?session=nonexistent 返回 404
// POST /messages?session=nonexistent returns 404
func TestServer_Messages_unknownSession(t *testing.T) {
	cfg := config.MCPConfig{DefaultTeamID: "t", DefaultOwnerID: "u"}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req := httptest.NewRequest(http.MethodPost, "/messages?session=nonexistent-uuid", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestServer_Messages_rejectsNonPOST POST 以外的方法返回 405 / Non-POST methods return 405
func TestServer_Messages_rejectsNonPOST(t *testing.T) {
	cfg := config.MCPConfig{DefaultTeamID: "t", DefaultOwnerID: "u"}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)

	req := httptest.NewRequest(http.MethodGet, "/messages?session=any", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestServer_Messages_invalidJSON 无效 JSON 请求体返回 400 / Invalid JSON body returns 400
func TestServer_Messages_invalidJSON(t *testing.T) {
	cfg := config.MCPConfig{DefaultTeamID: "t", DefaultOwnerID: "u"}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)

	// We need a session that actually exists. Use the handler directly by injecting a session.
	// Since we can't inject without SSE, test via full httptest.Server.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// First open SSE to create session, capture session ID
	client := &http.Client{Timeout: 3 * time.Second}
	sseReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/sse", nil)
	sseResp, err := client.Do(sseReq)
	require.NoError(t, err)
	defer sseResp.Body.Close()

	// Read endpoint event to get session ID
	sessionID := extractSessionID(t, sseResp)

	// Send invalid JSON
	body := bytes.NewBufferString(`not-json`)
	msgResp, err := http.Post(ts.URL+"/messages?session="+sessionID, "application/json", body)
	require.NoError(t, err)
	defer msgResp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, msgResp.StatusCode)
}

// TestServer_Messages_dispatches 完整流程：GET /sse → 捕获 session → POST /messages → 读取 SSE 响应
// Full flow: GET /sse (capture sessionID), POST /messages with ping, read SSE response containing pong
func TestServer_Messages_dispatches(t *testing.T) {
	cfg := config.MCPConfig{DefaultTeamID: "team1", DefaultOwnerID: "user1"}
	reg := mcp.NewRegistry()
	srv := mcp.NewServer(cfg, reg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Step 1: Open SSE connection
	client := &http.Client{Timeout: 5 * time.Second}
	sseReq, err := http.NewRequest(http.MethodGet, ts.URL+"/sse", nil)
	require.NoError(t, err)

	sseResp, err := client.Do(sseReq)
	require.NoError(t, err)
	defer sseResp.Body.Close()

	assert.Equal(t, http.StatusOK, sseResp.StatusCode)

	// Step 2: Read endpoint event and extract session ID
	sessionID := extractSessionID(t, sseResp)
	require.NotEmpty(t, sessionID, "session ID must not be empty")

	// Step 3: Send ping request via POST /messages
	pingBody := bytes.NewBufferString(`{"jsonrpc":"2.0","id":42,"method":"ping"}`)
	msgResp, err := http.Post(ts.URL+"/messages?session="+sessionID, "application/json", pingBody)
	require.NoError(t, err)
	defer msgResp.Body.Close()

	assert.Equal(t, http.StatusAccepted, msgResp.StatusCode)

	// Step 4: Read the SSE message event (pong response)
	// The scanner is positioned after the endpoint event; read the next event block
	scanner := bufio.NewScanner(sseResp.Body)
	var msgEventData string
	done := make(chan struct{})
	go func() {
		defer close(done)
		var currentData string
		var currentEvent string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				currentData = strings.TrimPrefix(line, "data: ")
			} else if line == "" && currentEvent == "message" {
				msgEventData = currentData
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for SSE message event")
	}

	require.NotEmpty(t, msgEventData, "SSE message event data must not be empty")

	// Verify the response is a valid JSON-RPC response for ping
	var resp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal([]byte(msgEventData), &resp))
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)

	// Verify the ID matches (42)
	var idVal float64
	require.NoError(t, json.Unmarshal(resp.ID, &idVal))
	assert.Equal(t, float64(42), idVal)
}

// extractSessionID SSE 响应中提取 endpoint event 的 session ID
// Extract session ID from endpoint event in SSE response
func extractSessionID(t *testing.T, resp *http.Response) string {
	t.Helper()
	scanner := bufio.NewScanner(resp.Body)
	var eventType, eventData string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		} else if line == "" && eventType == "endpoint" {
			break
		}
	}
	// eventData looks like: /messages?session=<uuid>
	parts := strings.SplitN(eventData, "session=", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
