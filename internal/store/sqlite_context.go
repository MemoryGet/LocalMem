package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/model"

	"github.com/google/uuid"
)

// 编译期接口检查 / Compile-time interface compliance check
var _ ContextStore = (*SQLiteContextStore)(nil)

// 上下文表全量列名（16列）/ Context table all columns (16 columns)
const contextColumns = `id, name, path, parent_id, scope, kind, description, mission, directives, disposition, metadata, depth, sort_order, memory_count, created_at, updated_at`

// SQLiteContextStore 基于 SQLite 的上下文存储 / SQLite-backed context store
type SQLiteContextStore struct {
	db *sql.DB
}

// NewSQLiteContextStore 创建 SQLite 上下文存储实例 / Create a new SQLite context store
func NewSQLiteContextStore(db *sql.DB) *SQLiteContextStore {
	return &SQLiteContextStore{db: db}
}

// Create 创建上下文 / Create a new context record
func (s *SQLiteContextStore) Create(ctx context.Context, c *model.Context) error {
	if c.Name == "" {
		return fmt.Errorf("name is required: %w", model.ErrInvalidInput)
	}

	now := time.Now().UTC()
	c.ID = uuid.New().String()
	c.CreatedAt = now
	c.UpdatedAt = now

	// 计算路径和深度
	if c.ParentID != "" {
		parent, err := s.Get(ctx, c.ParentID)
		if err != nil {
			return fmt.Errorf("failed to get parent context: %w", err)
		}
		c.Path = parent.Path + "/" + c.Name
		c.Depth = parent.Depth + 1
	} else {
		c.Path = "/" + c.Name
		c.Depth = 0
	}

	metadataJSON, err := marshalMetadata(c.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `INSERT INTO contexts (` + contextColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, query,
		c.ID, c.Name, c.Path, c.ParentID, c.Scope, c.Kind, c.Description,
		c.Mission, c.Directives, c.Disposition,
		metadataJSON, c.Depth, c.SortOrder, c.MemoryCount, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert context: %w", err)
	}

	return nil
}

// Get 获取上下文 / Get a context by ID
func (s *SQLiteContextStore) Get(ctx context.Context, id string) (*model.Context, error) {
	query := `SELECT ` + contextColumns + ` FROM contexts WHERE id = ?`

	c, err := s.scanContext(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrContextNotFound
		}
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	return c, nil
}

// GetByPath 通过路径获取上下文 / Get a context by its materialized path
func (s *SQLiteContextStore) GetByPath(ctx context.Context, path string) (*model.Context, error) {
	query := `SELECT ` + contextColumns + ` FROM contexts WHERE path = ?`

	c, err := s.scanContext(s.db.QueryRowContext(ctx, query, path))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrContextNotFound
		}
		return nil, fmt.Errorf("failed to get context by path: %w", err)
	}

	return c, nil
}

// Update 更新上下文 / Update an existing context
func (s *SQLiteContextStore) Update(ctx context.Context, c *model.Context) error {
	if c.ID == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	metadataJSON, err := marshalMetadata(c.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	c.UpdatedAt = time.Now().UTC()

	query := `UPDATE contexts SET name = ?, description = ?, mission = ?, directives = ?, disposition = ?, metadata = ?, kind = ?, sort_order = ?, updated_at = ?
		WHERE id = ?`

	result, err := s.db.ExecContext(ctx, query,
		c.Name, c.Description, c.Mission, c.Directives, c.Disposition, metadataJSON, c.Kind, c.SortOrder, c.UpdatedAt,
		c.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update context: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrContextNotFound
	}

	return nil
}

// Delete 删除上下文 / Delete a context by ID
func (s *SQLiteContextStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM contexts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete context: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrContextNotFound
	}

	return nil
}

// ListChildren 列出子上下文 / List direct child contexts
func (s *SQLiteContextStore) ListChildren(ctx context.Context, parentID string) ([]*model.Context, error) {
	query := `SELECT ` + contextColumns + ` FROM contexts WHERE parent_id = ? ORDER BY sort_order, name`

	rows, err := s.db.QueryContext(ctx, query, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to list children: %w", err)
	}
	defer rows.Close()

	return s.scanContexts(rows)
}

// ListSubtree 列出子树 / List entire subtree under the given path
func (s *SQLiteContextStore) ListSubtree(ctx context.Context, path string) ([]*model.Context, error) {
	query := `SELECT ` + contextColumns + ` FROM contexts WHERE path LIKE ? || '/%' ORDER BY path`

	rows, err := s.db.QueryContext(ctx, query, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list subtree: %w", err)
	}
	defer rows.Close()

	return s.scanContexts(rows)
}

