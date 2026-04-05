package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"iclude/internal/model"
)

// SQLiteSessionStore sessions 表的 SQLite 实现 / SQLite implementation for sessions table
type SQLiteSessionStore struct {
	db *sql.DB
}

// NewSQLiteSessionStore 创建 session store / Create session store
func NewSQLiteSessionStore(db *sql.DB) *SQLiteSessionStore {
	return &SQLiteSessionStore{db: db}
}

// Create 创建会话 / Create a session
func (s *SQLiteSessionStore) Create(ctx context.Context, sess *model.Session) error {
	metaJSON, err := marshalMetadata(sess.Metadata)
	if err != nil {
		return fmt.Errorf("create session: marshal metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, context_id, user_id, tool_name, project_id, project_dir, profile, state, started_at, last_seen_at, finalized_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.ContextID, sess.UserID, sess.ToolName,
		sess.ProjectID, sess.ProjectDir, sess.Profile, sess.State,
		sess.StartedAt, sess.LastSeenAt, sess.FinalizedAt, metaJSON,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Get 获取会话 / Get session by ID
func (s *SQLiteSessionStore) Get(ctx context.Context, id string) (*model.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, context_id, user_id, tool_name, project_id, project_dir, profile, state, started_at, last_seen_at, finalized_at, metadata
		FROM sessions WHERE id = ?`, id)

	sess := &model.Session{}
	var finalizedAt sql.NullTime
	var metadata sql.NullString
	err := row.Scan(
		&sess.ID, &sess.ContextID, &sess.UserID, &sess.ToolName,
		&sess.ProjectID, &sess.ProjectDir, &sess.Profile, &sess.State,
		&sess.StartedAt, &sess.LastSeenAt, &finalizedAt, &metadata,
	)
	if err == sql.ErrNoRows {
		return nil, model.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if finalizedAt.Valid {
		sess.FinalizedAt = &finalizedAt.Time
	}
	if metadata.Valid && metadata.String != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(metadata.String), &m); err == nil {
			sess.Metadata = m
		}
	}
	return sess, nil
}

// UpdateState 更新会话状态 / Update session state
func (s *SQLiteSessionStore) UpdateState(ctx context.Context, id, state string) error {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET state = ?, last_seen_at = ?
		WHERE id = ?`, state, now, id)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrSessionNotFound
	}
	return nil
}

// Touch 更新最后活跃时间 / Update last seen timestamp
func (s *SQLiteSessionStore) Touch(ctx context.Context, id string, ts time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET last_seen_at = ? WHERE id = ?`, ts, id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrSessionNotFound
	}
	return nil
}

// ListPendingFinalize 列出待终结会话 / List sessions pending finalize
func (s *SQLiteSessionStore) ListPendingFinalize(ctx context.Context, olderThan time.Duration, limit int) ([]*model.Session, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	cutoff := time.Now().Add(-olderThan)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, context_id, user_id, tool_name, project_id, project_dir, profile, state, started_at, last_seen_at, finalized_at, metadata
		FROM sessions
		WHERE state IN (?, ?) AND last_seen_at < ?
		ORDER BY last_seen_at ASC
		LIMIT ?`,
		model.SessionStateActive, model.SessionStatePendingRepair, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending finalize: %w", err)
	}
	defer rows.Close()

	var result []*model.Session
	for rows.Next() {
		sess := &model.Session{}
		var finalizedAt sql.NullTime
		var metadata sql.NullString
		if err := rows.Scan(
			&sess.ID, &sess.ContextID, &sess.UserID, &sess.ToolName,
			&sess.ProjectID, &sess.ProjectDir, &sess.Profile, &sess.State,
			&sess.StartedAt, &sess.LastSeenAt, &finalizedAt, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan pending session: %w", err)
		}
		if finalizedAt.Valid {
			sess.FinalizedAt = &finalizedAt.Time
		}
		if metadata.Valid && metadata.String != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(metadata.String), &m); err == nil {
				sess.Metadata = m
			}
		}
		result = append(result, sess)
	}
	return result, rows.Err()
}

// UpdateMetadata 合并更新会话元数据（事务内 read-modify-write，防止 TOCTOU 竞争）
// Merge-update session metadata within a transaction to prevent TOCTOU races
func (s *SQLiteSessionStore) UpdateMetadata(ctx context.Context, id string, patch map[string]any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update metadata: begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. 事务内读现有 metadata / Read existing metadata within transaction
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT metadata FROM sessions WHERE id = ?`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return model.ErrSessionNotFound
		}
		return fmt.Errorf("update metadata: read: %w", err)
	}

	merged := make(map[string]any)
	if raw.Valid && raw.String != "" {
		_ = json.Unmarshal([]byte(raw.String), &merged)
	}

	// 2. 合并 patch / Merge patch
	for k, v := range patch {
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}

	// 3. 事务内写回 / Write back within transaction
	out, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("update metadata: marshal: %w", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET metadata = ?, last_seen_at = ? WHERE id = ?`, string(out), now, id)
	if err != nil {
		return fmt.Errorf("update metadata: write: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrSessionNotFound
	}

	return tx.Commit()
}
