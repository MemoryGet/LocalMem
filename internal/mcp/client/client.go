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
	connectTimeout  = 3 * time.Second
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

// jsonRPCResponse JSON-RPC 2.0 响应体 / JSON-RPC 2.0 response body
type jsonRPCResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Client MCP JSON-RPC 客户端，持有 SSE 流用于同步响应 / MCP JSON-RPC client keeping SSE stream open for sync responses
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	endpoint   string       // e.g. "/messages?session=xxx" from SSE
	reqID      atomic.Int64
	sseResp    *http.Response      // 保持 SSE 流开启 / Keep SSE stream open
	responses  chan json.RawMessage // SSE 事件通道 / SSE event channel
	done       chan struct{}
}

// New 创建客户端 / Create client
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			// 不设全局超时，因为 SSE 流是长连接 / No global timeout — SSE stream is long-lived
			Timeout: 0,
		},
	}
}

// Connect 建立 SSE 会话，提取消息端点，并保持流开启用于同步响应
// Connect establishes an SSE session, extracts the message endpoint, and keeps the stream open for sync responses.
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

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("SSE connect returned status %d", resp.StatusCode)
	}

	// 先找到 endpoint 行，然后把流保留给后台读取协程
	// Find endpoint line first, then keep stream for background reader goroutine.
	scanner := bufio.NewScanner(resp.Body)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			c.endpoint = strings.TrimPrefix(line, "data: ")
			found = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		resp.Body.Close()
		return fmt.Errorf("SSE scan error: %w", err)
	}
	if !found {
		resp.Body.Close()
		return fmt.Errorf("SSE stream ended without endpoint data")
	}

	// 保存流，启动后台读取协程 / Save stream, start background reader goroutine
	c.sseResp = resp
	c.responses = make(chan json.RawMessage, 16)
	c.done = make(chan struct{})
	go c.readSSE()
	return nil
}

// readSSE 后台读取 SSE 流，将 JSON-RPC 响应发送到 channel
// readSSE reads the SSE stream in background, forwarding JSON-RPC responses to the channel.
func (c *Client) readSSE() {
	defer close(c.responses)
	scanner := bufio.NewScanner(c.sseResp.Body)
	for scanner.Scan() {
		select {
		case <-c.done:
			return
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		// 跳过 endpoint 事件行（已在 Connect 处理）/ Skip endpoint event lines (already handled in Connect)
		if strings.HasPrefix(data, "/messages") {
			continue
		}
		select {
		case c.responses <- json.RawMessage(data):
		case <-c.done:
			return
		}
	}
}

// Close 关闭 SSE 流 / Close the SSE stream
func (c *Client) Close() {
	if c.done != nil {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	}
	if c.sseResp != nil {
		c.sseResp.Body.Close()
	}
}

// callToolPost 发送 JSON-RPC tools/call POST 请求，返回请求 ID
// callToolPost sends a JSON-RPC tools/call POST request and returns the request ID.
func (c *Client) callToolPost(ctx context.Context, toolName string, arguments any) (int64, error) {
	if c.endpoint == "" {
		return 0, fmt.Errorf("not connected: call Connect first")
	}

	id := c.reqID.Add(1)
	payload := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      toolName,
			Arguments: arguments,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal tool call: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+c.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("failed to build tool call request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// 复用共享 httpClient，超时由调用方的 ctx 控制 / Reuse shared httpClient; timeout controlled by caller's ctx
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tool call request failed: %w", err)
	}
	defer resp.Body.Close()

	// 接受 202 Accepted 或 200 OK / Accept 202 Accepted or 200 OK
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("tool call returned unexpected status %d", resp.StatusCode)
	}
	return id, nil
}

// CallTool 调用 MCP 工具（fire-and-forget）/ Call MCP tool (fire-and-forget)
// POSTs a JSON-RPC 2.0 tools/call request; does not wait for the SSE response.
func (c *Client) CallTool(ctx context.Context, toolName string, arguments any) error {
	callCtx, cancel := context.WithTimeout(ctx, callToolTimeout)
	defer cancel()
	_, err := c.callToolPost(callCtx, toolName, arguments)
	return err
}

// CallToolSync 调用 MCP 工具并等待 SSE 流中的 JSON-RPC 响应
// CallToolSync calls an MCP tool and waits for the JSON-RPC response from the SSE stream.
func (c *Client) CallToolSync(ctx context.Context, toolName string, arguments any) (json.RawMessage, error) {
	callCtx, cancel := context.WithTimeout(ctx, callToolTimeout)
	defer cancel()

	id, err := c.callToolPost(callCtx, toolName, arguments)
	if err != nil {
		return nil, err
	}

	// 等待 SSE channel 中匹配 ID 的响应 / Wait for matching response ID from SSE channel
	for {
		select {
		case <-callCtx.Done():
			return nil, callCtx.Err()
		case msg, ok := <-c.responses:
			if !ok {
				return nil, fmt.Errorf("SSE stream closed before response received")
			}
			var rpcResp jsonRPCResponse
			if err := json.Unmarshal(msg, &rpcResp); err != nil {
				// 无法解析则跳过（可能是通知或其他消息）/ Skip unparseable messages (may be notifications)
				continue
			}
			if rpcResp.ID != id {
				// 不是我们的响应，继续等待 / Not our response, keep waiting
				continue
			}
			if rpcResp.Error != nil {
				return nil, fmt.Errorf("tool error: %s", rpcResp.Error.Message)
			}
			return rpcResp.Result, nil
		}
	}
}
