// Package mcp stdio 传输层（Content-Length framing）/ stdio transport for MCP JSON-RPC with Content-Length framing
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// readFrame 从 reader 读取一个 Content-Length 帧 / Read a single Content-Length framed message
func readFrame(reader *bufio.Reader) ([]byte, error) {
	// 读取 headers 直到空行 / Read headers until blank line
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")

		// 空行表示 header 结束 / Blank line signals end of headers
		if line == "" {
			break
		}

		// 解析 Content-Length / Parse Content-Length header
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length %q: %w", val, err)
			}
			contentLength = n
		}
		// 忽略其他 header（如 Content-Type）/ Ignore other headers
	}

	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	if contentLength > 4*1024*1024 {
		return nil, fmt.Errorf("Content-Length %d exceeds 4MB limit", contentLength)
	}

	// 按 Content-Length 读取 body / Read exactly Content-Length bytes
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("read body (%d bytes): %w", contentLength, err)
	}
	return body, nil
}

// writeFrame 写入一个 Content-Length 帧 / Write a single Content-Length framed message
func writeFrame(mu *sync.Mutex, writer io.Writer, data []byte) {
	mu.Lock()
	defer mu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	_, _ = writer.Write([]byte(header))
	_, _ = writer.Write(data)
}

// RunStdio 启动 stdio 传输层（Content-Length framing，阻塞直到 reader EOF 或 ctx 取消）
// Runs stdio transport with Content-Length framing: reads JSON-RPC from reader, writes responses to writer.
func RunStdio(ctx context.Context, registry *Registry, identity *model.Identity, reader io.Reader, writer io.Writer) error {
	session := NewSession("stdio", registry, identity)
	defer session.Close()

	var mu sync.Mutex
	bufReader := bufio.NewReaderSize(reader, 1024*1024) // 1MB 读缓冲 / 1MB read buffer

	// 转发 session.Out() 到 writer（处理异步消息）/ Forward async messages to writer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range session.Out() {
			writeFrame(&mu, writer, msg)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		body, err := readFrame(bufReader)
		if err != nil {
			// EOF 正常退出 / EOF is normal termination
			if err == io.EOF || strings.Contains(err.Error(), "EOF") {
				break
			}
			logger.Error("stdio: read frame error", zap.Error(err))
			errResp := &JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			out, _ := json.Marshal(errResp)
			writeFrame(&mu, writer, out)
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			errResp := &JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			out, _ := json.Marshal(errResp)
			writeFrame(&mu, writer, out)
			continue
		}

		// 通知（无 ID）不需要响应 / Notifications (no ID) don't get responses
		if req.ID == nil || string(req.ID) == "null" {
			logger.Debug("stdio: received notification", zap.String("method", req.Method))
			continue
		}

		resp := session.HandleRequest(ctx, &req)
		if resp != nil {
			out, err := json.Marshal(resp)
			if err != nil {
				logger.Error("stdio: failed to marshal response", zap.Error(err))
				continue
			}
			writeFrame(&mu, writer, out)
		}
	}

	session.Close()
	<-done
	return nil
}
