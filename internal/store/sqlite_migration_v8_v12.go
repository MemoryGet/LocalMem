package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV8ToV9 性能索引版本化 + 实体名索引 + 缺失列索引 + 复合索引 / Versioned performance indexes + entity name + missing column + composite indexes
func migrateV8ToV9(db *sql.DB) error {
	logger.Info("executing migration V8→V9")
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin V8→V9 tx: %w", err)
	}
	defer tx.Rollback()

	// memories 表索引（必须成功）/ Memory table indexes (must succeed)
	memIndexes := []string{
		// 原有性能索引（从 migrateAddPerformanceIndexes 迁入）
		`CREATE INDEX IF NOT EXISTS idx_memories_strength ON memories(strength) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at DESC) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_scope_kind ON memories(scope, kind) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_owner_team ON memories(owner_id, team_id) WHERE deleted_at IS NULL`,
		// 缺失列索引 / Missing column indexes
		`CREATE INDEX IF NOT EXISTS idx_memories_uri ON memories(uri) WHERE uri != '' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_document_id ON memories(document_id) WHERE document_id != '' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_parent_id ON memories(parent_id) WHERE parent_id != '' AND deleted_at IS NULL`,
		// 多租户复合索引（优化 visibilityCondition OR 路径）/ Multi-tenant composite index
		`CREATE INDEX IF NOT EXISTS idx_memories_team_vis_owner ON memories(team_id, visibility, owner_id) WHERE deleted_at IS NULL`,
	}
	for _, idx := range memIndexes {
		if _, err := tx.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("V8→V9 index creation failed: %w", err)
		}
	}

	// entities 表索引（表可能不存在，best-effort）/ Entity table index (table may not exist)
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_entities_lower_name ON entities(name COLLATE NOCASE)`); err != nil {
		logger.Warn("V8→V9: entities index skipped (table may not exist)", zap.Error(err))
	}

	// 写入版本号
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (9, datetime('now'))`); err != nil {
		return fmt.Errorf("V8→V9 version write failed: %w", err)
	}

	logger.Info("migration V8→V9 completed successfully")
	return tx.Commit()
}

// migrateV9ToV10 文档扩展字段（documents 表可能不存在）/ Document extension fields (table may not exist)
func migrateV9ToV10(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// documents 表可能不存在（旧库未启用文档功能）/ Table may not exist in older databases
	alterStmts := []string{
		`ALTER TABLE documents ADD COLUMN error_msg TEXT DEFAULT ''`,
		`ALTER TABLE documents ADD COLUMN stage TEXT DEFAULT ''`,
		`ALTER TABLE documents ADD COLUMN parser TEXT DEFAULT ''`,
	}
	for _, stmt := range alterStmts {
		if _, err := tx.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "no such table") || IsColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to execute %q: %w", stmt, err)
		}
	}

	// Add UNIQUE index on documents.content_hash (replaces non-unique index from V1) / 为 documents.content_hash 添加唯一索引
	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_documents_content_hash`); err != nil {
		// 索引可能不存在或文档表不存在，忽略错误 / Index may not exist or documents table may not exist, ignore error
		logger.Debug("V9→V10: drop non-unique index skipped", zap.Error(err))
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_content_hash_unique ON documents(content_hash) WHERE content_hash != ''`); err != nil {
		// documents 表可能不存在，忽略此错误 / Table may not exist in older databases, ignore
		logger.Debug("V9→V10: create unique index skipped (table may not exist)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (10, datetime('now'))`); err != nil {
		return fmt.Errorf("V9→V10 version write failed: %w", err)
	}

	logger.Info("migration V9→V10 completed: document extension fields + unique content_hash index")
	return tx.Commit()
}

// migrateV10ToV11 memory_tags 反向索引 + 文档索引 / memory_tags reverse index + document indexes
func migrateV10ToV11(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 按表分组，仅当表存在时才创建对应索引 / Only create indexes when the table exists
	tableIndexes := []struct {
		table string
		stmts []string
	}{
		{
			table: "memory_tags",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_memory_tags_tag_id ON memory_tags(tag_id)`,
			},
		},
		{
			table: "documents",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_documents_scope ON documents(scope) WHERE scope != ''`,
				`CREATE INDEX IF NOT EXISTS idx_documents_status_created ON documents(status, created_at)`,
			},
		},
	}

	for _, ti := range tableIndexes {
		var cnt int
		if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", ti.table).Scan(&cnt); err != nil {
			return fmt.Errorf("V10→V11 check table %s: %w", ti.table, err)
		}
		if cnt == 0 {
			logger.Info("migration V10→V11: skip indexes for missing table", zap.String("table", ti.table))
			continue
		}
		for _, stmt := range ti.stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("failed to execute %q: %w", stmt, err)
			}
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (11, datetime('now'))`); err != nil {
		return fmt.Errorf("V10→V11 schema_version: %w", err)
	}

	logger.Info("migration V10→V11 completed: memory_tags reverse index + document indexes")
	return tx.Commit()
}

// migrateV11ToV12 记忆演化层级 / Memory evolution layer (memory_class + derived_from)
func migrateV11ToV12(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 新增列（幂等）/ Add columns idempotently
	alterColumns := []string{
		`ALTER TABLE memories ADD COLUMN memory_class TEXT NOT NULL DEFAULT 'episodic'`,
		`ALTER TABLE memories ADD COLUMN derived_from TEXT`,
	}
	for _, stmt := range alterColumns {
		if _, err := tx.Exec(stmt); err != nil {
			if IsColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to alter table: %w", err)
		}
	}

	// 根据 kind 回填 memory_class / Backfill memory_class based on kind
	if _, err := tx.Exec(`UPDATE memories SET memory_class = 'procedural' WHERE kind = 'mental_model' AND memory_class = 'episodic'`); err != nil {
		return fmt.Errorf("failed to backfill procedural: %w", err)
	}
	if _, err := tx.Exec(`UPDATE memories SET memory_class = 'semantic' WHERE kind = 'consolidated' AND memory_class = 'episodic'`); err != nil {
		return fmt.Errorf("failed to backfill semantic: %w", err)
	}

	// 索引 / Index
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_memory_class ON memories(memory_class)`); err != nil {
		return fmt.Errorf("failed to create memory_class index: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (12, datetime('now'))`); err != nil {
		return fmt.Errorf("V11→V12 schema_version: %w", err)
	}

	logger.Info("migration V11→V12 completed: memory_class + derived_from columns")
	return tx.Commit()
}
