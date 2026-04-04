package store

import (
	"context"
	"database/sql"
	"fmt"

	"iclude/internal/model"
)

// SQLiteSessionFinalizeStore session_finalize_state 表的 SQLite 实现 / SQLite implementation for session_finalize_state
type SQLiteSessionFinalizeStore struct {
	db *sql.DB
}

// NewSQLiteSessionFinalizeStore 创建 session finalize store / Create session finalize store
func NewSQLiteSessionFinalizeStore(db *sql.DB) *SQLiteSessionFinalizeStore {
	return &SQLiteSessionFinalizeStore{db: db}
}

// Get 获取终态记录 / Get finalize state by session ID
func (s *SQLiteSessionFinalizeStore) Get(ctx context.Context, sessionID string) (*model.SessionFinalizeState, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT session_id, ingest_version, finalize_version, conversation_ingested, summary_memory_id, last_error, updated_at
		FROM session_finalize_state WHERE session_id = ?`, sessionID)

	st := &model.SessionFinalizeState{}
	var ingested int
	err := row.Scan(&st.SessionID, &st.IngestVersion, &st.FinalizeVersion, &ingested, &st.SummaryMemoryID, &st.LastError, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, model.ErrFinalizeStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get finalize state: %w", err)
	}
	st.ConversationIngested = ingested != 0
	return st, nil
}

// Upsert 插入或更新终态记录 / Insert or update finalize state
func (s *SQLiteSessionFinalizeStore) Upsert(ctx context.Context, st *model.SessionFinalizeState) error {
	ingested := 0
	if st.ConversationIngested {
		ingested = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_finalize_state (session_id, ingest_version, finalize_version, conversation_ingested, summary_memory_id, last_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(session_id) DO UPDATE SET
			ingest_version = excluded.ingest_version,
			finalize_version = excluded.finalize_version,
			conversation_ingested = excluded.conversation_ingested,
			summary_memory_id = excluded.summary_memory_id,
			last_error = excluded.last_error,
			updated_at = datetime('now')`,
		st.SessionID, st.IngestVersion, st.FinalizeVersion, ingested, st.SummaryMemoryID, st.LastError,
	)
	if err != nil {
		return fmt.Errorf("upsert finalize state: %w", err)
	}
	return nil
}

// MarkIngested 标记 conversation 已 ingest / Mark conversation as ingested
func (s *SQLiteSessionFinalizeStore) MarkIngested(ctx context.Context, sessionID string, version int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_finalize_state (session_id, ingest_version, conversation_ingested, updated_at)
		VALUES (?, ?, 1, datetime('now'))
		ON CONFLICT(session_id) DO UPDATE SET
			ingest_version = excluded.ingest_version,
			conversation_ingested = 1,
			updated_at = datetime('now')`,
		sessionID, version,
	)
	if err != nil {
		return fmt.Errorf("mark ingested: %w", err)
	}
	return nil
}

// MarkFinalized 标记已终结 / Mark session as finalized
func (s *SQLiteSessionFinalizeStore) MarkFinalized(ctx context.Context, sessionID string, version int, summaryMemoryID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_finalize_state (session_id, finalize_version, summary_memory_id, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(session_id) DO UPDATE SET
			finalize_version = excluded.finalize_version,
			summary_memory_id = excluded.summary_memory_id,
			updated_at = datetime('now')`,
		sessionID, version, summaryMemoryID,
	)
	if err != nil {
		return fmt.Errorf("mark finalized: %w", err)
	}
	return nil
}

// ListUnfinalized 列出未完成 finalize 的记录 / List unfinalized session states
func (s *SQLiteSessionFinalizeStore) ListUnfinalized(ctx context.Context, limit int) ([]*model.SessionFinalizeState, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, ingest_version, finalize_version, conversation_ingested, summary_memory_id, last_error, updated_at
		FROM session_finalize_state
		WHERE finalize_version = 0
		ORDER BY updated_at ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unfinalized: %w", err)
	}
	defer rows.Close()

	var result []*model.SessionFinalizeState
	for rows.Next() {
		st := &model.SessionFinalizeState{}
		var ingested int
		if err := rows.Scan(&st.SessionID, &st.IngestVersion, &st.FinalizeVersion, &ingested, &st.SummaryMemoryID, &st.LastError, &st.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan unfinalized: %w", err)
		}
		st.ConversationIngested = ingested != 0
		result = append(result, st)
	}
	return result, rows.Err()
}
