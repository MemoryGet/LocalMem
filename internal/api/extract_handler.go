package api

import (
	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// ExtractHandler 实体抽取处理器 / Entity extraction handler
type ExtractHandler struct {
	extractor *memory.Extractor
}

// NewExtractHandler 创建实体抽取处理器 / Create extract handler
func NewExtractHandler(extractor *memory.Extractor) *ExtractHandler {
	return &ExtractHandler{extractor: extractor}
}

// Extract 对已有记忆触发实体抽取 / Trigger entity extraction for existing memory
// POST /v1/memories/:id/extract
func (h *ExtractHandler) Extract(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	id := c.Param("id")
	if id == "" {
		Error(c, model.ErrInvalidInput)
		return
	}

	// 获取记忆（带可见性校验）/ Fetch memory with visibility check
	mem, err := h.extractor.GetMemoryStore().GetVisible(c.Request.Context(), id, identity)
	if err != nil {
		Error(c, err)
		return
	}

	// 调用抽取 / Call extraction
	resp, err := h.extractor.Extract(c.Request.Context(), &model.ExtractRequest{
		MemoryID: mem.ID,
		Content:  mem.Content,
		Scope:    mem.Scope,
		TeamID:   mem.TeamID,
	})
	if err != nil {
		Error(c, err)
		return
	}

	Success(c, resp)
}
