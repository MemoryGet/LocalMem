package search

import (
	"sort"

	"iclude/internal/model"
)

const defaultRRFK = 60

// MergeRRF 使用 Reciprocal Rank Fusion 融合多路检索结果 / Merge results using RRF algorithm
func MergeRRF(resultSets [][]*model.SearchResult, limit int) []*model.SearchResult {
	return MergeRRFWithK(resultSets, limit, defaultRRFK)
}

// MergeRRFWithK 使用自定义 k 值的 RRF 融合 / Merge with custom k parameter
func MergeRRFWithK(resultSets [][]*model.SearchResult, limit int, k int) []*model.SearchResult {
	if k <= 0 {
		k = defaultRRFK
	}

	// 按 memory ID 累加 RRF 分数
	scores := make(map[string]float64)
	memMap := make(map[string]*model.Memory)

	for _, results := range resultSets {
		for rank, r := range results {
			id := r.Memory.ID
			scores[id] += 1.0 / float64(k+rank+1)
			if existing, ok := memMap[id]; !ok || existing.Content == "" {
				memMap[id] = r.Memory
			}
		}
	}

	// 构建融合结果
	merged := make([]*model.SearchResult, 0, len(scores))
	for id, score := range scores {
		merged = append(merged, &model.SearchResult{
			Memory: memMap[id],
			Score:  score,
			Source: "hybrid",
		})
	}

	// 按 RRF 分数降序排列
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// RRFInput 加权RRF输入 / Weighted RRF input
type RRFInput struct {
	Results []*model.SearchResult
	Weight  float64
}

// MergeWeightedRRF 加权RRF融合 / Weighted RRF fusion
// score(id) = Σ weight × 1/(k + rank + 1)
func MergeWeightedRRF(inputs []RRFInput, k int, limit int) []*model.SearchResult {
	if k <= 0 {
		k = defaultRRFK
	}

	scores := make(map[string]float64)
	memMap := make(map[string]*model.Memory)

	for _, input := range inputs {
		if input.Weight == 0 {
			continue
		}
		for rank, r := range input.Results {
			id := r.Memory.ID
			scores[id] += input.Weight * (1.0 / float64(k+rank+1))
			if existing, ok := memMap[id]; !ok || existing.Content == "" {
				memMap[id] = r.Memory
			}
		}
	}

	merged := make([]*model.SearchResult, 0, len(scores))
	for id, score := range scores {
		merged = append(merged, &model.SearchResult{
			Memory: memMap[id],
			Score:  score,
			Source: "hybrid",
		})
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}
