// Package document 文档处理 / Document processing and chunking
package document

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Processor 文档处理器 / Document processor for upload, chunk, and embed
type Processor struct {
	docStore store.DocumentStore
	memStore store.MemoryStore
	embedder store.Embedder // 可为 nil / may be nil
}

// NewProcessor 创建文档处理器 / Create document processor
func NewProcessor(docStore store.DocumentStore, memStore store.MemoryStore, embedder store.Embedder) *Processor {
	return &Processor{
		docStore: docStore,
		memStore: memStore,
		embedder: embedder,
	}
}

// Upload 上传文档 / Upload a document (create record, status=pending)
func (p *Processor) Upload(ctx context.Context, req *model.CreateDocumentRequest) (*model.Document, error) {
	if req.Name == "" || req.DocType == "" {
		return nil, fmt.Errorf("name and doc_type are required: %w", model.ErrInvalidInput)
	}

	doc := &model.Document{
		Name:      req.Name,
		DocType:   req.DocType,
		Scope:     req.Scope,
		ContextID: req.ContextID,
		FilePath:  req.FilePath,
		FileSize:  req.FileSize,
		Status:    "pending",
		Metadata:  req.Metadata,
	}

	if err := p.docStore.Create(ctx, doc); err != nil {
		return nil, fmt.Errorf("failed to create document: %w", err)
	}

	return doc, nil
}

// Process 处理文档 / Process a document: chunk content and create memories
func (p *Processor) Process(ctx context.Context, docID string, content string) error {
	doc, err := p.docStore.Get(ctx, docID)
	if err != nil {
		return fmt.Errorf("failed to get document: %w", err)
	}

	// 更新状态为 processing
	if err := p.docStore.UpdateStatus(ctx, docID, "processing"); err != nil {
		return fmt.Errorf("failed to update document status: %w", err)
	}

	// 计算内容哈希
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	doc.ContentHash = hash

	// 分块
	chunks := chunkText(content, 1000)
	doc.ChunkCount = len(chunks)

	// 为每个块创建 memory
	for i, chunk := range chunks {
		mem := &model.Memory{
			Content:    chunk,
			SourceType: "document",
			SourceRef:  doc.Name,
			DocumentID: doc.ID,
			ChunkIndex: i,
			Scope:      doc.Scope,
			Kind:       "note",
		}
		if doc.ContextID != "" {
			mem.ContextID = doc.ContextID
		}

		if err := p.memStore.Create(ctx, mem); err != nil {
			logger.Error("failed to create memory for document chunk",
				zap.String("document_id", docID),
				zap.Int("chunk_index", i),
				zap.Error(err),
			)
			// 继续处理其他块
			continue
		}
	}

	// 更新文档记录
	doc.Status = "ready"
	if err := p.docStore.Update(ctx, doc); err != nil {
		// 降级：文档状态更新失败但记忆已创建
		logger.Error("failed to update document after processing",
			zap.String("document_id", docID),
			zap.Error(err),
		)
		return nil
	}

	logger.Info("document processed successfully",
		zap.String("document_id", docID),
		zap.Int("chunk_count", len(chunks)),
	)

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

// DeleteDocument 删除文档 / Delete document
func (p *Processor) DeleteDocument(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return p.docStore.Delete(ctx, id)
}

// chunkText 简单文本分块 / Simple text chunking by paragraphs or fixed length
func chunkText(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = 1000
	}

	// 先按段落分割
	paragraphs := strings.Split(text, "\n\n")

	var chunks []string
	var current strings.Builder

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// 如果当前块加上新段落超过限制，先保存当前块
		if current.Len() > 0 && current.Len()+len(para)+2 > maxLen {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}

		// 如果单个段落本身超过限制，按固定长度分割
		if len(para) > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			for len(para) > maxLen {
				// 尝试在单词边界分割
				idx := strings.LastIndex(para[:maxLen], " ")
				if idx <= 0 {
					idx = maxLen
				}
				chunks = append(chunks, strings.TrimSpace(para[:idx]))
				para = strings.TrimSpace(para[idx:])
			}
			if para != "" {
				current.WriteString(para)
			}
			continue
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}

	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	// 确保至少返回一个块
	if len(chunks) == 0 && strings.TrimSpace(text) != "" {
		chunks = append(chunks, strings.TrimSpace(text))
	}

	return chunks
}
