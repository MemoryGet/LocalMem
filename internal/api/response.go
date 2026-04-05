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
		return http.StatusNotFound, 404, "memory not found"
	case errors.Is(err, model.ErrInvalidInput):
		return http.StatusBadRequest, 400, "invalid input"
	case errors.Is(err, model.ErrConflict):
		return http.StatusConflict, 409, "resource conflict"
	case errors.Is(err, model.ErrStorageUnavailable):
		return http.StatusServiceUnavailable, 503, "storage unavailable"
	case errors.Is(err, model.ErrContextNotFound):
		return http.StatusNotFound, 404, "context not found"
	case errors.Is(err, model.ErrEntityNotFound):
		return http.StatusNotFound, 404, "entity not found"
	case errors.Is(err, model.ErrRelationNotFound):
		return http.StatusNotFound, 404, "relation not found"
	case errors.Is(err, model.ErrTagNotFound):
		return http.StatusNotFound, 404, "tag not found"
	case errors.Is(err, model.ErrDocumentNotFound):
		return http.StatusNotFound, 404, "document not found"
	case errors.Is(err, model.ErrPathConflict):
		return http.StatusConflict, 409, "path conflict"
	case errors.Is(err, model.ErrCircularReference):
		return http.StatusBadRequest, 400, "circular reference detected"
	case errors.Is(err, model.ErrDuplicateDocument):
		return http.StatusConflict, 409, "duplicate document"
	case errors.Is(err, model.ErrInvalidRetentionTier):
		return http.StatusBadRequest, 400, "invalid retention tier"
	case errors.Is(err, model.ErrReflectInvalidRequest):
		return http.StatusBadRequest, 400, "invalid reflect request"
	case errors.Is(err, model.ErrReflectNoMemories):
		return http.StatusNotFound, 404, "no relevant memories found"
	case errors.Is(err, model.ErrReflectTimeout):
		return http.StatusRequestTimeout, 408, "reflect operation timed out"
	case errors.Is(err, model.ErrReflectLLMFailed):
		return http.StatusBadGateway, 502, "LLM service error"
	case errors.Is(err, model.ErrExtractTimeout):
		return http.StatusRequestTimeout, 408, "extraction timed out"
	case errors.Is(err, model.ErrExtractLLMFailed):
		return http.StatusBadGateway, 502, "extraction LLM error"
	case errors.Is(err, model.ErrExtractParseFailed):
		return http.StatusBadGateway, 502, "extraction parse error"
	case errors.Is(err, model.ErrUnauthorized):
		return http.StatusUnauthorized, 401, "unauthorized"
	case errors.Is(err, model.ErrForbidden):
		return http.StatusForbidden, 403, "forbidden"
	case errors.Is(err, model.ErrFileTooLarge):
		return http.StatusRequestEntityTooLarge, 413, "file too large"
	case errors.Is(err, model.ErrUnsupportedFileType):
		return http.StatusBadRequest, 400, "unsupported file type"
	default:
		return http.StatusInternalServerError, 500, "internal server error"
	}
}
