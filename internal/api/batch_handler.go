// Package api HTTP 请求处理层 / HTTP request handler layer
package api

import (
	"errors"
	"net/http"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// BatchHandler 批量记忆操作处理器 / Batch memory operations handler
type BatchHandler struct {
	manager *memory.Manager
}

// NewBatchHandler 创建批量处理器 / Create batch handler
func NewBatchHandler(manager *memory.Manager) *BatchHandler {
	return &BatchHandler{manager: manager}
}

// batchGetRequest 批量获取请求体 / Batch get request body
type batchGetRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

// BatchGet 批量获取记忆 / Batch get memories by IDs
// POST /v1/memories/batch
func (h *BatchHandler) BatchGet(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req batchGetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, APIResponse{
			Code:    400,
			Message: "ids must not be empty",
		})
		return
	}
	if len(req.IDs) > 20 {
		c.JSON(http.StatusBadRequest, APIResponse{
			Code:    400,
			Message: "maximum 20 ids per request",
		})
		return
	}

	memories := make([]*model.Memory, 0, len(req.IDs))
	for _, id := range req.IDs {
		mem, err := h.manager.GetVisible(c.Request.Context(), id, identity)
		if err != nil {
			if errors.Is(err, model.ErrMemoryNotFound) {
				continue
			}
			Error(c, err)
			return
		}
		memories = append(memories, mem)
	}

	Success(c, gin.H{"memories": memories})
}
