// sqlite_memory_lifecycle.go 记忆生命周期与搜索操作 / Memory lifecycle and search operations
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/pkg/sqlbuilder"

	"go.uber.org/zap"
)

// SoftDelete 软删除记忆 / Soft delete a memory
func (s *SQLiteMemoryStore) SoftDelete(ctx context.Context, id string) error {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin soft delete tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE memories SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return fmt.Errorf("failed to soft delete memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	// 在同一事务内清理 FTS5 索引 / Remove FTS5 index within the same transaction
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, id).Scan(&rowid); err == nil {
		if ftsErr := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); ftsErr != nil {
			logger.Warn("soft delete: FTS5 cleanup failed", zap.String("id", id), zap.Error(ftsErr))
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit soft delete tx: %w", err)
	}

	return nil
}

// SoftDeleteByDocumentID 软删除关联文档的所有记忆 / Soft delete all memories linked to a document
func (s *SQLiteMemoryStore) SoftDeleteByDocumentID(ctx context.Context, documentID string) (int, error) {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin soft delete by document tx: %w", err)
	}
	defer tx.Rollback()

	// 先收集需要清理 FTS5 的 rowid 列表
	rows, err := tx.QueryContext(ctx, `SELECT rowid FROM memories WHERE document_id = ? AND deleted_at IS NULL`, documentID)
	if err != nil {
		return 0, fmt.Errorf("failed to query document memories: %w", err)
	}
	var rowIDs []int64
	for rows.Next() {
		var rowid int64
		if scanErr := rows.Scan(&rowid); scanErr == nil {
			rowIDs = append(rowIDs, rowid)
		}
	}
	rows.Close()

	result, err := tx.ExecContext(ctx,
		`UPDATE memories SET deleted_at = ?, updated_at = ? WHERE document_id = ? AND deleted_at IS NULL`,
		now, now, documentID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to soft delete document memories: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to check rows affected: %w", err)
	}

	// 清理 FTS5 索引 / Remove FTS5 index entries
	for _, rowid := range rowIDs {
		if ftsErr := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); ftsErr != nil {
			logger.Warn("soft delete by document: FTS5 cleanup failed",
				zap.String("document_id", documentID),
				zap.Int64("rowid", rowid),
				zap.Error(ftsErr),
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit soft delete by document tx: %w", err)
	}

	return int(affected), nil
}

