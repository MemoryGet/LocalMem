package model

import "time"

// Document 文档知识库 / Document knowledge base entry
type Document struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	DocType     string         `json:"doc_type"` // text / markdown / pdf / html
	Scope       string         `json:"scope,omitempty"`
	ContextID   string         `json:"context_id,omitempty"` // FK → contexts.id
	FilePath    string         `json:"file_path,omitempty"`
	FileSize    int64          `json:"file_size"`
	ContentHash string         `json:"content_hash,omitempty"`
	Status      string         `json:"status"` // pending / processing / ready / failed
	ChunkCount  int            `json:"chunk_count"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
