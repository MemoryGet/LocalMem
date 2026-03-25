// Package search_test 性能基准测试 / Performance benchmarks for Phase 2 success metrics
//
// 目标指标 / Target metrics:
//   - 三路 RRF 融合 P99 ≤ 300ms (含 SQLite FTS5 I/O)
//   - MMR 重排 overhead 可接受（≤ 100ms per 50 results）
package search_test

import (
	"context"
	"fmt"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search"
)

// ---- RRF Benchmarks ----

// BenchmarkWeightedRRF_ThreeWay 三路加权 RRF 融合纯计算耗时 / Three-way weighted RRF fusion computation time
func BenchmarkWeightedRRF_ThreeWay(b *testing.B) {
	sizes := []int{10, 50, 100}

	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			set1 := makeResultSet("fts", n)
			set2 := makeResultSet("qdrant", n)
			set3 := makeResultSet("graph", n)

			inputs := []search.RRFInput{
				{Results: set1, Weight: 1.0},
				{Results: set2, Weight: 1.0},
				{Results: set3, Weight: 0.8},
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = search.MergeWeightedRRF(inputs, 60, n)
			}
		})
	}
}

// BenchmarkWeightedRRF_TwoWay 双路 RRF 融合（SQLite-only 模式）/ Two-way RRF (SQLite-only mode)
func BenchmarkWeightedRRF_TwoWay(b *testing.B) {
	set1 := makeResultSet("fts", 20)
	set2 := makeResultSet("graph", 20)

	inputs := []search.RRFInput{
		{Results: set1, Weight: 1.0},
		{Results: set2, Weight: 0.8},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = search.MergeWeightedRRF(inputs, 60, 10)
	}
}

// ---- MMR Benchmarks ----

// BenchmarkMMRRerank MMR 多样性重排耗时 / MMR diversity re-ranking overhead
func BenchmarkMMRRerank(b *testing.B) {
	sizes := []int{10, 25, 50}
	dim := 128

	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d_dim=%d", n, dim), func(b *testing.B) {
			vs := makeBenchVecStore(n, dim)
			results := makeMMRResultSet(n)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = search.MMRRerank(context.Background(), results, vs, 0.7, n)
			}
		})
	}
}

// BenchmarkMMRRerank_Lambda MMR lambda 参数对性能影响 / MMR lambda effect on performance
func BenchmarkMMRRerank_Lambda(b *testing.B) {
	n := 30
	dim := 64
	vs := makeBenchVecStore(n, dim)
	results := makeMMRResultSet(n)

	lambdas := []float64{0.0, 0.5, 1.0}
	for _, lambda := range lambdas {
		b.Run(fmt.Sprintf("lambda=%.1f", lambda), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = search.MMRRerank(context.Background(), results, vs, lambda, n)
			}
		})
	}
}

// ---- Token Estimation Benchmark ----

// BenchmarkEstimateTokens 混合文本 token 估算性能 / Token estimation performance for mixed CJK+English text
func BenchmarkEstimateTokens(b *testing.B) {
	texts := []struct {
		name string
		text string
	}{
		{"english_short", "The quick brown fox jumps over the lazy dog"},
		{"chinese_short", "快速的棕色狐狸跳过了懒惰的狗"},
		{"mixed_medium", "IClude is a local-first 本地优先 hybrid memory system for AI applications. 支持多语言检索"},
		{"long_mixed", repeat("IClude memory system 记忆系统 supports hybrid retrieval. ", 20)},
	}

	for _, tc := range texts {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = search.EstimateTokens(tc.text)
			}
		})
	}
}

// ---- helpers ----

func makeResultSet(source string, n int) []*model.SearchResult {
	results := make([]*model.SearchResult, n)
	for i := range results {
		results[i] = &model.SearchResult{
			Memory: &model.Memory{ID: fmt.Sprintf("%s-%d", source, i), Content: fmt.Sprintf("content %d", i)},
			Score:  1.0 / float64(i+1),
			Source: source,
		}
	}
	return results
}

func makeMMRResultSet(n int) []*model.SearchResult {
	results := make([]*model.SearchResult, n)
	for i := range results {
		results[i] = &model.SearchResult{
			Memory: &model.Memory{ID: fmt.Sprintf("mem-%d", i)},
			Score:  1.0 / float64(i+1),
			Source: "sqlite",
		}
	}
	return results
}

// benchVecStore 基准测试用向量存储 stub / Vector store stub for benchmarks
type benchVecStore struct {
	vectors map[string][]float32
}

func makeBenchVecStore(n, dim int) *benchVecStore {
	vs := &benchVecStore{vectors: make(map[string][]float32, n)}
	for i := 0; i < n; i++ {
		vec := make([]float32, dim)
		// 生成正交方向向量 / Orthogonal-ish vectors to exercise MMR selection
		if i < dim {
			vec[i] = 1.0
		} else {
			vec[i%dim] = float32(i%3)*0.3 + 0.1
		}
		vs.vectors[fmt.Sprintf("mem-%d", i)] = vec
	}
	return vs
}

func (v *benchVecStore) GetVectors(ctx context.Context, ids []string) (map[string][]float32, error) {
	result := make(map[string][]float32, len(ids))
	for _, id := range ids {
		if vec, ok := v.vectors[id]; ok {
			result[id] = vec
		}
	}
	return result, nil
}

func (v *benchVecStore) Upsert(ctx context.Context, id string, vec []float32, payload map[string]any) error {
	return nil
}
func (v *benchVecStore) Search(ctx context.Context, vec []float32, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
	return nil, nil
}
func (v *benchVecStore) SearchFiltered(ctx context.Context, vec []float32, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	return nil, nil
}
func (v *benchVecStore) Delete(ctx context.Context, id string) error { return nil }
func (v *benchVecStore) Init(ctx context.Context) error              { return nil }
func (v *benchVecStore) Close() error                                { return nil }

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
