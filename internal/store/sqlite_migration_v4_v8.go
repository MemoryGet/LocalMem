package store

import (
	"context"
	"database/sql"
	"fmt"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV4ToV5 记忆归纳审计字段
func migrateV4ToV5(db *sql.DB) error {
	logger.Info("executing migration V4→V5")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 新增 consolidated_into 列
	if _, err := tx.Exec(`ALTER TABLE memories ADD COLUMN consolidated_into TEXT DEFAULT ''`); err != nil {
		if !IsColumnExistsError(err) {
			return fmt.Errorf("failed to add consolidated_into column: %w", err)
		}
	}

	// 记录 schema_version
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version) VALUES (5)`); err != nil {
		return fmt.Errorf("failed to record schema version: %w", err)
	}

	logger.Info("migration V4→V5 completed successfully")
	return tx.Commit()
}

// migrateV5ToV6 身份与归属字段 / Identity & Ownership fields
func migrateV5ToV6(db *sql.DB) error {
	logger.Info("executing migration V5→V6")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 新增 owner_id 和 visibility 列 / Add owner_id and visibility columns
	alterColumns := []string{
		`ALTER TABLE memories ADD COLUMN owner_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN visibility TEXT DEFAULT 'private'`,
	}
	for _, stmt := range alterColumns {
		if _, err := tx.Exec(stmt); err != nil {
			if IsColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to alter table: %w", err)
		}
	}

	// 索引 / Indexes
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_owner_id ON memories(owner_id)`); err != nil {
		return fmt.Errorf("failed to create owner_id index: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_visibility ON memories(visibility)`); err != nil {
		return fmt.Errorf("failed to create visibility index: %w", err)
	}

	// 老数据迁移：visibility 设为 team，空 team_id 回填 default
	// Backfill old data: set visibility to team, set empty team_id to default
	if _, err := tx.Exec(`UPDATE memories SET visibility = 'team' WHERE owner_id = ''`); err != nil {
		return fmt.Errorf("failed to backfill visibility: %w", err)
	}
	if _, err := tx.Exec(`UPDATE memories SET team_id = 'default' WHERE team_id = ''`); err != nil {
		return fmt.Errorf("failed to backfill team_id: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_version (version) VALUES (6)`); err != nil {
		return fmt.Errorf("failed to update schema version: %w", err)
	}

	logger.Info("migration V5→V6 completed successfully")
	return tx.Commit()
}

// migrateV6ToV7 图谱关联表索引 / Graph and memory-entity association table indexes
func migrateV6ToV7(db *sql.DB) error {
	logger.Info("executing migration V6→V7")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin V6→V7: %w", err)
	}
	defer tx.Rollback()

	// 按表分组，仅当表存在时才创建对应索引 / Only create indexes when the table exists
	tableIndexes := []struct {
		table string
		stmts []string
	}{
		{
			table: "entity_relations",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_entity_relations_source ON entity_relations(source_id)`,
				`CREATE INDEX IF NOT EXISTS idx_entity_relations_target ON entity_relations(target_id)`,
			},
		},
		{
			table: "memory_entities",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_memory_entities_entity_id ON memory_entities(entity_id)`,
				`CREATE INDEX IF NOT EXISTS idx_memory_entities_memory_id ON memory_entities(memory_id)`,
			},
		},
	}

	for _, ti := range tableIndexes {
		var cnt int
		if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", ti.table).Scan(&cnt); err != nil {
			return fmt.Errorf("V6→V7 check table %s: %w", ti.table, err)
		}
		if cnt == 0 {
			logger.Info("migration V6→V7: skip indexes for missing table", zap.String("table", ti.table))
			continue
		}
		for _, stmt := range ti.stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("V6→V7 exec %q: %w", stmt, err)
			}
		}
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_version(version) VALUES(7)`); err != nil {
		return fmt.Errorf("V6→V7 schema_version: %w", err)
	}

	logger.Info("migration V6→V7 completed successfully")
	return tx.Commit()
}

// migrateV7ToV8 创建异步任务队列表 / Create async task queue table
func migrateV7ToV8(db *sql.DB) error {
	logger.Info("executing migration V7→V8")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin V7→V8: %w", err)
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS async_tasks (
			id           TEXT PRIMARY KEY,
			type         TEXT NOT NULL,
			payload      TEXT NOT NULL DEFAULT '{}',
			status       TEXT NOT NULL DEFAULT 'pending',
			retry_count  INTEGER NOT NULL DEFAULT 0,
			max_retries  INTEGER NOT NULL DEFAULT 3,
			error_msg    TEXT NOT NULL DEFAULT '',
			created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
			scheduled_at DATETIME,
			completed_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_async_tasks_status_created ON async_tasks(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_async_tasks_scheduled_at ON async_tasks(scheduled_at) WHERE scheduled_at IS NOT NULL`,
		`INSERT OR IGNORE INTO schema_version(version) VALUES(8)`,
	}

	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("V7→V8 exec %q: %w", stmt, err)
		}
	}

	logger.Info("migration V7→V8 completed successfully")
	return tx.Commit()
}
