package search_test

import (
	"testing"

	"iclude/internal/model"
	"iclude/internal/search"

	"github.com/stretchr/testify/assert"
)

func TestMergeRRF(t *testing.T) {
	tests := []struct {
		name       string
		resultSets [][]*model.SearchResult
		limit      int
		wantLen    int
		wantFirst  string // 期望排名第一的 memory ID
	}{
		{
			name: "two overlapping sets",
			resultSets: [][]*model.SearchResult{
				{
					{Memory: &model.Memory{ID: "a"}, Score: 10, Source: "sqlite"},
					{Memory: &model.Memory{ID: "b"}, Score: 5, Source: "sqlite"},
					{Memory: &model.Memory{ID: "c"}, Score: 1, Source: "sqlite"},
				},
				{
					{Memory: &model.Memory{ID: "b"}, Score: 0.9, Source: "qdrant"},
					{Memory: &model.Memory{ID: "a"}, Score: 0.8, Source: "qdrant"},
					{Memory: &model.Memory{ID: "d"}, Score: 0.7, Source: "qdrant"},
				},
			},
			limit:     10,
			wantLen:   4,
			wantFirst: "a", // a: rank 0 + rank 1; b: rank 1 + rank 0 → same RRF; a should win or tie
		},
		{
			name: "single set passthrough",
			resultSets: [][]*model.SearchResult{
				{
					{Memory: &model.Memory{ID: "x"}, Score: 1, Source: "sqlite"},
					{Memory: &model.Memory{ID: "y"}, Score: 0.5, Source: "sqlite"},
				},
			},
			limit:     10,
			wantLen:   2,
			wantFirst: "x",
		},
		{
			name:       "empty sets",
			resultSets: [][]*model.SearchResult{},
			limit:      10,
			wantLen:    0,
		},
		{
			name: "limit applied",
			resultSets: [][]*model.SearchResult{
				{
					{Memory: &model.Memory{ID: "a"}, Score: 1},
					{Memory: &model.Memory{ID: "b"}, Score: 0.5},
					{Memory: &model.Memory{ID: "c"}, Score: 0.1},
				},
			},
			limit:   2,
			wantLen: 2,
		},
		{
			name: "disjoint sets",
			resultSets: [][]*model.SearchResult{
				{
					{Memory: &model.Memory{ID: "a"}, Score: 1, Source: "sqlite"},
				},
				{
					{Memory: &model.Memory{ID: "b"}, Score: 0.9, Source: "qdrant"},
				},
			},
			limit:   10,
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := search.MergeRRF(tt.resultSets, tt.limit)
			assert.Len(t, results, tt.wantLen)

			if tt.wantFirst != "" && len(results) > 0 {
				assert.Equal(t, tt.wantFirst, results[0].Memory.ID)
			}

			// 所有融合结果 Source 应为 hybrid
			for _, r := range results {
				assert.Equal(t, "hybrid", r.Source)
				assert.Greater(t, r.Score, float64(0))
			}

			// 结果应按分数降序排列
			for i := 1; i < len(results); i++ {
				assert.GreaterOrEqual(t, results[i-1].Score, results[i].Score)
			}
		})
	}
}

func TestMergeRRFWithK_CustomK(t *testing.T) {
	sets := [][]*model.SearchResult{
		{
			{Memory: &model.Memory{ID: "a"}, Score: 1},
		},
	}

	// k=0 应使用默认值 60
	r1 := search.MergeRRFWithK(sets, 10, 0)
	r2 := search.MergeRRFWithK(sets, 10, 60)
	assert.Equal(t, r1[0].Score, r2[0].Score)

	// k=1 应给出不同分数
	r3 := search.MergeRRFWithK(sets, 10, 1)
	assert.NotEqual(t, r1[0].Score, r3[0].Score)
}
