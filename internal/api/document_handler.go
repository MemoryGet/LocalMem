package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// DocumentHandler 文档处理器 / Document handler
type DocumentHandler struct {
	processor *document.Processor
	fileStore document.FileStore
	docCfg    config.DocumentConfig
}

// NewDocumentHandler 创建文档处理器 / Create document handler
func NewDocumentHandler(processor *document.Processor, fileStore document.FileStore, docCfg config.DocumentConfig) *DocumentHandler {
	return &DocumentHandler{processor: processor, fileStore: fileStore, docCfg: docCfg}
}

// Upload 文件上传 / POST /v1/documents/upload (multipart/form-data)
func (h *DocumentHandler) Upload(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		Error(c, fmt.Errorf("file is required: %w", model.ErrInvalidInput))
		return
	}
	defer file.Close()

	// 校验文件大小
	if header.Size > h.docCfg.MaxFileSize {
		Error(c, model.ErrFileTooLarge)
		return
	}

	// 校验文件类型
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(header.Filename)), ".")
	if !h.isAllowedType(ext) {
		Error(c, model.ErrUnsupportedFileType)
		return
	}

	// Magic bytes 内容类型检测 / Content-based MIME type detection
	magicBuf := make([]byte, 512)
	n, _ := io.ReadAtLeast(file, magicBuf, 1)
	if n > 0 {
		detectedMIME := http.DetectContentType(magicBuf[:n])
		if !isAllowedMIME(detectedMIME) {
			Error(c, model.ErrUnsupportedFileType)
			return
		}
	}
	// 重置文件读取位置 / Reset file read position
	if seeker, ok := file.(io.Seeker); ok {
		seeker.Seek(0, io.SeekStart)
	}

	// 文档名
	name := c.PostForm("name")
	if name == "" {
		name = header.Filename
	}
	scope := identity.OwnerID
	contextID := c.PostForm("context_id")

	// 解析 metadata
	var metadata map[string]any
	if metaStr := c.PostForm("metadata"); metaStr != "" {
		if err := json.Unmarshal([]byte(metaStr), &metadata); err != nil {
			Error(c, fmt.Errorf("invalid metadata JSON: %w", model.ErrInvalidInput))
			return
		}
	}

	// 创建文档记录
	doc, err := h.processor.Upload(c.Request.Context(), name, ext, scope, contextID, header.Size, metadata)
	if err != nil {
		Error(c, err)
		return
	}

	// 计算 SHA-256 做去重
	hasher := sha256.New()
	teeReader := io.TeeReader(file, hasher)

	// 保存文件
	if h.fileStore == nil {
		Error(c, fmt.Errorf("file store not configured: %w", model.ErrStorageUnavailable))
		return
	}
	filePath, err := h.fileStore.Save(c.Request.Context(), doc.ID, header.Filename, teeReader)
	if err != nil {
		Error(c, fmt.Errorf("failed to save file: %w", err))
		return
	}

	contentHash := fmt.Sprintf("%x", hasher.Sum(nil))

	// 文件级去重检查
	existing, dupErr := h.processor.GetDocumentByHash(c.Request.Context(), contentHash, scope)
	if dupErr == nil && existing != nil {
		_ = h.fileStore.Delete(c.Request.Context(), filepath.Dir(filePath))
		Success(c, existing)
		return
	}

	// 更新文件路径和哈希
	doc.FilePath = filePath
	doc.ContentHash = contentHash
	h.processor.UpdateDocFilePath(c.Request.Context(), doc)

	// 异步处理
	h.processor.ProcessAsync(doc.ID)

	Created(c, doc)
}

// Status 获取处理状态 / GET /v1/documents/:id/status
func (h *DocumentHandler) Status(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	doc, err := h.processor.GetDocument(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	if doc.Scope != "" && doc.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

	Success(c, gin.H{
		"id":          doc.ID,
		"status":      doc.Status,
		"stage":       doc.Stage,
		"parser":      doc.Parser,
		"chunk_count": doc.ChunkCount,
		"error_msg":   doc.ErrorMsg,
	})
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

	scope := identity.OwnerID
	if identity.IsSystem() {
		scope = c.Query("scope")
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 200 {
		limit = 200
	}
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

// Process 手动纯文本处理 / POST /v1/documents/:id/reprocess
func (h *DocumentHandler) Process(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	// 授权检查：先获取文档验证归属 / Authorization: fetch doc first, verify ownership
	doc, err := h.processor.GetDocument(c.Request.Context(), c.Param("id"))
	if err != nil {
		Error(c, err)
		return
	}
	if doc.Scope != "" && doc.Scope != identity.OwnerID && !identity.IsSystem() {
		Error(c, model.ErrForbidden)
		return
	}

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

// isAllowedType 检查文件类型白名单 / Check file type allowlist
func (h *DocumentHandler) isAllowedType(ext string) bool {
	for _, allowed := range h.docCfg.AllowedTypes {
		if strings.EqualFold(ext, allowed) {
			return true
		}
	}
	return false
}

// isAllowedMIME 检查 MIME 类型白名单 / Check MIME type allowlist
func isAllowedMIME(mime string) bool {
	allowed := []string{
		"application/pdf",
		"application/zip",          // docx/pptx/xlsx are ZIP-based
		"application/x-gzip",
		"text/plain",
		"text/html",
		"text/xml",
		"image/png",
		"image/jpeg",
		"image/gif",
		"application/octet-stream", // fallback for unrecognized binary
	}
	for _, a := range allowed {
		if strings.HasPrefix(mime, a) {
			return true
		}
	}
	return false
}
