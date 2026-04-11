package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"iclude/internal/model"
)

var _ CandidateStore = (*SQLiteCandidateStore)(nil)

// SQLiteCandidateStore 候选实体 SQLite 存储 / SQLite candidate entity store
type SQLiteCandidateStore struct {
	db *sql.DB
}

// NewSQLiteCandidateStore 创建候选实体存储 / Create candidate store
func NewSQLiteCandidateStore(db *sql.DB) *SQLiteCandidateStore {
	return &SQLiteCandidateStore{db: db}
}

// UpsertCandidate 创建或更新候选 / Upsert candidate
func (s *SQLiteCandidateStore) UpsertCandidate(ctx context.Context, name, scope, memoryID string) error {
	now := time.Now().UTC()

	// 尝试更新 / Try update
	result, err := s.db.ExecContext(ctx, `
		UPDATE entity_candidates
		SET hit_count = hit_count + 1,
		    memory_ids = json_insert(memory_ids, '$[#]', ?),
		    updated_at = ?
		WHERE name = ? AND scope = ?`,
		memoryID, now, name, scope,
	)
	if err != nil {
		return fmt.Errorf("update candidate: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		return nil
	}

	// 不存在，创建 / Create new
	idsJSON, _ := json.Marshal([]string{memoryID})
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO entity_candidates (name, scope, first_seen, hit_count, memory_ids, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)`,
		name, scope, now, string(idsJSON), now, now,
	)
	if err != nil {
		return fmt.Errorf("insert candidate: %w", err)
	}
	return nil
}

// ListPromotable 列出可晋升候选 / List promotable candidates
func (s *SQLiteCandidateStore) ListPromotable(ctx context.Context, minHits int) ([]*model.EntityCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, scope, first_seen, hit_count, memory_ids, created_at, updated_at
		 FROM entity_candidates WHERE hit_count >= ? ORDER BY hit_count DESC`,
		minHits,
	)
	if err != nil {
		return nil, fmt.Errorf("list promotable: %w", err)
	}
	defer rows.Close()

	var candidates []*model.EntityCandidate
	for rows.Next() {
		var c model.EntityCandidate
		var idsJSON string
		if err := rows.Scan(&c.Name, &c.Scope, &c.FirstSeen, &c.HitCount, &idsJSON, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		if err := json.Unmarshal([]byte(idsJSON), &c.MemoryIDs); err != nil {
			c.MemoryIDs = nil
		}
		candidates = append(candidates, &c)
	}
	return candidates, rows.Err()
}

// DeleteCandidate 删除候选 / Delete candidate
func (s *SQLiteCandidateStore) DeleteCandidate(ctx context.Context, name, scope string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM entity_candidates WHERE name = ? AND scope = ?`,
		name, scope,
	)
	if err != nil {
		return fmt.Errorf("delete candidate: %w", err)
	}
	return nil
}
