package api

import (
	"fmt"
	"strconv"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// ConversationHandler 对话导入处理器 / Conversation ingest handler
type ConversationHandler struct {
	manager *memory.Manager
}

// NewConversationHandler 创建对话处理器 / Create conversation handler
func NewConversationHandler(manager *memory.Manager) *ConversationHandler {
	return &ConversationHandler{manager: manager}
}

// Ingest 批量导入对话 / Ingest a conversation as multiple memories
// POST /v1/conversations
func (h *ConversationHandler) Ingest(c *gin.Context, identity *model.Identity) {
	var req model.IngestConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}

	if len(req.Messages) > 500 {
		Error(c, fmt.Errorf("too many messages: max 500, got %d: %w", len(req.Messages), model.ErrInvalidInput))
		return
	}

	contextID, memories, err := h.manager.IngestConversation(c.Request.Context(), &req, identity)
	if err != nil {
		Error(c, err)
		return
	}

	Created(c, gin.H{
		"context_id": contextID,
		"count":      len(memories),
		"memories":   memories,
	})
}

// GetConversation 按轮次顺序获取对话 / Get conversation memories ordered by turn
// GET /v1/conversations/:context_id
func (h *ConversationHandler) GetConversation(c *gin.Context, identity *model.Identity) {
	contextID := c.Param("context_id")
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit > 200 {
		limit = 200
	}

	memories, err := h.manager.GetConversation(c.Request.Context(), contextID, identity, offset, limit)
	if err != nil {
		Error(c, err)
		return
	}

	Success(c, gin.H{
		"context_id": contextID,
		"count":      len(memories),
		"memories":   memories,
	})
}
