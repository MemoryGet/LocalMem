// Package document provides document parsing and ingestion capabilities.
// 文档解析与摄取能力包。
package document

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

var doclingTypes = map[string]bool{
	"pdf": true, "docx": true, "pptx": true, "xlsx": true,
	"html": true, "md": true, "png": true, "jpg": true, "jpeg": true,
}

// DoclingParser Docling HTTP 解析器 / Docling HTTP parser via docling-serve
type DoclingParser struct {
	baseURL string
	client  *http.Client
}

// NewDoclingParser 创建 Docling 解析器 / Create Docling parser
func NewDoclingParser(baseURL string, timeout time.Duration) *DoclingParser {
	return &DoclingParser{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

// Supports 是否支持该文件类型 / Check if Docling supports this type
func (p *DoclingParser) Supports(docType string) bool {
	return doclingTypes[docType]
}

// Parse 调用 docling-serve 解析文件 / Call docling-serve to parse file
func (p *DoclingParser) Parse(ctx context.Context, filePath string, docType string) (*ParseResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, fmt.Errorf("failed to copy file: %w", err)
	}
	writer.Close()

	url := fmt.Sprintf("%s/v1alpha/convert/file", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docling request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("docling returned %d: %s", resp.StatusCode, string(body))
	}

	var doclingResp struct {
		Document struct {
			MdContent string `json:"md_content"`
		} `json:"document"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doclingResp); err != nil {
		return nil, fmt.Errorf("failed to decode docling response: %w", err)
	}

	logger.Debug("docling parse completed",
		zap.String("file", filepath.Base(filePath)),
		zap.Int("content_len", len(doclingResp.Document.MdContent)),
	)

	return &ParseResult{
		Content:    doclingResp.Document.MdContent,
		Format:     "markdown",
		Metadata:   doclingResp.Metadata,
		ParserName: "docling",
	}, nil
}

// Ping 健康检查 / Health check
func (p *DoclingParser) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docling health check returned %d", resp.StatusCode)
	}
	return nil
}
