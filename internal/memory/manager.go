// Package memory 记忆管理业务逻辑 / Memory management business logic
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
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
}

// Manager 记忆管理器，负责 CRUD 和双后端写入 / Memory manager handling CRUD with dual-backend writes
type Manager struct {
	memStore     store.MemoryStore
	vecStore     store.VectorStore  // 可为 nil / may be nil
	embedder     store.Embedder     // 可为 nil / may be nil
	tagStore     store.TagStore     // 可为 nil / may be nil
	contextStore store.ContextStore // 可为 nil / may be nil
	extractor    *Extractor         // 可为 nil / may be nil
	llm          llm.Provider       // 可为 nil / may be nil (used for abstract generation)
	taskQueue    TaskEnqueuer       // 可为 nil / may be nil
	cfg          ManagerConfig
}

// NewManager 创建记忆管理器 / Create a new memory manager
// vecStore、embedder、tagStore、contextStore、extractor 均为可选，传 nil 表示未启用
func NewManager(memStore store.MemoryStore, vecStore store.VectorStore, embedder store.Embedder, tagStore store.TagStore, contextStore store.ContextStore, extractor *Extractor, llmProvider llm.Provider, cfg ManagerConfig, taskQueue ...TaskEnqueuer) *Manager {
	m := &Manager{
		memStore:     memStore,
		vecStore:     vecStore,
		embedder:     embedder,
		tagStore:     tagStore,
		contextStore: contextStore,
		extractor:    extractor,
		llm:          llmProvider,
		cfg:          cfg,
	}
	if len(taskQueue) > 0 {
		m.taskQueue = taskQueue[0]
	}
	return m
}

// SetQueue 设置任务队列（支持延迟注入）/ Set task queue (supports deferred injection)
func (m *Manager) SetQueue(q TaskEnqueuer) {
	m.taskQueue = q
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

	// 哈希去重 / Hash deduplication
	contentHash := ContentHash(req.Content)
	dedupResult, err := m.checkHashDedup(ctx, contentHash)
	if err != nil {
		logger.Warn("hash dedup check failed, proceeding with create", zap.Error(err))
	} else if dedupResult.IsDuplicate {
		if err := m.memStore.Reinforce(ctx, dedupResult.ExistingMemory.ID); err != nil {
			logger.Warn("failed to reinforce duplicate memory", zap.Error(err))
		}
		return dedupResult.ExistingMemory, nil
	}

	// 提前生成 embedding（余弦去重 + Qdrant 写入共用）/ Resolve embedding early for vector dedup + upsert
	var embedding []float32
	if m.vecStore != nil {
		embedding, err = m.resolveEmbedding(ctx, req.Embedding, req.Content)
		if err != nil {
			logger.Warn("failed to generate embedding, skipping vector dedup",
				zap.Error(err),
			)
		}
	}

	// 余弦相似度去重 / Cosine similarity dedup
	dedupCfg := m.cfg.Dedup
	if embedding != nil {
		vecDedup, err := checkVectorDedup(ctx, embedding, m.vecStore, dedupCfg)
		if err != nil {
			logger.Warn("vector dedup check failed, proceeding with create", zap.Error(err))
		} else if vecDedup.IsDuplicate && vecDedup.ExistingMemory != nil {
			if err := m.memStore.Reinforce(ctx, vecDedup.ExistingMemory.ID); err != nil {
				logger.Warn("failed to reinforce vector-duplicate memory", zap.Error(err))
			}
			return vecDedup.ExistingMemory, nil
		}
	}

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
		Abstract:      req.Abstract,
		Summary:       req.Summary,
		HappenedAt:    req.HappenedAt,
		SourceType:    req.SourceType,
		SourceRef:     req.SourceRef,
		RetentionTier: req.RetentionTier,
		MessageRole:   req.MessageRole,
		TurnNumber:    req.TurnNumber,
		ContentHash:   contentHash,
	}

	// 应用等级默认值
	ResolveTierDefaults(mem)

	// 显式传入的值覆盖等级默认值
	if req.Strength != nil {
		mem.Strength = *req.Strength
	}
	if req.DecayRate != nil {
		mem.DecayRate = *req.DecayRate
	}
	if req.ExpiresAt != nil {
		mem.ExpiresAt = req.ExpiresAt
	}

	// 写入 SQLite
	if err := m.memStore.Create(ctx, mem); err != nil {
		return nil, fmt.Errorf("failed to create memory in store: %w", err)
	}

	// 处理标签 / Handle tags
	if m.tagStore != nil && len(req.Tags) > 0 {
		m.handleCreateTags(ctx, mem.ID, mem.Scope, req.Tags)
	}

	// 递增上下文记忆计数 / Increment context memory count
	if m.contextStore != nil && mem.ContextID != "" {
		if err := m.contextStore.IncrementMemoryCount(ctx, mem.ContextID); err != nil {
			logger.Warn("failed to increment context memory count",
				zap.String("memory_id", mem.ID),
				zap.String("context_id", mem.ContextID),
				zap.Error(err),
			)
		}
	}

	// 向量写入（best-effort，复用已生成的 embedding）
	if m.vecStore != nil && embedding != nil {
		payload := buildVectorPayload(mem)
		if err := m.vecStore.Upsert(ctx, mem.ID, embedding, payload); err != nil {
			logger.Error("failed to upsert vector, SQLite write succeeded",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
		} else {
			mem.EmbeddingID = mem.ID
		}
	}

	// 异步生成摘要（content 短则直接用 content，否则调 LLM）/ Async abstract generation
	if mem.Abstract == "" && m.llm != nil {
		if len([]rune(mem.Content)) <= 50 {
			mem.Abstract = mem.Content
			_ = m.memStore.Update(ctx, mem)
		} else {
			m.asyncGenerateAbstract(mem.ID, mem.Content)
		}
	}

	// 自动实体抽取（异步，优先队列，回退 goroutine）/ Auto entity extraction (prefer queue, fallback goroutine)
	if req.AutoExtract && m.extractor != nil {
		extractReq := &model.ExtractRequest{
			MemoryID: mem.ID,
			Content:  mem.Content,
			Scope:    mem.Scope,
			TeamID:   mem.TeamID,
		}
		if m.taskQueue != nil {
			payload, _ := json.Marshal(extractReq)
			if _, err := m.taskQueue.Enqueue(ctx, "entity_extract", payload); err != nil {
				logger.Warn("failed to enqueue extract task, falling back to goroutine",
					zap.String("memory_id", mem.ID),
					zap.Error(err),
				)
				m.asyncExtract(extractReq)
			}
		} else {
			m.asyncExtract(extractReq)
		}
	}

	return mem, nil
}

