package api

import (
	"fmt"
	"strconv"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// GraphStats 图谱统计数据 / Graph statistics
type GraphStats struct {
	TotalEntities    int                     `json:"total_entities"`
	TypeDistribution map[string]int          `json:"type_distribution"`
	RecentEntities   []*model.Entity         `json:"recent_entities"`
	Entities         []*model.Entity         `json:"entities"`
	TotalRelations   int                     `json:"total_relations"`
	Relations        []*model.EntityRelation `json:"relations"`
}

// GraphHandler 知识图谱处理器 / Graph handler
type GraphHandler struct {
	manager *memory.GraphManager
}

// NewGraphHandler 创建图谱处理器 / Create graph handler
func NewGraphHandler(manager *memory.GraphManager) *GraphHandler {
	return &GraphHandler{manager: manager}
}

// CreateEntity 创建实体 / POST /v1/entities
func (h *GraphHandler) CreateEntity(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) GetEntity(c *gin.Context, identity *model.Identity) {
	entity, err := h.manager.GetEntity(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	// 所有权校验 / Ownership check
	if entity.Scope != "" && entity.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}
	Success(c, entity)
}

// UpdateEntity 更新实体 / PUT /v1/entities/:id
func (h *GraphHandler) UpdateEntity(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) DeleteEntity(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) ListEntities(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) GetEntityRelations(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) GetEntityMemories(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) CreateRelation(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) DeleteRelation(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) CreateMemoryEntity(c *gin.Context, identity *model.Identity) {
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
func (h *GraphHandler) DeleteMemoryEntity(c *gin.Context, identity *model.Identity) {
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

// GetEntityProfile 获取实体聚合视图 / Get entity profile
func (h *GraphHandler) GetEntityProfile(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	if id == "" {
		Error(c, fmt.Errorf("entity id is required: %w", model.ErrInvalidInput))
		return
	}
	limitStr := c.DefaultQuery("limit", "50")
	limit := 50
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}
	profile, err := h.manager.GetEntityProfile(c.Request.Context(), id, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, profile)
}

// SearchEntities 按名称搜索实体 / Search entities by name
func (h *GraphHandler) SearchEntities(c *gin.Context, identity *model.Identity) {
	q := c.Query("q")
	if q == "" {
		Error(c, fmt.Errorf("query parameter 'q' is required: %w", model.ErrInvalidInput))
		return
	}
	scope := c.Query("scope")
	limitStr := c.DefaultQuery("limit", "20")
	limit := 20
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}
	entities, err := h.manager.FindEntitiesByName(c.Request.Context(), q, scope, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, entities)
}

// GetRelationEvidence 查询两实体之间的证据记忆 / GET /v1/graph/evidence?source_id=X&target_id=Y
func (h *GraphHandler) GetRelationEvidence(c *gin.Context) {
	sourceID := c.Query("source_id")
	targetID := c.Query("target_id")
	if sourceID == "" || targetID == "" {
		c.JSON(400, gin.H{"error": "source_id and target_id are required"})
		return
	}
	limit := 10
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	memories, err := h.manager.GetRelationEvidence(c.Request.Context(), sourceID, targetID, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, memories)
}

// GetGraphStats 图谱统计：无 scope 过滤，供监控面板使用 / Graph stats without scope filter for monitoring
// GET /v1/graph/stats?limit=500
func (h *GraphHandler) GetGraphStats(c *gin.Context) {
	limit := 500
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	if limit > 500 {
		limit = 500
	}
	entities, err := h.manager.ListEntities(c.Request.Context(), "", "", limit)
	if err != nil {
		Error(c, err)
		return
	}
	typeDist := make(map[string]int)
	for _, e := range entities {
		typeDist[e.EntityType]++
	}
	recent := entities
	if len(recent) > 20 {
		recent = recent[:20]
	}

	// 一次性查全量关系 / Fetch all relations in one query
	relations, relErr := h.manager.ListAllRelations(c.Request.Context(), 5000)
	if relErr != nil {
		relations = nil
	}
	// 过滤：只保留两端都在实体集内的关系 / Filter: only keep relations where both ends are in entity set
	entitySet := make(map[string]bool, len(entities))
	for _, e := range entities {
		entitySet[e.ID] = true
	}
	filtered := relations[:0]
	for _, r := range relations {
		if entitySet[r.SourceID] && entitySet[r.TargetID] {
			filtered = append(filtered, r)
		}
	}

	Success(c, &GraphStats{
		TotalEntities:    len(entities),
		TypeDistribution: typeDist,
		RecentEntities:   recent,
		Entities:         entities,
		TotalRelations:   len(filtered),
		Relations:        filtered,
	})
}
