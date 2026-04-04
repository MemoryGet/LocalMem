// sqlite_migration_v17_v21.go 数据库迁移 V17→V21 / Database migrations V17→V21
package store

import (
	"database/sql"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV17ToV18 新增 sessions 表 / Add sessions table
func migrateV17ToV18(db *sql.DB) error {
	logger.Info("executing migration V17→V18: sessions table")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id            TEXT PRIMARY KEY,
			context_id    TEXT NOT NULL DEFAULT '',
			user_id       TEXT NOT NULL DEFAULT '',
			tool_name     TEXT NOT NULL DEFAULT '',
			project_id    TEXT NOT NULL DEFAULT '',
			project_dir   TEXT NOT NULL DEFAULT '',
			profile       TEXT NOT NULL DEFAULT '',
			state         TEXT NOT NULL DEFAULT 'created',
			started_at    DATETIME NOT NULL,
			last_seen_at  DATETIME NOT NULL,
			finalized_at  DATETIME,
			metadata      TEXT
		)
	`); err != nil {
		return err
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_sessions_context_id ON sessions(context_id) WHERE context_id != ''`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_project_state_last_seen ON sessions(project_id, state, last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_tool_started_at ON sessions(tool_name, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_state_last_seen ON sessions(state, last_seen_at)`,
	}
	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			logger.Warn("V17→V18: index creation failed (non-fatal)", zap.String("sql", idx), zap.Error(err))
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (18, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V17→V18 completed: sessions table")
	return nil
}

// migrateV18ToV19 新增 session_finalize_state 表 / Add session finalize state table
func migrateV18ToV19(db *sql.DB) error {
	logger.Info("executing migration V18→V19: session_finalize_state table")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS session_finalize_state (
			session_id             TEXT PRIMARY KEY,
			ingest_version         INTEGER NOT NULL DEFAULT 0,
			finalize_version       INTEGER NOT NULL DEFAULT 0,
			conversation_ingested  INTEGER NOT NULL DEFAULT 0,
			summary_memory_id      TEXT NOT NULL DEFAULT '',
			last_error             TEXT NOT NULL DEFAULT '',
			updated_at             DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return err
	}

	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_finalize_state_updated_at ON session_finalize_state(updated_at)`); err != nil {
		logger.Warn("V18→V19: index creation failed (non-fatal)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (19, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V18→V19 completed: session_finalize_state table")
	return nil
}

// migrateV19ToV20 新增 transcript_cursors 表 / Add transcript cursors table
func migrateV19ToV20(db *sql.DB) error {
	logger.Info("executing migration V19→V20: transcript_cursors table")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS transcript_cursors (
			session_id    TEXT NOT NULL,
			source_path   TEXT NOT NULL,
			byte_offset   INTEGER NOT NULL DEFAULT 0,
			last_turn_id  TEXT NOT NULL DEFAULT '',
			last_read_at  DATETIME NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (session_id, source_path)
		)
	`); err != nil {
		return err
	}

	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_transcript_cursors_last_read_at ON transcript_cursors(last_read_at)`); err != nil {
		logger.Warn("V19→V20: index creation failed (non-fatal)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (20, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V19→V20 completed: transcript_cursors table")
	return nil
}

// migrateV20ToV21 新增 idempotency_keys 表 / Add idempotency keys table
func migrateV20ToV21(db *sql.DB) error {
	logger.Info("executing migration V20→V21: idempotency_keys table")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS idempotency_keys (
			scope         TEXT NOT NULL,
			idem_key      TEXT NOT NULL,
			resource_type TEXT NOT NULL DEFAULT '',
			resource_id   TEXT NOT NULL DEFAULT '',
			created_at    DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return err
	}

	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_idempotency_scope_key_unique ON idempotency_keys(scope, idem_key)`,
		`CREATE INDEX IF NOT EXISTS idx_idempotency_created_at ON idempotency_keys(created_at)`,
	}
	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			logger.Warn("V20→V21: index creation failed (non-fatal)", zap.String("sql", idx), zap.Error(err))
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (21, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V20→V21 completed: idempotency_keys table")
	return nil
}
