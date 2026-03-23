// Package search_test 加权 RRF 测试 / Weighted RRF tests
package search_test

import (
	"testing"

	"iclude/internal/model"
	"iclude/internal/search"

	"github.com/stretchr/testify/assert"
)

func makeResult(id string, score float64, source string) *model.SearchResult {
	return &model.SearchResult{
		Memory: &model.Memory{ID: id, Content: "content-" + id},
		Score:  score,
		Source: source,
	}
}

func TestWeightedRRF_EqualWeights(t *testing.T) {
	set1 := []*model.SearchResult{makeResult("a", 1.0, "sqlite"), makeResult("b", 0.5, "sqlite")}
	set2 := []*model.SearchResult{makeResult("b", 1.0, "qdrant"), makeResult("c", 0.5, "qdrant")}

	// 等权加权 RRF 应与标准 RRF 结果一致
	weighted := search.MergeWeightedRRF([]search.RRFInput{
		{Results: set1, Weight: 1.0},
		{Results: set2, Weight: 1.0},
	}, 60, 10)

	standard := search.MergeRRFWithK([][]*model.SearchResult{set1, set2}, 10, 60)

	assert.Len(t, weighted, 3)
	assert.Len(t, standard, 3)
	// 相同 ID 的分数应相等
	wScores := map[string]float64{}
	sScores := map[string]float64{}
	for _, r := range weighted {
		wScores[r.Memory.ID] = r.Score
	}
	for _, r := range standard {
		sScores[r.Memory.ID] = r.Score
	}
	for id, ws := range wScores {
		assert.InDelta(t, sScores[id], ws, 0.0001, "scores should match for %s", id)
	}
}

func TestWeightedRRF_GraphLowerWeight(t *testing.T) {
	// "a" 在两路中都排第1，但权重不同
	set1 := []*model.SearchResult{makeResult("a", 1.0, "sqlite")}
	set2 := []*model.SearchResult{makeResult("b", 1.0, "graph")}

	results := search.MergeWeightedRRF([]search.RRFInput{
		{Results: set1, Weight: 1.0},
		{Results: set2, Weight: 0.8},
	}, 60, 10)

	assert.Len(t, results, 2)
	// "a" 权重 1.0 应排在 "b" 权重 0.8 前面
	assert.Equal(t, "a", results[0].Memory.ID)
	assert.Equal(t, "b", results[1].Memory.ID)
	assert.Greater(t, results[0].Score, results[1].Score)
}

func TestWeightedRRF_ThreeWayFusion(t *testing.T) {
	set1 := []*model.SearchResult{makeResult("a", 1.0, "sqlite"), makeResult("b", 0.5, "sqlite")}
	set2 := []*model.SearchResult{makeResult("b", 1.0, "qdrant"), makeResult("c", 0.5, "qdrant")}
	set3 := []*model.SearchResult{makeResult("c", 1.0, "graph"), makeResult("d", 0.5, "graph")}

	results := search.MergeWeightedRRF([]search.RRFInput{
		{Results: set1, Weight: 1.0},
		{Results: set2, Weight: 1.0},
		{Results: set3, Weight: 0.8},
	}, 60, 10)

	assert.Len(t, results, 4)
	// "b" 出现在两路中（权重 1.0+1.0），应排最前
	assert.Equal(t, "b", results[0].Memory.ID)
}

func TestWeightedRRF_TwoWayFusion(t *testing.T) {
	set1 := []*model.SearchResult{makeResult("a", 1.0, "sqlite")}
	set2 := []*model.SearchResult{makeResult("b", 1.0, "qdrant")}

	// 空的第三路不影响结果
	results := search.MergeWeightedRRF([]search.RRFInput{
		{Results: set1, Weight: 1.0},
		{Results: set2, Weight: 1.0},
		{Results: nil, Weight: 0.8}, // empty
	}, 60, 10)

	assert.Len(t, results, 2)
}

func TestWeightedRRF_ZeroWeight(t *testing.T) {
	set1 := []*model.SearchResult{makeResult("a", 1.0, "sqlite")}
	set2 := []*model.SearchResult{makeResult("b", 1.0, "graph")}

	results := search.MergeWeightedRRF([]search.RRFInput{
		{Results: set1, Weight: 1.0},
		{Results: set2, Weight: 0}, // zero weight, excluded
	}, 60, 10)

	assert.Len(t, results, 1)
	assert.Equal(t, "a", results[0].Memory.ID)
}