// Restore 恢复软删除的记忆 / Restore a soft-deleted memory
func (s *SQLiteMemoryStore) Restore(ctx context.Context, id string) error {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restore tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE memories SET deleted_at = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`, now, id)
	if err != nil {
		return fmt.Errorf("failed to restore memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	// 重建 FTS5 索引（SoftDelete 时已清除）/ Rebuild FTS5 index (cleared during SoftDelete)
	var mem model.Memory
	if err := tx.QueryRowContext(ctx, `SELECT id, content, COALESCE(abstract, ''), COALESCE(summary, '') FROM memories WHERE id = ?`, id).Scan(&mem.ID, &mem.Content, &mem.Abstract, &mem.Summary); err == nil {
		if syncErr := s.syncFTS5Tx(ctx, tx, &mem); syncErr != nil {
			logger.Warn("failed to rebuild FTS5 on restore", zap.String("id", id), zap.Error(syncErr))
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore tx: %w", err)
	}

	return nil
}

// CleanupExpired 软删除已过期记忆（单事务，消除 TOCTOU）/ Soft delete expired memories in a single transaction
func (s *SQLiteMemoryStore) CleanupExpired(ctx context.Context) (int, error) {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin cleanup expired tx: %w", err)
	}
	defer tx.Rollback()

	// 在事务内查出即将软删除的行的 rowid / Collect rowids within transaction
	expiredRows, err := tx.QueryContext(ctx,
		`SELECT rowid FROM memories WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`, now)
	if err != nil {
		return 0, fmt.Errorf("failed to query expired rowids: %w", err)
	}
	var rowids []int64
	for expiredRows.Next() {
		var rowid int64
		if err := expiredRows.Scan(&rowid); err != nil {
			expiredRows.Close()
			return 0, fmt.Errorf("failed to scan expired rowid: %w", err)
		}
		rowids = append(rowids, rowid)
	}
	expiredRows.Close()

	// 在事务内清理 FTS5 索引 / Clean FTS5 within transaction
	for _, rowid := range rowids {
		_ = s.deleteFTS5ByRowIDTx(ctx, tx, rowid)
	}

	// 在事务内执行软删除 / Soft delete within transaction
	result, err := tx.ExecContext(ctx,
		`UPDATE memories SET deleted_at = ?, updated_at = ?
		WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`,
		now, now, now)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired memories: %w", err)
	}
	expiredAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit cleanup expired tx: %w", err)
	}

	return int(expiredAffected), nil
}

// PurgeDeleted 硬删除已软删除超过指定时间的记忆 / Hard delete old soft-deleted memories
func (s *SQLiteMemoryStore) PurgeDeleted(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan)

	// 原子删除：SELECT + FTS5 + 关联表 + 主记录全在事务内，避免 TOCTOU / Atomic purge: SELECT + FTS5 + associations + main records in one tx, prevents TOCTOU
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	// 在事务内查询候选记录 / Query purge candidates inside the transaction
	rows, err := tx.QueryContext(ctx, `SELECT rowid, id FROM memories WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to query purge candidates: %w", err)
	}
	var rowids []int64
	var memoryIDs []string
	for rows.Next() {
		var rowid int64
		var id string
		if err := rows.Scan(&rowid, &id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("failed to scan purge candidate: %w", err)
		}
		rowids = append(rowids, rowid)
		memoryIDs = append(memoryIDs, id)
	}
	rows.Close()

	// 在事务内清理 FTS5 条目 / Clean FTS5 within transaction
	for _, rowid := range rowids {
		if ftsErr := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); ftsErr != nil {
			logger.Warn("purge: FTS5 cleanup failed", zap.Int64("rowid", rowid), zap.Error(ftsErr))
		}
	}

	if len(memoryIDs) > 0 {
		placeholders := strings.Repeat("?,", len(memoryIDs))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]interface{}, len(memoryIDs))
		for i, id := range memoryIDs {
			args[i] = id
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_tags WHERE memory_id IN (`+placeholders+`)`, args...); err != nil {
			return 0, fmt.Errorf("failed to delete memory_tags during purge: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_entities WHERE memory_id IN (`+placeholders+`)`, args...); err != nil {
			return 0, fmt.Errorf("failed to delete memory_entities during purge: %w", err)
		}
	}

	// 硬删除
	result, err := tx.ExecContext(ctx,
		`DELETE FROM memories WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to purge deleted memories: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit purge tx: %w", err)
	}

	return int(affected), nil
}

// ListExpired 列出已过期记忆 / List expired memories
func (s *SQLiteMemoryStore) ListExpired(ctx context.Context, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 100
	}

	now := time.Now().UTC()
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL
		ORDER BY expires_at ASC
		LIMIT ?`

	expiredRows, err := s.db.QueryContext(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list expired memories: %w", err)
	}
	defer expiredRows.Close()

	return s.scanMemories(expiredRows)
}

