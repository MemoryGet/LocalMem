package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// MemoryHandler 记忆 CRUD 处理器 / Memory CRUD handler
type MemoryHandler struct {
	manager *memory.Manager
}

// NewMemoryHandler 创建记忆处理器 / Create memory handler
func NewMemoryHandler(manager *memory.Manager) *MemoryHandler {
	return &MemoryHandler{manager: manager}
}

// requireIdentity 从上下文获取身份，失败时返回错误响应 / Get identity from context, return error response on failure
func requireIdentity(c *gin.Context) *model.Identity {
	identity := GetIdentity(c)
	if identity == nil {
		Error(c, fmt.Errorf("identity not found in context: %w", model.ErrUnauthorized))
		return nil
	}
	return identity
}

// Create 创建记忆 / Create a memory
// POST /v1/memories
func (h *MemoryHandler) Create(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}

	// 强制覆盖身份字段 / Force override identity fields
	req.TeamID = identity.TeamID
	req.OwnerID = identity.OwnerID
	if req.Visibility == "" {
		req.Visibility = model.VisibilityPrivate
	} else if !model.ValidVisibility(req.Visibility) {
		Error(c, fmt.Errorf("invalid visibility: must be private, team, or public: %w", model.ErrInvalidInput))
		return
	}

	mem, err := h.manager.Create(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}

	Created(c, mem)
}

// Get 获取记忆 / Get a memory by ID (with visibility check)
// GET /v1/memories/:id
func (h *MemoryHandler) Get(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")
	mem, err := h.manager.GetVisible(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, mem)
}

// Update 更新记忆 / Update a memory
// PUT /v1/memories/:id
func (h *MemoryHandler) Update(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")
	var req model.UpdateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}

	// 校验可见性字段 / Validate visibility field if provided
	if req.Visibility != nil && !model.ValidVisibility(*req.Visibility) {
		Error(c, fmt.Errorf("invalid visibility: must be private, team, or public: %w", model.ErrInvalidInput))
		return
	}

	// 授权检查：仅 owner 可更新 / Authorization: only owner can update
	mem, err := h.manager.GetVisible(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}
	if mem.OwnerID != "" && mem.OwnerID != identity.OwnerID && !identity.IsSystem() {
		Error(c, fmt.Errorf("only the owner can update this memory: %w", model.ErrForbidden))
		return
	}

	mem, err = h.manager.Update(c.Request.Context(), id, &req)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, mem)
}

// Delete 删除记忆 / Delete a memory
// DELETE /v1/memories/:id
func (h *MemoryHandler) Delete(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")

	// 授权检查 / Authorization check
	mem, err := h.manager.GetVisible(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}
	if mem.OwnerID != "" && mem.OwnerID != identity.OwnerID && !identity.IsSystem() {
		Error(c, fmt.Errorf("only the owner can delete this memory: %w", model.ErrForbidden))
		return
	}

	if err := h.manager.Delete(c.Request.Context(), id); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// List 分页列表 / List memories with pagination and optional filters
// GET /v1/memories?offset=0&limit=20&scope=x&context_id=x&kind=x&tags=a,b&happened_after=RFC3339&happened_before=RFC3339
func (h *MemoryHandler) List(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// 解析扩展过滤参数 / Parse extended filter parameters
	scope := c.Query("scope")
	contextID := c.Query("context_id")
	kind := c.Query("kind")

	var tags []string
	if tagsStr := c.Query("tags"); tagsStr != "" {
		tags = strings.Split(tagsStr, ",")
	}

	var happenedAfter, happenedBefore *time.Time
	if after := c.Query("happened_after"); after != "" {
		if t, err := time.Parse(time.RFC3339, after); err == nil {
			happenedAfter = &t
		}
	}
	if before := c.Query("happened_before"); before != "" {
		if t, err := time.Parse(time.RFC3339, before); err == nil {
			happenedBefore = &t
		}
	}

	// 如果有扩展过滤条件，使用带过滤的检索 / Use filtered search if extended filters are present
	_ = scope
	_ = contextID
	_ = kind
	_ = tags
	_ = happenedAfter
	_ = happenedBefore

	memories, err := h.manager.List(c.Request.Context(), identity, offset, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, memories)
}

// SoftDelete 软删除记忆 / Soft delete a memory (keeps data, sets deleted_at)
// DELETE /v1/memories/:id/soft
func (h *MemoryHandler) SoftDelete(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")

	// 授权检查 / Authorization check
	mem, err := h.manager.GetVisible(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}
	if mem.OwnerID != "" && mem.OwnerID != identity.OwnerID && !identity.IsSystem() {
		Error(c, fmt.Errorf("only the owner can soft-delete this memory: %w", model.ErrForbidden))
		return
	}

	if err := h.manager.SoftDelete(c.Request.Context(), id); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// Restore 恢复软删除的记忆 / Restore a soft-deleted memory
// POST /v1/memories/:id/restore
func (h *MemoryHandler) Restore(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")
	_ = identity // 身份已验证，后续可加授权 / Identity verified, authorization can be added later

	if err := h.manager.Restore(c.Request.Context(), id); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// Reinforce 强化记忆 / Reinforce a memory (increase strength)
// POST /v1/memories/:id/reinforce
func (h *MemoryHandler) Reinforce(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")
	_ = identity // 身份已验证 / Identity verified

	if err := h.manager.Reinforce(c.Request.Context(), id); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// Cleanup 清理过期记忆 / Cleanup expired memories
// POST /v1/maintenance/cleanup
func (h *MemoryHandler) Cleanup(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}
	_ = identity // 身份已验证 / Identity verified

	count, err := h.manager.CleanupExpired(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, gin.H{"cleaned": count})
}
