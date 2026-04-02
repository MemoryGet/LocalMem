package eval

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMRR 测试平均倒数排名 / Test Mean Reciprocal Rank
func TestMRR(t *testing.T) {
	cases := []struct {
		name     string
		ranks    []int
		expected float64
	}{
		{
			name:     "all rank 1",
			ranks:    []int{1, 1, 1},
			expected: 1.0,
		},
		{
			name:     "mixed ranks",
			ranks:    []int{1, 2, 4},
			expected: (1.0 + 0.5 + 0.25) / 3.0,
		},
		{
			name:     "some miss",
			ranks:    []int{1, -1, 2},
			expected: (1.0 + 0.0 + 0.5) / 3.0,
		},
		{
			name:     "all miss",
			ranks:    []int{-1, -1, -1},
			expected: 0.0,
		},
		{
			name:     "empty",
			ranks:    []int{},
			expected: 0.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MRR(tc.ranks)
			assert.InDelta(t, tc.expected, got, 0.001)
		})
	}
}

// TestRecallAtK 测试 Top-K 召回率 / Test Recall at K
func TestRecallAtK(t *testing.T) {
	cases := []struct {
		name     string
		ranks    []int
		k        int
		expected float64
	}{
		{
			name:     "all in top-k",
			ranks:    []int{1, 2, 3},
			k:        5,
			expected: 1.0,
		},
		{
			name:     "some in top-k",
			ranks:    []int{1, 6, 3},
			k:        5,
			expected: 2.0 / 3.0,
		},
		{
			name:     "none in top-k",
			ranks:    []int{6, 7, 8},
			k:        5,
			expected: 0.0,
		},
		{
			name:     "miss counts as not in top-k",
			ranks:    []int{-1, -1, 2},
			k:        5,
			expected: 1.0 / 3.0,
		},
		{
			name:     "empty",
			ranks:    []int{},
			k:        5,
			expected: 0.0,
		},
		{
			name:     "recall@1",
			ranks:    []int{1, 2, -1},
			k:        1,
			expected: 1.0 / 3.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RecallAtK(tc.ranks, tc.k)
			assert.InDelta(t, tc.expected, got, 0.001)
		})
	}
}

// TestNDCGAtK 测试归一化折损累积增益 / Test Normalized DCG at K
func TestNDCGAtK(t *testing.T) {
	cases := []struct {
		name     string
		ranks    []int
		k        int
		expected float64
	}{
		{
			name:     "hit at rank 1",
			ranks:    []int{1},
			k:        5,
			expected: 1.0,
		},
		{
			name:     "hit at rank 2 (DCG = 1/log2(3))",
			ranks:    []int{2},
			k:        5,
			expected: 1.0 / math.Log2(3),
		},
		{
			name:     "miss",
			ranks:    []int{-1},
			k:        5,
			expected: 0.0,
		},
		{
			name:     "empty",
			ranks:    []int{},
			k:        5,
			expected: 0.0,
		},
		{
			name:     "mixed: hit@1 and hit@3",
			ranks:    []int{1, 3},
			k:        5,
			expected: (1.0 + 1.0/math.Log2(4)) / 2.0,
		},
		{
			name:     "rank beyond k counts as miss",
			ranks:    []int{6},
			k:        5,
			expected: 0.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NDCGAtK(tc.ranks, tc.k)
			assert.InDelta(t, tc.expected, got, 0.001)
		})
	}
}

// TestHitRate 测试命中率 / Test HitRate
func TestHitRate(t *testing.T) {
	cases := []struct {
		name     string
		ranks    []int
		expected float64
	}{
		{
			name:     "all hit",
			ranks:    []int{1, 2, 3},
			expected: 100.0,
		},
		{
			name:     "half hit",
			ranks:    []int{1, -1, 3, -1},
			expected: 50.0,
		},
		{
			name:     "none hit",
			ranks:    []int{-1, -1},
			expected: 0.0,
		},
		{
			name:     "empty",
			ranks:    []int{},
			expected: 0.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HitRate(tc.ranks)
			assert.InDelta(t, tc.expected, got, 0.001)
		})
	}
}

// TestAggregate 测试聚合指标计算 / Test Aggregate metrics computation
func TestAggregate(t *testing.T) {
	results := []CaseResult{
		{Query: "q1", Expected: "e1", Category: "factual", Difficulty: "easy", Hit: true, Rank: 1, Score: 0.9, ResultCount: 5},
		{Query: "q2", Expected: "e2", Category: "factual", Difficulty: "medium", Hit: true, Rank: 3, Score: 0.7, ResultCount: 5},
		{Query: "q3", Expected: "e3", Category: "semantic", Difficulty: "hard", Hit: false, Rank: -1, Score: 0.0, ResultCount: 5},
		{Query: "q4", Expected: "e4", Category: "semantic", Difficulty: "easy", Hit: true, Rank: 2, Score: 0.8, ResultCount: 5},
	}

	m := Aggregate(results)

	assert.Equal(t, 4, m.Total)
	// 3 out of 4 hit → hit_rate = 75%
	assert.InDelta(t, 75.0, m.HitRate, 0.001)
	// MRR: (1/1 + 1/3 + 0 + 1/2) / 4
	assert.InDelta(t, (1.0+1.0/3.0+0+0.5)/4.0, m.MRR, 0.001)
	// RecallAt1: only q1 hits at rank 1 → 1/4
	assert.InDelta(t, 0.25, m.RecallAt1, 0.001)
	// RecallAt3: q1(rank1), q2(rank3), q4(rank2) → 3/4
	assert.InDelta(t, 0.75, m.RecallAt3, 0.001)
	// RecallAt5: same 3 hits → 3/4
	assert.InDelta(t, 0.75, m.RecallAt5, 0.001)
	// RecallAt10: same 3 hits → 3/4
	assert.InDelta(t, 0.75, m.RecallAt10, 0.001)
	// NDCG5 > 0 (some hits within k=5)
	assert.Greater(t, m.NDCG5, 0.0)
	// NDCG10 > 0
	assert.Greater(t, m.NDCG10, 0.0)
}

// TestAggregate_Empty 测试空结果集 / Test empty result set
func TestAggregate_Empty(t *testing.T) {
	m := Aggregate([]CaseResult{})
	assert.Equal(t, 0, m.Total)
	assert.InDelta(t, 0.0, m.HitRate, 0.001)
	assert.InDelta(t, 0.0, m.MRR, 0.001)
	assert.InDelta(t, 0.0, m.RecallAt1, 0.001)
	assert.InDelta(t, 0.0, m.NDCG5, 0.001)
}
