// Package logger 结构化日志 / Structured logging with zap
package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var log *zap.Logger

// stdioMode 标记是否为 stdio 模式（日志写 stderr 避免污染 stdout）/ Flag for stdio mode: logs go to stderr
var stdioMode bool

// SetStdioMode 设置 stdio 模式，必须在 InitLogger 之前调用 / Must be called before InitLogger
func SetStdioMode(enabled bool) {
	stdioMode = enabled
}

// InitLogger 初始化日志器 / Initialize the global logger
func InitLogger() {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// stdio 模式日志写 stderr，避免污染 JSON-RPC 通信流 / In stdio mode, write logs to stderr to avoid corrupting JSON-RPC stream
	output := os.Stdout
	if stdioMode {
		output = os.Stderr
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(output),
		zapcore.InfoLevel,
	)

	log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
}

// GetLogger 获取日志器实例 / Get logger instance
func GetLogger() *zap.Logger {
	if log == nil {
		InitLogger()
	}
	return log
}

// Debug 调试日志 / Debug level log
func Debug(msg string, fields ...zap.Field) {
	GetLogger().Debug(msg, fields...)
}

// Info 信息日志 / Info level log
func Info(msg string, fields ...zap.Field) {
	GetLogger().Info(msg, fields...)
}

// Warn 警告日志 / Warning level log
func Warn(msg string, fields ...zap.Field) {
	GetLogger().Warn(msg, fields...)
}

// Error 错误日志 / Error level log
func Error(msg string, fields ...zap.Field) {
	GetLogger().Error(msg, fields...)
}

// Fatal 致命错误日志 / Fatal level log (process exits after)
func Fatal(msg string, fields ...zap.Field) {
	GetLogger().Fatal(msg, fields...)
}
