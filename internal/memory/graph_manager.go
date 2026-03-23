package memory

import (
	"context"
	"fmt"

	"iclude/internal/model"
	"iclude/internal/store"
)

// GraphManager 知识图谱管理器 / Knowledge graph manager
type GraphManager struct {
	graphStore store.GraphStore
}

// NewGraphManager 创建图谱管理器 / Create graph manager
func NewGraphManager(graphStore store.GraphStore) *GraphManager {
	return &GraphManager{graphStore: graphStore}
}

// CreateEntity 创建实体 / Create entity
func (m *GraphManager) CreateEntity(ctx context.Context, req *model.CreateEntityRequest) (*model.Entity, error) {
	if req.Name == "" || req.EntityType == "" {
		return nil, fmt.Errorf("name and entity_type are required: %w", model.ErrInvalidInput)
	}
	entity := &model.Entity{
		Name:        req.Name,
		EntityType:  req.EntityType,
		Scope:       req.Scope,
		Description: req.Description,
		Metadata:    req.Metadata,
	}
	if err := m.graphStore.CreateEntity(ctx, entity); err != nil {
		return nil, fmt.Errorf("failed to create entity: %w", err)
	}
	return entity, nil
}

// GetEntity 获取实体 / Get entity
func (m *GraphManager) GetEntity(ctx context.Context, id string) (*model.Entity, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.graphStore.GetEntity(ctx, id)
}

// ListEntities 列出实体 / List entities
func (m *GraphManager) ListEntities(ctx context.Context, scope, entityType string, limit int) ([]*model.Entity, error) {
	if limit <= 0 {
		limit = 20
	}
	return m.graphStore.ListEntities(ctx, scope, entityType, limit)
}

// UpdateEntity 更新实体 / Update entity
func (m *GraphManager) UpdateEntity(ctx context.Context, id string, req *model.UpdateEntityRequest) (*model.Entity, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	entity, err := m.graphStore.GetEntity(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		entity.Name = *req.Name
	}
	if req.Description != nil {
		entity.Description = *req.Description
	}
	if req.Metadata != nil {
		entity.Metadata = req.Metadata
	}
	if err := m.graphStore.UpdateEntity(ctx, entity); err != nil {
		return nil, fmt.Errorf("failed to update entity: %w", err)
	}
	return entity, nil
}

// DeleteEntity 删除实体 / Delete entity
func (m *GraphManager) DeleteEntity(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.graphStore.DeleteEntity(ctx, id)
}

// CreateRelation 创建关系 / Create relation
func (m *GraphManager) CreateRelation(ctx context.Context, req *model.CreateEntityRelationRequest) (*model.EntityRelation, error) {
	if req.SourceID == "" || req.TargetID == "" || req.RelationType == "" {
		return nil, fmt.Errorf("source_id, target_id, and relation_type are required: %w", model.ErrInvalidInput)
	}
	rel := &model.EntityRelation{
		SourceID:     req.SourceID,
		TargetID:     req.TargetID,
		RelationType: req.RelationType,
		Weight:       req.Weight,
		Metadata:     req.Metadata,
	}
	if rel.Weight == 0 {
		rel.Weight = 1.0
	}
	if err := m.graphStore.CreateRelation(ctx, rel); err != nil {
		return nil, fmt.Errorf("failed to create relation: %w", err)
	}
	return rel, nil
}

// DeleteRelation 删除关系 / Delete relation
func (m *GraphManager) DeleteRelation(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.graphStore.DeleteRelation(ctx, id)
}

// GetEntityRelations 获取实体关系 / Get entity relations
func (m *GraphManager) GetEntityRelations(ctx context.Context, entityID string) ([]*model.EntityRelation, error) {
	if entityID == "" {
		return nil, fmt.Errorf("entity_id is required: %w", model.ErrInvalidInput)
	}
	return m.graphStore.GetEntityRelations(ctx, entityID)
}

// CreateMemoryEntity 关联记忆和实体 / Associate memory with entity
func (m *GraphManager) CreateMemoryEntity(ctx context.Context, req *model.CreateMemoryEntityRequest) error {
	if req.MemoryID == "" || req.EntityID == "" {
		return fmt.Errorf("memory_id and entity_id are required: %w", model.ErrInvalidInput)
	}
	me := &model.MemoryEntity{
		MemoryID: req.MemoryID,
		EntityID: req.EntityID,
		Role:     req.Role,
	}
	return m.graphStore.CreateMemoryEntity(ctx, me)
}

// DeleteMemoryEntity 解除记忆和实体关联 / Remove memory-entity association
func (m *GraphManager) DeleteMemoryEntity(ctx context.Context, memoryID, entityID string) error {
	return m.graphStore.DeleteMemoryEntity(ctx, memoryID, entityID)
}

// GetEntityMemories 获取实体关联的记忆 / Get memories for entity
func (m *GraphManager) GetEntityMemories(ctx context.Context, entityID string, limit int) ([]*model.Memory, error) {
	if entityID == "" {
		return nil, fmt.Errorf("entity_id is required: %w", model.ErrInvalidInput)
	}
	if limit <= 0 {
		limit = 20
	}
	return m.graphStore.GetEntityMemories(ctx, entityID, limit)
}

// GetMemoryEntities 获取记忆关联的实体 / Get entities for memory
func (m *GraphManager) GetMemoryEntities(ctx context.Context, memoryID string) ([]*model.Entity, error) {
	if memoryID == "" {
		return nil, fmt.Errorf("memory_id is required: %w", model.ErrInvalidInput)
	}
	return m.graphStore.GetMemoryEntities(ctx, memoryID)
}
