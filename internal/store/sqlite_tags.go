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
var _ TagStore = (*SQLiteTagStore)(nil)

// SQLiteTagStore 基于 SQLite 的标签存储 / SQLite-backed tag store
type SQLiteTagStore struct {
	db *sql.DB
}

// NewSQLiteTagStore 创建 SQLite 标签存储实例 / Create a new SQLite tag store
func NewSQLiteTagStore(db *sql.DB) *SQLiteTagStore {
	return &SQLiteTagStore{db: db}
}

// CreateTag 创建标签 / Create a new tag
func (s *SQLiteTagStore) CreateTag(ctx context.Context, tag *model.Tag) error {
	tag.ID = uuid.New().String()
	tag.CreatedAt = time.Now().UTC()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tags (id, name, scope, created_at) VALUES (?, ?, ?, ?)`,
		tag.ID, tag.Name, tag.Scope, tag.CreatedAt,
	)
	if err != nil {
		// 检查 UNIQUE(name, scope) 冲突
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return model.ErrConflict
		}
		return fmt.Errorf("failed to create tag: %w", err)
	}

	return nil
}

// GetTag 获取标签 / Get a tag by ID
func (s *SQLiteTagStore) GetTag(ctx context.Context, id string) (*model.Tag, error) {
	var tag model.Tag
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, scope, created_at FROM tags WHERE id = ?`, id,
	).Scan(&tag.ID, &tag.Name, &tag.Scope, &tag.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrTagNotFound
		}
		return nil, fmt.Errorf("failed to get tag: %w", err)
	}

	return &tag, nil
}

// ListTags 列出标签（可选 scope 过滤）/ List tags with optional scope filter
func (s *SQLiteTagStore) ListTags(ctx context.Context, scope string) ([]*model.Tag, error) {
	var query string
	var args []any
	if scope == "" {
		query = `SELECT id, name, scope, created_at FROM tags ORDER BY name`
	} else {
		query = `SELECT id, name, scope, created_at FROM tags WHERE scope = ? ORDER BY name`
		args = append(args, scope)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags: %w", err)
	}
	defer rows.Close()

	var tags []*model.Tag
	for rows.Next() {
		var tag model.Tag
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Scope, &tag.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan tag row: %w", err)
		}
		tags = append(tags, &tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate tag rows: %w", err)
	}

	return tags, nil
}

// DeleteTag 删除标签及其关联（原子操作）/ Delete a tag and its memory associations (atomic)
func (s *SQLiteTagStore) DeleteTag(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete tag tx: %w", err)
	}
	defer tx.Rollback()

	// 先删除关联表记录
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_tags WHERE tag_id = ?`, id); err != nil {
		return fmt.Errorf("failed to delete memory_tags for tag: %w", err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete tag: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrTagNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete tag tx: %w", err)
	}

	return nil
}

// TagMemory 给记忆打标签 / Associate a tag with a memory
func (s *SQLiteTagStore) TagMemory(ctx context.Context, memoryID, tagID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_tags (memory_id, tag_id, created_at) VALUES (?, ?, ?)`,
		memoryID, tagID, time.Now().UTC(),
	)
	if err != nil {
		// 重复关联视为冲突
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return model.ErrConflict
		}
		return fmt.Errorf("failed to tag memory: %w", err)
	}

	return nil
}

// UntagMemory 移除记忆标签 / Remove a tag from a memory
func (s *SQLiteTagStore) UntagMemory(ctx context.Context, memoryID, tagID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_tags WHERE memory_id = ? AND tag_id = ?`,
		memoryID, tagID,
	)
	if err != nil {
		return fmt.Errorf("failed to untag memory: %w", err)
	}

	return nil
}

// GetMemoryTags 获取记忆的所有标签 / Get all tags for a memory
func (s *SQLiteTagStore) GetMemoryTags(ctx context.Context, memoryID string) ([]*model.Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.name, t.scope, t.created_at
		FROM tags t JOIN memory_tags mt ON t.id = mt.tag_id
		WHERE mt.memory_id = ?
		ORDER BY t.name`,
		memoryID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory tags: %w", err)
	}
	defer rows.Close()

	var tags []*model.Tag
	for rows.Next() {
		var tag model.Tag
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Scope, &tag.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan memory tag row: %w", err)
		}
		tags = append(tags, &tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate memory tag rows: %w", err)
	}

	return tags, nil
}

