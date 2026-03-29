// Package store 存储接口与实现 / Storage interfaces and implementations
package store

import (
	"context"
	"time"

	"iclude/internal/model"
)

// MemoryReader 记忆读取接口 / Memory read operations
type MemoryReader interface {
	// Get 获取单条记忆 / Get a memory by ID
	Get(ctx context.Context, id string) (*model.Memory, error)

	// GetVisible 带可见性校验获取记忆 / Get memory with visibility check
	GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error)

	// List 分页列表（带可见性过滤）/ List memories with pagination and visibility filtering
	List(ctx context.Context, identity *model.Identity, offset, limit int) ([]*model.Memory, error)

	// ListByContext 按上下文列出记忆（带可见性过滤）/ List memories by context ID with visibility filtering
	ListByContext(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error)

	// ListByContextOrdered 按轮次顺序列出上下文记忆（带可见性过滤）/ List memories by context ordered by turn number with visibility filtering
	ListByContextOrdered(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error)

	// GetByURI 通过 URI 获取记忆 / Get memory by URI
	GetByURI(ctx context.Context, uri string) (*model.Memory, error)

	// GetByContentHash 通过内容哈希获取记忆 / Get memory by content hash
	// 未找到时返回 (nil, model.ErrMemoryNotFound)
	GetByContentHash(ctx context.Context, contentHash string) (*model.Memory, error)

	// ListTimeline 时间线查询 / List memories by timeline
	ListTimeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error)

	// GetOwnerID 获取记忆的 owner_id（含 soft-deleted）/ Get owner_id including soft-deleted memories
	GetOwnerID(ctx context.Context, id string) (string, error)

	// ListMissingAbstract 列出缺少摘要的记忆（排除软删除）/ List memories missing abstract (excluding soft-deleted)
	ListMissingAbstract(ctx context.Context, limit int) ([]*model.Memory, error)
}

// MemoryWriter 记忆写入接口 / Memory write operations
type MemoryWriter interface {
	// Create 创建记忆 / Create a memory
	Create(ctx context.Context, mem *model.Memory) error

	// Update 更新记忆 / Update a memory
	Update(ctx context.Context, mem *model.Memory) error

	// Delete 删除记忆（硬删除）/ Delete a memory by ID (hard delete)
	Delete(ctx context.Context, id string) error

	// CreateBatch 批量创建记忆（单事务）/ Batch create memories in a single transaction
	CreateBatch(ctx context.Context, memories []*model.Memory) error

	// Reinforce 强化记忆 / Reinforce a memory (increase strength)
	Reinforce(ctx context.Context, id string) error

	// IncrementAccessCount 批量递增访问计数 / Increment access count by delta
	IncrementAccessCount(ctx context.Context, id string, delta int) error
}

// MemorySearch 记忆搜索接口 / Memory search operations
type MemorySearch interface {
	// SearchText 全文检索（带可见性过滤）/ Full-text search with visibility filtering
	SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error)

	// SearchTextFiltered 带过滤条件的全文检索 / Full-text search with filters
	SearchTextFiltered(ctx context.Context, query string, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error)
}

// MemoryLifecycle 记忆生命周期接口 / Memory lifecycle operations
type MemoryLifecycle interface {
	// SoftDelete 软删除记忆 / Soft delete a memory
	SoftDelete(ctx context.Context, id string) error

	// Restore 恢复软删除的记忆 / Restore a soft-deleted memory
	Restore(ctx context.Context, id string) error

	// CleanupExpired 软删除已过期记忆 / Soft delete expired memories
	CleanupExpired(ctx context.Context) (int, error)

	// PurgeDeleted 硬删除旧的软删除记录 / Hard delete old soft-deleted memories
	PurgeDeleted(ctx context.Context, olderThan time.Duration) (int, error)

	// ListExpired 列出已过期记忆 / List expired memories
	ListExpired(ctx context.Context, limit int) ([]*model.Memory, error)

	// ListWeak 列出弱记忆 / List weak memories below threshold
	ListWeak(ctx context.Context, threshold float64, limit int) ([]*model.Memory, error)

	// SoftDeleteByDocumentID 软删除关联文档的所有记忆 / Soft delete all memories linked to a document
	SoftDeleteByDocumentID(ctx context.Context, documentID string) (int, error)
}