// asyncExtract 回退的异步 goroutine 抽取 / Fallback async goroutine extraction
func (m *Manager) asyncExtract(req *model.ExtractRequest) {
	extractTimeout := m.cfg.Extract.Timeout
	if extractTimeout <= 0 {
		extractTimeout = 30 * time.Second
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
		defer cancel()
		if _, err := m.extractor.Extract(ctx, req); err != nil {
			logger.Warn("auto extract failed",
				zap.String("memory_id", req.MemoryID),
				zap.Error(err),
			)
		}
	}()
}

// asyncGenerateAbstract 异步生成记忆摘要 / Async generate memory abstract via LLM
func (m *Manager) asyncGenerateAbstract(memoryID, content string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		abstract, err := m.generateAbstract(ctx, content)
		if err != nil {
			logger.Warn("async abstract generation failed",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
			return
		}

		mem, err := m.memStore.Get(ctx, memoryID)
		if err != nil {
			logger.Warn("failed to get memory for abstract update",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
			return
		}
		mem.Abstract = abstract
		if err := m.memStore.Update(ctx, mem); err != nil {
			logger.Warn("failed to update memory abstract",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
		}
	}()
}

// generateAbstract 调用 LLM 生成一句话摘要 / Call LLM to generate one-line abstract
func (m *Manager) generateAbstract(ctx context.Context, content string) (string, error) {
	temp := 0.1
	resp, err := m.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "用一句话（≤100字）概括以下内容的核心信息，直接输出摘要，不加前缀。"},
			{Role: "user", Content: content},
		},
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("llm chat failed: %w", err)
	}
	abstract := strings.TrimSpace(resp.Content)
	if len([]rune(abstract)) > 150 {
		abstract = string([]rune(abstract)[:150])
	}
	return abstract, nil
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

