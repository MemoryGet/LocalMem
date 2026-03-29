// Package client MCP JSON-RPC 客户端，用于 CLI 钩子 / MCP JSON-RPC client for CLI hooks
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	connectTimeout  = 10 * time.Second
	callToolTimeout = 5 * time.Second
)

// jsonRPCRequest JSON-RPC 2.0 请求体 / JSON-RPC 2.0 request body
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// toolCallParams tools/call 方法参数 / Parameters for tools/call method
type toolCallParams struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

// Client MCP JSON-RPC 客户端 / MCP JSON-RPC client
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	endpoint   string // e.g. "/messages?session=xxx" from SSE
	reqID      atomic.Int64
}

// New 创建客户端 / Create client
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: connectTimeout,
		},
	}
}

// Connect 建立 SSE 会话，提取消息端点 / Establish SSE session and extract message endpoint
// GET /sse, reads first "data: " line to get the session endpoint, then closes the stream.
func (c *Client) Connect(ctx context.Context) error {
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(connectCtx, http.MethodGet, c.baseURL+"/sse", nil)
	if err != nil {
		return fmt.Errorf("failed to build SSE request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connect failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE connect returned status %d", resp.StatusCode)
	}

	// 扫描响应直到找到第一个 "data: " 行 / Scan response until first "data: " line
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			c.endpoint = strings.TrimPrefix(line, "data: ")
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE scan error: %w", err)
	}
	return fmt.Errorf("SSE stream ended without endpoint data")
}

// CallTool 调用 MCP 工具（fire-and-forget）/ Call MCP tool (fire-and-forget)
// POSTs a JSON-RPC 2.0 tools/call request and returns nil on 202 or 200.
func (c *Client) CallTool(ctx context.Context, toolName string, arguments any) error {
	if c.endpoint == "" {
		return fmt.Errorf("not connected: call Connect first")
	}

	callCtx, cancel := context.WithTimeout(ctx, callToolTimeout)
	defer cancel()

	payload := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      c.reqID.Add(1),
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      toolName,
			Arguments: arguments,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal tool call: %w", err)
	}

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build tool call request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// 为 CallTool 使用较短超时的客户端 / Use a client with the shorter timeout for CallTool
	callClient := &http.Client{Timeout: callToolTimeout}
	resp, err := callClient.Do(req)
	if err != nil {
		return fmt.Errorf("tool call request failed: %w", err)
	}
	defer resp.Body.Close()

	// 接受 202 Accepted 或 200 OK / Accept 202 Accepted or 200 OK
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tool call returned unexpected status %d", resp.StatusCode)
	}
	return nil
}