// Move 移动上下文到新的父节点 / Move context to a new parent (recompute path + depth for subtree)
func (s *SQLiteContextStore) Move(ctx context.Context, id string, newParentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 获取当前上下文
	var oldPath, name string
	var oldDepth int
	err = tx.QueryRowContext(ctx, `SELECT path, name, depth FROM contexts WHERE id = ?`, id).Scan(&oldPath, &name, &oldDepth)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.ErrContextNotFound
		}
		return fmt.Errorf("failed to get context for move: %w", err)
	}

	// 计算新路径和深度
	var newPath string
	var newDepth int
	if newParentID != "" {
		var parentPath string
		var parentDepth int
		err = tx.QueryRowContext(ctx, `SELECT path, depth FROM contexts WHERE id = ?`, newParentID).Scan(&parentPath, &parentDepth)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("parent context not found: %w", model.ErrContextNotFound)
			}
			return fmt.Errorf("failed to get new parent context: %w", err)
		}
		newPath = parentPath + "/" + name
		newDepth = parentDepth + 1
	} else {
		newPath = "/" + name
		newDepth = 0
	}

	depthDelta := newDepth - oldDepth

	// 更新当前节点
	_, err = tx.ExecContext(ctx,
		`UPDATE contexts SET parent_id = ?, path = ?, depth = ?, updated_at = ? WHERE id = ?`,
		newParentID, newPath, newDepth, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("failed to update context path: %w", err)
	}

	// 更新所有后代节点的路径和深度
	// 将 oldPath 前缀替换为 newPath
	descendantRows, err := tx.QueryContext(ctx,
		`SELECT id, path, depth FROM contexts WHERE path LIKE ? || '/%'`, oldPath,
	)
	if err != nil {
		return fmt.Errorf("failed to query descendants: %w", err)
	}

	type descendant struct {
		id    string
		path  string
		depth int
	}
	var descendants []descendant
	for descendantRows.Next() {
		var d descendant
		if err := descendantRows.Scan(&d.id, &d.path, &d.depth); err != nil {
			descendantRows.Close()
			return fmt.Errorf("failed to scan descendant: %w", err)
		}
		descendants = append(descendants, d)
	}
	descendantRows.Close()
	if err := descendantRows.Err(); err != nil {
		return fmt.Errorf("failed to iterate descendants: %w", err)
	}

	now := time.Now().UTC()
	for _, d := range descendants {
		updatedPath := newPath + strings.TrimPrefix(d.path, oldPath)
		updatedDepth := d.depth + depthDelta
		_, err = tx.ExecContext(ctx,
			`UPDATE contexts SET path = ?, depth = ?, updated_at = ? WHERE id = ?`,
			updatedPath, updatedDepth, now, d.id,
		)
		if err != nil {
			return fmt.Errorf("failed to update descendant %s: %w", d.id, err)
		}
	}

	return tx.Commit()
}

// IncrementMemoryCount 递增记忆计数 / Atomically increment memory count by 1
func (s *SQLiteContextStore) IncrementMemoryCount(ctx context.Context, id string) error {
	return s.IncrementMemoryCountBy(ctx, id, 1)
}

// IncrementMemoryCountBy 递增记忆计数（指定增量）/ Atomically increment memory count by delta
func (s *SQLiteContextStore) IncrementMemoryCountBy(ctx context.Context, id string, delta int) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE contexts SET memory_count = MAX(0, memory_count + ?), updated_at = ? WHERE id = ?`,
		delta, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("failed to increment memory count: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrContextNotFound
	}

	return nil
}

// DecrementMemoryCount 递减记忆计数 / Atomically decrement memory count by 1
func (s *SQLiteContextStore) DecrementMemoryCount(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE contexts SET memory_count = MAX(0, memory_count - 1), updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("failed to decrement memory count: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrContextNotFound
	}

	return nil
}

// ---- 扫描辅助结构体 / Scan helper structs ----

// ctxScanDest Context 扫描目标（16列）/ Context scan destination (16 columns)
type ctxScanDest struct {
	c       model.Context
	metaStr sql.NullString
}

// scanFields 返回扫描目标字段列表 / Returns scan destination fields
func (d *ctxScanDest) scanFields() []any {
	return []any{
		&d.c.ID, &d.c.Name, &d.c.Path, &d.c.ParentID, &d.c.Scope, &d.c.Kind, &d.c.Description,
		&d.c.Mission, &d.c.Directives, &d.c.Disposition,
		&d.metaStr, &d.c.Depth, &d.c.SortOrder, &d.c.MemoryCount, &d.c.CreatedAt, &d.c.UpdatedAt,
	}
}

// toContext 将扫描结果转为 Context / Convert scan result to Context
func (d *ctxScanDest) toContext() (*model.Context, error) {
	if d.metaStr.Valid {
		if err := json.Unmarshal([]byte(d.metaStr.String), &d.c.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	return &d.c, nil
}

// scanContext 从单行扫描 Context 对象 / Scan Context from a single row
func (s *SQLiteContextStore) scanContext(row *sql.Row) (*model.Context, error) {
	var d ctxScanDest
	if err := row.Scan(d.scanFields()...); err != nil {
		return nil, err
	}
	return d.toContext()
}

// scanContextFromRows 从结果集行扫描 Context 对象 / Scan Context from rows
func (s *SQLiteContextStore) scanContextFromRows(rows *sql.Rows) (*model.Context, error) {
	var d ctxScanDest
	if err := rows.Scan(d.scanFields()...); err != nil {
		return nil, err
	}
	return d.toContext()
}

// scanContexts 扫描多行上下文 / Scan multiple context rows
func (s *SQLiteContextStore) scanContexts(rows *sql.Rows) ([]*model.Context, error) {
	var contexts []*model.Context
	for rows.Next() {
		c, err := s.scanContextFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan context row: %w", err)
		}
		contexts = append(contexts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate context rows: %w", err)
	}
	return contexts, nil
}
