// Package memory 记忆生命周期与批量操作 / Memory lifecycle and batch operations
package memory

import (
	"context"
	"fmt"
	"strings"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

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
	m.checkCrystallization(ctx, id)
	return nil
}

// checkCrystallization 自动晶化检查 / Auto-crystallization check after reinforce
func (m *Manager) checkCrystallization(ctx context.Context, id string) {
	cfg := m.cfg.Crystallization
	if !cfg.Enabled {
		return
	}
	mem, err := m.memStore.Get(ctx, id)
	if err != nil {
		logger.Warn("crystallization: failed to get memory after reinforce", zap.String("id", id), zap.Error(err))
		return
	}
	if !ShouldCrystallize(mem, cfg) {
		return
	}
	newTier, newDecayRate := PromoteTier(mem.RetentionTier)
	if newTier == mem.RetentionTier {
		return
	}
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

// IngestConversation 批量导入对话 / Ingest a conversation as multiple memories under a context
func (m *Manager) IngestConversation(ctx context.Context, req *model.IngestConversationRequest, identity *model.Identity) (string, []*model.Memory, error) {
	if len(req.Messages) == 0 {
		return "", nil, fmt.Errorf("messages is required: %w", model.ErrInvalidInput)
	}

	contextID, err := m.resolveConversationContext(ctx, req)
	if err != nil {
		return "", nil, err
	}

	memories := m.buildConversationMemories(req, contextID, identity)

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

// resolveConversationContext 解析或创建对话上下文 / Resolve or create conversation context
func (m *Manager) resolveConversationContext(ctx context.Context, req *model.IngestConversationRequest) (string, error) {
	if req.ContextID != "" {
		return req.ContextID, nil
	}
	if m.contextStore == nil {
		return "", nil
	}
	ctxObj := &model.Context{
		Name:        fmt.Sprintf("conversation-%s", req.Provider),
		Scope:       req.Scope,
		ContextType: "session",
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
		return "", fmt.Errorf("failed to create conversation context: %w", err)
	}
	return ctxObj.ID, nil
}

// buildConversationMemories 构建对话记忆对象 / Build conversation memory objects
func (m *Manager) buildConversationMemories(req *model.IngestConversationRequest, contextID string, identity *model.Identity) []*model.Memory {
	nf := m.cfg.Ingest.NoiseFilter
	memories := make([]*model.Memory, 0, len(req.Messages))
	for i, msg := range req.Messages {
		// 噪声预过滤：跳过过短或匹配噪声模式的内容 / Noise pre-filter: skip short or pattern-matched content
		if IsNoiseContent(msg.Content, nf.MinContentLength, nf.Patterns) {
			logger.Debug("skipping noise message during ingest",
				zap.Int("turn", i+1),
				zap.Int("content_runes", len([]rune(msg.Content))),
			)
			continue
		}

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
	return memories
}

// GetConversation 按轮次顺序获取对话记忆 / Get conversation memories ordered by turn number
func (m *Manager) GetConversation(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if contextID == "" {
		return nil, fmt.Errorf("context_id is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.ListByContextOrdered(ctx, contextID, identity, offset, limit)
}

// DeleteChunksByDocumentID 软删除文档的所有分块记忆 / Soft delete all chunk memories for a document
func (m *Manager) DeleteChunksByDocumentID(ctx context.Context, documentID string) (int, error) {
	if documentID == "" {
		return 0, fmt.Errorf("document_id is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.SoftDeleteByDocumentID(ctx, documentID)
}

// CleanupExpired 清理过期记忆 / Cleanup expired memories
func (m *Manager) CleanupExpired(ctx context.Context) (int, error) {
	return m.memStore.CleanupExpired(ctx)
}

// ListBySourceRef 按来源引用列出记忆 / List memories by source_ref
func (m *Manager) ListBySourceRef(ctx context.Context, sourceRef string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if sourceRef == "" {
		return nil, fmt.Errorf("source_ref is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.ListBySourceRef(ctx, sourceRef, identity, offset, limit)
}

// SoftDeleteBySourceRef 按来源引用批量软删除（带归属校验）/ Soft delete with identity filtering
func (m *Manager) SoftDeleteBySourceRef(ctx context.Context, sourceRef string, identity *model.Identity) (int, error) {
	if sourceRef == "" {
		return 0, fmt.Errorf("source_ref is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.SoftDeleteBySourceRef(ctx, sourceRef, identity)
}

// RestoreBySourceRef 按来源引用批量恢复（带归属校验）/ Restore with identity filtering
func (m *Manager) RestoreBySourceRef(ctx context.Context, sourceRef string, identity *model.Identity) (int, error) {
	if sourceRef == "" {
		return 0, fmt.Errorf("source_ref is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.RestoreBySourceRef(ctx, sourceRef, identity)
}

// ListDerivedFrom 查询由指定记忆衍生出的记忆 / List memories derived from a given memory ID
func (m *Manager) ListDerivedFrom(ctx context.Context, id string, identity *model.Identity) ([]*model.Memory, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.ListDerivedFrom(ctx, id, identity)
}

// ListConsolidatedInto 查询被归纳到指定记忆的原始记忆 / List memories consolidated into a given memory ID
func (m *Manager) ListConsolidatedInto(ctx context.Context, id string, identity *model.Identity) ([]*model.Memory, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.memStore.ListConsolidatedInto(ctx, id, identity)
}

// ScopePolicyChecker scope 策略检查接口 / Scope policy checker interface
type ScopePolicyChecker interface {
	GetByScope(ctx context.Context, scope string) (*model.ScopePolicy, error)
}

// CheckAndDowngradeScope 检查写入权限，不通过时降级 scope / Check write permission, downgrade scope if denied
// 返回 (实际 scope, 是否降级, 原因) / Returns (actual scope, was downgraded, reason)
func CheckAndDowngradeScope(ctx context.Context, checker ScopePolicyChecker, scope, ownerID string) (actualScope string, downgraded bool, reason string) {
	if checker == nil || !strings.HasPrefix(scope, "project/") {
		return scope, false, ""
	}

	policy, err := checker.GetByScope(ctx, scope)
	if err != nil {
		// 无策略 = 不限制 / No policy = unrestricted
		return scope, false, ""
	}

	if policy.CanWrite(ownerID) {
		return scope, false, ""
	}

	downgradedScope := "user/" + ownerID
	return downgradedScope, true, fmt.Sprintf("not in allowed_writers for %s", scope)
}
