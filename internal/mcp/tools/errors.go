package tools

import (
	"iclude/internal/logger"
	"iclude/internal/mcp"

	"go.uber.org/zap"
)

// toolError 记录详细错误日志并返回脱敏消息给客户端 / Log detailed error and return sanitized message to client
func toolError(op string, err error) (*mcp.ToolResult, error) {
	logger.Error("mcp tool error", zap.String("op", op), zap.Error(err))
	return mcp.ErrorResult(op + " failed"), nil
}

// toolInputError 输入参数错误（可安全展示给客户端）/ Input validation error (safe to show)
func toolInputError(msg string) (*mcp.ToolResult, error) {
	return mcp.ErrorResult(msg), nil
}
