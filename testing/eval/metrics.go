// Package eval 提供 IR 检索评测指标 / provides IR retrieval evaluation metrics.
package eval

import "math"

// CaseResult 单用例评测结果 / Single evaluation case result
type CaseResult struct {
	Query       string  `json:"query"`
	Expected    string  `json:"expected"`
	Category    string  `json:"category"`
	Difficulty  string  `json:"difficulty"`
	Hit         bool    `json:"hit"`
	Rank        int     `json:"rank"`        // 1-based, -1 = miss
	Score       float64 `json:"score"`
	ResultCount int     `json:"result_count"`
}

// AggregateMetrics 聚合指标 / Aggregated evaluation metrics
type AggregateMetrics struct {
	Total      int     `json:"total"`
	HitRate    float64 `json:"hit_rate"`
	MRR        float64 `json:"mrr"`
	NDCG5      float64 `json:"ndcg@5"`
	NDCG10     float64 `json:"ndcg@10"`
	RecallAt1  float64 `json:"recall@1"`
	RecallAt3  float64 `json:"recall@3"`
	RecallAt5  float64 `json:"recall@5"`
	RecallAt10 float64 `json:"recall@10"`
}

// MRR 计算平均倒数排名 / Compute Mean Reciprocal Rank.
// ranks 为 1-based 排名列表，-1 表示未命中 / ranks are 1-based; -1 means miss.
func MRR(ranks []int) float64 {
	if len(ranks) == 0 {
		return 0.0
	}
	var sum float64
	for _, r := range ranks {
		if r > 0 {
			sum += 1.0 / float64(r)
		}
	}
	return sum / float64(len(ranks))
}

// RecallAtK 计算 Top-K 召回率 / Compute fraction of queries with a hit in top-k.
// 返回值范围 [0, 1] / Return value in [0, 1].
func RecallAtK(ranks []int, k int) float64 {
	if len(ranks) == 0 {
		return 0.0
	}
	var hits int
	for _, r := range ranks {
		if r > 0 && r <= k {
			hits++
		}
	}
	return float64(hits) / float64(len(ranks))
}

// NDCGAtK 计算归一化折损累积增益（二元相关性）/ Compute Normalized DCG at K with binary relevance.
// idealDCG = 1.0（单个相关文档排在第 1 位）/ idealDCG = 1.0 (single relevant doc at rank 1).
func NDCGAtK(ranks []int, k int) float64 {
	if len(ranks) == 0 {
		return 0.0
	}
	// idealDCG = 1/log2(2) = 1.0
	const idealDCG = 1.0
	var total float64
	for _, r := range ranks {
		var dcg float64
		if r > 0 && r <= k {
			// DCG = 1 / log2(rank + 1)
			dcg = 1.0 / math.Log2(float64(r)+1)
		}
		total += dcg / idealDCG
	}
	return total / float64(len(ranks))
}

// HitRate 计算命中率（百分比 0-100）/ Compute hit rate as percentage (0-100).
// 任意排名命中即算中 / Any non-miss rank counts as a hit.
func HitRate(ranks []int) float64 {
	if len(ranks) == 0 {
		return 0.0
	}
	var hits int
	for _, r := range ranks {
		if r > 0 {
			hits++
		}
	}
	return float64(hits) / float64(len(ranks)) * 100.0
}

// Aggregate 从用例结果列表计算所有聚合指标 / Compute all aggregate metrics from case results.
func Aggregate(results []CaseResult) AggregateMetrics {
	if len(results) == 0 {
		return AggregateMetrics{}
	}

	ranks := make([]int, len(results))
	for i, r := range results {
		ranks[i] = r.Rank
	}

	return AggregateMetrics{
		Total:      len(results),
		HitRate:    HitRate(ranks),
		MRR:        MRR(ranks),
		NDCG5:      NDCGAtK(ranks, 5),
		NDCG10:     NDCGAtK(ranks, 10),
		RecallAt1:  RecallAtK(ranks, 1),
		RecallAt3:  RecallAtK(ranks, 3),
		RecallAt5:  RecallAtK(ranks, 5),
		RecallAt10: RecallAtK(ranks, 10),
	}
}