// Update 更新记忆 / Update a memory
func (m *Manager) Update(ctx context.Context, id string, req *model.UpdateMemoryRequest) (*model.Memory, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	// 获取现有记忆
	mem, err := m.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// 更新字段
	if req.Content != nil {
		mem.Content = *req.Content
	}
	if req.Metadata != nil {
		mem.Metadata = req.Metadata
	}
	if req.ContextID != nil {
		mem.ContextID = *req.ContextID
	}
	if req.Kind != nil {
		mem.Kind = *req.Kind
	}
	if req.SubKind != nil {
		mem.SubKind = *req.SubKind
	}
	if req.Scope != nil {
		mem.Scope = *req.Scope
	}
	if req.Abstract != nil {
		mem.Abstract = *req.Abstract
	}
	if req.Summary != nil {
		mem.Summary = *req.Summary
	}
	if req.HappenedAt != nil {
		mem.HappenedAt = req.HappenedAt
	}
	if req.SourceType != nil {
		mem.SourceType = *req.SourceType
	}
	if req.SourceRef != nil {
		mem.SourceRef = *req.SourceRef
	}
	if req.MessageRole != nil {
		mem.MessageRole = *req.MessageRole
	}
	if req.TurnNumber != nil {
		mem.TurnNumber = *req.TurnNumber
	}

	// 处理 retention tier 变更
	if req.RetentionTier != nil {
		if err := ValidateRetentionTier(*req.RetentionTier); err != nil {
			return nil, err
		}
		mem.RetentionTier = *req.RetentionTier
		// 重新应用等级默认值（除非显式传了 DecayRate/ExpiresAt）
		if req.DecayRate == nil {
			decayRate, _ := model.DefaultDecayParams(mem.RetentionTier)
			mem.DecayRate = decayRate
		}
	}
	if req.Strength != nil {
		mem.Strength = *req.Strength
	}
	if req.DecayRate != nil {
		mem.DecayRate = *req.DecayRate
	}
	if req.ExpiresAt != nil {
		mem.ExpiresAt = req.ExpiresAt
	}

	if err := m.memStore.Update(ctx, mem); err != nil {
		return nil, fmt.Errorf("failed to update memory: %w", err)
	}

	// 处理标签更新 / Handle tag updates
	if m.tagStore != nil && req.Tags != nil {
		m.handleUpdateTags(ctx, mem.ID, mem.Scope, req.Tags)
	}

	// 向量更新（best-effort）
	if m.vecStore != nil && req.Content != nil {
		embedding, err := m.resolveEmbedding(ctx, req.Embedding, *req.Content)
		if err != nil {
			logger.Warn("failed to generate embedding on update",
				zap.String("memory_id", id),
				zap.Error(err),
			)
		} else if embedding != nil {
			payload := buildVectorPayload(mem)
			if err := m.vecStore.Upsert(ctx, mem.ID, embedding, payload); err != nil {
				logger.Error("failed to update vector",
					zap.String("memory_id", id),
					zap.Error(err),
				)
			}
		}
	}

	return mem, nil
}

// Delete 软删除记忆 / Soft delete a memory by ID
func (m *Manager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	// 获取记忆以检查 ContextID / Get memory to check ContextID
	mem, err := m.memStore.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get memory for delete: %w", err)
	}

	// 软删除 / Soft delete
	if err := m.memStore.SoftDelete(ctx, id); err != nil {
		return fmt.Errorf("failed to soft delete memory: %w", err)
	}

	// 递减上下文记忆计数 / Decrement context memory count
	if m.contextStore != nil && mem.ContextID != "" {
		if err := m.contextStore.DecrementMemoryCount(ctx, mem.ContextID); err != nil {
			logger.Warn("failed to decrement context memory count",
				zap.String("memory_id", id),
				zap.String("context_id", mem.ContextID),
				zap.Error(err),
			)
		}
	}

	// best-effort 删除向量
	if m.vecStore != nil {
		if err := m.vecStore.Delete(ctx, id); err != nil {
			logger.Error("failed to delete vector, SQLite delete succeeded",
				zap.String("memory_id", id),
				zap.Error(err),
			)
		}
	}

	return nil
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

// SoftDelete 软删除记忆 / Soft delete a memory (sets deleted_at, keeps data)
func (m *Manager) SoftDelete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.SoftDelete(ctx, id)
}

