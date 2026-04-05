package api

import (
	"fmt"
	"net/http"

	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/gin-gonic/gin"
)

// ScopePolicyHandler scope 策略管理 handler / Scope policy management handler
type ScopePolicyHandler struct {
	store store.ScopePolicyStore
}

// NewScopePolicyHandler 创建 handler / Create handler
func NewScopePolicyHandler(s store.ScopePolicyStore) *ScopePolicyHandler {
	return &ScopePolicyHandler{store: s}
}

// List GET /v1/scope-policies
func (h *ScopePolicyHandler) List(c *gin.Context, identity *model.Identity) {
	policies, err := h.store.List(c.Request.Context(), identity.TeamID)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, policies)
}

// Create POST /v1/scope-policies
func (h *ScopePolicyHandler) Create(c *gin.Context, identity *model.Identity) {
	var req model.ScopePolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Scope == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope is required"})
		return
	}
	req.TeamID = identity.TeamID
	req.CreatedBy = identity.OwnerID

	if err := h.store.Create(c.Request.Context(), &req); err != nil {
		Error(c, err)
		return
	}
	Created(c, req)
}

// Get GET /v1/scope-policies/:scope
func (h *ScopePolicyHandler) Get(c *gin.Context, identity *model.Identity) {
	scope := c.Param("scope")
	policy, err := h.store.GetByScope(c.Request.Context(), scope)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, policy)
}

// Update PUT /v1/scope-policies/:scope
func (h *ScopePolicyHandler) Update(c *gin.Context, identity *model.Identity) {
	scope := c.Param("scope")
	existing, err := h.store.GetByScope(c.Request.Context(), scope)
	if err != nil {
		Error(c, err)
		return
	}

	// 鉴权：仅 created_by 可修改 / Auth: only created_by can update
	if existing.CreatedBy != identity.OwnerID {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("only the policy creator (%s) can update", existing.CreatedBy)})
		return
	}

	var req model.ScopePolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Scope = scope
	if err := h.store.Update(c.Request.Context(), &req); err != nil {
		Error(c, err)
		return
	}
	Success(c, req)
}

// Delete DELETE /v1/scope-policies/:scope
func (h *ScopePolicyHandler) Delete(c *gin.Context, identity *model.Identity) {
	scope := c.Param("scope")
	existing, err := h.store.GetByScope(c.Request.Context(), scope)
	if err != nil {
		Error(c, err)
		return
	}

	// 鉴权 / Auth
	if existing.CreatedBy != identity.OwnerID {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("only the policy creator (%s) can delete", existing.CreatedBy)})
		return
	}

	if err := h.store.Delete(c.Request.Context(), scope); err != nil {
		Error(c, err)
		return
	}
	Success(c, gin.H{"deleted": scope})
}
