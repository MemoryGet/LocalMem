package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"iclude/internal/model"
)

// SQLiteIdempotencyStore idempotency_keys 表的 SQLite 实现 / SQLite implementation for idempotency_keys
type SQLiteIdempotencyStore struct {
	db *sql.DB
}

// NewSQLiteIdempotencyStore 创建幂等键 store / Create idempotency store
func NewSQLiteIdempotencyStore(db *sql.DB) *SQLiteIdempotencyStore {
	return &SQLiteIdempotencyStore{db: db}
}

// Reserve 尝试预留幂等键，返回 true 表示首次预留成功 / Try to reserve key, returns true if first reservation
func (s *SQLiteIdempotencyStore) Reserve(ctx context.Context, scope, key, resourceType string) (bool, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO idempotency_keys (scope, idem_key, resource_type, created_at)
		VALUES (?, ?, ?, datetime('now'))`,
		scope, key, resourceType,
	)
	if err != nil {
		if IsUniqueConstraintError(err) {
			return false, nil // 已存在 / Already reserved
		}
		return false, fmt.Errorf("reserve idempotency key: %w", err)
	}
	return true, nil
}

// BindResource 绑定已预留的幂等键到具体资源 / Bind reserved key to resource ID
func (s *SQLiteIdempotencyStore) BindResource(ctx context.Context, scope, key, resourceID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE idempotency_keys SET resource_id = ? WHERE scope = ? AND idem_key = ?`,
		resourceID, scope, key,
	)
	if err != nil {
		return fmt.Errorf("bind idempotency resource: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrIdempotencyNotFound
	}
	return nil
}

// Get 获取幂等记录 / Get idempotency record
func (s *SQLiteIdempotencyStore) Get(ctx context.Context, scope, key string) (*model.IdempotencyRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT scope, idem_key, resource_type, resource_id, created_at
		FROM idempotency_keys WHERE scope = ? AND idem_key = ?`,
		scope, key)

	r := &model.IdempotencyRecord{}
	err := row.Scan(&r.Scope, &r.IdemKey, &r.ResourceType, &r.ResourceID, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, model.ErrIdempotencyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get idempotency record: %w", err)
	}
	return r, nil
}

// PurgeExpired 清理过期幂等键 / Purge expired idempotency keys
func (s *SQLiteIdempotencyStore) PurgeExpired(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := s.db.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge expired idempotency keys: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