// Restore 恢复软删除的记忆 / Restore a soft-deleted memory
func (m *Manager) Restore(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.Restore(ctx, id)
}

// RestoreWithIdentity 带归属检查的恢复 / Restore with owner identity check
func (m *Manager) RestoreWithIdentity(ctx context.Context, id string, identity *model.Identity) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	// 查询 owner_id（含 soft-deleted 记忆）/ Query owner_id including soft-deleted
	ownerID, err := m.memStore.GetOwnerID(ctx, id)
	if err != nil {
		return err
	}
	if ownerID != "" && ownerID != identity.OwnerID && identity.OwnerID != model.SystemOwnerID {
		return fmt.Errorf("only the owner can restore this memory: %w", model.ErrForbidden)
	}
	return m.memStore.Restore(ctx, id)
}

// Reinforce 强化记忆 / Reinforce a memory (increase strength and reinforced_count)
func (m *Manager) Reinforce(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	if err := m.memStore.Reinforce(ctx, id); err != nil {
		return err
	}

	// 自动晶化检查 / Auto-crystallization check
	cfg := m.cfg.Crystallization
	if cfg.Enabled {
		mem, err := m.memStore.Get(ctx, id)
		if err != nil {
			logger.Warn("crystallization: failed to get memory after reinforce", zap.String("id", id), zap.Error(err))
			return nil
		}
		if ShouldCrystallize(mem, cfg) {
			newTier, newDecayRate := PromoteTier(mem.RetentionTier)
			if newTier != mem.RetentionTier {
				mem.RetentionTier = newTier
				mem.DecayRate = newDecayRate
				if err := m.memStore.Update(ctx, mem); err != nil {
					logger.Warn("crystallization: failed to promote tier",
						zap.String("id", id),
						zap.String("new_tier", newTier),
						zap.Error(err),
					)
				} else {
					logger.Info("memory crystallized",
						zap.String("id", id),
						zap.String("new_tier", newTier),
					)
				}
			}
		}
	}
	return nil
}

// IngestConversation 批量导入对话 / Ingest a conversation as multiple memories under a context
func (m *Manager) IngestConversation(ctx context.Context, req *model.IngestConversationRequest, identity *model.Identity) (string, []*model.Memory, error) {
	if len(req.Messages) == 0 {
		return "", nil, fmt.Errorf("messages is required: %w", model.ErrInvalidInput)
	}

	contextID := req.ContextID

	// 如果未指定 contextID，创建新的 session context
	if contextID == "" && m.contextStore != nil {
		ctxObj := &model.Context{
			Name:  fmt.Sprintf("conversation-%s", req.Provider),
			Scope: req.Scope,
			Kind:  "session",
			Metadata: map[string]any{
				"provider":    req.Provider,
				"external_id": req.ExternalID,
			},
		}
		if req.Metadata != nil {
			for k, v := range req.Metadata {
				ctxObj.Metadata[k] = v
			}
		}
		if err := m.contextStore.Create(ctx, ctxObj); err != nil {
			return "", nil, fmt.Errorf("failed to create conversation context: %w", err)
		}
		contextID = ctxObj.ID
	}

	// 构建全部记忆对象 / Build all memory objects
	memories := make([]*model.Memory, 0, len(req.Messages))
	for i, msg := range req.Messages {
		turnNumber := msg.TurnNumber
		if turnNumber == 0 {
			turnNumber = i + 1
		}

		mem := &model.Memory{
			Content:       msg.Content,
			Metadata:      msg.Metadata,
			ContextID:     contextID,
			Scope:         req.Scope,
			SourceType:    "conversation",
			SourceRef:     req.ExternalID,
			RetentionTier: model.TierStandard,
			MessageRole:   msg.Role,
			TurnNumber:    turnNumber,
			IsLatest:      true,
			TeamID:        identity.TeamID,
			OwnerID:       identity.OwnerID,
			Visibility:    model.VisibilityPrivate,
		}
		ResolveTierDefaults(mem)
		memories = append(memories, mem)
	}

	// 单事务批量写入 / Batch insert in a single transaction
	if err := m.memStore.CreateBatch(ctx, memories); err != nil {
		return "", nil, fmt.Errorf("failed to batch insert conversation memories: %w", err)
	}

	// 批量递增上下文记忆计数（best-effort）/ Batch increment context memory count
	if m.contextStore != nil && contextID != "" && len(memories) > 0 {
		if err := m.contextStore.IncrementMemoryCountBy(ctx, contextID, len(memories)); err != nil {
			logger.Warn("failed to increment context memory count",
				zap.String("context_id", contextID),
				zap.Int("delta", len(memories)),
				zap.Error(err),
			)
		}
	}

	return contextID, memories, nil
}

