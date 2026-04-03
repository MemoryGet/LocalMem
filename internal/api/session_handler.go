// session_handler.go 多会话协同处理器 / Multi-session collaboration handler (B6 + B7)
package api

import (
	"strconv"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// SessionHandler 多会话协同处理器 / Session collaboration handler
type SessionHandler struct {
	manager       *memory.Manager
	summarizer    *memory.SessionSummarizer // B7: 可为 nil / may be nil
	lineageTracer *memory.LineageTracer     // B7: 可为 nil / may be nil
}

// NewSessionHandler 创建会话处理器 / Create session handler
func NewSessionHandler(manager *memory.Manager, summarizer *memory.SessionSummarizer, lineageTracer *memory.LineageTracer) *SessionHandler {
	return &SessionHandler{manager: manager, summarizer: summarizer, lineageTracer: lineageTracer}
}

// ListBySourceRef 按来源引用列出记忆 / List memories by source_ref
// GET /v1/memories/by-source/:sourceRef
func (h *SessionHandler) ListBySourceRef(c *gin.Context, identity *model.Identity) {
	sourceRef := c.Param("sourceRef")
	if sourceRef == "" {
		Error(c, model.ErrInvalidInput)
		return
	}

	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	memories, err := h.manager.ListBySourceRef(c.Request.Context(), sourceRef, identity, offset, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, memories)
}

// SoftDeleteBySourceRef 按来源引用批量软删除 / Soft delete all memories with source_ref
// DELETE /v1/memories/by-source/:sourceRef
func (h *SessionHandler) SoftDeleteBySourceRef(c *gin.Context, identity *model.Identity) {
	sourceRef := c.Param("sourceRef")
	if sourceRef == "" {
		Error(c, model.ErrInvalidInput)
		return
	}

	count, err := h.manager.SoftDeleteBySourceRef(c.Request.Context(), sourceRef, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, gin.H{"deleted": count})
}

// RestoreBySourceRef 按来源引用批量恢复 / Restore all soft-deleted memories with source_ref
// POST /v1/memories/by-source/:sourceRef/restore
func (h *SessionHandler) RestoreBySourceRef(c *gin.Context, identity *model.Identity) {
	sourceRef := c.Param("sourceRef")
	if sourceRef == "" {
		Error(c, model.ErrInvalidInput)
		return
	}

	count, err := h.manager.RestoreBySourceRef(c.Request.Context(), sourceRef, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, gin.H{"restored": count})
}

// ListDerivedFrom 查询由指定记忆衍生出的记忆 / List memories derived from a given memory
// GET /v1/memories/:id/derived-from
func (h *SessionHandler) ListDerivedFrom(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	memories, err := h.manager.ListDerivedFrom(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, memories)
}

// ListConsolidatedInto 查询被归纳到指定记忆的原始记忆 / List source memories consolidated into a given memory
// GET /v1/memories/:id/consolidated-into
func (h *SessionHandler) ListConsolidatedInto(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	memories, err := h.manager.ListConsolidatedInto(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, memories)
}

// Summarize 总结会话记忆（按 context_id）/ Summarize session memories by context ID
// POST /v1/sessions/:contextId/summarize
func (h *SessionHandler) Summarize(c *gin.Context, identity *model.Identity) {
	if h.summarizer == nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	contextID := c.Param("contextId")
	if contextID == "" {
		Error(c, model.ErrInvalidInput)
		return
	}

	resp, err := h.summarizer.Summarize(c.Request.Context(), contextID, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, resp)
}

// SummarizeBySourceRef 总结会话记忆（按 source_ref）/ Summarize session memories by source_ref
// POST /v1/sessions/by-source/:sourceRef/summarize
func (h *SessionHandler) SummarizeBySourceRef(c *gin.Context, identity *model.Identity) {
	if h.summarizer == nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	sourceRef := c.Param("sourceRef")
	if sourceRef == "" {
		Error(c, model.ErrInvalidInput)
		return
	}

	resp, err := h.summarizer.SummarizeBySourceRef(c.Request.Context(), sourceRef, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, resp)
}

// Lineage 溯源查询 — 展示完整演化链 / Lineage query — show complete evolution chain
// GET /v1/memories/:id/lineage
func (h *SessionHandler) Lineage(c *gin.Context, identity *model.Identity) {
	if h.lineageTracer == nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	id := c.Param("id")
	resp, err := h.lineageTracer.Trace(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, resp)
}
