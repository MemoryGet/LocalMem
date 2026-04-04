package model

import "errors"

var (
	// ErrMemoryNotFound 记忆不存在 / Memory not found
	ErrMemoryNotFound = errors.New("memory not found")

	// ErrInvalidInput 无效输入 / Invalid input
	ErrInvalidInput = errors.New("invalid input")

	// ErrStorageUnavailable 存储后端不可用 / Storage backend unavailable
	ErrStorageUnavailable = errors.New("storage backend unavailable")

	// ErrEmbeddingFailed 向量生成失败 / Embedding generation failed
	ErrEmbeddingFailed = errors.New("embedding generation failed")

	// ErrConflict 资源冲突 / Resource conflict
	ErrConflict = errors.New("resource conflict")

	// ErrContextNotFound 上下文不存在 / Context not found
	ErrContextNotFound = errors.New("context not found")

	// ErrEntityNotFound 实体不存在 / Entity not found
	ErrEntityNotFound = errors.New("entity not found")

	// ErrRelationNotFound 关系不存在 / Relation not found
	ErrRelationNotFound = errors.New("relation not found")

	// ErrTagNotFound 标签不存在 / Tag not found
	ErrTagNotFound = errors.New("tag not found")

	// ErrDocumentNotFound 文档不存在 / Document not found
	ErrDocumentNotFound = errors.New("document not found")

	// ErrPathConflict 路径冲突 / Path conflict
	ErrPathConflict = errors.New("path conflict")

	// ErrCircularReference 循环引用 / Circular reference detected
	ErrCircularReference = errors.New("circular reference")

	// ErrDuplicateDocument 重复文档 / Duplicate document
	ErrDuplicateDocument = errors.New("duplicate document")

	// ErrInvalidRetentionTier 无效的知识保留等级 / Invalid retention tier
	ErrInvalidRetentionTier = errors.New("invalid retention tier")

	// ErrReflectTimeout 反思推理超时 / Reflect reasoning timeout
	ErrReflectTimeout = errors.New("reflect: timeout exceeded")

	// ErrReflectTokenBudgetExceeded 反思推理token预算超出 / Reflect token budget exceeded
	ErrReflectTokenBudgetExceeded = errors.New("reflect: token budget exceeded")

	// ErrReflectNoMemories 反思检索无结果 / No memories found for reflection
	ErrReflectNoMemories = errors.New("reflect: no relevant memories found")

	// ErrReflectLLMFailed LLM调用失败 / LLM call failed during reflection
	ErrReflectLLMFailed = errors.New("reflect: llm call failed")

	// ErrReflectInvalidRequest 反思请求参数无效 / Invalid reflect request
	ErrReflectInvalidRequest = errors.New("reflect: invalid request")

	// ErrExtractTimeout 实体抽取超时 / Entity extraction timeout
	ErrExtractTimeout = errors.New("extract: timeout exceeded")

	// ErrExtractLLMFailed 实体抽取LLM调用失败 / LLM call failed during extraction
	ErrExtractLLMFailed = errors.New("extract: llm call failed")

	// ErrExtractParseFailed 实体抽取输出解析失败 / Extraction output parse failed
	ErrExtractParseFailed = errors.New("extract: output parse failed")

	// ErrDuplicateMemory 重复记忆内容 / Duplicate memory content
	ErrDuplicateMemory = errors.New("duplicate memory")

	// ErrUnauthorized 认证失败 / Authentication required
	ErrUnauthorized = errors.New("authentication required")

	// ErrForbidden 无权访问 / Access denied
	ErrForbidden = errors.New("access denied")

	// ErrFileTooLarge 文件过大 / File too large
	ErrFileTooLarge = errors.New("file too large")

	// ErrUnsupportedFileType 不支持的文件类型 / Unsupported file type
	ErrUnsupportedFileType = errors.New("unsupported file type")

	// ErrParseFailure 文档解析失败 / Document parse failure
	ErrParseFailure = errors.New("document parse failure")

	// ErrSessionNotFound 会话不存在 / Session not found
	ErrSessionNotFound = errors.New("session not found")

	// ErrFinalizeStateNotFound 终态记录不存在 / Finalize state not found
	ErrFinalizeStateNotFound = errors.New("finalize state not found")

	// ErrCursorNotFound 游标不存在 / Cursor not found
	ErrCursorNotFound = errors.New("cursor not found")

	// ErrIdempotencyNotFound 幂等记录不存在 / Idempotency record not found
	ErrIdempotencyNotFound = errors.New("idempotency record not found")
)
