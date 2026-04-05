package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"iclude/internal/model"

	"github.com/google/uuid"
)

// SQLiteScopePolicyStore scope_policies 表的 SQLite 实现 / SQLite impl for scope_policies
type SQLiteScopePolicyStore struct {
	db *sql.DB
}

// NewSQLiteScopePolicyStore 创建 scope policy store / Create scope policy store
func NewSQLiteScopePolicyStore(db *sql.DB) *SQLiteScopePolicyStore {
	return &SQLiteScopePolicyStore{db: db}
}

// Create 创建策略 / Create a scope policy
func (s *SQLiteScopePolicyStore) Create(ctx context.Context, p *model.ScopePolicy) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	writersJSON, err := json.Marshal(p.AllowedWriters)
	if err != nil {
		return fmt.Errorf("marshal allowed_writers: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scope_policies (id, scope, display_name, team_id, allowed_writers, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Scope, p.DisplayName, p.TeamID, string(writersJSON), p.CreatedBy, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create scope policy: %w", err)
	}
	return nil
}

// GetByScope 按 scope 获取策略 / Get policy by scope
func (s *SQLiteScopePolicyStore) GetByScope(ctx context.Context, scope string) (*model.ScopePolicy, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, scope, display_name, team_id, allowed_writers, created_by, created_at, updated_at
		FROM scope_policies WHERE scope = ?`, scope)

	p := &model.ScopePolicy{}
	var writersJSON string
	err := row.Scan(&p.ID, &p.Scope, &p.DisplayName, &p.TeamID, &writersJSON, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, model.ErrScopePolicyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get scope policy: %w", err)
	}
	if writersJSON != "" {
		_ = json.Unmarshal([]byte(writersJSON), &p.AllowedWriters)
	}
	return p, nil
}

// List 列出团队的所有策略 / List all policies for a team
func (s *SQLiteScopePolicyStore) List(ctx context.Context, teamID string) ([]*model.ScopePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scope, display_name, team_id, allowed_writers, created_by, created_at, updated_at
		FROM scope_policies WHERE team_id = ? ORDER BY scope`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list scope policies: %w", err)
	}
	defer rows.Close()

	var result []*model.ScopePolicy
	for rows.Next() {
		p := &model.ScopePolicy{}
		var writersJSON string
		if err := rows.Scan(&p.ID, &p.Scope, &p.DisplayName, &p.TeamID, &writersJSON, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan scope policy: %w", err)
		}
		if writersJSON != "" {
			_ = json.Unmarshal([]byte(writersJSON), &p.AllowedWriters)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// Update 更新策略 / Update a scope policy
func (s *SQLiteScopePolicyStore) Update(ctx context.Context, p *model.ScopePolicy) error {
	p.UpdatedAt = time.Now()
	writersJSON, err := json.Marshal(p.AllowedWriters)
	if err != nil {
		return fmt.Errorf("marshal allowed_writers: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE scope_policies SET display_name = ?, allowed_writers = ?, updated_at = ?
		WHERE scope = ?`,
		p.DisplayName, string(writersJSON), p.UpdatedAt, p.Scope,
	)
	if err != nil {
		return fmt.Errorf("update scope policy: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrScopePolicyNotFound
	}
	return nil
}

// Delete 删除策略 / Delete a scope policy
func (s *SQLiteScopePolicyStore) Delete(ctx context.Context, scope string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM scope_policies WHERE scope = ?`, scope)
	if err != nil {
		return fmt.Errorf("delete scope policy: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrScopePolicyNotFound
	}
	return nil
}
