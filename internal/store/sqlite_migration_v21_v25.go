// sqlite_migration_v21_v25.go 数据库迁移 V21→V25 / Database migrations V21→V25
package store

import (
	"database/sql"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV21ToV22 重建 session 子表以添加 FK 约束 + entities 复合索引
// Recreate session child tables with FK constraints + add entities composite index
func migrateV21ToV22(db *sql.DB) error {
	logger.Info("executing migration V21→V22: FK constraints on session tables + entities composite index")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// --- 1. session_finalize_state: 重建带 FK 约束 / Recreate with FK constraint ---
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_finalize_state_new (
		session_id             TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		ingest_version         INTEGER NOT NULL DEFAULT 0,
		finalize_version       INTEGER NOT NULL DEFAULT 0,
		conversation_ingested  INTEGER NOT NULL DEFAULT 0,
		summary_memory_id      TEXT NOT NULL DEFAULT '',
		last_error             TEXT NOT NULL DEFAULT '',
		updated_at             DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_finalize_state_new
		SELECT session_id, ingest_version, finalize_version, conversation_ingested,
			summary_memory_id, last_error, updated_at
		FROM session_finalize_state`); err != nil {
		logger.Warn("V21→V22: copy session_finalize_state data failed (table may not exist)", zap.Error(err))
	}

	if _, err := tx.Exec(`DROP TABLE IF EXISTS session_finalize_state`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE session_finalize_state_new RENAME TO session_finalize_state`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_finalize_state_updated_at ON session_finalize_state(updated_at)`); err != nil {
		logger.Warn("V21→V22: session_finalize_state index failed (non-fatal)", zap.Error(err))
	}

	// --- 2. transcript_cursors: 重建带 FK 约束 / Recreate with FK constraint ---
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS transcript_cursors_new (
		session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		source_path   TEXT NOT NULL,
		byte_offset   INTEGER NOT NULL DEFAULT 0,
		last_turn_id  TEXT NOT NULL DEFAULT '',
		last_read_at  DATETIME NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (session_id, source_path)
	)`); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO transcript_cursors_new
		SELECT session_id, source_path, byte_offset, last_turn_id, last_read_at
		FROM transcript_cursors`); err != nil {
		logger.Warn("V21→V22: copy transcript_cursors data failed (table may not exist)", zap.Error(err))
	}

	if _, err := tx.Exec(`DROP TABLE IF EXISTS transcript_cursors`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE transcript_cursors_new RENAME TO transcript_cursors`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_transcript_cursors_last_read_at ON transcript_cursors(last_read_at)`); err != nil {
		logger.Warn("V21→V22: transcript_cursors index failed (non-fatal)", zap.Error(err))
	}

	// --- 3. entities 复合索引 / Entities composite index ---
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_entities_scope_type_updated ON entities(scope, entity_type, updated_at DESC)`); err != nil {
		logger.Warn("V21→V22: entities composite index failed (non-fatal)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (22, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V21→V22 completed: FK constraints on session tables + entities composite index")
	return nil
}

// migrateV22ToV23 memories 表新增 candidate_for 列 / Add candidate_for column to memories
func migrateV22ToV23(db *sql.DB) error {
	logger.Info("executing migration V22→V23: add candidate_for column to memories")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE memories ADD COLUMN candidate_for TEXT DEFAULT ''`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Info("V22→V23: candidate_for column already exists, skipping")
	}

	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_candidate_for ON memories(candidate_for) WHERE candidate_for != ''`); err != nil {
		logger.Warn("V22→V23: candidate_for index failed (non-fatal)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (23, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V22→V23 completed: candidate_for column added")
	return nil
}

// migrateV23ToV24 新增 scope_policies 表 / Add scope_policies table
func migrateV23ToV24(db *sql.DB) error {
	logger.Info("executing migration V23→V24: add scope_policies table")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS scope_policies (
		id               TEXT PRIMARY KEY,
		scope            TEXT NOT NULL UNIQUE,
		display_name     TEXT NOT NULL DEFAULT '',
		team_id          TEXT NOT NULL DEFAULT '',
		allowed_writers  TEXT NOT NULL DEFAULT '[]',
		created_by       TEXT NOT NULL DEFAULT '',
		created_at       DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at       DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (24, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V23→V24 completed: scope_policies table added")
	return nil
}

// migrateV24ToV25 补充缺失索引 / Add missing indexes for heartbeat queries
func migrateV24ToV25(db *sql.DB) error {
	logger.Info("executing migration V24→V25: add missing excerpt index")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// idx_memories_missing_excerpt: heartbeat ListMissingExcerpt 定期全表扫描优化
	// Optimizes heartbeat ListMissingExcerpt which otherwise does a full table scan
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_missing_excerpt ON memories(created_at DESC) WHERE (excerpt = '' OR excerpt IS NULL) AND deleted_at IS NULL`); err != nil {
		logger.Warn("V24→V25: missing excerpt index failed (non-fatal)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (25, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V24→V25 completed: missing excerpt index added")
	return nil
}
