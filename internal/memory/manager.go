// Package memory 记忆管理业务逻辑 / Memory management business logic
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/model"
	"iclude/internal/store"
)

// TaskEnqueuer 任务入队接口（与 queue 包解耦）/ Task enqueue interface (decoupled from queue package)
type TaskEnqueuer interface {
	Enqueue(ctx context.Context, taskType string, payload json.RawMessage) (string, error)
}

// ManagerConfig 管理器配置（通过构造函数注入，替代全局单例）/ Manager config injected via constructor
type ManagerConfig struct {
	Dedup           config.DedupConfig
	Extract         config.ExtractConfig
	Crystallization config.CrystallizationConfig
	Ingest          config.IngestConfig
}

// ManagerDeps 管理器依赖 / Manager dependencies
type ManagerDeps struct {
	MemStore     store.MemoryStore
	VecStore     store.VectorStore  // 可为 nil / may be nil
	Embedder     store.Embedder     // 可为 nil / may be nil
	TagStore     store.TagStore     // 可为 nil / may be nil
	ContextStore store.ContextStore // 可为 nil / may be nil
	Extractor    *Extractor         // 可为 nil / may be nil
	LLMProvider  llm.Provider       // 可为 nil / may be nil
	Config       ManagerConfig
	TaskQueue    TaskEnqueuer // 可为 nil / may be nil
}

// Manager 记忆管理器，负责 CRUD 和双后端写入 / Memory manager handling CRUD with dual-backend writes
type Manager struct {
	memStore     store.MemoryStore
	vecStore     store.VectorStore  // 可为 nil / may be nil
	embedder     store.Embedder     // 可为 nil / may be nil
	tagStore     store.TagStore     // 可为 nil / may be nil
	contextStore store.ContextStore // 可为 nil / may be nil
	extractor    *Extractor         // 可为 nil / may be nil
	resolver     *EntityResolver    // 向量实体解析器 / Vector entity resolver (optional)
	llm          llm.Provider       // 可为 nil / may be nil (used for excerpt generation)
	taskQueue    TaskEnqueuer       // 可为 nil / may be nil
	cfg          ManagerConfig
}

// NewManager 创建记忆管理器 / Create a new memory manager
func NewManager(deps ManagerDeps) *Manager {
	return &Manager{
		memStore:     deps.MemStore,
		vecStore:     deps.VecStore,
		embedder:     deps.Embedder,
		tagStore:     deps.TagStore,
		contextStore: deps.ContextStore,
		extractor:    deps.Extractor,
		llm:          deps.LLMProvider,
		cfg:          deps.Config,
		taskQueue:    deps.TaskQueue,
	}
}

// SetQueue 设置任务队列（支持延迟注入）/ Set task queue (supports deferred injection)
func (m *Manager) SetQueue(q TaskEnqueuer) {
	m.taskQueue = q
}

// SetResolver 设置向量实体解析器 / Set vector entity resolver
func (m *Manager) SetResolver(r *EntityResolver) {
	m.resolver = r
}

// Create 创建记忆 / Create a new memory
// 若请求含 embedding 直接使用，否则由 Embedder 自动生成
func (m *Manager) Create(ctx context.Context, req *model.CreateMemoryRequest) (*model.Memory, error) {
	if req.Content == "" {
		return nil, fmt.Errorf("content is required: %w", model.ErrInvalidInput)
	}

	// 校验 retention tier
	if req.RetentionTier != "" {
		if err := ValidateRetentionTier(req.RetentionTier); err != nil {
			return nil, err
		}
	}

	// 完整去重检查（哈希 + 向量，带可见性隔离）/ Full dedup check (hash + vector, with visibility isolation)
	dedupIdentity := &model.Identity{TeamID: req.TeamID, OwnerID: req.OwnerID}
	existing, contentHash, embedding, err := m.dedupCheck(ctx, req.Content, req.Embedding, dedupIdentity)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	mem := buildMemoryFromRequest(req, contentHash)

	// 显式传入的值覆盖等级默认值
	applyExplicitOverrides(mem, req)

	// 写入 SQLite
	if err := m.memStore.Create(ctx, mem); err != nil {
		return nil, fmt.Errorf("failed to create memory in store: %w", err)
	}

	// 副作用：溯源、标签、上下文计数、向量、摘要、实体抽取
	m.handleDerivations(ctx, mem, req.DerivedFrom)
	if m.tagStore != nil && len(req.Tags) > 0 {
		m.handleCreateTags(ctx, mem.ID, mem.Scope, req.Tags)
	}
	m.handleContextCount(ctx, mem.ID, mem.ContextID)
	m.handleVectorWrite(ctx, mem, embedding)
	m.handleExcerptGeneration(ctx, mem)
	m.handleAutoExtract(ctx, mem, req.AutoExtract)

	return mem, nil
}

