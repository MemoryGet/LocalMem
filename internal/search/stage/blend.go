// Package stage 检索管线阶段实现 / Pipeline stage implementations
package stage

import (
	"sort"

	"iclude/internal/model"
)

// blendScores 统一分数混合 / Unified score blending for rerankers
// Scans maxBaseScore from candidates, normalizes base scores, blends with external scores,
// returns copies sorted by final score descending.
// Only candidates present in externalScores are included in the output.
func blendScores(candidates []*model.SearchResult, externalScores map[int]float64, weight float64) []*model.SearchResult {
	if len(candidates) == 0 || len(externalScores) == 0 {
		return nil
	}

	maxBase := findMaxBaseScore(candidates)

	type scored struct {
		res   *model.SearchResult
		final float64
	}

	items := make([]scored, 0, len(externalScores))
	for idx, ext := range externalScores {
		if idx < 0 || idx >= len(candidates) || candidates[idx] == nil {
			continue
		}
		baseNorm := candidates[idx].Score / maxBase
		final := (1-weight)*baseNorm + weight*ext
		items = append(items, scored{
			res:   candidates[idx],
			final: final,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].final > items[j].final
	})

	out := make([]*model.SearchResult, 0, len(items))
	for _, item := range items {
		resCopy := *item.res
		resCopy.Score = item.final
		out = append(out, &resCopy)
	}
	return out
}

// findMaxBaseScore 查找候选中最大基础分数 / Find max base score among candidates
func findMaxBaseScore(candidates []*model.SearchResult) float64 {
	maxScore := 0.0
	for _, c := range candidates {
		if c != nil && c.Score > maxScore {
			maxScore = c.Score
		}
	}
	if maxScore <= 0 {
		maxScore = 1
	}
	return maxScore
}
