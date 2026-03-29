// Package document provides document parsing and ingestion capabilities.
// 文档解析与摄取能力包。
package document

import (
	"context"
	"fmt"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// Parser 文档解析器接口 / Document parser interface
type Parser interface {
	Parse(ctx context.Context, filePath string, docType string) (*ParseResult, error)
	Supports(docType string) bool
}

// ParseResult 解析结果 / Parse result
type ParseResult struct {
	Content  string         `json:"content"`
	Format   string         `json:"format"`   // "markdown" | "plaintext"
	Metadata map[string]any `json:"metadata"`
}

// ParseRouter 解析路由器 / Parse router with primary → fallback chain
type ParseRouter struct {
	primary  Parser
	fallback Parser
}

// NewParseRouter 创建解析路由器 / Create parse router
func NewParseRouter(primary, fallback Parser) *ParseRouter {
	return &ParseRouter{primary: primary, fallback: fallback}
}

// Parse 解析文件（降级链）/ Parse file with fallback chain
func (r *ParseRouter) Parse(ctx context.Context, filePath string, docType string) (*ParseResult, error) {
	if r.primary != nil && r.primary.Supports(docType) {
		result, err := r.primary.Parse(ctx, filePath, docType)
		if err == nil {
			return result, nil
		}
		logger.Warn("primary parser failed, trying fallback",
			zap.String("doc_type", docType),
			zap.Error(err),
		)
	}

	if r.fallback != nil && r.fallback.Supports(docType) {
		result, err := r.fallback.Parse(ctx, filePath, docType)
		if err == nil {
			return result, nil
		}
		return nil, fmt.Errorf("all parsers failed for %s: fallback: %w", docType, err)
	}

	return nil, fmt.Errorf("no parser supports doc_type %q", docType)
}

// ParserUsed 返回实际使用的解析器名称 / Return which parser would be used
func (r *ParseRouter) ParserUsed(docType string) string {
	if r.primary != nil && r.primary.Supports(docType) {
		return "docling"
	}
	if r.fallback != nil && r.fallback.Supports(docType) {
		return "tika"
	}
	return "none"
}