// buildMemoryFromRequest 从请求构建记忆对象 / Build memory object from create request
func buildMemoryFromRequest(req *model.CreateMemoryRequest, contentHash string) *model.Memory {
	mem := &model.Memory{
		Content:       req.Content,
		Metadata:      req.Metadata,
		TeamID:        req.TeamID,
		OwnerID:       req.OwnerID,
		Visibility:    req.Visibility,
		IsLatest:      true,
		ContextID:     req.ContextID,
		Kind:          req.Kind,
		SubKind:       req.SubKind,
		Scope:         req.Scope,
		Excerpt:       req.Excerpt,
		Summary:       req.Summary,
		HappenedAt:    req.HappenedAt,
		SourceType:    req.SourceType,
		SourceRef:     req.SourceRef,
		DocumentID:    req.DocumentID,
		ChunkIndex:    req.ChunkIndex,
		RetentionTier: req.RetentionTier,
		MessageRole:   req.MessageRole,
		TurnNumber:    req.TurnNumber,
		ContentHash:   contentHash,
		MemoryClass:   req.MemoryClass,
		CandidateFor:  req.CandidateFor,
	}

	// 应用等级默认值
	ResolveTierDefaults(mem)

	// 自动填充 visibility（仅当未显式指定时）/ Auto-fill visibility when not explicitly set
	if mem.Visibility == "" {
		mem.Visibility = resolveDefaultVisibility(mem.Scope, mem.Kind)
	}

	return mem
}

// applyExplicitOverrides 显式传入的值覆盖等级默认值 / Explicit values override tier defaults
func applyExplicitOverrides(mem *model.Memory, req *model.CreateMemoryRequest) {
	if req.Strength != nil {
		mem.Strength = *req.Strength
	}
	if req.DecayRate != nil {
		mem.DecayRate = *req.DecayRate
	}
	if req.ExpiresAt != nil {
		mem.ExpiresAt = req.ExpiresAt
	}
}

// Get 获取单条记忆 / Get a memory by ID
func (m *Manager) Get(ctx context.Context, id string) (*model.Memory, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	mem, err := m.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// 递增访问计数（best-effort）/ Increment access count (best-effort)
	_ = m.memStore.IncrementAccessCount(ctx, id, 1)
	return mem, nil
}

// GetVisible 带可见性校验获取记忆 / Get a memory with visibility check
func (m *Manager) GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	mem, err := m.memStore.GetVisible(ctx, id, identity)
	if err != nil {
		return nil, err
	}
	// 递增访问计数（best-effort）/ Increment access count (best-effort)
	_ = m.memStore.IncrementAccessCount(ctx, id, 1)
	return mem, nil
}

// List 分页列表（带可见性过滤）/ List memories with pagination and visibility filtering
func (m *Manager) List(ctx context.Context, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return m.memStore.List(ctx, identity, offset, limit)
}

// resolveDefaultVisibility 按 scope+kind 决定默认可见性 / Determine default visibility by scope and kind
// project/ 下非 observation 默认 team，其他一律 private / project/ non-observation defaults to team
func resolveDefaultVisibility(scope, kind string) string {
	if strings.HasPrefix(scope, "project/") && kind != "observation" {
		return model.VisibilityTeam
	}
	return model.VisibilityPrivate
}
