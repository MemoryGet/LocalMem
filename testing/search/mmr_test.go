// Package search_test MMR 多样性重排测试 / MMR diversity re-ranking tests
package search_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockVectorStore 用于 MMR 测试的 VectorStore stub
type mockVectorStore struct {
	vectors map[string][]float32
}

func (m *mockVectorStore) GetVectors(ctx context.Context, ids []string) (map[string][]float32, error) {
	result := make(map[string][]float32)
	for _, id := range ids {
		if v, ok := m.vectors[id]; ok {
			result[id] = v
		}
	}
	return result, nil
}

func (m *mockVectorStore) Upsert(ctx context.Context, id string, vec []float32, payload map[string]any) error {
	return nil
}
func (m *mockVectorStore) Search(ctx context.Context, vec []float32, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
	return nil, nil
}
func (m *mockVectorStore) SearchFiltered(ctx context.Context, vec []float32, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	return nil, nil
}
func (m *mockVectorStore) Delete(ctx context.Context, id string) error { return nil }
func (m *mockVectorStore) Init(ctx context.Context) error              { return nil }
func (m *mockVectorStore) Close() error                                { return nil }

func makeMMRResult(id string, score float64) *model.SearchResult {
	return &model.SearchResult{
		Memory: &model.Memory{ID: id},
		Score:  score,
		Source: "sqlite",
	}
}

// TestMMRRerank_EmptyInput 空输入直接返回 / Empty input returns as-is
func TestMMRRerank_EmptyInput(t *testing.T) {
	vs := &mockVectorStore{vectors: map[string][]float32{}}
	result := search.MMRRerank(context.Background(), nil, vs, 0.7, 5)
	assert.Nil(t, result)
}

// TestMMRRerank_SingleResult 单条结果直接返回 / Single result bypasses MMR
func TestMMRRerank_SingleResult(t *testing.T) {
	vs := &mockVectorStore{vectors: map[string][]float32{"a": {1, 0, 0}}}
	results := []*model.SearchResult{makeMMRResult("a", 1.0)}
	out := search.MMRRerank(context.Background(), results, vs, 0.7, 5)
	require.Len(t, out, 1)
	assert.Equal(t, "a", out[0].Memory.ID)
}

// TestMMRRerank_NilVecStore VectorStore 为 nil 时跳过重排 / Nil vecStore skips re-ranking
func TestMMRRerank_NilVecStore(t *testing.T) {
	results := []*model.SearchResult{
		makeMMRResult("a", 1.0),
		makeMMRResult("b", 0.8),
	}
	out := search.MMRRerank(context.Background(), results, nil, 0.7, 5)
	assert.Equal(t, results, out)
}

// TestMMRRerank_DiversityEffect lambda=0 时更倾向多样性 / lambda=0 maximizes diversity
func TestMMRRerank_DiversityEffect(t *testing.T) {
	// a 和 b 向量相似（高余弦），c 与 a/b 不同
	// lambda=0 → 纯多样性：第二条应选 c 而非 b
	vs := &mockVectorStore{
		vectors: map[string][]float32{
			"a": {1, 0, 0},
			"b": {0.99, 0.01, 0},  // 与 a 非常相似
			"c": {0, 1, 0},        // 与 a 正交（不相似）
		},
	}
	results := []*model.SearchResult{
		makeMMRResult("a", 1.0), // highest score, will be selected first
		makeMMRResult("b", 0.9),
		makeMMRResult("c", 0.5),
	}
	out := search.MMRRerank(context.Background(), results, vs, 0.0, 2)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Memory.ID)
	// lambda=0 下 c 应优先于 b（因为 c 与 a 更多样）
	assert.Equal(t, "c", out[1].Memory.ID)
}

// TestMMRRerank_TopKLimit topK 限制输出数量 / topK correctly limits output
func TestMMRRerank_TopKLimit(t *testing.T) {
	vs := &mockVectorStore{
		vectors: map[string][]float32{
			"a": {1, 0},
			"b": {0, 1},
			"c": {1, 1},
		},
	}
	results := []*model.SearchResult{
		makeMMRResult("a", 1.0),
		makeMMRResult("b", 0.9),
		makeMMRResult("c", 0.8),
	}
	out := search.MMRRerank(context.Background(), results, vs, 0.7, 2)
	assert.Len(t, out, 2)
}

// TestMMRRerank_MissingVectors 没有向量的记忆跳过 / Records without vectors are skipped
func TestMMRRerank_MissingVectors(t *testing.T) {
	vs := &mockVectorStore{
		vectors: map[string][]float32{
			"a": {1, 0},
			// "b" has no vector
		},
	}
	results := []*model.SearchResult{
		makeMMRResult("a", 1.0),
		makeMMRResult("b", 0.5),
	}
	// Should not panic even with missing vectors
	out := search.MMRRerank(context.Background(), results, vs, 0.7, 5)
	assert.NotNil(t, out)
}