// MemoryStore 完整记忆存储接口（组合子接口）/ Complete memory store interface (composite)
type MemoryStore interface {
	MemoryReader
	MemoryWriter
	MemorySearch
	MemoryLifecycle

	// Init 初始化存储（建表等）/ Initialize storage (create tables etc.)
	Init(ctx context.Context) error

	// Close 关闭存储连接 / Close storage connection
	Close() error

	// DB 获取底层数据库连接（供其他 store 共用）/ Get underlying db connection for sharing
	DB() interface{}
}

// VectorStore 向量存储接口 / Vector storage interface (Qdrant)
type VectorStore interface {
	// Init 初始化向量存储（创建集合等）/ Initialize vector storage
	Init(ctx context.Context) error

	// Close 关闭连接 / Close connection
	Close() error

	// Upsert 插入或更新向量 / Insert or update vector
	Upsert(ctx context.Context, memoryID string, embedding []float32, payload map[string]any) error

	// Delete 删除向量 / Delete vector by memory ID
	Delete(ctx context.Context, memoryID string) error

	// Search 向量检索（带可见性过滤）/ Vector similarity search with visibility filtering
	Search(ctx context.Context, embedding []float32, identity *model.Identity, limit int) ([]*model.SearchResult, error)

	// SearchFiltered 带过滤条件的向量检索 / Vector search with filters
	SearchFiltered(ctx context.Context, embedding []float32, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error)

	// GetVectors 批量获取向量 / Batch retrieve vectors by memory IDs
	GetVectors(ctx context.Context, ids []string) (map[string][]float32, error)
}