// GetConversation 按轮次顺序获取对话记忆 / Get conversation memories ordered by turn number
func (m *Manager) GetConversation(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if contextID == "" {
		return nil, fmt.Errorf("context_id is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.ListByContextOrdered(ctx, contextID, identity, offset, limit)
}

// CleanupExpired 清理过期记忆 / Cleanup expired memories
func (m *Manager) CleanupExpired(ctx context.Context) (int, error) {
	return m.memStore.CleanupExpired(ctx)
}

// buildVectorPayload 构建向量存储的 payload / Build payload for vector store upsert
func buildVectorPayload(mem *model.Memory) map[string]any {
	return map[string]any{
		"memory_id":      mem.ID,
		"team_id":        mem.TeamID,
		"created_at":     mem.CreatedAt.Format("2006-01-02T15:04:05Z"),
		"scope":          mem.Scope,
		"kind":           mem.Kind,
		"context_id":     mem.ContextID,
		"abstract":       mem.Abstract,
		"retention_tier": mem.RetentionTier,
		"message_role":   mem.MessageRole,
	}
}

// resolveEmbedding 解析 embedding：用户提供则直接用，否则通过 Embedder 生成
func (m *Manager) resolveEmbedding(ctx context.Context, provided []float32, content string) ([]float32, error) {
	if len(provided) > 0 {
		return provided, nil
	}
	if m.embedder == nil {
		return nil, nil
	}
	embedding, err := m.embedder.Embed(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}
	return embedding, nil
}

// handleCreateTags 处理创建记忆时的标签关联 / Handle tag association during memory creation
func (m *Manager) handleCreateTags(ctx context.Context, memoryID, scope string, tags []string) {
	for _, tagName := range tags {
		tagID, err := m.findOrCreateTag(ctx, tagName, scope)
		if err != nil {
			logger.Warn("failed to find or create tag",
				zap.String("memory_id", memoryID),
				zap.String("tag_name", tagName),
				zap.Error(err),
			)
			continue
		}
		if err := m.tagStore.TagMemory(ctx, memoryID, tagID); err != nil {
			logger.Warn("failed to tag memory",
				zap.String("memory_id", memoryID),
				zap.String("tag_id", tagID),
				zap.Error(err),
			)
		}
	}
}

// handleUpdateTags 处理更新记忆时的标签替换 / Handle tag replacement during memory update
func (m *Manager) handleUpdateTags(ctx context.Context, memoryID, scope string, tags []string) {
	// 获取现有标签并移除
	existingTags, err := m.tagStore.GetMemoryTags(ctx, memoryID)
	if err != nil {
		logger.Warn("failed to get existing tags for update",
			zap.String("memory_id", memoryID),
			zap.Error(err),
		)
	} else {
		for _, t := range existingTags {
			if err := m.tagStore.UntagMemory(ctx, memoryID, t.ID); err != nil {
				logger.Warn("failed to untag memory during update",
					zap.String("memory_id", memoryID),
					zap.String("tag_id", t.ID),
					zap.Error(err),
				)
			}
		}
	}

	// 关联新标签
	m.handleCreateTags(ctx, memoryID, scope, tags)
}

// findOrCreateTag 查找或创建标签 / Find existing tag by name or create a new one
func (m *Manager) findOrCreateTag(ctx context.Context, name, scope string) (string, error) {
	// 尝试在已有标签中查找同名标签
	existingTags, err := m.tagStore.ListTags(ctx, scope)
	if err != nil {
		return "", fmt.Errorf("failed to list tags: %w", err)
	}
	for _, t := range existingTags {
		if t.Name == name {
			return t.ID, nil
		}
	}

	// 创建新标签
	tag := &model.Tag{
		Name:  name,
		Scope: scope,
	}
	if err := m.tagStore.CreateTag(ctx, tag); err != nil {
		return "", fmt.Errorf("failed to create tag: %w", err)
	}
	return tag.ID, nil
}
