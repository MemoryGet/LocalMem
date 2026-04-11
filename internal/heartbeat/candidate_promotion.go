package heartbeat

import (
	"context"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// runCandidatePromotion 候选实体晋升 / Promote candidate entities to real entities
func (e *Engine) runCandidatePromotion(ctx context.Context, minHits int) error {
	if e.candidateStore == nil || e.graphStore == nil {
		return nil
	}

	candidates, err := e.candidateStore.ListPromotable(ctx, minHits)
	if err != nil {
		return err
	}

	for _, c := range candidates {
		entity := &model.Entity{
			Name:       c.Name,
			EntityType: "concept",
			Scope:      c.Scope,
		}
		if err := e.graphStore.CreateEntity(ctx, entity); err != nil {
			logger.Warn("promotion: create entity failed", zap.String("name", c.Name), zap.Error(err))
			continue
		}

		for _, memID := range c.MemoryIDs {
			me := &model.MemoryEntity{
				MemoryID:   memID,
				EntityID:   entity.ID,
				Role:       "mentioned",
				Confidence: 0.9,
			}
			_ = e.graphStore.CreateMemoryEntity(ctx, me)
		}

		if err := e.candidateStore.DeleteCandidate(ctx, c.Name, c.Scope); err != nil {
			logger.Warn("promotion: delete candidate failed", zap.String("name", c.Name), zap.Error(err))
		}

		logger.Info("promoted candidate to entity",
			zap.String("name", c.Name),
			zap.String("entity_id", entity.ID),
			zap.Int("backfilled", len(c.MemoryIDs)),
		)
	}

	return nil
}
