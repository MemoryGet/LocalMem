// sqlite_memory_read.go 记忆读取操作 / Memory read operations
package store

import (
	"context"
	"database/sql"
	"fmt"

	"iclude/internal/model"
	"iclude/pkg/sqlbuilder"
)

// Get 获取单条记忆（纯读，不修改访问计数）/ Get a memory by ID (read-only, does not update access count)
// 调用方如需记录访问，请显式调用 IncrementAccessCount / Callers should use IncrementAccessCount explicitly
func (s *SQLiteMemoryStore) Get(ctx context.Context, id string) (*model.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE id = ? AND deleted_at IS NULL`

	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	return mem, nil
}

// GetVisible 带可见性校验获取记忆 / Get memory with visibility check
func (s *SQLiteMemoryStore) GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error) {
	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE id = ? AND deleted_at IS NULL AND ` + visCond

	args := append([]interface{}{id}, visArgs...)
	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get visible memory: %w", err)
	}
	return mem, nil
}

// List 分页列表（排除软删除）/ List memories with pagination (exclude soft-deleted)
func (s *SQLiteMemoryStore) List(ctx context.Context, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE deleted_at IS NULL AND ` + visCond + `
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?`

	args := append(visArgs, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// ListByContext 按上下文列出记忆 / List memories by context ID
func (s *SQLiteMemoryStore) ListByContext(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE context_id = ? AND deleted_at IS NULL AND ` + visCond + `
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?`

	args := append([]interface{}{contextID}, visArgs...)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories by context: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// ListByContextOrdered 按轮次顺序列出上下文记忆 / List memories by context ordered by turn number
func (s *SQLiteMemoryStore) ListByContextOrdered(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE context_id = ? AND deleted_at IS NULL AND ` + visCond + `
		ORDER BY turn_number ASC, created_at ASC
		LIMIT ? OFFSET ?`

	args := append([]interface{}{contextID}, visArgs...)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories by context ordered: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// GetByURI 通过 URI 获取记忆 / Get memory by URI
func (s *SQLiteMemoryStore) GetByURI(ctx context.Context, uri string) (*model.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE uri = ? AND deleted_at IS NULL`
	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, uri))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get memory by URI: %w", err)
	}
	return mem, nil
}

// GetByContentHash 通过内容哈希获取记忆 / Get memory by content hash
func (s *SQLiteMemoryStore) GetByContentHash(ctx context.Context, contentHash string) (*model.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE content_hash = ? AND deleted_at IS NULL`
	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, contentHash))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get memory by content hash: %w", err)
	}
	return mem, nil
}

// ListTimeline 时间线查询 / List memories by timeline
func (s *SQLiteMemoryStore) ListTimeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	qb := sqlbuilder.Select(memoryColumns).
		From("memories").
		OrderBy("COALESCE(happened_at, created_at) DESC").
		Limit(limit)

	qb.Where().And("deleted_at IS NULL")
	qb.Where().AndIf(req.Scope != "", "scope = ?", req.Scope)
	qb.Where().AndIf(req.SourceRef != "", "source_ref = ?", req.SourceRef)
	qb.Where().AndIf(req.After != nil, "COALESCE(happened_at, created_at) >= ?", req.After)
	qb.Where().AndIf(req.Before != nil, "COALESCE(happened_at, created_at) <= ?", req.Before)

	// 可见性过滤：使用请求中携带的身份信息 / Apply visibility filter using identity from request
	// 无身份时仅返回公开记忆，防止越权访问 / Without identity, only public memories are visible
	if req.TeamID != "" || req.OwnerID != "" {
		identity := &model.Identity{TeamID: req.TeamID, OwnerID: req.OwnerID}
		visCond, visArgs := visibilityCondition("", identity)
		qb.Where().And(visCond, visArgs...)
	} else {
		qb.Where().And("visibility = 'public'")
	}

	sqlQuery, args := qb.Build()

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list timeline: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// GetOwnerID 获取记忆的 owner_id（含 soft-deleted）/ Get owner_id including soft-deleted memories
func (s *SQLiteMemoryStore) GetOwnerID(ctx context.Context, id string) (string, error) {
	var ownerID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM memories WHERE id = ?`, id).Scan(&ownerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", model.ErrMemoryNotFound
		}
		return "", fmt.Errorf("failed to get owner_id: %w", err)
	}
	if ownerID.Valid {
		return ownerID.String, nil
	}
	return "", nil
}

// ListMissingExcerpt 列出缺少摘要的记忆 / List memories missing excerpt
func (s *SQLiteMemoryStore) ListMissingExcerpt(ctx context.Context, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE (excerpt = '' OR excerpt IS NULL) AND deleted_at IS NULL ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list missing excerpt: %w", err)
	}
	defer rows.Close()

	var memories []*model.Memory
	for rows.Next() {
		mem, err := s.scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		memories = append(memories, mem)
	}
	return memories, rows.Err()
}

// ListBySourceRef 按来源引用列出记忆 / List memories by source_ref
func (s *SQLiteMemoryStore) ListBySourceRef(ctx context.Context, sourceRef string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE source_ref = ? AND deleted_at IS NULL AND ` + visCond + `
		ORDER BY COALESCE(happened_at, created_at) DESC
		LIMIT ? OFFSET ?`

	args := append([]interface{}{sourceRef}, visArgs...)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories by source_ref: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// ListDerivedFrom 查询由指定记忆衍生出的记忆 / List memories whose derived_from contains the given ID
func (s *SQLiteMemoryStore) ListDerivedFrom(ctx context.Context, id string, identity *model.Identity) ([]*model.Memory, error) {
	visCond, visArgs := visibilityCondition("", identity)
	// derived_from 是 JSON 数组文本列，用 json_each 精确匹配 / Use json_each for exact match in JSON array
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE id IN (
			SELECT memories.id FROM memories, json_each(memories.derived_from) AS j
			WHERE j.value = ? AND memories.deleted_at IS NULL
		) AND deleted_at IS NULL AND ` + visCond + `
		ORDER BY created_at DESC`

	args := append([]interface{}{id}, visArgs...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list derived-from memories: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// ListConsolidatedInto 查询被归纳到指定记忆的原始记忆 / List memories whose consolidated_into equals the given ID
func (s *SQLiteMemoryStore) ListConsolidatedInto(ctx context.Context, id string, identity *model.Identity) ([]*model.Memory, error) {
	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE consolidated_into = ? AND deleted_at IS NULL AND ` + visCond + `
		ORDER BY created_at DESC`

	args := append([]interface{}{id}, visArgs...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list consolidated-into memories: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}
