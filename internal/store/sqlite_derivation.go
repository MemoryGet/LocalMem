// sqlite_derivation.go 记忆溯源关联操作 / Memory derivation junction table operations
package store

import (
	"context"
	"fmt"
	"time"
)

// AddDerivations 批量添加溯源关系 / Batch add derivation links (source → target)
func (s *SQLiteMemoryStore) AddDerivations(ctx context.Context, sourceIDs []string, targetID string) error {
	if len(sourceIDs) == 0 || targetID == "" {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin derivation tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO memory_derivations (source_id, target_id, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare derivation insert: %w", err)
	}
	defer stmt.Close()

	for _, srcID := range sourceIDs {
		if srcID == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, srcID, targetID, now); err != nil {
			return fmt.Errorf("add derivation %s → %s: %w", srcID, targetID, err)
		}
	}

	return tx.Commit()
}

// GetDerivedFrom 获取目标记忆的来源 ID 列表 / Get source IDs for a derived memory
func (s *SQLiteMemoryStore) GetDerivedFrom(ctx context.Context, targetID string) ([]string, error) {
	if targetID == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT source_id FROM memory_derivations WHERE target_id = ? ORDER BY created_at`, targetID)
	if err != nil {
		return nil, fmt.Errorf("query derived-from for %s: %w", targetID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan derived-from row: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetDerivedTo 获取由某记忆衍生出的目标 ID 列表 / Get target IDs derived from a source memory
func (s *SQLiteMemoryStore) GetDerivedTo(ctx context.Context, sourceID string) ([]string, error) {
	if sourceID == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT target_id FROM memory_derivations WHERE source_id = ? ORDER BY created_at`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("query derived-to for %s: %w", sourceID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan derived-to row: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
