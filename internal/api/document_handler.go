package api

import (
	"strconv"

	"iclude/internal/document"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// DocumentHandler 文档处理器 / Document handler
type DocumentHandler struct {
	processor *document.Processor
}

// NewDocumentHandler 创建文档处理器 / Create document handler
func NewDocumentHandler(processor *document.Processor) *DocumentHandler {
	return &DocumentHandler{processor: processor}
}

// Upload 上传文档 / POST /v1/documents
func (h *DocumentHandler) Upload(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req model.CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	doc, err := h.processor.Upload(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Created(c, doc)
}

// Get 获取文档 / GET /v1/documents/:id
func (h *DocumentHandler) Get(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	doc, err := h.processor.GetDocument(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	// 授权检查：scope 不匹配则拒绝 / Authorization: reject if scope does not match owner
	if doc.Scope != "" && doc.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}
	Success(c, doc)
}

// List 列出文档 / GET /v1/documents?scope=x&offset=0&limit=20
func (h *DocumentHandler) List(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	// 强制以当前用户 OwnerID 作为 scope 过滤，防止跨用户数据泄露 / Force scope to caller's OwnerID to prevent cross-user data leakage
	scope := identity.OwnerID
	if identity.IsSystem() {
		// 系统身份允许使用请求中指定的 scope / System identity may use the requested scope
		scope = c.Query("scope")
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	docs, err := h.processor.ListDocuments(c.Request.Context(), scope, offset, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, docs)
}

// Delete 删除文档 / DELETE /v1/documents/:id
func (h *DocumentHandler) Delete(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	// 授权检查：先获取文档，验证 scope 归属 / Authorization: fetch doc first, verify scope ownership
	doc, err := h.processor.GetDocument(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	if doc.Scope != "" && doc.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	if err := h.processor.DeleteDocument(c.Request.Context(), c.Param("id")); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}

// Process 处理文档 / POST /v1/documents/:id/reprocess
func (h *DocumentHandler) Process(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}
	_ = identity // 身份已验证 / Identity verified

	var body struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if err := h.processor.Process(c.Request.Context(), c.Param("id"), body.Content); err != nil {
		Error(c, err)
		return
	}
	Success(c, nil)
}