// GetMemoriesByTag 获取标签下的所有记忆 / Get all memories with a specific tag
func (s *SQLiteTagStore) GetMemoriesByTag(ctx context.Context, tagID string, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `SELECT ` + memoryColumnsAliased + `
		FROM memories m JOIN memory_tags mt ON m.id = mt.memory_id
		WHERE mt.tag_id = ? AND m.deleted_at IS NULL
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, tagID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get memories by tag: %w", err)
	}
	defer rows.Close()

	var memories []*model.Memory
	for rows.Next() {
		mem, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		memories = append(memories, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate memory rows: %w", err)
	}

	return memories, nil
}

// GetTagNamesByMemoryIDs 批量获取多条记忆的标签名 / Batch get tag names for multiple memories
func (s *SQLiteTagStore) GetTagNamesByMemoryIDs(ctx context.Context, ids []string) (map[string][]string, error) {
	result := make(map[string][]string)
	if len(ids) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT mt.memory_id, t.name FROM memory_tags mt JOIN tags t ON mt.tag_id = t.id WHERE mt.memory_id IN (%s) ORDER BY t.name`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch get tag names: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var memID, tagName string
		if err := rows.Scan(&memID, &tagName); err != nil {
			return nil, fmt.Errorf("failed to scan tag name row: %w", err)
		}
		result[memID] = append(result[memID], tagName)
	}
	return result, rows.Err()
}

// scanMemoryRow 从结果集行扫描 Memory 对象（35 列），与 SQLiteMemoryStore.scanMemoryFromRows 相同
// Scan a Memory from a rows cursor (35 columns), mirrors SQLiteMemoryStore.scanMemoryFromRows
func scanMemoryRow(rows *sql.Rows) (*model.Memory, error) {
	var (
		mem              model.Memory
		metaStr          sql.NullString
		isLatestInt      int
		happenedAt       sql.NullTime
		deletedAt        sql.NullTime
		lastAccessedAt   sql.NullTime
		expiresAt        sql.NullTime
		strength         sql.NullFloat64
		decayRate        sql.NullFloat64
		reinforcedCount  sql.NullInt64
		chunkIndex       sql.NullInt64
		retentionTier    sql.NullString
		messageRole      sql.NullString
		turnNumber       sql.NullInt64
		contentHash      sql.NullString
		consolidatedInto sql.NullString
		ownerID          sql.NullString
		visibility       sql.NullString
	)

	err := rows.Scan(
		&mem.ID, &mem.Content, &metaStr, &mem.TeamID,
		&mem.EmbeddingID, &mem.ParentID, &isLatestInt, &mem.AccessCount,
		&mem.CreatedAt, &mem.UpdatedAt,
		&mem.URI, &mem.ContextID, &mem.Kind, &mem.SubKind, &mem.Scope, &mem.Abstract, &mem.Summary,
		&happenedAt, &mem.SourceType, &mem.SourceRef, &mem.DocumentID, &chunkIndex,
		&deletedAt, &strength, &decayRate, &lastAccessedAt, &reinforcedCount, &expiresAt,
		&retentionTier, &messageRole, &turnNumber, &contentHash, &consolidatedInto,
		&ownerID, &visibility,
	)
	if err != nil {
		return nil, err
	}

	mem.IsLatest = isLatestInt != 0
	if metaStr.Valid {
		if err := json.Unmarshal([]byte(metaStr.String), &mem.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	applyNullables(&mem, happenedAt, deletedAt, lastAccessedAt, expiresAt, strength, decayRate, reinforcedCount, chunkIndex)
	applyV3Nullables(&mem, retentionTier, messageRole, turnNumber)
	if contentHash.Valid {
		mem.ContentHash = contentHash.String
	}
	if consolidatedInto.Valid {
		mem.ConsolidatedInto = consolidatedInto.String
	}
	if ownerID.Valid {
		mem.OwnerID = ownerID.String
	}
	if visibility.Valid {
		mem.Visibility = visibility.String
	}

	return &mem, nil
}
