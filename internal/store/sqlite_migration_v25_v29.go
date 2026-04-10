package store

import (
	"database/sql"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV25ToV26 实体关系生命周期字段 + 实体软删除 + source_ref 前缀索引
// Entity relation lifecycle fields + entity soft delete + source_ref prefix index
func migrateV25ToV26(db *sql.DB) error {
	logger.Info("executing migration V25→V26: entity lifecycle + source_ref index")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// entity_relations: 新增 mention_count / Add mention_count
	if _, err := tx.Exec(`ALTER TABLE entity_relations ADD COLUMN mention_count INTEGER DEFAULT 1`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: mention_count column already exists")
	}

	// entity_relations: 新增 last_seen_at / Add last_seen_at
	if _, err := tx.Exec(`ALTER TABLE entity_relations ADD COLUMN last_seen_at DATETIME`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: last_seen_at column already exists")
	}

	// entity_relations: 新增 updated_at / Add updated_at
	if _, err := tx.Exec(`ALTER TABLE entity_relations ADD COLUMN updated_at DATETIME`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: updated_at column already exists")
	}

	// entities: 新增 deleted_at / Add soft delete
	if _, err := tx.Exec(`ALTER TABLE entities ADD COLUMN deleted_at DATETIME DEFAULT NULL`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: deleted_at column already exists")
	}

	// 回填已有 entity_relations 的 last_seen_at 和 updated_at / Backfill existing rows
	if _, err := tx.Exec(`UPDATE entity_relations SET last_seen_at = created_at, updated_at = created_at WHERE last_seen_at IS NULL`); err != nil {
		logger.Warn("V25→V26: backfill last_seen_at failed (non-fatal)", zap.Error(err))
	}

	// 新增索引 / Add indexes
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_source_ref_prefix ON memories(source_ref)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_deleted_at ON entities(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_entity_relations_last_seen ON entity_relations(last_seen_at)`,
	}
	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			logger.Warn("V25→V26: index creation failed (non-fatal)", zap.Error(err), zap.String("sql", idx))
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (26, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V25→V26 completed: entity lifecycle fields + source_ref index")
	return nil
}
