package api

import (
	"iclude/internal/config"
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"

	"github.com/gin-gonic/gin"
)

// ReflectHandler 反思推理处理器 / Reflect reasoning handler
type ReflectHandler struct {
	engine     *reflectpkg.ReflectEngine
	reflectCfg config.ReflectConfig
}

// NewReflectHandler 创建反思处理器 / Create reflect handler
func NewReflectHandler(engine *reflectpkg.ReflectEngine, cfg config.ReflectConfig) *ReflectHandler {
	return &ReflectHandler{engine: engine, reflectCfg: cfg}
}

// Reflect 处理反思推理请求 / Handle reflect reasoning request
// POST /v1/reflect
func (h *ReflectHandler) Reflect(c *gin.Context, identity *model.Identity) {
	var req model.ReflectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrReflectInvalidRequest)
		return
	}

	// 强制覆盖身份字段 / Force override identity fields from middleware
	req.TeamID = identity.TeamID
	// 当调用者拥有实名身份时，将 scope 限定到其 OwnerID，防止跨用户读取
	// When the caller has a real identity, confine scope to their OwnerID to prevent cross-user reads
	if !identity.IsSystem() && identity.OwnerID != "" && identity.OwnerID != "anonymous" {
		req.Scope = identity.OwnerID
	}

	// 服务端上限校验，防止客户端绕过配置 / Enforce server-side max_rounds limit
	if req.MaxRounds <= 0 || req.MaxRounds > h.reflectCfg.MaxRounds {
		req.MaxRounds = h.reflectCfg.MaxRounds
	}

	resp, err := h.engine.Reflect(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, resp)
}
