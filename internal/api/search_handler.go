package api

import (
	"strconv"
	"time"

	"iclude/internal/model"
	"iclude/internal/search"

	"github.com/gin-gonic/gin"
)

// SearchHandler 检索处理器 / Search handler
type SearchHandler struct {
	retriever *search.Retriever
}

// NewSearchHandler 创建检索处理器 / Create search handler
func NewSearchHandler(retriever *search.Retriever) *SearchHandler {
	return &SearchHandler{retriever: retriever}
}

// Retrieve 执行检索 / Execute retrieval
// POST /v1/retrieve
func (h *SearchHandler) Retrieve(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.RetrieveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}

	// 强制覆盖身份字段 / Force override identity from middleware
	req.TeamID = identity.TeamID
	if req.Filters == nil {
		req.Filters = &model.SearchFilters{}
	}
	req.Filters.TeamID = identity.TeamID
	req.Filters.OwnerID = identity.OwnerID

	results, err := h.retriever.Retrieve(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}

	// Token 裁剪（handler 层，不影响 Retrieve 签名）/ Token trimming at handler level
	resp := model.RetrieveResponse{Results: results}
	if req.MaxTokens > 0 {
		resp.Results, resp.TotalTokens, resp.Truncated = search.TrimByTokenBudget(results, req.MaxTokens)
	}

	Success(c, resp)
}

// Timeline 时间线查询 / Timeline query
// GET /v1/timeline
func (h *SearchHandler) Timeline(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.TimelineRequest
	req.Scope = c.Query("scope")
	req.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "20"))

	// 注入身份信息 / Inject identity
	req.TeamID = identity.TeamID
	req.OwnerID = identity.OwnerID

	if after := c.Query("after"); after != "" {
		t, err := time.Parse(time.RFC3339, after)
		if err == nil {
			req.After = &t
		}
	}
	if before := c.Query("before"); before != "" {
		t, err := time.Parse(time.RFC3339, before)
		if err == nil {
			req.Before = &t
		}
	}

	results, err := h.retriever.Timeline(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, results)
}
