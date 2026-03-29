package search

import (
	"context"
	"math"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/mathutil"

	"go.uber.org/zap"
)

// MMRRerank 最大边际相关性重排 / Maximal Marginal Relevance re-ranking
// 在 RRF 融合结果中选择既相关又多样的子集
// lambda: 相关性权重（0~1），推荐 0.7; topK: 最终返回数量
func MMRRerank(ctx context.Context, results []*model.SearchResult, vecStore store.VectorStore, lambda float64, topK int) []*model.SearchResult {
	if len(results) <= 1 || vecStore == nil {
		return results
	}
	if topK <= 0 || topK > len(results) {
		topK = len(results)
	}

	// 收集需要获取向量的 ID
	ids := make([]string, 0, len(results))
	for _, r := range results {
		if r.Memory != nil && r.Memory.ID != "" {
			ids = append(ids, r.Memory.ID)
		}
	}

	// 获取向量
	vectors, err := vecStore.GetVectors(ctx, ids)
	if err != nil {
		logger.Warn("MMR: failed to get vectors, skipping re-ranking", zap.Error(err))
		return results
	}
	if len(vectors) == 0 {
		return results
	}

	// 归一化 RRF 分数到 [0,1]
	maxScore := results[0].Score
	if maxScore <= 0 {
		maxScore = 1.0
	}

	// 贪心迭代选择
	selected := make([]*model.SearchResult, 0, topK)
	remaining := make(map[int]bool, len(results))
	for i := range results {
		remaining[i] = true
	}

	// 第一个永远选最高分
	selected = append(selected, results[0])
	delete(remaining, 0)

	for len(selected) < topK && len(remaining) > 0 {
		bestScore := math.Inf(-1)
		bestIdx := -1

		for i := range remaining {
			normScore := results[i].Score / maxScore
			vec := vectors[results[i].Memory.ID]
			if len(vec) == 0 {
				// 没有向量的结果只考虑相关性
				mmrScore := lambda * normScore
				if mmrScore > bestScore {
					bestScore = mmrScore
					bestIdx = i
				}
				continue
			}

			// 计算与已选集合的最大相似度
			maxSim := 0.0
			for _, s := range selected {
				sv := vectors[s.Memory.ID]
				if len(sv) == 0 {
					continue
				}
				sim := mathutil.CosineSimilarity(vec, sv)
				if sim > maxSim {
					maxSim = sim
				}
			}

			mmrScore := lambda*normScore - (1-lambda)*maxSim
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}
		selected = append(selected, results[bestIdx])
		delete(remaining, bestIdx)
	}

	return selected
}

