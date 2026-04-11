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
	centroidMgr    *CentroidManager
	vecStore       store.VectorStore
	cfg            config.ResolverConfig
}

// NewEntityResolver 创建实体解析器 / Create entity resolver
func NewEntityResolver(
	tok tokenizer.Tokenizer,
	graphStore store.GraphStore,
	candidateStore store.CandidateStore,
	centroidMgr *CentroidManager,
	vecStore store.VectorStore,
	cfg config.ResolverConfig,
) *EntityResolver {
	return &EntityResolver{
		tokenizer:      tok,
		graphStore:     graphStore,
		candidateStore: candidateStore,
		centroidMgr:    centroidMgr,
		vecStore:       vecStore,
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

// ResolveLayer2 实体质心匹配（Layer 2）/ Entity centroid matching
func (r *EntityResolver) ResolveLayer2(ctx context.Context, embedding []float32) ([]EntityAssociation, error) {
	if r.centroidMgr == nil || len(embedding) == 0 {
		return nil, nil
	}
	matches, err := r.centroidMgr.SearchSimilar(ctx, embedding, 10, r.cfg.CentroidThreshold)
	if err != nil {
		return nil, err
	}
	var associations []EntityAssociation
	for _, m := range matches {
		associations = append(associations, EntityAssociation{
			EntityID:   m.EntityID,
			Confidence: 0.7,
		})
	}
	return associations, nil
}

// ResolveLayer3 近邻传播（Layer 3）/ Neighbor propagation
func (r *EntityResolver) ResolveLayer3(ctx context.Context, embedding []float32) ([]EntityAssociation, error) {
	if r.vecStore == nil || len(embedding) == 0 {
		return nil, nil
	}
	neighbors, err := r.vecStore.Search(ctx, embedding, nil, r.cfg.NeighborK)
	if err != nil {
		return nil, err
	}
	if len(neighbors) == 0 {
		return nil, nil
	}
	memIDs := make([]string, 0, len(neighbors))
	for _, n := range neighbors {
		if n.Memory != nil {
			memIDs = append(memIDs, n.Memory.ID)
		}
	}
	entitiesMap, err := r.graphStore.GetMemoriesEntities(ctx, memIDs)
	if err != nil {
		return nil, err
	}
	freq := make(map[string]int)
	for _, entities := range entitiesMap {
		for _, e := range entities {
			freq[e.ID]++
		}
	}
	var associations []EntityAssociation
	for entityID, count := range freq {
		if count >= r.cfg.NeighborMinCount {
			associations = append(associations, EntityAssociation{
				EntityID:   entityID,
				Confidence: 0.5,
			})
		}
	}
	return associations, nil
}

// mergeAssociations 合并三层结果 / Merge associations from multiple layers
func mergeAssociations(layers ...[]EntityAssociation) []EntityAssociation {
	type entry struct {
		maxConf    float64
		layerCount int
	}
	byEntity := make(map[string]entry)
	for _, layer := range layers {
		seen := make(map[string]bool)
		for _, a := range layer {
			if seen[a.EntityID] {
				continue
			}
			seen[a.EntityID] = true
			e := byEntity[a.EntityID]
			if a.Confidence > e.maxConf {
				e.maxConf = a.Confidence
			}
			e.layerCount++
			byEntity[a.EntityID] = e
		}
	}
	var result []EntityAssociation
	for entityID, e := range byEntity {
		conf := e.maxConf
		if e.layerCount > 1 {
			conf += 0.1
		}
		if conf > 1.0 {
			conf = 1.0
		}
		result = append(result, EntityAssociation{EntityID: entityID, Confidence: conf})
	}
	return result
}

// writeAssociations 写入关联和共现关系 / Write associations and co-occurrence relations
func (r *EntityResolver) writeAssociations(ctx context.Context, memoryID string, associations []EntityAssociation) {
	for _, assoc := range associations {
		me := &model.MemoryEntity{
			MemoryID: memoryID, EntityID: assoc.EntityID,
			Role: "mentioned", Confidence: assoc.Confidence,
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
			_, _ = r.graphStore.UpdateRelationStats(ctx,
				associations[i].EntityID, associations[j].EntityID, "related_to")
		}
	}
}

// ResolveWithEmbeddings 三层解析 / Three-layer resolution with embeddings
func (r *EntityResolver) ResolveWithEmbeddings(ctx context.Context, memories []*model.Memory, embeddings [][]float32) error {
	for i, mem := range memories {
		var embedding []float32
		if i < len(embeddings) {
			embedding = embeddings[i]
		}

		l1, _ := r.ResolveLayer1(ctx, mem)
		var l2, l3 []EntityAssociation
		if len(embedding) > 0 {
			l2, _ = r.ResolveLayer2(ctx, embedding)
			l3, _ = r.ResolveLayer3(ctx, embedding)
		}

		merged := mergeAssociations(l1, l2, l3)
		r.writeAssociations(ctx, mem.ID, merged)
	}
	return nil
}

// Resolve 兼容无 embedding 的调用 / Compatible call without embeddings
func (r *EntityResolver) Resolve(ctx context.Context, memories []*model.Memory) error {
	return r.ResolveWithEmbeddings(ctx, memories, nil)
}
