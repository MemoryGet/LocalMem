// sqlite_migration_v16_v20.go 数据库迁移 V16→V20 / Database migrations V16→V20
package store

import (
	"database/sql"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV16ToV17 添加 source_ref + consolidated_into 索引 / Add indexes for B6/B7 query paths
func migrateV16ToV17(db *sql.DB) error {
	logger.Info("executing migration V16→V17: source_ref + consolidated_into indexes")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_source_ref ON memories(source_ref) WHERE source_ref != '' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_consolidated_into ON memories(consolidated_into) WHERE consolidated_into != '' AND deleted_at IS NULL`,
	}

	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			logger.Warn("V16→V17: index creation failed (non-fatal)", zap.String("sql", idx), zap.Error(err))
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (17, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V16→V17 completed: source_ref + consolidated_into indexes")
	return nil
}