// Embedder 向量嵌入接口 / Embedding interface
type Embedder interface {
	// Embed 单条文本向量化 / Embed a single text
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch 批量文本向量化 / Embed multiple texts
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// ContextStore 上下文存储接口 / Context storage interface
type ContextStore interface {
	// Create 创建上下文 / Create a context
	Create(ctx context.Context, c *model.Context) error

	// Get 获取上下文 / Get context by ID
	Get(ctx context.Context, id string) (*model.Context, error)

	// GetByPath 通过路径获取上下文 / Get context by path
	GetByPath(ctx context.Context, path string) (*model.Context, error)

	// Update 更新上下文 / Update a context
	Update(ctx context.Context, c *model.Context) error

	// Delete 删除上下文 / Delete a context
	Delete(ctx context.Context, id string) error

	// ListChildren 列出子上下文 / List child contexts
	ListChildren(ctx context.Context, parentID string) ([]*model.Context, error)

	// ListSubtree 列出子树 / List entire subtree under a context
	ListSubtree(ctx context.Context, path string) ([]*model.Context, error)

	// Move 移动上下文 / Move context to new parent
	Move(ctx context.Context, id string, newParentID string) error

	// IncrementMemoryCount 递增记忆计数 / Increment memory count by 1
	IncrementMemoryCount(ctx context.Context, id string) error

	// IncrementMemoryCountBy 递增记忆计数（指定增量）/ Increment memory count by delta
	IncrementMemoryCountBy(ctx context.Context, id string, delta int) error

	// DecrementMemoryCount 递减记忆计数 / Decrement memory count
	DecrementMemoryCount(ctx context.Context, id string) error
}

// TagStore 标签存储接口 / Tag storage interface
type TagStore interface {
	// CreateTag 创建标签 / Create a tag
	CreateTag(ctx context.Context, tag *model.Tag) error

	// GetTag 获取标签 / Get tag by ID
	GetTag(ctx context.Context, id string) (*model.Tag, error)

	// ListTags 列出标签 / List all tags with optional scope filter
	ListTags(ctx context.Context, scope string) ([]*model.Tag, error)

	// DeleteTag 删除标签 / Delete a tag
	DeleteTag(ctx context.Context, id string) error

	// TagMemory 给记忆打标签 / Associate a tag with a memory
	TagMemory(ctx context.Context, memoryID, tagID string) error

	// UntagMemory 移除记忆标签 / Remove tag from memory
	UntagMemory(ctx context.Context, memoryID, tagID string) error

	// GetMemoryTags 获取记忆的所有标签 / Get all tags for a memory
	GetMemoryTags(ctx context.Context, memoryID string) ([]*model.Tag, error)

	// GetMemoriesByTag 获取标签下的所有记忆 / Get all memories with a specific tag
	GetMemoriesByTag(ctx context.Context, tagID string, limit int) ([]*model.Memory, error)

	// GetTagNamesByMemoryIDs 批量获取多条记忆的标签名 / Batch get tag names for multiple memories
	GetTagNamesByMemoryIDs(ctx context.Context, ids []string) (map[string][]string, error)
}

// GraphStore 知识图谱存储接口 / Knowledge graph storage interface
type GraphStore interface {
	// CreateEntity 创建实体 / Create an entity
	CreateEntity(ctx context.Context, entity *model.Entity) error

	// GetEntity 获取实体 / Get entity by ID
	GetEntity(ctx context.Context, id string) (*model.Entity, error)

	// ListEntities 列出实体 / List entities with optional filters
	ListEntities(ctx context.Context, scope, entityType string, limit int) ([]*model.Entity, error)

	// UpdateEntity 更新实体 / Update an entity
	UpdateEntity(ctx context.Context, entity *model.Entity) error

	// DeleteEntity 删除实体 / Delete an entity
	DeleteEntity(ctx context.Context, id string) error

	// CreateRelation 创建关系 / Create an entity relation
	CreateRelation(ctx context.Context, rel *model.EntityRelation) error

	// DeleteRelation 删除关系 / Delete an entity relation
	DeleteRelation(ctx context.Context, id string) error

	// GetEntityRelations 获取实体关系 / Get relations for an entity
	GetEntityRelations(ctx context.Context, entityID string) ([]*model.EntityRelation, error)

	// CreateMemoryEntity 创建记忆-实体关联 / Create memory-entity association
	CreateMemoryEntity(ctx context.Context, me *model.MemoryEntity) error

	// DeleteMemoryEntity 删除记忆-实体关联 / Delete memory-entity association
	DeleteMemoryEntity(ctx context.Context, memoryID, entityID string) error

	// GetEntityMemories 获取实体关联的记忆 / Get memories associated with an entity
	GetEntityMemories(ctx context.Context, entityID string, limit int) ([]*model.Memory, error)

	// GetMemoryEntities 获取记忆关联的实体 / Get entities associated with a memory
	GetMemoryEntities(ctx context.Context, memoryID string) ([]*model.Entity, error)

	// FindEntitiesByName 按名称模糊匹配实体（索引查询，替代 ListEntities 全量扫描）/ Find entities by name (indexed query)
	FindEntitiesByName(ctx context.Context, name string, scope string, limit int) ([]*model.Entity, error)
}

// DocumentStore 文档存储接口 / Document storage interface
type DocumentStore interface {
	// Create 创建文档 / Create a document
	Create(ctx context.Context, doc *model.Document) error

	// Get 获取文档 / Get document by ID
	Get(ctx context.Context, id string) (*model.Document, error)

	// List 列出文档 / List documents
	List(ctx context.Context, scope string, offset, limit int) ([]*model.Document, error)

	// Update 更新文档 / Update a document
	Update(ctx context.Context, doc *model.Document) error

	// Delete 删除文档 / Delete a document
	Delete(ctx context.Context, id string) error

	// GetByHash 通过内容哈希获取文档 / Get document by content hash
	GetByHash(ctx context.Context, contentHash string) (*model.Document, error)

	// ListByStatus 按状态列出文档 / List documents by status
	ListByStatus(ctx context.Context, statuses []string, limit int) ([]*model.Document, error)

	// UpdateStatus 更新文档状态 / Update document status
	UpdateStatus(ctx context.Context, id string, status string) error

	// UpdateErrorMsg 更新文档错误信息 / Update document error message
	UpdateErrorMsg(ctx context.Context, id string, msg string) error
}
