package heartbeat

import (
	"context"
	"time"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// runRelationCleanup 清理过期弱关系 + 软删孤儿实体 / Cleanup stale relations + soft-delete orphan entities
func (e *Engine) runRelationCleanup(ctx context.Context) error {
	if e.graphStore == nil {
		return nil
	}

	// 清理弱关系：mention_count < 3 且 last_seen_at 超过 90 天 / Stale: low mention + old
	cutoff := time.Now().AddDate(0, 0, -90)
	deleted, err := e.graphStore.CleanupStaleRelations(ctx, 3, cutoff)
	if err != nil {
		logger.Warn("heartbeat: stale relation cleanup failed", zap.Error(err))
	} else if deleted > 0 {
		logger.Info("heartbeat: cleaned stale relations", zap.Int64("deleted", deleted))
	}

	// 软删孤儿实体 / Soft-delete orphan entities
	orphans, err := e.graphStore.CleanupOrphanEntities(ctx)
	if err != nil {
		logger.Warn("heartbeat: orphan entity cleanup failed", zap.Error(err))
	} else if orphans > 0 {
		logger.Info("heartbeat: soft-deleted orphan entities", zap.Int64("count", orphans))
	}

	// 硬删 30 天前软删的实体 / Purge entities soft-deleted over 30 days ago
	purgeCutoff := time.Now().AddDate(0, 0, -30)
	purged, err := e.graphStore.PurgeDeletedEntities(ctx, purgeCutoff)
	if err != nil {
		logger.Warn("heartbeat: entity purge failed", zap.Error(err))
	} else if purged > 0 {
		logger.Info("heartbeat: purged deleted entities", zap.Int64("count", purged))
	}

	return nil
}
