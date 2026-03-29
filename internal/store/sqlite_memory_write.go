// sqlite_memory_write.go 记忆写入操作 / Memory write operations
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Create 创建记忆 / Create a new memory record
func (s *SQLiteMemoryStore) Create(ctx context.Context, mem *model.Memory) error {
	if mem.Content == "" {
		return fmt.Errorf("content is required: %w", model.ErrInvalidInput)
	}

	now := time.Now().UTC()
	mem.ID = uuid.New().String()
	mem.CreatedAt = now
	mem.UpdatedAt = now
	if !mem.IsLatest {
		mem.IsLatest = true
	}

	// 设置默认值
	if mem.Strength == 0 {
		mem.Strength = 1.0
	}
	if mem.DecayRate == 0 {
		mem.DecayRate = 0.01
	}
	if mem.Scope == "" {
		mem.Scope = "default"
	}
	if mem.LastAccessedAt == nil {
		mem.LastAccessedAt = &now
	}
	if mem.RetentionTier == "" {
		mem.RetentionTier = "standard"
	}
	if mem.Visibility == "" {
		mem.Visibility = model.VisibilityPrivate
	}

	metadataJSON, err := marshalMetadata(mem.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO memories (` + memoryColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = tx.ExecContext(ctx, query,
		mem.ID, mem.Content, metadataJSON, mem.TeamID,
		mem.EmbeddingID, mem.ParentID, boolToInt(mem.IsLatest), mem.AccessCount,
		mem.CreatedAt, mem.UpdatedAt,
		mem.URI, mem.ContextID, mem.Kind, mem.SubKind, mem.Scope, mem.Abstract, mem.Summary,
		timeToNull(mem.HappenedAt), mem.SourceType, mem.SourceRef, mem.DocumentID, mem.ChunkIndex,
		timeToNull(mem.DeletedAt), mem.Strength, mem.DecayRate, timeToNull(mem.LastAccessedAt),
		mem.ReinforcedCount, timeToNull(mem.ExpiresAt),
		mem.RetentionTier, mem.MessageRole, mem.TurnNumber, mem.ContentHash, mem.ConsolidatedInto,
		mem.OwnerID, mem.Visibility,
	)
	if err != nil {
		return fmt.Errorf("failed to insert memory: %w", err)
	}

	// 同步 FTS5（external content 模式）
	if err := s.syncFTS5Tx(ctx, tx, mem); err != nil {
		return fmt.Errorf("failed to sync FTS5 after insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create tx: %w", err)
	}

	return nil
}

// CreateBatch 批量创建记忆（单事务）/ Batch create memories in a single transaction
func (s *SQLiteMemoryStore) CreateBatch(ctx context.Context, memories []*model.Memory) error {
	if len(memories) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	insertQuery := `INSERT INTO memories (` + memoryColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	insertStmt, err := tx.PrepareContext(ctx, insertQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	defer insertStmt.Close()

	for _, mem := range memories {
		now := time.Now().UTC()
		mem.ID = uuid.New().String()
		mem.CreatedAt = now
		mem.UpdatedAt = now
		if !mem.IsLatest {
			mem.IsLatest = true
		}
		if mem.Strength == 0 {
			mem.Strength = 1.0
		}
		if mem.DecayRate == 0 {
			mem.DecayRate = 0.01
		}
		if mem.Scope == "" {
			mem.Scope = "default"
		}
		if mem.LastAccessedAt == nil {
			mem.LastAccessedAt = &now
		}
		if mem.RetentionTier == "" {
			mem.RetentionTier = "standard"
		}
		if mem.Visibility == "" {
			mem.Visibility = model.VisibilityPrivate
		}

		metadataJSON, err := marshalMetadata(mem.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}

		_, err = insertStmt.ExecContext(ctx,
			mem.ID, mem.Content, metadataJSON, mem.TeamID,
			mem.EmbeddingID, mem.ParentID, boolToInt(mem.IsLatest), mem.AccessCount,
			mem.CreatedAt, mem.UpdatedAt,
			mem.URI, mem.ContextID, mem.Kind, mem.SubKind, mem.Scope, mem.Abstract, mem.Summary,
			timeToNull(mem.HappenedAt), mem.SourceType, mem.SourceRef, mem.DocumentID, mem.ChunkIndex,
			timeToNull(mem.DeletedAt), mem.Strength, mem.DecayRate, timeToNull(mem.LastAccessedAt),
			mem.ReinforcedCount, timeToNull(mem.ExpiresAt),
			mem.RetentionTier, mem.MessageRole, mem.TurnNumber, mem.ContentHash, mem.ConsolidatedInto,
			mem.OwnerID, mem.Visibility,
		)
		if err != nil {
			return fmt.Errorf("failed to insert memory %s: %w", mem.ID, err)
		}
	}

	// FTS5 同步在事务内，保证原子性 / Sync FTS5 inside transaction for atomicity
	for _, mem := range memories {
		if err := s.syncFTS5Tx(ctx, tx, mem); err != nil {
			logger.Warn("CreateBatch: FTS5 sync failed",
				zap.String("id", mem.ID),
				zap.Error(err),
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit batch insert: %w", err)
	}

	return nil
}

// Update 更新记忆 / Update an existing memory
func (s *SQLiteMemoryStore) Update(ctx context.Context, mem *model.Memory) error {
	if mem.ID == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	metadataJSON, err := marshalMetadata(mem.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	mem.UpdatedAt = time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 先读取旧行的 rowid 用于 FTS5 删除
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, mem.ID).Scan(&rowid); err != nil {
		if err == sql.ErrNoRows {
			return model.ErrMemoryNotFound
		}
		return fmt.Errorf("failed to get rowid: %w", err)
	}

	// 删除旧 FTS5 行
	if err := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); err != nil {
		return fmt.Errorf("failed to delete old FTS5 entry: %w", err)
	}

	query := `UPDATE memories SET content = ?, metadata = ?, team_id = ?, embedding_id = ?, parent_id = ?,
		is_latest = ?, updated_at = ?,
		uri = ?, context_id = ?, kind = ?, sub_kind = ?, scope = ?, abstract = ?, summary = ?,
		happened_at = ?, source_type = ?, source_ref = ?, document_id = ?, chunk_index = ?,
		strength = ?, decay_rate = ?, last_accessed_at = ?, reinforced_count = ?, expires_at = ?,
		retention_tier = ?, message_role = ?, turn_number = ?, owner_id = ?, visibility = ?
		WHERE id = ?`

	result, err := tx.ExecContext(ctx, query,
		mem.Content, metadataJSON, mem.TeamID, mem.EmbeddingID, mem.ParentID,
		boolToInt(mem.IsLatest), mem.UpdatedAt,
		mem.URI, mem.ContextID, mem.Kind, mem.SubKind, mem.Scope, mem.Abstract, mem.Summary,
		timeToNull(mem.HappenedAt), mem.SourceType, mem.SourceRef, mem.DocumentID, mem.ChunkIndex,
		mem.Strength, mem.DecayRate, timeToNull(mem.LastAccessedAt), mem.ReinforcedCount, timeToNull(mem.ExpiresAt),
		mem.RetentionTier, mem.MessageRole, mem.TurnNumber, mem.OwnerID, mem.Visibility,
		mem.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	// 插入新 FTS5 行
	if err := s.syncFTS5Tx(ctx, tx, mem); err != nil {
		return fmt.Errorf("failed to sync FTS5 after update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update tx: %w", err)
	}

	return nil
}

// Delete 删除记忆（硬删除）/ Delete a memory by ID (hard delete)
func (s *SQLiteMemoryStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete tx: %w", err)
	}
	defer tx.Rollback()

	// 获取 rowid 用于 FTS5 清理
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, id).Scan(&rowid); err != nil {
		if err == sql.ErrNoRows {
			return model.ErrMemoryNotFound
		}
		return fmt.Errorf("failed to get rowid for delete: %w", err)
	}

	// 在同一事务内删除 FTS5 条目
	if err := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); err != nil {
		return fmt.Errorf("failed to delete FTS5 entry: %w", err)
	}

	// 删除主表记录
	result, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete tx: %w", err)
	}

	return nil
}

// Reinforce 强化记忆 / Reinforce a memory (increase strength)
func (s *SQLiteMemoryStore) Reinforce(ctx context.Context, id string) error {
	now := time.Now().UTC()
	// strength += 0.1 * (1 - strength)
	result, err := s.db.ExecContext(ctx,
		`UPDATE memories SET strength = strength + 0.1 * (1.0 - strength),
		reinforced_count = reinforced_count + 1,
		last_accessed_at = ?,
		updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return fmt.Errorf("failed to reinforce memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	return nil
}

// IncrementAccessCount 递增访问计数 / Increment access count by delta
func (s *SQLiteMemoryStore) IncrementAccessCount(ctx context.Context, id string, delta int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE memories SET access_count = access_count + ? WHERE id = ? AND deleted_at IS NULL`,
		delta, id)
	if err != nil {
		return fmt.Errorf("failed to increment access count: %w", err)
	}
	return nil
}
