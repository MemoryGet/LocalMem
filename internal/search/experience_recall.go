// experience_recall.go 相似经验主动召回 / Similar experience proactive recall (B7)
package search

import (
	"context"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// ExperienceHint 经验提示 / Experience hint attached to memory creation response
type ExperienceHint struct {
	MemoryID string `json:"memory_id"` // 相关 procedural 记忆 ID / Related procedural memory ID
	Excerpt string `json:"excerpt"` // 摘要 / Excerpt
	Score    float64 `json:"score"`    // 相关度分数 / Relevance score
}

// ExperienceRecaller 经验召回器 / Proactive experience recaller
type ExperienceRecaller struct {
	retriever *Retriever
}

// NewExperienceRecaller 创建经验召回器 / Create experience recaller
func NewExperienceRecaller(retriever *Retriever) *ExperienceRecaller {
	return &ExperienceRecaller{retriever: retriever}
}

// Recall 根据内容查找相关 procedural 记忆 / Find related procedural memories by content
func (r *ExperienceRecaller) Recall(ctx context.Context, content string, identity *model.Identity, limit int) []ExperienceHint {
	if r.retriever == nil || content == "" {
		return nil
	}
	if limit <= 0 {
		limit = 3
	}

	noRetry := true
	results, err := r.retriever.Retrieve(ctx, &model.RetrieveRequest{
		Query:       content,
		TeamID:      identity.TeamID,
		OwnerID:     identity.OwnerID,
		Limit:       limit,
		MemoryClass: "procedural",
		Filters: &model.SearchFilters{
			TeamID:  identity.TeamID,
			OwnerID: identity.OwnerID,
		},
		RerankEnabled: &noRetry, // 启用重排提升质量 / Enable rerank
		NoRetry:       true,     // 不做自适应重试 / No adaptive retry
	})
	if err != nil {
		logger.Debug("experience recall failed, non-blocking",
			zap.Error(err),
		)
		return nil
	}

	var hints []ExperienceHint
	for _, res := range results {
		if res.Memory == nil {
			continue
		}
		excerpt := res.Memory.Excerpt
		if excerpt == "" {
			runes := []rune(res.Memory.Content)
			if len(runes) > 100 {
				runes = runes[:100]
			}
			excerpt = string(runes)
		}
		hints = append(hints, ExperienceHint{
			MemoryID: res.Memory.ID,
			Excerpt:  excerpt,
			Score:    res.Score,
		})
	}
	return hints
}
