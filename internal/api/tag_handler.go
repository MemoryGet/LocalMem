package api

import (
	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/gin-gonic/gin"
)

// TagHandler 标签处理器 / Tag handler
type TagHandler struct {
	tagStore store.TagStore
}

// NewTagHandler 创建标签处理器 / Create tag handler
func NewTagHandler(tagStore store.TagStore) *TagHandler {
	return &TagHandler{tagStore: tagStore}
}

// CreateTag 创建标签 / Create tag
// POST /v1/tags
func (h *TagHandler) CreateTag(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var tag model.Tag
	if err := c.ShouldBindJSON(&tag); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if !identity.IsSystem() {
		tag.Scope = identity.OwnerID
	}
	if err := h.tagStore.CreateTag(c.Request.Context(), &tag); err != nil {
		Error(c, err)
		return
	}
	Created(c, tag)
}

// ListTags 列出标签 / List tags
// GET /v1/tags?scope=xxx
func (h *TagHandler) ListTags(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	scope := c.Query("scope")
	if !identity.IsSystem() {
		scope = identity.OwnerID
	}
	tags, err := h.tagStore.ListTags(c.Request.Context(), scope)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, tags)
}

// DeleteTag 删除标签 / Delete tag
// DELETE /v1/tags/:id
func (h *TagHandler) DeleteTag(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")
	tag, err := h.tagStore.GetTag(c.Request.Context(), id)
	if err != nil {
		Error(c, err)
		return
	}
	if tag.Scope != "" && tag.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.tagStore.DeleteTag(c.Request.Context(), id); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// TagMemory 给记忆打标签 / Tag a memory
// POST /v1/memories/:id/tags
func (h *TagHandler) TagMemory(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	memoryID := c.Param("id")
	var body struct {
		TagID string `json:"tag_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if err := h.tagStore.TagMemory(c.Request.Context(), memoryID, body.TagID); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// UntagMemory 移除记忆标签 / Remove tag from memory
// DELETE /v1/memories/:id/tags/:tag_id
func (h *TagHandler) UntagMemory(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	memoryID := c.Param("id")
	tagID := c.Param("tag_id")
	if err := h.tagStore.UntagMemory(c.Request.Context(), memoryID, tagID); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// GetMemoryTags 获取记忆标签 / Get memory tags
// GET /v1/memories/:id/tags
func (h *TagHandler) GetMemoryTags(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	memoryID := c.Param("id")
	tags, err := h.tagStore.GetMemoryTags(c.Request.Context(), memoryID)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, tags)
}
