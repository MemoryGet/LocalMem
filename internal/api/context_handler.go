package api

import (
	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// ContextHandler 上下文处理器 / Context handler
type ContextHandler struct {
	manager *memory.ContextManager
}

// NewContextHandler 创建上下文处理器 / Create context handler
func NewContextHandler(manager *memory.ContextManager) *ContextHandler {
	return &ContextHandler{manager: manager}
}

// Create 创建上下文 / Create a context
// POST /v1/contexts
func (h *ContextHandler) Create(c *gin.Context, identity *model.Identity) {
	var req model.CreateContextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	// 非系统用户强制绑定 scope / Force scope from identity for non-system users
	if !identity.IsSystem() {
		req.Scope = identity.OwnerID
	}
	ctx, err := h.manager.Create(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Created(c, ctx)
}

// Get 获取上下文 / Get context
// GET /v1/contexts/:id
func (h *ContextHandler) Get(c *gin.Context, identity *model.Identity) {
	_ = identity // 身份已验证 / Identity verified

	id := c.Param("id")
	ctx, err := h.manager.Get(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, ctx)
}

// Update 更新上下文 / Update context
// PUT /v1/contexts/:id
func (h *ContextHandler) Update(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	// 授权检查：验证上下文归属 / Authorization: verify context ownership
	existing, err := h.manager.Get(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	if existing.Scope != "" && existing.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	var req model.UpdateContextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	ctx, err := h.manager.Update(c.Request.Context(), id, &req)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, ctx)
}

// Delete 删除上下文 / Delete context
// DELETE /v1/contexts/:id
func (h *ContextHandler) Delete(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	// 授权检查：验证上下文归属 / Authorization: verify context ownership
	existing, err := h.manager.Get(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	if existing.Scope != "" && existing.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.manager.Delete(c.Request.Context(), id); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// ListChildren 列出子上下文 / List child contexts
// GET /v1/contexts/:id/children
func (h *ContextHandler) ListChildren(c *gin.Context, identity *model.Identity) {
	_ = identity // 身份已验证，读操作无需 scope 过滤 / Identity verified; read ops need no scope filter

	id := c.Param("id")
	children, err := h.manager.ListChildren(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, children)
}

// ListSubtree 列出子树 / List subtree
// GET /v1/contexts/:id/tree
func (h *ContextHandler) ListSubtree(c *gin.Context, identity *model.Identity) {
	_ = identity // 身份已验证 / Identity verified

	id := c.Param("id")
	tree, err := h.manager.ListSubtree(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, tree)
}

// Move 移动上下文 / Move context
// POST /v1/contexts/:id/move
func (h *ContextHandler) Move(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	// 授权检查：验证上下文归属 / Authorization: verify context ownership
	existing, err := h.manager.Get(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	if existing.Scope != "" && existing.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	var req model.MoveContextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if err := h.manager.Move(c.Request.Context(), id, req.NewParentID); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// ListMemories 列出上下文中的记忆 / List memories in context
// GET /v1/contexts/:id/memories
func (h *ContextHandler) ListMemories(c *gin.Context, identity *model.Identity) {
	_ = identity // 身份已验证 / Identity verified

	// 由 memory handler 的 List 配合 context_id 过滤处理
	// Handled by memory handler's List with context_id filter
	Success(c, nil)
}
