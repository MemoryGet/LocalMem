package api

import (
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"

	"github.com/gin-gonic/gin"
)

// ReflectHandler 反思推理处理器 / Reflect reasoning handler
type ReflectHandler struct {
	engine *reflectpkg.ReflectEngine
}

// NewReflectHandler 创建反思处理器 / Create reflect handler
func NewReflectHandler(engine *reflectpkg.ReflectEngine) *ReflectHandler {
	return &ReflectHandler{engine: engine}
}

// Reflect 处理反思推理请求 / Handle reflect reasoning request
// POST /v1/reflect
func (h *ReflectHandler) Reflect(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.ReflectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrReflectInvalidRequest)
		return
	}

	// 强制覆盖身份字段 / Force override identity from middleware
	req.TeamID = identity.TeamID

	resp, err := h.engine.Reflect(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, resp)
}
