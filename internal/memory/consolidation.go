package memory

import (
	"context"
	"fmt"
	"math"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Consolidator 记忆归纳引擎 / Memory consolidation engine
// 定期找到相似记忆簇，用 LLM 归纳为浓缩版永久记忆
type Consolidator struct {
	memStore store.MemoryStore
	vecStore store.VectorStore // 可为 nil / may be nil
	llm      llm.Provider     // 可为 nil / may be nil
}

// NewConsolidator 创建归纳引擎 / Create a new consolidator
func NewConsolidator(memStore store.MemoryStore, vecStore store.VectorStore, llmProvider llm.Provider) *Consolidator {
	return &Consolidator{
		memStore: memStore,
		vecStore: vecStore,
		llm:      llmProvider,
	}
}

// Run 执行一次归纳（由调度器调用）/ Execute one consolidation run
func (c *Consolidator) Run(ctx context.Context) error {
	cfg := config.GetConfig()
	if c.vecStore == nil || c.llm == nil {
		logger.Debug("consolidation: skipped (vecStore or llm unavailable)")
		return nil
	}

	// 获取候选记忆
	candidates, err := c.selectCandidates(ctx, cfg.Consolidation)
	if err != nil {
		return fmt.Errorf("consolidation: failed to select candidates: %w", err)
	}
	if len(candidates) < 2 {
		logger.Debug("consolidation: not enough candidates", zap.Int("count", len(candidates)))
		return nil
	}

	// 获取向量
	ids := make([]string, len(candidates))
	for i, m := range candidates {
		ids[i] = m.ID
	}
	vectors, err := c.vecStore.GetVectors(ctx, ids)
	if err != nil {
		return fmt.Errorf("consolidation: failed to get vectors: %w", err)
	}

	// 层次聚类
	clusters := agglomerativeClustering(candidates, vectors, cfg.Consolidation.SimilarityThreshold, cfg.Consolidation.MinClusterSize)
	if len(clusters) == 0 {
		logger.Debug("consolidation: no clusters found")
		return nil
	}

	logger.Info("consolidation: clusters found", zap.Int("clusters", len(clusters)))

	// 对每个簇执行归纳
	for i, cluster := range clusters {
		if err := c.consolidateCluster(ctx, cluster, i); err != nil {
			logger.Warn("consolidation: cluster failed",
				zap.Int("cluster", i),
				zap.Error(err),
			)
		}
	}

	return nil
}

// selectCandidates 选取候选记忆 / Select candidate memories for consolidation
func (c *Consolidator) selectCandidates(ctx context.Context, cfg config.ConsolidationConfig) ([]*model.Memory, error) {
	// 简化实现：通过 List 获取，按条件过滤（系统身份查看所有公开+团队记忆）
	sysIdentity := &model.Identity{TeamID: "default", OwnerID: model.SystemOwnerID}
	memories, err := c.memStore.List(ctx, sysIdentity, 0, cfg.MaxMemoriesPerRun)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().AddDate(0, 0, -cfg.MinAgeDays)
	var candidates []*model.Memory
	for _, m := range memories {
		if m.CreatedAt.After(cutoff) {
			continue
		}
		if m.RetentionTier == model.TierPermanent || m.RetentionTier == model.TierEphemeral {
			continue
		}
		if m.ConsolidatedInto != "" {
			continue
		}
		if m.Kind == "consolidated" {
			continue
		}
		candidates = append(candidates, m)
	}
	return candidates, nil
}

// consolidateCluster 归纳一个簇 / Consolidate a single cluster
func (c *Consolidator) consolidateCluster(ctx context.Context, cluster []*model.Memory, idx int) error {
	// 构建 LLM prompt
	var contents string
	maxStrength := 0.0
	totalReinforced := 0
	for _, m := range cluster {
		contents += fmt.Sprintf("- %s\n", m.Content)
		if m.Strength > maxStrength {
			maxStrength = m.Strength
		}
		totalReinforced += m.ReinforcedCount
	}

	prompt := fmt.Sprintf("Consolidate these %d related memories into one comprehensive memory. Preserve all unique facts. Remove redundancy. Output only the consolidated text:\n\n%s", len(cluster), contents)

	resp, err := c.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are a memory consolidation engine. Output only the consolidated memory content, nothing else."},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return fmt.Errorf("LLM consolidation failed: %w", err)
	}

	// 创建归纳记忆
	consolidated := &model.Memory{
		Content:       resp.Content,
		RetentionTier: model.TierPermanent,
		Kind:          "consolidated",
		Strength:      math.Min(maxStrength*1.1, 1.0),
		SourceType:    "consolidation",
	}
	if len(cluster) > 0 {
		consolidated.Scope = cluster[0].Scope
		consolidated.TeamID = cluster[0].TeamID
	}
	ResolveTierDefaults(consolidated)

	if err := c.memStore.Create(ctx, consolidated); err != nil {
		return fmt.Errorf("failed to create consolidated memory: %w", err)
	}

	// soft-delete 原始记忆并设置 consolidated_into
	for _, m := range cluster {
		m.ConsolidatedInto = consolidated.ID
		if err := c.memStore.Update(ctx, m); err != nil {
			logger.Warn("consolidation: failed to set consolidated_into",
				zap.String("memory_id", m.ID),
				zap.Error(err),
			)
		}
		if err := c.memStore.SoftDelete(ctx, m.ID); err != nil {
			logger.Warn("consolidation: failed to soft-delete source",
				zap.String("memory_id", m.ID),
				zap.Error(err),
			)
		}
	}

	logger.Info("consolidation: cluster merged",
		zap.Int("cluster", idx),
		zap.Int("source_count", len(cluster)),
		zap.String("consolidated_id", consolidated.ID),
	)
	return nil
}

