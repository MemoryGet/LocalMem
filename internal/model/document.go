package model

import "time"

// Document 文档知识库 / Document knowledge base entry
type Document struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	DocType     string         `json:"doc_type"`             // pdf / docx / pptx / xlsx / md / html / txt / png / jpg
	Scope       string         `json:"scope,omitempty"`
	ContextID   string         `json:"context_id,omitempty"` // FK → contexts.id
	FilePath    string         `json:"file_path,omitempty"`
	FileSize    int64          `json:"file_size"`
	ContentHash string         `json:"content_hash,omitempty"`
	// Status 文档处理状态（高级生命周期）/ Document processing status (high-level lifecycle)
	// Values: pending → parsing → chunking → embedding → ready | failed
	Status     string         `json:"status"`
	ChunkCount int            `json:"chunk_count"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	ErrorMsg   string         `json:"error_msg,omitempty"` // 失败原因 / Failure reason
	// Stage 当前处理阶段（可选细粒度）/ Current processing stage (optional fine-grained)
	// 当 Status 足以表达时，Stage 可为空。用于追踪子步骤。
	// When Status is sufficient, Stage may be empty. Used for sub-step tracking.
	Stage string `json:"stage,omitempty"`
	Parser      string         `json:"parser,omitempty"`     // 实际使用的解析器 / Parser used (docling/tika)
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