// ListWeak 列出弱记忆 / List weak memories below threshold
func (s *SQLiteMemoryStore) ListWeak(ctx context.Context, threshold float64, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE strength < ? AND deleted_at IS NULL
		ORDER BY strength ASC
		LIMIT ?`

	weakRows, err := s.db.QueryContext(ctx, query, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list weak memories: %w", err)
	}
	defer weakRows.Close()

	return s.scanMemories(weakRows)
}

// SearchText 全文检索 / Full-text search using FTS5
func (s *SQLiteMemoryStore) SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required: %w", model.ErrInvalidInput)
	}
	if limit <= 0 {
		limit = 10
	}

	// 查询预分词
	tokenizedQuery, err := s.tokenizer.Tokenize(ctx, query)
	if err != nil {
		tokenizedQuery = query // 分词失败回退原文
	}
	// FTS5 语法净化：包裹为短语查询防止操作符注入 / Sanitize for FTS5 syntax injection
	tokenizedQuery = sanitizeFTS5Query(tokenizedQuery)

	visCond, visArgs := visibilityCondition("m.", identity)
	w := s.bm25Weights
	// 用 CTE 消除 bm25() 重复计算 / Use CTE to avoid computing bm25() twice
	sqlQuery := `WITH ranked AS (
		SELECT ` + memoryColumnsAliased + `,
			bm25(memories_fts, ?, ?, ?) AS rank
		FROM memories m
		JOIN memories_fts f ON m.rowid = f.rowid
		WHERE memories_fts MATCH ? AND m.deleted_at IS NULL AND ` + visCond + `
	)
	SELECT * FROM ranked ORDER BY rank LIMIT ?`

	args := []interface{}{w[0], w[1], w[2], tokenizedQuery}
	args = append(args, visArgs...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %w", err)
	}
	defer rows.Close()

	queryWords := extractQueryWords(query)
	var results []*model.SearchResult
	for rows.Next() {
		mem, rank, err := s.scanMemoryWithRank(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}
		bm25Score := -rank
		coverageScore := wordCoverage(mem.Content+" "+mem.Abstract, queryWords)
		hybridScore := 0.7*bm25Score + 0.3*coverageScore*bm25Score
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  hybridScore,
			Source: "sqlite",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate search results: %w", err)
	}

	return results, nil
}

// SearchTextFiltered 带过滤条件的全文检索 / Full-text search with filters
func (s *SQLiteMemoryStore) SearchTextFiltered(ctx context.Context, query string, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required: %w", model.ErrInvalidInput)
	}
	if limit <= 0 {
		limit = 10
	}

	// 查询预分词
	tokenizedQuery, err := s.tokenizer.Tokenize(ctx, query)
	if err != nil {
		tokenizedQuery = query
	}
	// FTS5 语法净化 / Sanitize for FTS5 syntax injection
	tokenizedQuery = sanitizeFTS5Query(tokenizedQuery)

	// 使用 sqlbuilder 构建 WHERE 子句
	wb := sqlbuilder.NewWhere()
	wb.And("memories_fts MATCH ?", tokenizedQuery)
	wb.And("m.deleted_at IS NULL")

	if filters != nil {
		wb.AndIf(filters.Scope != "", "m.scope = ?", filters.Scope)
		wb.AndIf(filters.ContextID != "", "m.context_id = ?", filters.ContextID)
		wb.AndIf(filters.Kind != "", "m.kind = ?", filters.Kind)
		wb.AndIf(filters.SourceType != "", "m.source_type = ?", filters.SourceType)
		wb.AndIf(filters.HappenedAfter != nil, "m.happened_at >= ?", filters.HappenedAfter)
		wb.AndIf(filters.HappenedBefore != nil, "m.happened_at <= ?", filters.HappenedBefore)
		wb.AndIf(filters.MinStrength > 0, "m.strength >= ?", filters.MinStrength)
		if !filters.IncludeExpired {
			wb.And("(m.expires_at IS NULL OR m.expires_at > ?)", time.Now().UTC())
		}
		wb.AndIf(filters.RetentionTier != "", "m.retention_tier = ?", filters.RetentionTier)
		wb.AndIf(filters.MessageRole != "", "m.message_role = ?", filters.MessageRole)

		// 可见性过滤（TeamID/OwnerID 由 API 层注入）/ Visibility filtering (injected by API layer)
		if filters.TeamID != "" || filters.OwnerID != "" {
			identity := &model.Identity{TeamID: filters.TeamID, OwnerID: filters.OwnerID}
			visCond, visArgs := visibilityCondition("m.", identity)
			wb.And(visCond, visArgs...)
		}
	}

	whereClause, whereArgs := wb.Build()
	w := s.bm25Weights

	// 用 CTE 消除 bm25() 重复计算 / Use CTE to avoid computing bm25() twice
	sqlQuery := fmt.Sprintf(`WITH ranked AS (
		SELECT %s,
			bm25(memories_fts, ?, ?, ?) AS rank
		FROM memories m
		JOIN memories_fts f ON m.rowid = f.rowid
		WHERE %s
	)
	SELECT * FROM ranked ORDER BY rank LIMIT ?`, memoryColumnsAliased, whereClause)

	finalArgs := make([]interface{}, 0, len(whereArgs)+4)
	finalArgs = append(finalArgs, w[0], w[1], w[2])
	finalArgs = append(finalArgs, whereArgs...)
	finalArgs = append(finalArgs, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, finalArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories with filters: %w", err)
	}
	defer rows.Close()

	queryWords := extractQueryWords(query)
	var results []*model.SearchResult
	for rows.Next() {
		mem, rank, err := s.scanMemoryWithRank(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan filtered search result: %w", err)
		}
		bm25Score := -rank
		coverageScore := wordCoverage(mem.Content+" "+mem.Abstract, queryWords)
		hybridScore := 0.7*bm25Score + 0.3*coverageScore*bm25Score
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  hybridScore,
			Source: "sqlite",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate filtered search results: %w", err)
	}

	return results, nil
}
