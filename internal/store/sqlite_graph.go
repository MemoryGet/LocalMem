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
var _ GraphStore = (*SQLiteGraphStore)(nil)

// SQLiteGraphStore 基于 SQLite 的知识图谱存储 / SQLite-backed knowledge graph store
type SQLiteGraphStore struct {
	db *sql.DB
}

// NewSQLiteGraphStore 创建知识图谱存储实例 / Create a new SQLite graph store
func NewSQLiteGraphStore(db *sql.DB) *SQLiteGraphStore {
	return &SQLiteGraphStore{db: db}
}

// CreateEntity 创建实体 / Create a new entity
func (s *SQLiteGraphStore) CreateEntity(ctx context.Context, entity *model.Entity) error {
	now := time.Now().UTC()
	entity.ID = uuid.New().String()
	entity.CreatedAt = now
	entity.UpdatedAt = now

	metadataJSON, err := marshalMetadata(entity.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal entity metadata: %w", err)
	}

	query := `INSERT INTO entities (id, name, entity_type, scope, description, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, query,
		entity.ID, entity.Name, entity.EntityType, entity.Scope, entity.Description,
		metadataJSON, entity.CreatedAt, entity.UpdatedAt,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("entity with same name, type and scope already exists: %w", model.ErrConflict)
		}
		return fmt.Errorf("failed to insert entity: %w", err)
	}

	return nil
}

// GetEntity 获取实体 / Get entity by ID
func (s *SQLiteGraphStore) GetEntity(ctx context.Context, id string) (*model.Entity, error) {
	query := `SELECT id, name, entity_type, scope, description, metadata, created_at, updated_at
		FROM entities WHERE id = ?`

	entity, err := scanEntity(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}

	return entity, nil
}

// ListEntities 列出实体 / List entities with optional scope and type filters
func (s *SQLiteGraphStore) ListEntities(ctx context.Context, scope, entityType string, limit int) ([]*model.Entity, error) {
	if limit <= 0 {
		limit = 20
	}

	var conditions []string
	var args []interface{}

	if scope != "" {
		conditions = append(conditions, "scope = ?")
		args = append(args, scope)
	}
	if entityType != "" {
		conditions = append(conditions, "entity_type = ?")
		args = append(args, entityType)
	}

	query := `SELECT id, name, entity_type, scope, description, metadata, created_at, updated_at FROM entities`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list entities: %w", err)
	}
	defer rows.Close()

	var entities []*model.Entity
	for rows.Next() {
		entity, err := scanEntityFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan entity row: %w", err)
		}
		entities = append(entities, entity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate entity rows: %w", err)
	}

	return entities, nil
}

// UpdateEntity 更新实体 / Update an existing entity
func (s *SQLiteGraphStore) UpdateEntity(ctx context.Context, entity *model.Entity) error {
	if entity.ID == "" {
		return fmt.Errorf("entity id is required: %w", model.ErrInvalidInput)
	}

	metadataJSON, err := marshalMetadata(entity.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal entity metadata: %w", err)
	}

	entity.UpdatedAt = time.Now().UTC()

	query := `UPDATE entities SET name = ?, description = ?, metadata = ?, updated_at = ? WHERE id = ?`

	result, err := s.db.ExecContext(ctx, query,
		entity.Name, entity.Description, metadataJSON, entity.UpdatedAt, entity.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update entity: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrEntityNotFound
	}

	return nil
}

// DeleteEntity 删除实体（级联删除关系和关联）/ Delete an entity with cascade
func (s *SQLiteGraphStore) DeleteEntity(ctx context.Context, id string) error {
	// 级联删除 entity_relations 中相关的行
	if _, err := s.db.ExecContext(ctx, `DELETE FROM entity_relations WHERE source_id = ? OR target_id = ?`, id, id); err != nil {
		return fmt.Errorf("failed to cascade delete entity relations: %w", err)
	}

	// 级联删除 memory_entities 中相关的行
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_entities WHERE entity_id = ?`, id); err != nil {
		return fmt.Errorf("failed to cascade delete memory entities: %w", err)
	}

	// 删除实体本身
	result, err := s.db.ExecContext(ctx, `DELETE FROM entities WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrEntityNotFound
	}

	return nil
}

// CreateRelation 创建关系 / Create an entity relation
func (s *SQLiteGraphStore) CreateRelation(ctx context.Context, rel *model.EntityRelation) error {
	now := time.Now().UTC()
	rel.ID = uuid.New().String()
	rel.CreatedAt = now

	if rel.Weight == 0 {
		rel.Weight = 1.0
	}

	metadataJSON, err := marshalMetadata(rel.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal relation metadata: %w", err)
	}

	query := `INSERT INTO entity_relations (id, source_id, target_id, relation_type, weight, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, query,
		rel.ID, rel.SourceID, rel.TargetID, rel.RelationType, rel.Weight,
		metadataJSON, rel.CreatedAt,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("relation already exists: %w", model.ErrConflict)
		}
		return fmt.Errorf("failed to insert relation: %w", err)
	}

	return nil
}

// DeleteRelation 删除关系 / Delete an entity relation by ID
func (s *SQLiteGraphStore) DeleteRelation(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM entity_relations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete relation: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrRelationNotFound
	}

	return nil
}

