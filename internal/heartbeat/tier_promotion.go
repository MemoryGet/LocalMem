package heartbeat

import (
	"context"
	"fmt"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// minShortTermAge 最小存活时间阈值（short_term 晋升条件）/ Minimum age threshold for short_term promotion
const minShortTermAge = 24 * time.Hour

// minShortTermReinforceCount short_term 晋升所需最低强化次数 / Minimum reinforce count for short_term promotion
const minShortTermReinforceCount = 2

// runTierPromotion 按强化次数+存活时间晋升 short_term→standard 和 standard→long_term
// Promote memories: short_term→standard and standard→long_term based on reinforce count + age
func (e *Engine) runTierPromotion(ctx context.Context) error {
	const batchLimit = 100

	candidates, err := e.memStore.List(ctx, nil, 0, batchLimit)
	if err != nil {
		return fmt.Errorf("list tier promotion candidates: %w", err)
	}

	crystalCfg := config.GetConfig().Crystallization
	promoted := 0
	now := time.Now()

	for _, mem := range candidates {
		if ctx.Err() != nil {
			break
		}

		oldTier := mem.RetentionTier
		var newTier string

		switch mem.RetentionTier {
		case model.TierShortTerm:
			// conversation 类型不晋升 / skip conversation kind
			if mem.Kind == "conversation" {
				continue
			}
			if mem.ReinforcedCount >= minShortTermReinforceCount && now.Sub(mem.CreatedAt) >= minShortTermAge {
				newTier = model.TierStandard
			}
		case model.TierStandard:
			if memory.ShouldCrystallize(mem, crystalCfg) {
				newTier = model.TierLongTerm
			}
		}

		if newTier == "" {
			continue
		}

		mem.RetentionTier = newTier
		dr, _ := model.DefaultDecayParams(newTier)
		mem.DecayRate = dr

		if err := e.memStore.Update(ctx, mem); err != nil {
			logger.Warn("heartbeat: tier promotion update failed",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
			continue
		}

		promoted++
		logger.Info("heartbeat: tier promoted",
			zap.String("memory_id", mem.ID),
			zap.String("from", oldTier),
			zap.String("to", newTier),
		)
	}

	if promoted > 0 {
		logger.Info("heartbeat: tier promotion round completed", zap.Int("promoted", promoted))
	}
	return nil
}
