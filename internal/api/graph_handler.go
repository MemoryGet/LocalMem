package api

import (
	"fmt"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// GraphHandler 知识图谱处理器 / Graph handler
type GraphHandler struct {
	manager *memory.GraphManager
}

// NewGraphHandler 创建图谱处理器 / Create graph handler
func NewGraphHandler(manager *memory.GraphManager) *GraphHandler {
	return &GraphHandler{manager: manager}
}

// CreateEntity 创建实体 / POST /v1/entities
func (h *GraphHandler) CreateEntity(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.CreateEntityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if !identity.IsSystem() {
		req.Scope = identity.OwnerID
	}
	entity, err := h.manager.CreateEntity(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Created(c, entity)
}

// GetEntity 获取实体 / GET /v1/entities/:id
func (h *GraphHandler) GetEntity(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	entity, err := h.manager.GetEntity(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, entity)
}

// UpdateEntity 更新实体 / PUT /v1/entities/:id
func (h *GraphHandler) UpdateEntity(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	entity, err := h.manager.GetEntity(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	if entity.Scope != "" && entity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	var req model.UpdateEntityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	updated, err := h.manager.UpdateEntity(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, updated)
}

// DeleteEntity 删除实体 / DELETE /v1/entities/:id
func (h *GraphHandler) DeleteEntity(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	entity, err := h.manager.GetEntity(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	if entity.Scope != "" && entity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.manager.DeleteEntity(c.Request.Context(), c.Param("id")); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// ListEntities 列出实体 / GET /v1/entities?scope=x&type=y&limit=20
func (h *GraphHandler) ListEntities(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	scope := c.Query("scope")
	if !identity.IsSystem() {
		scope = identity.OwnerID
	}
	entityType := c.Query("type")
	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if limit > 200 {
		limit = 200
	}
	entities, err := h.manager.ListEntities(c.Request.Context(), scope, entityType, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, entities)
}

// GetEntityRelations 获取实体关系 / GET /v1/entities/:id/relations
func (h *GraphHandler) GetEntityRelations(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	// 授权检查：先获取实体，验证 scope 归属 / Authorization: fetch entity and verify scope ownership
	entity, err := h.manager.GetEntity(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	if entity.Scope != "" && entity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	relations, err := h.manager.GetEntityRelations(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, relations)
}

// GetEntityMemories 获取实体记忆 / GET /v1/entities/:id/memories
func (h *GraphHandler) GetEntityMemories(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	entity, err := h.manager.GetEntity(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	if entity.Scope != "" && entity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if limit > 200 {
		limit = 200
	}
	memories, err := h.manager.GetEntityMemories(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, memories)
}

// CreateRelation 创建关系 / POST /v1/entity-relations
func (h *GraphHandler) CreateRelation(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.CreateEntityRelationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}

	// 授权检查：验证源实体归属 / Authorization: verify source entity ownership
	sourceEntity, err := h.manager.GetEntity(c.Request.Context(), req.SourceID)
	if err != nil {
		Error(c, err)
		return
	}
	if sourceEntity.Scope != "" && sourceEntity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	rel, err := h.manager.CreateRelation(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Created(c, rel)
}

// DeleteRelation 删除关系 / DELETE /v1/entity-relations/:id
func (h *GraphHandler) DeleteRelation(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	// 授权检查：获取关系→获取源实体→验证 scope 归属
	// Authorization: fetch relation → fetch source entity → verify scope ownership
	rel, err := h.manager.GetRelation(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	sourceEntity, err := h.manager.GetEntity(c.Request.Context(), rel.SourceID)
	if err != nil {
		Error(c, err)
		return
	}
	if sourceEntity.Scope != "" && sourceEntity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.manager.DeleteRelation(c.Request.Context(), c.Param("id")); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// CreateMemoryEntity 创建记忆-实体关联 / POST /v1/memory-entities
func (h *GraphHandler) CreateMemoryEntity(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.CreateMemoryEntityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}

	// 授权检查：验证实体归属 / Authorization: verify entity ownership
	entity, err := h.manager.GetEntity(c.Request.Context(), req.EntityID)
	if err != nil {
		Error(c, err)
		return
	}
	if entity.Scope != "" && entity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.manager.CreateMemoryEntity(c.Request.Context(), &req); err != nil {
		Error(c, err)
		return
	}
	Created(c, nil)
}

// DeleteMemoryEntity 删除记忆-实体关联 / DELETE /v1/memory-entities
func (h *GraphHandler) DeleteMemoryEntity(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	memoryID := c.Query("memory_id")
	entityID := c.Query("entity_id")

	// 授权检查：验证实体归属 / Authorization: verify entity ownership
	entity, err := h.manager.GetEntity(c.Request.Context(), entityID)
	if err != nil {
		Error(c, err)
		return
	}
	if entity.Scope != "" && entity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.manager.DeleteMemoryEntity(c.Request.Context(), memoryID, entityID); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}