// GetEntityRelations 获取实体的所有关系 / Get all relations for an entity
func (s *SQLiteGraphStore) GetEntityRelations(ctx context.Context, entityID string) ([]*model.EntityRelation, error) {
	query := `SELECT id, source_id, target_id, relation_type, weight, metadata, created_at
		FROM entity_relations WHERE source_id = ? OR target_id = ?`

	rows, err := s.db.QueryContext(ctx, query, entityID, entityID)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity relations: %w", err)
	}
	defer rows.Close()

	var relations []*model.EntityRelation
	for rows.Next() {
		rel, err := scanRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan relation row: %w", err)
		}
		relations = append(relations, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate relation rows: %w", err)
	}

	return relations, nil
}

// CreateMemoryEntity 创建记忆-实体关联 / Create a memory-entity association
func (s *SQLiteGraphStore) CreateMemoryEntity(ctx context.Context, me *model.MemoryEntity) error {
	now := time.Now().UTC()
	me.CreatedAt = now

	query := `INSERT INTO memory_entities (memory_id, entity_id, role, created_at)
		VALUES (?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query, me.MemoryID, me.EntityID, me.Role, me.CreatedAt)
	if err != nil {
		if isUniqueConstraintError(err) {
			return fmt.Errorf("memory-entity association already exists: %w", model.ErrConflict)
		}
		return fmt.Errorf("failed to insert memory-entity association: %w", err)
	}

	return nil
}

// DeleteMemoryEntity 删除记忆-实体关联 / Delete a memory-entity association
func (s *SQLiteGraphStore) DeleteMemoryEntity(ctx context.Context, memoryID, entityID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM memory_entities WHERE memory_id = ? AND entity_id = ?`, memoryID, entityID)
	if err != nil {
		return fmt.Errorf("failed to delete memory-entity association: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("memory-entity association not found: %w", model.ErrEntityNotFound)
	}

	return nil
}

// GetEntityMemories 获取实体关联的记忆 / Get memories associated with an entity
func (s *SQLiteGraphStore) GetEntityMemories(ctx context.Context, entityID string, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `SELECT ` + memoryColumnsAliased + `
		FROM memories m
		JOIN memory_entities me ON m.id = me.memory_id
		WHERE me.entity_id = ? AND m.deleted_at IS NULL
		ORDER BY m.updated_at DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, entityID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity memories: %w", err)
	}
	defer rows.Close()

	var memories []*model.Memory
	for rows.Next() {
		mem, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		memories = append(memories, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate memory rows: %w", err)
	}

	return memories, nil
}

// GetMemoryEntities 获取记忆关联的实体 / Get entities associated with a memory
func (s *SQLiteGraphStore) GetMemoryEntities(ctx context.Context, memoryID string) ([]*model.Entity, error) {
	query := `SELECT e.id, e.name, e.entity_type, e.scope, e.description, e.metadata, e.created_at, e.updated_at
		FROM entities e
		JOIN memory_entities me ON e.id = me.entity_id
		WHERE me.memory_id = ?
		ORDER BY e.name`

	rows, err := s.db.QueryContext(ctx, query, memoryID)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory entities: %w", err)
	}
	defer rows.Close()

	var entities []*model.Entity
	for rows.Next() {
		entity, err := scanEntityFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan entity row: %w", err)
		}
		entities = append(entities, entity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate entity rows: %w", err)
	}

	return entities, nil
}

// ---- 扫描辅助函数 ----

// scanEntity 从单行扫描 Entity 对象
func scanEntity(row *sql.Row) (*model.Entity, error) {
	var (
		entity  model.Entity
		metaStr sql.NullString
	)

	err := row.Scan(
		&entity.ID, &entity.Name, &entity.EntityType, &entity.Scope,
		&entity.Description, &metaStr, &entity.CreatedAt, &entity.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if metaStr.Valid {
		if err := json.Unmarshal([]byte(metaStr.String), &entity.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal entity metadata: %w", err)
		}
	}

	return &entity, nil
}

// scanEntityFromRows 从结果集行扫描 Entity 对象
func scanEntityFromRows(rows *sql.Rows) (*model.Entity, error) {
	var (
		entity  model.Entity
		metaStr sql.NullString
	)

	err := rows.Scan(
		&entity.ID, &entity.Name, &entity.EntityType, &entity.Scope,
		&entity.Description, &metaStr, &entity.CreatedAt, &entity.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if metaStr.Valid {
		if err := json.Unmarshal([]byte(metaStr.String), &entity.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal entity metadata: %w", err)
		}
	}

	return &entity, nil
}

// scanRelation 从结果集行扫描 EntityRelation 对象
func scanRelation(rows *sql.Rows) (*model.EntityRelation, error) {
	var (
		rel     model.EntityRelation
		metaStr sql.NullString
	)

	err := rows.Scan(
		&rel.ID, &rel.SourceID, &rel.TargetID, &rel.RelationType,
		&rel.Weight, &metaStr, &rel.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if metaStr.Valid {
		if err := json.Unmarshal([]byte(metaStr.String), &rel.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal relation metadata: %w", err)
		}
	}

	return &rel, nil
}

// isUniqueConstraintError 检查是否为唯一约束冲突错误
func isUniqueConstraintError(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
