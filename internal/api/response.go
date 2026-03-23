// Package api HTTP 请求处理层 / HTTP request handler layer
package api

import (
	"errors"
	"net/http"

	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// APIResponse 统一响应格式 / Unified API response format
type APIResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Success 成功响应 / Success response
func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, APIResponse{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Created 创建成功响应 / Created response
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, APIResponse{
		Code:    0,
		Message: "created",
		Data:    data,
	})
}

// Error 错误响应 / Error response with automatic status code mapping
func Error(c *gin.Context, err error) {
	status, code, msg := mapError(err)
	c.JSON(status, APIResponse{
		Code:    code,
		Message: msg,
	})
}

// mapError 将业务错误映射为 HTTP 状态码
func mapError(err error) (httpStatus int, code int, message string) {
	switch {
	case errors.Is(err, model.ErrMemoryNotFound):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrInvalidInput):
		return http.StatusBadRequest, 400, err.Error()
	case errors.Is(err, model.ErrConflict):
		return http.StatusConflict, 409, err.Error()
	case errors.Is(err, model.ErrStorageUnavailable):
		return http.StatusServiceUnavailable, 503, err.Error()
	case errors.Is(err, model.ErrContextNotFound):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrEntityNotFound):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrRelationNotFound):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrTagNotFound):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrDocumentNotFound):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrPathConflict):
		return http.StatusConflict, 409, err.Error()
	case errors.Is(err, model.ErrCircularReference):
		return http.StatusBadRequest, 400, err.Error()
	case errors.Is(err, model.ErrDuplicateDocument):
		return http.StatusConflict, 409, err.Error()
	case errors.Is(err, model.ErrInvalidRetentionTier):
		return http.StatusBadRequest, 400, err.Error()
	case errors.Is(err, model.ErrReflectInvalidRequest):
		return http.StatusBadRequest, 400, err.Error()
	case errors.Is(err, model.ErrReflectNoMemories):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrReflectTimeout):
		return http.StatusRequestTimeout, 408, err.Error()
	case errors.Is(err, model.ErrReflectLLMFailed):
		return http.StatusBadGateway, 502, err.Error()
	case errors.Is(err, model.ErrExtractTimeout):
		return http.StatusRequestTimeout, 408, err.Error()
	case errors.Is(err, model.ErrExtractLLMFailed):
		return http.StatusBadGateway, 502, err.Error()
	case errors.Is(err, model.ErrExtractParseFailed):
		return http.StatusBadGateway, 502, err.Error()
	case errors.Is(err, model.ErrUnauthorized):
		return http.StatusUnauthorized, 401, err.Error()
	case errors.Is(err, model.ErrForbidden):
		return http.StatusForbidden, 403, err.Error()
	default:
		return http.StatusInternalServerError, 500, "internal server error"
	}
}
