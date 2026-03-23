package heartbeat

import (
	"context"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// runOrphanCleanup 孤儿清理 / Orphan cleanup — remove stale graph associations
// 找到关联已 soft-delete 记忆的实体关联，清理无效的 memory-entity 关联
func (e *Engine) runOrphanCleanup(ctx context.Context) error {
	// 获取所有实体
	entities, err := e.graphStore.ListEntities(ctx, "", "", 1000)
	if err != nil {
		return err
	}

	cleanupCount := 0
	for _, entity := range entities {
		// 获取实体关联的记忆
		memories, err := e.graphStore.GetEntityMemories(ctx, entity.ID, 100)
		if err != nil {
			continue
		}

		for _, mem := range memories {
			// 检查关联的记忆是否仍然存在（未 soft-delete）
			existing, err := e.memStore.Get(ctx, mem.ID)
			if err != nil || existing == nil || existing.DeletedAt != nil {
				// 记忆已删除，清理关联
				if err := e.graphStore.DeleteMemoryEntity(ctx, mem.ID, entity.ID); err != nil {
					logger.Warn("heartbeat: failed to delete orphan memory-entity",
						zap.String("memory_id", mem.ID),
						zap.String("entity_id", entity.ID),
						zap.Error(err),
					)
					continue
				}
				cleanupCount++
			}
		}
	}

	if cleanupCount > 0 {
		logger.Info("heartbeat: orphan cleanup completed",
			zap.Int("removed_associations", cleanupCount),
		)
	}
	return nil
}
