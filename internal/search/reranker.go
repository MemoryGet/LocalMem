package search

import (
	"context"
	"sort"
	"strings"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"
)

// Reranker 精排接口 / Re-ranking interface
type Reranker interface {
	Rerank(ctx context.Context, query string, results []*model.SearchResult) []*model.SearchResult
}

// NewReranker 根据配置创建精排器 / Create reranker from config
func NewReranker(cfg config.RerankConfig) Reranker {
	if !cfg.Enabled {
		return nil
	}

	provider := strings.TrimSpace(strings.ToLower(cfg.Provider))
	if provider == "" {
		provider = "overlap"
	}

	switch provider {
	case "overlap":
		return &OverlapReranker{cfg: cfg}
	case "remote":
		return NewRemoteReranker(cfg)
	default:
		logger.Warn("rerank: unsupported provider, disabling")
		return nil
	}
}

// OverlapReranker 使用查询-文档重叠度进行轻量精排 / Lightweight reranker based on query-document overlap
type OverlapReranker struct {
	cfg config.RerankConfig
}

// Rerank 对前 top_k 个候选做重排，其余顺序保持不变 / Rerank top-k candidates, preserve the rest
func (r *OverlapReranker) Rerank(ctx context.Context, query string, results []*model.SearchResult) []*model.SearchResult {
	if err := ctx.Err(); err != nil || len(results) <= 1 {
		return results
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return results
	}

	topK := r.cfg.TopK
	if topK <= 1 || topK > len(results) {
		topK = len(results)
	}

	weight := r.cfg.ScoreWeight
	if weight <= 0 {
		weight = 0.7
	}
	if weight > 1 {
		weight = 1
	}

	terms := expandRerankTerms(query)
	queryNorm := normalizeRerankText(query)
	if queryNorm == "" || len(terms) == 0 {
		return results
	}

	subset := append([]*model.SearchResult(nil), results[:topK]...)
	maxBaseScore := 0.0
	for _, res := range subset {
		if res != nil && res.Score > maxBaseScore {
			maxBaseScore = res.Score
		}
	}
	if maxBaseScore <= 0 {
		maxBaseScore = 1
	}

	type scoredResult struct {
		res       *model.SearchResult
		index     int
		final     float64
		baseScore float64
	}
	scored := make([]scoredResult, 0, len(subset))
	for i, res := range subset {
		if res == nil || res.Memory == nil {
			continue
		}
		baseNorm := res.Score / maxBaseScore
		overlap := scoreOverlap(queryNorm, terms, res.Memory)
		final := (1-weight)*baseNorm + weight*overlap
		scored = append(scored, scoredResult{
			res:       res,
			index:     i,
			final:     final,
			baseScore: res.Score,
		})
	}
	if len(scored) <= 1 {
		return results
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].final != scored[j].final {
			return scored[i].final > scored[j].final
		}
		if scored[i].baseScore != scored[j].baseScore {
			return scored[i].baseScore > scored[j].baseScore
		}
		return scored[i].index < scored[j].index
	})

	reranked := append([]*model.SearchResult(nil), results...)
	for i, item := range scored {
		item.res.Score = item.final
		reranked[i] = item.res
	}
	return reranked
}

func scoreOverlap(queryNorm string, terms []string, mem *model.Memory) float64 {
	doc := normalizeRerankText(strings.Join([]string{mem.Content, mem.Excerpt, mem.Summary}, " "))
	if doc == "" {
		return 0
	}

	phraseBoost := 0.0
	if strings.Contains(doc, queryNorm) {
		phraseBoost = 1.0
	}

	matched := 0
	for _, term := range terms {
		if strings.Contains(doc, term) {
			matched++
		}
	}

	coverage := float64(matched) / float64(len(terms))
	return 0.35*phraseBoost + 0.65*coverage
}

func expandRerankTerms(query string) []string {
	parts := splitRerankTerms(query)
	seen := make(map[string]bool, len(parts)*2)
	terms := make([]string, 0, len(parts)*2)
	for _, part := range parts {
		if !seen[part] {
			seen[part] = true
			terms = append(terms, part)
		}
		runes := []rune(part)
		if isHanOnly(runes) && len(runes) > 1 {
			for i := 0; i < len(runes)-1; i++ {
				bigram := string(runes[i : i+2])
				if !seen[bigram] {
					seen[bigram] = true
					terms = append(terms, bigram)
				}
			}
		}
	}
	return terms
}
