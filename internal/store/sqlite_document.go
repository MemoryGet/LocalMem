package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/model"

	"github.com/google/uuid"
)

// 编译期接口检查 / Compile-time interface compliance check
var _ DocumentStore = (*SQLiteDocumentStore)(nil)

// 文档表列名（16列）
const documentColumns = `id, name, doc_type, scope, context_id, file_path,
	file_size, content_hash, status, chunk_count, metadata,
	error_msg, stage, parser, created_at, updated_at`

// SQLiteDocumentStore 基于 SQLite 的文档存储 / SQLite-backed document store
type SQLiteDocumentStore struct {
	db *sql.DB
}

// NewSQLiteDocumentStore 创建文档存储实例 / Create a new SQLite document store
func NewSQLiteDocumentStore(db *sql.DB) *SQLiteDocumentStore {
	return &SQLiteDocumentStore{db: db}
}

// scanDocument 扫描一行到 Document 结构体
func scanDocument(scanner interface{ Scan(...any) error }) (*model.Document, error) {
	var doc model.Document
	var metadataRaw sql.NullString

	err := scanner.Scan(
		&doc.ID,
		&doc.Name,
		&doc.DocType,
		&doc.Scope,
		&doc.ContextID,
		&doc.FilePath,
		&doc.FileSize,
		&doc.ContentHash,
		&doc.Status,
		&doc.ChunkCount,
		&metadataRaw,
		&doc.ErrorMsg,
		&doc.Stage,
		&doc.Parser,
		&doc.CreatedAt,
		&doc.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if metadataRaw.Valid && metadataRaw.String != "" {
		if err := json.Unmarshal([]byte(metadataRaw.String), &doc.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal document metadata: %w", err)
		}
	}

	return &doc, nil
}

// Create 创建文档 / Create a new document record
func (s *SQLiteDocumentStore) Create(ctx context.Context, doc *model.Document) error {
	now := time.Now().UTC()
	doc.ID = uuid.New().String()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	if doc.Status == "" {
		doc.Status = "pending"
	}

	// 检查 content_hash 唯一性
	if doc.ContentHash != "" {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents WHERE content_hash = ?`, doc.ContentHash).Scan(&exists)
		if err == nil && exists > 0 {
			return fmt.Errorf("document with same content hash already exists: %w", model.ErrDuplicateDocument)
		}
	}

	metadataVal, err := marshalMetadata(doc.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := fmt.Sprintf(`INSERT INTO documents (%s) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, documentColumns)
	_, err = s.db.ExecContext(ctx, query,
		doc.ID, doc.Name, doc.DocType, doc.Scope, doc.ContextID, doc.FilePath,
		doc.FileSize, doc.ContentHash, doc.Status, doc.ChunkCount, metadataVal,
		doc.ErrorMsg, doc.Stage, doc.Parser,
		doc.CreatedAt, doc.UpdatedAt,
	)
	if err != nil {
		// 检测 content_hash 唯一约束冲突
		if strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "content_hash") {
			return fmt.Errorf("document with same content hash already exists: %w", model.ErrDuplicateDocument)
		}
		return fmt.Errorf("failed to insert document: %w", err)
	}
	return nil
}

// Get 获取文档 / Get a document by ID
func (s *SQLiteDocumentStore) Get(ctx context.Context, id string) (*model.Document, error) {
	query := fmt.Sprintf(`SELECT %s FROM documents WHERE id = ?`, documentColumns)
	row := s.db.QueryRowContext(ctx, query, id)

	doc, err := scanDocument(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("failed to get document: %w", err)
	}
	return doc, nil
}

// List 分页列出文档 / List documents with pagination and optional scope filter
func (s *SQLiteDocumentStore) List(ctx context.Context, scope string, offset, limit int) ([]*model.Document, error) {
	query := fmt.Sprintf(`SELECT %s FROM documents WHERE (scope = ? OR ? = '') ORDER BY created_at DESC LIMIT ? OFFSET ?`, documentColumns)
	rows, err := s.db.QueryContext(ctx, query, scope, scope, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list documents: %w", err)
	}
	defer rows.Close()

	var docs []*model.Document
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// Update 更新文档 / Update an existing document
func (s *SQLiteDocumentStore) Update(ctx context.Context, doc *model.Document) error {
	doc.UpdatedAt = time.Now().UTC()

	metadataVal, err := marshalMetadata(doc.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `UPDATE documents SET name = ?, doc_type = ?, scope = ?, context_id = ?, file_path = ?,
		file_size = ?, content_hash = ?, status = ?, chunk_count = ?, metadata = ?,
		error_msg = ?, stage = ?, parser = ?, updated_at = ?
		WHERE id = ?`
	_, err = s.db.ExecContext(ctx, query,
		doc.Name, doc.DocType, doc.Scope, doc.ContextID, doc.FilePath,
		doc.FileSize, doc.ContentHash, doc.Status, doc.ChunkCount, metadataVal,
		doc.ErrorMsg, doc.Stage, doc.Parser,
		doc.UpdatedAt, doc.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update document: %w", err)
	}
	return nil
}

// Delete 删除文档 / Delete a document by ID
func (s *SQLiteDocumentStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrDocumentNotFound
	}
	return nil
}

// GetByHash 通过内容哈希获取文档 / Get a document by content hash
func (s *SQLiteDocumentStore) GetByHash(ctx context.Context, contentHash string) (*model.Document, error) {
	query := fmt.Sprintf(`SELECT %s FROM documents WHERE content_hash = ?`, documentColumns)
	row := s.db.QueryRowContext(ctx, query, contentHash)

	doc, err := scanDocument(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("failed to get document by hash: %w", err)
	}
	return doc, nil
}

// ListByStatus 按状态列出文档 / List documents filtered by statuses
func (s *SQLiteDocumentStore) ListByStatus(ctx context.Context, statuses []string, limit int) ([]*model.Document, error) {
	if len(statuses) == 0 {
		return nil, nil
	}

	// 动态构造占位符
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses)+1)
	for i, status := range statuses {
		placeholders[i] = "?"
		args[i] = status
	}
	args[len(statuses)] = limit

	query := fmt.Sprintf(`SELECT %s FROM documents WHERE status IN (%s) ORDER BY created_at LIMIT ?`,
		documentColumns, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list documents by status: %w", err)
	}
	defer rows.Close()

	var docs []*model.Document
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// UpdateStatus 更新文档状态 / Update document status by ID
func (s *SQLiteDocumentStore) UpdateStatus(ctx context.Context, id string, status string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE documents SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	if err != nil {
		return fmt.Errorf("failed to update document status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrDocumentNotFound
	}
	return nil
}

// UpdateErrorMsg 更新文档错误信息 / Update document error message
func (s *SQLiteDocumentStore) UpdateErrorMsg(ctx context.Context, id string, msg string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE documents SET error_msg = ?, updated_at = ? WHERE id = ?`, msg, now, id)
	if err != nil {
		return fmt.Errorf("failed to update document error_msg: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrDocumentNotFound
	}
	return nil
}
