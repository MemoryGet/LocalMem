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
		if IsUniqueConstraintError(err) {
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

// DeleteEntity 删除实体（原子级联删除关系和关联）/ Delete an entity with atomic cascade
func (s *SQLiteGraphStore) DeleteEntity(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete entity tx: %w", err)
	}
	defer tx.Rollback()

	// 级联删除 entity_relations 中相关的行
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_relations WHERE source_id = ? OR target_id = ?`, id, id); err != nil {
		return fmt.Errorf("failed to cascade delete entity relations: %w", err)
	}

	// 级联删除 memory_entities 中相关的行
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_entities WHERE entity_id = ?`, id); err != nil {
		return fmt.Errorf("failed to cascade delete memory entities: %w", err)
	}

	// 删除实体本身
	result, err := tx.ExecContext(ctx, `DELETE FROM entities WHERE id = ?`, id)
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete entity tx: %w", err)
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
		if IsUniqueConstraintError(err) {
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

// GetRelation 获取单条关系 / Get a single entity relation by ID
func (s *SQLiteGraphStore) GetRelation(ctx context.Context, id string) (*model.EntityRelation, error) {
	query := `SELECT id, source_id, target_id, relation_type, weight, metadata, created_at
		FROM entity_relations WHERE id = ?`

	var d relationScanDest
	if err := s.db.QueryRowContext(ctx, query, id).Scan(d.scanFields()...); err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrRelationNotFound
		}
		return nil, fmt.Errorf("failed to get relation: %w", err)
	}
	return d.toRelation()
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
		if IsUniqueConstraintError(err) {
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

// GetMemoriesEntities 批量获取记忆关联的实体 / Batch get entities for multiple memories
func (s *SQLiteGraphStore) GetMemoriesEntities(ctx context.Context, memoryIDs []string) (map[string][]*model.Entity, error) {
	result := make(map[string][]*model.Entity, len(memoryIDs))
	if len(memoryIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(memoryIDs))
	args := make([]interface{}, len(memoryIDs))
	for i, id := range memoryIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := `SELECT me.memory_id, e.id, e.name, e.entity_type, e.scope, e.description, e.metadata, e.created_at, e.updated_at
		FROM entities e
		JOIN memory_entities me ON e.id = me.entity_id
		WHERE me.memory_id IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY me.memory_id, e.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch get memory entities: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var memoryID string
		var d entityScanDest
		dest := append([]any{&memoryID}, d.scanFields()...)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("failed to scan batch entity row: %w", err)
		}
		entity, err := d.toEntity()
		if err != nil {
			return nil, fmt.Errorf("failed to convert batch entity: %w", err)
		}
		result[memoryID] = append(result[memoryID], entity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate batch entity rows: %w", err)
	}

	return result, nil
}

// ---- 扫描辅助结构体 / Scan helper structs ----

// entityScanDest Entity 扫描目标（8列）/ Entity scan destination (8 columns)
type entityScanDest struct {
	entity  model.Entity
	metaStr sql.NullString
}

// scanFields 返回扫描目标字段列表 / Returns scan destination fields
func (d *entityScanDest) scanFields() []any {
	return []any{
		&d.entity.ID, &d.entity.Name, &d.entity.EntityType, &d.entity.Scope,
		&d.entity.Description, &d.metaStr, &d.entity.CreatedAt, &d.entity.UpdatedAt,
	}
}

// toEntity 将扫描结果转为 Entity / Convert scan result to Entity
func (d *entityScanDest) toEntity() (*model.Entity, error) {
	if d.metaStr.Valid {
		if err := json.Unmarshal([]byte(d.metaStr.String), &d.entity.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal entity metadata: %w", err)
		}
	}
	return &d.entity, nil
}

// scanEntity 从单行扫描 Entity 对象 / Scan Entity from a single row
func scanEntity(row *sql.Row) (*model.Entity, error) {
	var d entityScanDest
	if err := row.Scan(d.scanFields()...); err != nil {
		return nil, err
	}
	return d.toEntity()
}

// scanEntityFromRows 从结果集行扫描 Entity 对象 / Scan Entity from rows
func scanEntityFromRows(rows *sql.Rows) (*model.Entity, error) {
	var d entityScanDest
	if err := rows.Scan(d.scanFields()...); err != nil {
		return nil, err
	}
	return d.toEntity()
}

// relationScanDest EntityRelation 扫描目标（7列）/ EntityRelation scan destination (7 columns)
type relationScanDest struct {
	rel     model.EntityRelation
	metaStr sql.NullString
}

// scanFields 返回扫描目标字段列表 / Returns scan destination fields
func (d *relationScanDest) scanFields() []any {
	return []any{
		&d.rel.ID, &d.rel.SourceID, &d.rel.TargetID, &d.rel.RelationType,
		&d.rel.Weight, &d.metaStr, &d.rel.CreatedAt,
	}
}

// toRelation 将扫描结果转为 EntityRelation / Convert scan result to EntityRelation
func (d *relationScanDest) toRelation() (*model.EntityRelation, error) {
	if d.metaStr.Valid {
		if err := json.Unmarshal([]byte(d.metaStr.String), &d.rel.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal relation metadata: %w", err)
		}
	}
	return &d.rel, nil
}

// scanRelation 从结果集行扫描 EntityRelation 对象 / Scan EntityRelation from rows
func scanRelation(rows *sql.Rows) (*model.EntityRelation, error) {
	var d relationScanDest
	if err := rows.Scan(d.scanFields()...); err != nil {
		return nil, err
	}
	return d.toRelation()
}

// FindEntitiesByName 按名称匹配实体（大小写不敏感）/ Find entities by name (case-insensitive)
func (s *SQLiteGraphStore) FindEntitiesByName(ctx context.Context, name string, scope string, limit int) ([]*model.Entity, error) {
	if limit <= 0 {
		limit = 20
	}

	var conditions []string
	var args []interface{}

	conditions = append(conditions, "name = ? COLLATE NOCASE")
	args = append(args, name)

	if scope != "" {
		conditions = append(conditions, "scope = ?")
		args = append(args, scope)
	}

	query := `SELECT id, name, entity_type, scope, description, metadata, created_at, updated_at FROM entities WHERE ` +
		strings.Join(conditions, " AND ") + ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to find entities by name: %w", err)
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

