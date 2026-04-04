package store

import (
	"context"
	"database/sql"
	"fmt"

	"iclude/internal/model"
)

// SQLiteTranscriptCursorStore transcript_cursors 表的 SQLite 实现 / SQLite implementation for transcript_cursors
type SQLiteTranscriptCursorStore struct {
	db *sql.DB
}

// NewSQLiteTranscriptCursorStore 创建 transcript cursor store / Create transcript cursor store
func NewSQLiteTranscriptCursorStore(db *sql.DB) *SQLiteTranscriptCursorStore {
	return &SQLiteTranscriptCursorStore{db: db}
}

// Get 获取游标 / Get cursor by session ID and source path
func (s *SQLiteTranscriptCursorStore) Get(ctx context.Context, sessionID, sourcePath string) (*model.TranscriptCursor, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT session_id, source_path, byte_offset, last_turn_id, last_read_at
		FROM transcript_cursors WHERE session_id = ? AND source_path = ?`,
		sessionID, sourcePath)

	c := &model.TranscriptCursor{}
	err := row.Scan(&c.SessionID, &c.SourcePath, &c.ByteOffset, &c.LastTurnID, &c.LastReadAt)
	if err == sql.ErrNoRows {
		return nil, model.ErrCursorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get transcript cursor: %w", err)
	}
	return c, nil
}

// Upsert 插入或更新游标 / Insert or update cursor
func (s *SQLiteTranscriptCursorStore) Upsert(ctx context.Context, c *model.TranscriptCursor) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO transcript_cursors (session_id, source_path, byte_offset, last_turn_id, last_read_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(session_id, source_path) DO UPDATE SET
			byte_offset = excluded.byte_offset,
			last_turn_id = excluded.last_turn_id,
			last_read_at = datetime('now')`,
		c.SessionID, c.SourcePath, c.ByteOffset, c.LastTurnID,
	)
	if err != nil {
		return fmt.Errorf("upsert transcript cursor: %w", err)
	}
	return nil
}

// ListBySession 列出会话的所有游标 / List all cursors for a session
func (s *SQLiteTranscriptCursorStore) ListBySession(ctx context.Context, sessionID string) ([]*model.TranscriptCursor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, source_path, byte_offset, last_turn_id, last_read_at
		FROM transcript_cursors WHERE session_id = ?
		ORDER BY last_read_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list transcript cursors by session: %w", err)
	}
	defer rows.Close()

	var result []*model.TranscriptCursor
	for rows.Next() {
		c := &model.TranscriptCursor{}
		if err := rows.Scan(&c.SessionID, &c.SourcePath, &c.ByteOffset, &c.LastTurnID, &c.LastReadAt); err != nil {
			return nil, fmt.Errorf("scan transcript cursor: %w", err)
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// DeleteBySession 删除会话的所有游标 / Delete all cursors for a session
func (s *SQLiteTranscriptCursorStore) DeleteBySession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM transcript_cursors WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("delete transcript cursors by session: %w", err)
	}
	return nil
}
