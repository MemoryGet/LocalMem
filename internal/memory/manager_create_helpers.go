// Package memory Create 方法的副作用辅助函数 / Side-effect helpers for Create method
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// handleDerivations 写入溯源关系到 junction 表 / Write derivation links to junction table
func (m *Manager) handleDerivations(ctx context.Context, mem *model.Memory, derivedFrom []string) {
	if len(derivedFrom) == 0 {
		return
	}
	if err := m.memStore.AddDerivations(ctx, derivedFrom, mem.ID); err != nil {
		logger.Warn("failed to add derivation links",
			zap.String("memory_id", mem.ID),
			zap.Error(err),
		)
	}
	mem.DerivedFrom = derivedFrom // 保留在内存对象上供调用方使用 / Keep on in-memory object for callers
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
	// 按名称索引查找，O(1) / Indexed lookup by name, O(1)
	existing, err := m.tagStore.GetTagByName(ctx, name, scope)
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, model.ErrTagNotFound) {
		return "", fmt.Errorf("failed to lookup tag: %w", err)
	}

	// 标签不存在，创建新标签 / Tag not found, create new one
	tag := &model.Tag{
		Name:  name,
		Scope: scope,
	}
	if err := m.tagStore.CreateTag(ctx, tag); err != nil {
		return "", fmt.Errorf("failed to create tag: %w", err)
	}
	return tag.ID, nil
}

// handleContextCount 递增上下文记忆计数 / Increment context memory count
func (m *Manager) handleContextCount(ctx context.Context, memoryID, contextID string) {
	if m.contextStore == nil || contextID == "" {
		return
	}
	if err := m.contextStore.IncrementMemoryCount(ctx, contextID); err != nil {
		logger.Warn("failed to increment context memory count",
			zap.String("memory_id", memoryID),
			zap.String("context_id", contextID),
			zap.Error(err),
		)
	}
}

// handleVectorWrite 向量写入（best-effort，复用已生成的 embedding）/ Vector write (best-effort, reuse embedding)
func (m *Manager) handleVectorWrite(ctx context.Context, mem *model.Memory, embedding []float32) {
	if m.vecStore == nil || embedding == nil {
		return
	}
	payload := buildVectorPayload(mem)
	if err := m.vecStore.Upsert(ctx, mem.ID, embedding, payload); err != nil {
		logger.Error("failed to upsert vector, SQLite write succeeded",
			zap.String("memory_id", mem.ID),
			zap.Error(err),
		)
	}
}

// handleExcerptGeneration 同步生成丰富摘要 / Sync rich excerpt generation for FTS indexing
func (m *Manager) handleExcerptGeneration(ctx context.Context, mem *model.Memory) {
	if mem.Excerpt != "" || m.llm == nil {
		return
	}
	if len([]rune(mem.Content)) <= 50 {
		mem.Excerpt = mem.Content
	} else {
		excerpt, err := m.generateExcerpt(ctx, mem.Content)
		if err != nil {
			logger.Warn("sync excerpt generation failed, using content truncation",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
			runes := []rune(mem.Content)
			if len(runes) > 100 {
				runes = runes[:100]
			}
			mem.Excerpt = string(runes)
		} else {
			mem.Excerpt = excerpt
		}
	}
	// 更新 SQLite（含 FTS 索引）
	if err := m.memStore.Update(ctx, mem); err != nil {
		logger.Warn("failed to update memory with excerpt",
			zap.String("memory_id", mem.ID),
			zap.Error(err),
		)
	}
}

// handleAutoExtract 自动实体抽取（异步，优先队列，回退 goroutine）/ Auto entity extraction (prefer queue, fallback goroutine)
func (m *Manager) handleAutoExtract(ctx context.Context, mem *model.Memory, autoExtract bool) {
	if !autoExtract || m.extractor == nil {
		return
	}
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

// asyncExtract 回退的异步 goroutine 抽取 / Fallback async goroutine extraction
func (m *Manager) asyncExtract(req *model.ExtractRequest) {
	extractTimeout := m.cfg.Extract.Timeout
	if extractTimeout <= 0 {
		extractTimeout = 30 * time.Second
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("recovered panic in asyncExtract", zap.Any("panic", r))
			}
		}()
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

// asyncGenerateExcerpt 异步生成记忆摘要 / Async generate memory excerpt via LLM
func (m *Manager) asyncGenerateExcerpt(memoryID, content string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("recovered panic in asyncGenerateExcerpt", zap.Any("panic", r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		excerpt, err := m.generateExcerpt(ctx, content)
		if err != nil {
			logger.Warn("async excerpt generation failed",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
			return
		}

		mem, err := m.memStore.Get(ctx, memoryID)
		if err != nil {
			logger.Warn("failed to get memory for excerpt update",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
			return
		}
		mem.Excerpt = excerpt
		if err := m.memStore.Update(ctx, mem); err != nil {
			logger.Warn("failed to update memory excerpt",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
		}
	}()
}

// generateExcerpt 调用 LLM 生成一句话摘要 / Call LLM to generate one-line excerpt
func (m *Manager) generateExcerpt(ctx context.Context, content string) (string, error) {
	temp := 0.1
	resp, err := m.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "生成一段丰富的检索摘要（≤150字），要求：\n1. 概括核心信息\n2. 补充隐含的上位概念和关联词（如\"小橘是橘猫\"→补充\"宠物、猫咪、养猫\"）\n3. 包含中英文关键术语（如\"数据库迁移\"→\"database migration\"）\n4. 添加可能的搜索意图词（如\"部署在阿里云\"→\"服务器、云服务、hosting\"）\n直接输出摘要，不加前缀或解释。"},
			{Role: "user", Content: content},
		},
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("llm chat failed: %w", err)
	}
	excerpt := strings.TrimSpace(resp.Content)
	if len([]rune(excerpt)) > 200 {
		excerpt = string([]rune(excerpt)[:200])
	}
	return excerpt, nil
}

// buildVectorPayload 构建向量存储的 payload / Build payload for vector store upsert
func buildVectorPayload(mem *model.Memory) map[string]any {
	return map[string]any{
		"memory_id":      mem.ID,
		"team_id":        mem.TeamID,
		"owner_id":       mem.OwnerID,
		"visibility":     mem.Visibility,
		"created_at":     mem.CreatedAt.Format("2006-01-02T15:04:05Z"),
		"scope":          mem.Scope,
		"kind":           mem.Kind,
		"context_id":     mem.ContextID,
		"excerpt":        mem.Excerpt,
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
