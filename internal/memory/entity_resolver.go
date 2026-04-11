package memory

import (
	"context"
	"strings"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// EntityResolver 向量驱动实体解析器 / Vector-driven entity resolver
type EntityResolver struct {
	tokenizer      tokenizer.Tokenizer
	graphStore     store.GraphStore
	candidateStore store.CandidateStore
	cfg            config.ResolverConfig
}

// NewEntityResolver 创建实体解析器 / Create entity resolver
func NewEntityResolver(
	tok tokenizer.Tokenizer,
	graphStore store.GraphStore,
	candidateStore store.CandidateStore,
	cfg config.ResolverConfig,
) *EntityResolver {
	return &EntityResolver{
		tokenizer:      tok,
		graphStore:     graphStore,
		candidateStore: candidateStore,
		cfg:            cfg,
	}
}

// EntityAssociation 实体关联结果 / Entity association result
type EntityAssociation struct {
	EntityID   string
	Confidence float64
}

// ResolveLayer1 分词精确匹配（Layer 1）/ Tokenizer exact match
func (r *EntityResolver) ResolveLayer1(ctx context.Context, mem *model.Memory) ([]EntityAssociation, error) {
	if r.tokenizer == nil {
		return nil, nil
	}

	// 分词 / Tokenize
	tokenized, err := r.tokenizer.Tokenize(ctx, mem.Content)
	if err != nil {
		return nil, err
	}

	terms := strings.Fields(tokenized)
	seen := make(map[string]bool)
	var associations []EntityAssociation

	for _, term := range terms {
		// 过滤短词（< 2 字符）/ Filter short terms
		if len([]rune(term)) < 2 {
			continue
		}
		// 去重 / Deduplicate
		lower := strings.ToLower(term)
		if seen[lower] {
			continue
		}
		seen[lower] = true

		// 匹配已知实体 / Match known entities
		entities, err := r.graphStore.FindEntitiesByName(ctx, term, mem.Scope, 1)
		if err != nil {
			logger.Debug("layer1: entity lookup failed", zap.String("term", term), zap.Error(err))
			continue
		}

		if len(entities) > 0 {
			associations = append(associations, EntityAssociation{
				EntityID:   entities[0].ID,
				Confidence: 0.9,
			})
		} else if r.candidateStore != nil {
			// 未命中 → 候选 / No match → candidate
			if err := r.candidateStore.UpsertCandidate(ctx, term, mem.Scope, mem.ID); err != nil {
				logger.Debug("layer1: upsert candidate failed", zap.String("term", term), zap.Error(err))
			}
		}
	}

	return associations, nil
}

// Resolve 执行实体解析并写入关联（当前仅 Layer 1）/ Execute resolution and write associations
func (r *EntityResolver) Resolve(ctx context.Context, memories []*model.Memory) error {
	for _, mem := range memories {
		associations, err := r.ResolveLayer1(ctx, mem)
		if err != nil {
			logger.Warn("resolver: layer1 failed", zap.String("memory_id", mem.ID), zap.Error(err))
			continue
		}

		// 写入关联 / Write associations
		for _, assoc := range associations {
			me := &model.MemoryEntity{
				MemoryID:   mem.ID,
				EntityID:   assoc.EntityID,
				Role:       "mentioned",
				Confidence: assoc.Confidence,
			}
			if err := r.graphStore.CreateMemoryEntity(ctx, me); err != nil {
				// 忽略已存在错误 / Ignore already-exists errors
				if !strings.Contains(err.Error(), "already exists") {
					logger.Debug("resolver: create memory_entity failed", zap.Error(err))
				}
			}
		}

		// 共现关系更新 / Co-occurrence relation update
		for i := 0; i < len(associations)-1; i++ {
			for j := i + 1; j < len(associations); j++ {
				if _, err := r.graphStore.UpdateRelationStats(ctx,
					associations[i].EntityID,
					associations[j].EntityID,
					"related_to",
				); err != nil {
					logger.Debug("resolver: update relation stats failed", zap.Error(err))
				}
			}
		}
	}
	return nil
}
