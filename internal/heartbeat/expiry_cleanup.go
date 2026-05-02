package heartbeat

import (
	"context"
	"fmt"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// runExpiryCleanup 软删除 expires_at 已到期的记忆（主要是 ephemeral tier）
// Soft delete expired memories (main use case: ephemeral tier)
func (e *Engine) runExpiryCleanup(ctx context.Context) error {
	deleted, err := e.memStore.CleanupExpired(ctx)
	if err != nil {
		return fmt.Errorf("cleanup expired memories: %w", err)
	}
	if deleted > 0 {
		logger.Info("heartbeat: expired memories soft-deleted", zap.Int("count", deleted))
	}
	return nil
}
