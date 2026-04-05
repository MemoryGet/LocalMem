// Package memory 记忆更新与删除 / Memory update and delete operations
package memory

import (
	"context"
	"fmt"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// Update 更新记忆 / Update a memory
func (m *Manager) Update(ctx context.Context, id string, req *model.UpdateMemoryRequest) (*model.Memory, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	// 获取现有记忆（保存旧 context_id 用于计数同步）/ Get existing memory (save old context_id for count sync)
	mem, err := m.memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	oldContextID := mem.ContextID

	applyUpdateFields(mem, req)

	if err := m.memStore.Update(ctx, mem); err != nil {
		return nil, fmt.Errorf("failed to update memory: %w", err)
	}

	// 处理标签更新 / Handle tag updates
	if m.tagStore != nil && req.Tags != nil {
		m.handleUpdateTags(ctx, mem.ID, mem.Scope, req.Tags)
	}

	// context_id 变更时同步 memory_count / Sync memory_count when context_id changes
	m.syncContextCountOnUpdate(ctx, id, oldContextID, mem.ContextID)

	// 向量更新（best-effort）
	m.handleVectorUpdate(ctx, mem, req)

	return mem, nil
}

// applyUpdateFields 将请求字段应用到记忆对象 / Apply request fields to memory object
func applyUpdateFields(mem *model.Memory, req *model.UpdateMemoryRequest) {
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
	if req.Excerpt != nil {
		mem.Excerpt = *req.Excerpt
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
	applyRetentionTierUpdate(mem, req)

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

// applyRetentionTierUpdate 处理 retention tier 变更及衰减率联动 / Handle retention tier change with decay rate cascade
func applyRetentionTierUpdate(mem *model.Memory, req *model.UpdateMemoryRequest) {
	if req.RetentionTier == nil {
		return
	}
	// 校验在调用方（Update）中已做过，此处假设已验证 / Validation done by caller
	mem.RetentionTier = *req.RetentionTier
	// 重新应用等级默认值（除非显式传了 DecayRate/ExpiresAt）
	if req.DecayRate == nil {
		decayRate, _ := model.DefaultDecayParams(mem.RetentionTier)
		mem.DecayRate = decayRate
	}
}

// syncContextCountOnUpdate 上下文变更时同步计数 / Sync context memory count on context_id change
func (m *Manager) syncContextCountOnUpdate(ctx context.Context, memoryID, oldContextID, newContextID string) {
	if m.contextStore == nil || oldContextID == newContextID {
		return
	}
	if oldContextID != "" {
		if err := m.contextStore.DecrementMemoryCount(ctx, oldContextID); err != nil {
			logger.Warn("failed to decrement old context memory count on update",
				zap.String("memory_id", memoryID),
				zap.String("old_context_id", oldContextID),
				zap.Error(err),
			)
		}
	}
	if newContextID != "" {
		if err := m.contextStore.IncrementMemoryCount(ctx, newContextID); err != nil {
			logger.Warn("failed to increment new context memory count on update",
				zap.String("memory_id", memoryID),
				zap.String("new_context_id", newContextID),
				zap.Error(err),
			)
		}
	}
}

// handleVectorUpdate 向量更新（best-effort）/ Vector update (best-effort)
func (m *Manager) handleVectorUpdate(ctx context.Context, mem *model.Memory, req *model.UpdateMemoryRequest) {
	if m.vecStore == nil || req.Content == nil {
		return
	}
	embedding, err := m.resolveEmbedding(ctx, req.Embedding, *req.Content)
	if err != nil {
		logger.Warn("failed to generate embedding on update",
			zap.String("memory_id", mem.ID),
			zap.Error(err),
		)
		return
	}
	if embedding == nil {
		return
	}
	payload := buildVectorPayload(mem)
	if err := m.vecStore.Upsert(ctx, mem.ID, embedding, payload); err != nil {
		logger.Error("failed to update vector",
			zap.String("memory_id", mem.ID),
			zap.Error(err),
		)
	}
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
