// Package mcp stdio 传输层（NDJSON）/ stdio transport for MCP JSON-RPC with newline-delimited JSON
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// RunStdio 启动 stdio 传输层（NDJSON，阻塞直到 reader EOF 或 ctx 取消）
// Runs stdio transport with newline-delimited JSON: reads JSON-RPC from reader, writes responses to writer.
// Per MCP spec: "Messages are delimited by newlines, and MUST NOT contain embedded newlines."
func RunStdio(ctx context.Context, registry *Registry, identity *model.Identity, reader io.Reader, writer io.Writer) error {
	session := NewSession("stdio", registry, identity)
	defer session.Close()

	var mu sync.Mutex
	writeLine := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = writer.Write(data)
		_, _ = writer.Write([]byte("\n"))
	}

	// 转发 session.Out() 到 writer（处理异步消息）/ Forward async messages to writer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range session.Out() {
			writeLine(msg)
		}
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB 行缓冲 / 1MB line buffer
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			errResp := &JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			out, _ := json.Marshal(errResp)
			writeLine(out)
			continue
		}

		// 通知和普通请求都需要经过 HandleRequest（握手通知需要更新状态）/ Both notifications and requests go through HandleRequest
		resp := session.HandleRequest(ctx, &req)

		// 通知（无 ID）不需要响应 / Notifications (no ID) don't get responses
		if req.ID == nil || string(req.ID) == "null" {
			logger.Debug("stdio: received notification", zap.String("method", req.Method))
			continue
		}

		if resp != nil {
			out, err := json.Marshal(resp)
			if err != nil {
				logger.Error("stdio: failed to marshal response", zap.Error(err))
				continue
			}
			writeLine(out)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	session.Close()
	<-done
	return nil
}