// agglomerativeClustering 层次聚类 / Agglomerative clustering with average linkage
func agglomerativeClustering(memories []*model.Memory, vectors map[string][]float32, simThreshold float64, minSize int) [][]*model.Memory {
	n := len(memories)
	if n < 2 {
		return nil
	}

	// 初始：每个记忆一个簇
	type cluster struct {
		members []*model.Memory
	}
	clusters := make([]*cluster, n)
	for i, m := range memories {
		clusters[i] = &cluster{members: []*model.Memory{m}}
	}

	// 计算平均链接距离
	avgDistance := func(a, b *cluster) float64 {
		total := 0.0
		count := 0
		for _, ma := range a.members {
			va := vectors[ma.ID]
			if len(va) == 0 {
				continue
			}
			for _, mb := range b.members {
				vb := vectors[mb.ID]
				if len(vb) == 0 {
					continue
				}
				total += 1.0 - cosineSimFloat32(va, vb)
				count++
			}
		}
		if count == 0 {
			return 1.0
		}
		return total / float64(count)
	}

	distThreshold := 1.0 - simThreshold

	// 贪心合并
	for {
		bestDist := 2.0
		bestI, bestJ := -1, -1

		for i := 0; i < len(clusters); i++ {
			if clusters[i] == nil {
				continue
			}
			for j := i + 1; j < len(clusters); j++ {
				if clusters[j] == nil {
					continue
				}
				d := avgDistance(clusters[i], clusters[j])
				if d < bestDist {
					bestDist = d
					bestI = i
					bestJ = j
				}
			}
		}

		if bestI < 0 || bestDist > distThreshold {
			break
		}

		// 合并 j 到 i
		clusters[bestI].members = append(clusters[bestI].members, clusters[bestJ].members...)
		clusters[bestJ] = nil
	}

	// 收集满足最小大小的簇
	var result [][]*model.Memory
	for _, c := range clusters {
		if c != nil && len(c.members) >= minSize {
			result = append(result, c.members)
		}
	}
	return result
}

// cosineSimFloat32 两个 float32 向量的余弦相似度
func cosineSimFloat32(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	d := math.Sqrt(na) * math.Sqrt(nb)
	if d == 0 {
		return 0
	}
	return dot / d
}
