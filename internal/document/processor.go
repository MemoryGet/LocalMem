// Package document 文档处理 / Document processing and chunking
package document

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Processor 文档处理器 / Document processor for upload, parse, chunk, and ingest
type Processor struct {
	docStore    store.DocumentStore
	memManager  *memory.Manager
	embedder    store.Embedder
	fileStore   FileStore
	parseRouter *ParseRouter
	chunker     Chunker
	sem         chan struct{}
	cfg         ProcessorConfig
}

// ProcessorConfig 处理器配置 / Processor configuration
type ProcessorConfig struct {
	ProcessTimeout    time.Duration
	CleanupAfterParse bool
	KeepImages        bool
	ChunkingOpts      ChunkOptions
}

// NewProcessor 创建文档处理器 / Create document processor
func NewProcessor(
	docStore store.DocumentStore,
	memManager *memory.Manager,
	embedder store.Embedder,
	fileStore FileStore,
	parseRouter *ParseRouter,
	chunker Chunker,
	opts ...ProcessorOption,
) *Processor {
	p := &Processor{
		docStore:    docStore,
		memManager:  memManager,
		embedder:    embedder,
		fileStore:   fileStore,
		parseRouter: parseRouter,
		chunker:     chunker,
		sem:         make(chan struct{}, 3),
		cfg: ProcessorConfig{
			ProcessTimeout:    10 * time.Minute,
			CleanupAfterParse: true,
			KeepImages:        true,
			ChunkingOpts: ChunkOptions{
				MaxTokens:       512,
				OverlapTokens:   50,
				ContextPrefix:   true,
				KeepTableIntact: true,
				KeepCodeIntact:  true,
			},
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessorOption 处理器选项 / Processor functional option
type ProcessorOption func(*Processor)

// WithMaxConcurrent 设置最大并发数 / Set max concurrent processing
func WithMaxConcurrent(n int) ProcessorOption {
	return func(p *Processor) {
		if n > 0 {
			p.sem = make(chan struct{}, n)
		}
	}
}

// WithProcessorConfig 设置处理器配置 / Set processor config
func WithProcessorConfig(cfg ProcessorConfig) ProcessorOption {
	return func(p *Processor) {
		p.cfg = cfg
	}
}

// Upload 上传文档（创建记录）/ Upload document — create record with status=pending
func (p *Processor) Upload(ctx context.Context, name, docType, scope, contextID string, fileSize int64, metadata map[string]any) (*model.Document, error) {
	if name == "" || docType == "" {
		return nil, fmt.Errorf("name and doc_type are required: %w", model.ErrInvalidInput)
	}

	doc := &model.Document{
		Name:      name,
		DocType:   docType,
		Scope:     scope,
		ContextID: contextID,
		FileSize:  fileSize,
		Status:    "pending",
		Metadata:  metadata,
	}

	if err := p.docStore.Create(ctx, doc); err != nil {
		return nil, fmt.Errorf("failed to create document: %w", err)
	}

	return doc, nil
}

// ProcessAsync 异步处理文档 / Asynchronously process a document
func (p *Processor) ProcessAsync(docID string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("document processing panicked",
					zap.String("document_id", docID),
					zap.Any("panic", r),
				)
				ctx := context.Background()
				_ = p.docStore.UpdateStatus(ctx, docID, "failed")
				_ = p.docStore.UpdateErrorMsg(ctx, docID, "internal processing error")
			}
		}()

		p.sem <- struct{}{}
		defer func() { <-p.sem }()

		ctx, cancel := context.WithTimeout(context.Background(), p.cfg.ProcessTimeout)
		defer cancel()

		if err := p.processDocument(ctx, docID); err != nil {
			logger.Error("document processing failed",
				zap.String("document_id", docID),
				zap.Error(err),
			)
			_ = p.docStore.UpdateStatus(ctx, docID, "failed")
			_ = p.docStore.UpdateErrorMsg(ctx, docID, "document processing failed")
		}
	}()
}

