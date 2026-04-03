// Package document provides document parsing and ingestion capabilities.
// 文档解析与摄取能力包。
package document

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// TikaParser Tika 解析器 / Apache Tika parser
type TikaParser struct {
	baseURL string
	client  *http.Client
}

// NewTikaParser 创建 Tika 解析器 / Create Tika parser
func NewTikaParser(baseURL string, timeout time.Duration) *TikaParser {
	return &TikaParser{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

// Supports Tika 支持几乎所有格式 / Tika supports nearly all formats
func (p *TikaParser) Supports(docType string) bool {
	return true
}

// Parse 调用 Tika 解析文件 / Call Tika to parse file
func (p *TikaParser) Parse(ctx context.Context, filePath string, docType string) (*ParseResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	url := fmt.Sprintf("%s/tika", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tika request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxDocParserResponseSize = 100 << 20 // 100 MB
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB for error messages
		return nil, fmt.Errorf("tika returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocParserResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read tika response: %w", err)
	}

	content := string(body)
	if len(content) == 0 {
		return nil, fmt.Errorf("tika returned empty content for %s", filepath.Base(filePath))
	}

	logger.Debug("tika parse completed",
		zap.String("file", filepath.Base(filePath)),
		zap.Int("content_len", len(content)),
	)

	return &ParseResult{
		Content:    content,
		Format:     "plaintext",
		ParserName: "tika",
	}, nil
}

// Ping 健康检查 / Health check
func (p *TikaParser) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/tika", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tika health check returned %d", resp.StatusCode)
	}
	return nil
}
