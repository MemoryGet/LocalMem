package api

import (
	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// TagHandler 标签处理器 / Tag handler
type TagHandler struct {
	tagMgr *memory.TagManager
}

// NewTagHandler 创建标签处理器 / Create tag handler
func NewTagHandler(tagMgr *memory.TagManager) *TagHandler {
	return &TagHandler{tagMgr: tagMgr}
}

// CreateTag 创建标签 / Create tag
// POST /v1/tags
func (h *TagHandler) CreateTag(c *gin.Context, identity *model.Identity) {
	var tag model.Tag
	if err := c.ShouldBindJSON(&tag); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if !identity.IsSystem() {
		tag.Scope = identity.OwnerID
	}
	if err := h.tagMgr.CreateTag(c.Request.Context(), &tag); err != nil {
		Error(c, err)
		return
	}
	Created(c, tag)
}

// ListTags 列出标签 / List tags
// GET /v1/tags?scope=xxx
func (h *TagHandler) ListTags(c *gin.Context, identity *model.Identity) {
	scope := c.Query("scope")
	if !identity.IsSystem() {
		scope = identity.OwnerID
	}
	tags, err := h.tagMgr.ListTags(c.Request.Context(), scope)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, tags)
}

// DeleteTag 删除标签 / Delete tag
// DELETE /v1/tags/:id
func (h *TagHandler) DeleteTag(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	tag, err := h.tagMgr.GetTag(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	if tag.Scope != "" && tag.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.tagMgr.DeleteTag(c.Request.Context(), id); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// TagMemory 给记忆打标签 / Tag a memory
// POST /v1/memories/:id/tags
func (h *TagHandler) TagMemory(c *gin.Context, identity *model.Identity) {
	memoryID := c.Param("id")

	// 授权检查：验证调用者拥有该记忆 / Authorization: verify caller owns the memory
	mem, err := h.tagMgr.GetVisible(c.Request.Context(), memoryID, identity)
	if err != nil {
		Error(c, err)
		return
	}
	if mem.OwnerID != "" && mem.OwnerID != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	var body struct {
		TagID string `json:"tag_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if err := h.tagMgr.TagMemory(c.Request.Context(), memoryID, body.TagID); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// UntagMemory 移除记忆标签 / Remove tag from memory
// DELETE /v1/memories/:id/tags/:tag_id
func (h *TagHandler) UntagMemory(c *gin.Context, identity *model.Identity) {
	memoryID := c.Param("id")

	// 授权检查：验证调用者拥有该记忆 / Authorization: verify caller owns the memory
	mem, err := h.tagMgr.GetVisible(c.Request.Context(), memoryID, identity)
	if err != nil {
		Error(c, err)
		return
	}
	if mem.OwnerID != "" && mem.OwnerID != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	tagID := c.Param("tag_id")
	if err := h.tagMgr.UntagMemory(c.Request.Context(), memoryID, tagID); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// GetMemoryTags 获取记忆标签 / Get memory tags
// GET /v1/memories/:id/tags
func (h *TagHandler) GetMemoryTags(c *gin.Context, identity *model.Identity) {
	memoryID := c.Param("id")

	// 授权检查：验证调用者可见该记忆 / Authorization: verify memory is visible to caller
	mem, err := h.tagMgr.GetVisible(c.Request.Context(), memoryID, identity)
	if err != nil {
		Error(c, err)
		return
	}
	if mem.OwnerID != "" && mem.OwnerID != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	tags, err := h.tagMgr.GetMemoryTags(c.Request.Context(), memoryID)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, tags)
}