// processDocument 执行文档处理全流程 / Execute full document processing pipeline
func (p *Processor) processDocument(ctx context.Context, docID string) error {
	doc, err := p.docStore.Get(ctx, docID)
	if err != nil {
		return fmt.Errorf("failed to get document: %w", err)
	}

	// Stage 1: 解析
	_ = p.docStore.UpdateStatus(ctx, docID, "parsing")
	doc.Stage = "parsing"

	if p.parseRouter == nil || doc.FilePath == "" {
		return fmt.Errorf("parse router or file path not available: %w", model.ErrParseFailure)
	}

	result, err := p.parseRouter.Parse(ctx, doc.FilePath, doc.DocType)
	if err != nil {
		return fmt.Errorf("parse failed: %w", err)
	}

	doc.Parser = result.ParserName

	// 计算内容哈希
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(result.Content)))
	doc.ContentHash = hash

	// Stage 2: 分块
	_ = p.docStore.UpdateStatus(ctx, docID, "chunking")
	doc.Stage = "chunking"

	var chunks []Chunk
	if p.chunker != nil {
		opts := p.cfg.ChunkingOpts
		opts.DocName = doc.Name
		chunks = p.chunker.Chunk(result.Content, opts)
	}
	doc.ChunkCount = len(chunks)

	// Stage 3: 入库
	_ = p.docStore.UpdateStatus(ctx, docID, "embedding")
	doc.Stage = "embedding"

	var failedChunks []int
	for _, chunk := range chunks {
		req := &model.CreateMemoryRequest{
			Content:    chunk.Content,
			SourceType: "document",
			SourceRef:  doc.Name,
			DocumentID: doc.ID,
			ChunkIndex: chunk.Index,
			Scope:      doc.Scope,
			Kind:       "note",
			Summary:    chunk.Heading,
			Metadata:   map[string]any{"chunk_type": chunk.ChunkType},
		}
		if chunk.PageStart > 0 {
			req.Metadata["page_start"] = chunk.PageStart
		}
		if doc.ContextID != "" {
			req.ContextID = doc.ContextID
		}

		if _, err := p.memManager.Create(ctx, req); err != nil {
			logger.Error("failed to create memory for chunk",
				zap.String("document_id", docID),
				zap.Int("chunk_index", chunk.Index),
				zap.Error(err),
			)
			failedChunks = append(failedChunks, chunk.Index)
			continue
		}
	}

	// 清理源文件
	if p.fileStore != nil && p.cfg.CleanupAfterParse {
		isImage := isImageType(doc.DocType)
		if !isImage || !p.cfg.KeepImages {
			dir := filepath.Dir(doc.FilePath)
			if err := p.fileStore.Delete(ctx, dir); err != nil {
				logger.Warn("failed to cleanup source file", zap.Error(err))
			}
		}
	}

	// 更新文档状态
	doc.Status = "ready"
	doc.Stage = ""
	if len(failedChunks) > 0 {
		doc.ErrorMsg = fmt.Sprintf("failed chunks: %v", failedChunks)
	}
	if err := p.docStore.Update(ctx, doc); err != nil {
		logger.Error("failed to update document after processing", zap.Error(err))
		return nil
	}

	logger.Info("document processed successfully",
		zap.String("document_id", docID),
		zap.String("parser", doc.Parser),
		zap.Int("chunk_count", doc.ChunkCount),
	)
	return nil
}

// Process 手动处理（兼容现有 /reprocess 端点）/ Manual process with raw content (backward compatible)
func (p *Processor) Process(ctx context.Context, docID string, content string) error {
	doc, err := p.docStore.Get(ctx, docID)
	if err != nil {
		return fmt.Errorf("failed to get document: %w", err)
	}

	_ = p.docStore.UpdateStatus(ctx, docID, "processing")

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	doc.ContentHash = hash

	chunker := p.chunker
	if chunker == nil {
		chunker = NewTextChunker()
	}
	opts := p.cfg.ChunkingOpts
	opts.DocName = doc.Name
	chunks := chunker.Chunk(content, opts)
	doc.ChunkCount = len(chunks)
	doc.Parser = "manual"

	for _, chunk := range chunks {
		req := &model.CreateMemoryRequest{
			Content:    chunk.Content,
			SourceType: "document",
			SourceRef:  doc.Name,
			DocumentID: doc.ID,
			ChunkIndex: chunk.Index,
			Scope:      doc.Scope,
			Kind:       "note",
		}
		if doc.ContextID != "" {
			req.ContextID = doc.ContextID
		}

		if _, err := p.memManager.Create(ctx, req); err != nil {
			logger.Error("failed to create memory for document chunk",
				zap.String("document_id", docID),
				zap.Int("chunk_index", chunk.Index),
				zap.Error(err),
			)
			continue
		}
	}

	doc.Status = "ready"
	if err := p.docStore.Update(ctx, doc); err != nil {
		logger.Error("failed to update document after processing", zap.Error(err))
		return nil
	}

	return nil
}

// GetDocument 获取文档 / Get document by ID
func (p *Processor) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return p.docStore.Get(ctx, id)
}

// ListDocuments 列出文档 / List documents
func (p *Processor) ListDocuments(ctx context.Context, scope string, offset, limit int) ([]*model.Document, error) {
	if limit <= 0 {
		limit = 20
	}
	return p.docStore.List(ctx, scope, offset, limit)
}

// DeleteDocument 删除文档及关联资源 / Delete document with associated resources
func (p *Processor) DeleteDocument(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	doc, err := p.docStore.Get(ctx, id)
	if err != nil {
		return err
	}

	// 软删除关联的分块记忆 / Soft delete associated chunk memories
	if p.memManager != nil {
		if n, err := p.memManager.DeleteChunksByDocumentID(ctx, id); err != nil {
			logger.Warn("failed to delete chunk memories for document",
				zap.String("document_id", id),
				zap.Error(err),
			)
		} else {
			logger.Info("deleted chunk memories for document",
				zap.String("document_id", id),
				zap.Int("count", n),
			)
		}
	}

	// 清理源文件
	if p.fileStore != nil && doc.FilePath != "" {
		dir := filepath.Dir(doc.FilePath)
		_ = p.fileStore.Delete(ctx, dir)
	}

	return p.docStore.Delete(ctx, id)
}

// GetDocumentByHash 通过哈希和 scope 查找文档 / Find document by content hash + scope
func (p *Processor) GetDocumentByHash(ctx context.Context, hash, scope string) (*model.Document, error) {
	doc, err := p.docStore.GetByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	if doc.Scope != scope {
		return nil, model.ErrDocumentNotFound
	}
	return doc, nil
}

// UpdateDocFilePath 更新文档文件路径和哈希 / Update document file path and content hash
func (p *Processor) UpdateDocFilePath(ctx context.Context, doc *model.Document) {
	_ = p.docStore.Update(ctx, doc)
}

// isImageType 判断是否为图片类型 / Check if doc type is an image
func isImageType(docType string) bool {
	switch strings.ToLower(docType) {
	case "png", "jpg", "jpeg", "gif", "bmp", "webp", "tiff":
		return true
	}
	return false
}
