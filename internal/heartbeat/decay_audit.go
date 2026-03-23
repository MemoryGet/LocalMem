package heartbeat

import (
	"context"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// runDecayAudit 衰减审计 / Decay audit — soft-delete zombie memories
// 找到 effectiveStrength < threshold、reinforceCount=0、age > minDays 的记忆
func (e *Engine) runDecayAudit(ctx context.Context, cfg config.HeartbeatConfig) error {
	cutoff := time.Now().AddDate(0, 0, -cfg.DecayAuditMinAgeDays)

	// 获取候选：低强度、无强化、老记忆
	candidates, err := e.memStore.ListWeak(ctx, cfg.DecayAuditThreshold, 200)
	if err != nil {
		return err
	}

	auditCount := 0
	for _, mem := range candidates {
		if mem.ReinforcedCount > 0 {
			continue
		}
		if mem.CreatedAt.After(cutoff) {
			continue
		}
		if mem.RetentionTier == model.TierPermanent {
			continue
		}

		// Go 代码计算真实有效强度（SQLite 无 EXP 函数）
		effective := memory.CalculateEffectiveStrength(
			mem.Strength, mem.DecayRate, mem.LastAccessedAt,
			mem.RetentionTier, mem.AccessCount, 0.15,
		)
		if effective >= cfg.DecayAuditThreshold {
			continue
		}

		// soft-delete 僵尸记忆
		if err := e.memStore.SoftDelete(ctx, mem.ID); err != nil {
			logger.Warn("heartbeat: failed to soft-delete zombie memory",
				zap.String("id", mem.ID),
				zap.Float64("effective_strength", effective),
				zap.Error(err),
			)
			continue
		}
		auditCount++
	}

	if auditCount > 0 {
		logger.Info("heartbeat: decay audit completed",
			zap.Int("soft_deleted", auditCount),
		)
	}
	return nil
}
