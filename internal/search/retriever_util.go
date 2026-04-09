// Package search 检索工具函数 / Retrieval utility functions
package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/pkg/tokenutil"

	"go.uber.org/zap"
)

// EstimateTokens 估算文本token数 / Estimate token count for text
// 委托给 pkg/tokenutil 统一实现 / Delegates to shared tokenutil package
func EstimateTokens(text string) int {
	return tokenutil.EstimateTokens(text)
}

// Deprecated: TrimByTokenBudget is used by retrieveLegacy() and search_handler. Use stage.TrimStage for pipeline mode.
// TrimByTokenBudget 按token预算裁剪检索结果 / Trim search results by token budget
// 至少返回 1 条结果（即使单条超出预算）
func TrimByTokenBudget(results []*model.SearchResult, maxTokens int) ([]*model.SearchResult, int, bool) {
	if maxTokens <= 0 || len(results) == 0 {
		total := 0
		for _, r := range results {
			total += EstimateTokens(r.Memory.Content)
		}
		return results, total, false
	}

	var trimmed []*model.SearchResult
	totalTokens := 0
	for i, r := range results {
		tokens := EstimateTokens(r.Memory.Content)
		if totalTokens+tokens > maxTokens && i > 0 {
			return trimmed, totalTokens, true
		}
		trimmed = append(trimmed, r)
		totalTokens += tokens
	}
	return trimmed, totalTokens, false
}

// backfillMemories 回填空壳 Memory 对象 / Backfill incomplete Memory objects from MemoryStore
// Qdrant 搜索结果仅含 ID，需从 SQLite 获取完整字段（Content/Strength/DecayRate 等）
func (r *Retriever) backfillMemories(ctx context.Context, results []*model.SearchResult) []*model.SearchResult {
	filled := make([]*model.SearchResult, 0, len(results))
	for _, res := range results {
		if res.Memory == nil {
			continue
		}
		// 检测空壳：Content 为空说明是 Qdrant 返回的不完整对象
		if res.Memory.Content == "" {
			mem, err := r.memStore.Get(ctx, res.Memory.ID)
			if err != nil {
				logger.Debug("backfill: failed to get memory, skipping",
					zap.String("id", res.Memory.ID), zap.Error(err))
				continue
			}
			// 创建副本避免修改共享指针 / Create copy to avoid mutating shared pointer
			newRes := *res
			newRes.Memory = mem
			filled = append(filled, &newRes)
			continue
		}
		filled = append(filled, res)
	}
	return filled
}

// injectCoreMemories 在检索结果前注入 core 记忆 / Prepend core memories to search results
func (r *Retriever) injectCoreMemories(ctx context.Context, req *model.RetrieveRequest, results []*model.SearchResult) []*model.SearchResult {
	// 构建要查询的 scope 列表 / Build scope list to query
	var scopes []string
	if req.Filters != nil && req.Filters.Scope != "" {
		scopes = append(scopes, req.Filters.Scope)
	}
	// 始终包含 user/ scope / Always include user scope
	identity := r.resolveIdentity(req)
	if identity.OwnerID != "" {
		userScope := "user/" + identity.OwnerID
		found := false
		for _, s := range scopes {
			if s == userScope {
				found = true
				break
			}
		}
		if !found {
			scopes = append(scopes, userScope)
		}
	}

	if len(scopes) == 0 {
		return results
	}

	coreBlocks, err := r.coreProvider.GetCoreBlocksMultiScope(ctx, scopes, identity)
	if err != nil {
		logger.Debug("core injection failed, skipping", zap.Error(err))
		return results
	}
	if len(coreBlocks) == 0 {
		return results
	}

	// 去重：排除已在检索结果中的 core 记忆 / Deduplicate: skip core blocks already in results
	existingIDs := make(map[string]bool, len(results))
	for _, res := range results {
		if res.Memory != nil {
			existingIDs[res.Memory.ID] = true
		}
	}

	var injected []*model.SearchResult
	for _, m := range coreBlocks {
		if existingIDs[m.ID] {
			continue
		}
		injected = append(injected, &model.SearchResult{
			Memory: m,
			Score:  2.0, // core 固定高分确保排在前面 / Fixed high score to ensure top position
			Source: "core",
		})
	}

	if len(injected) > 0 {
		logger.Debug("core memories injected",
			zap.Int("count", len(injected)),
			zap.Int("scopes", len(scopes)),
		)
	}

	// core 置顶 + 原结果 / Core first + original results
	return append(injected, results...)
}

// resolveReranker 解析当前请求应使用的 reranker / Resolve reranker for current request
func (r *Retriever) resolveReranker(req *model.RetrieveRequest) Reranker {
	rerankCfg := r.cfg.Rerank
	if req != nil {
		if req.RerankEnabled != nil {
			rerankCfg.Enabled = *req.RerankEnabled
		}
		if strings.TrimSpace(req.RerankProvider) != "" {
			rerankCfg.Provider = req.RerankProvider
		}
	}
	return NewReranker(rerankCfg)
}

// resolveEmbedding 解析 embedding
func (r *Retriever) resolveEmbedding(ctx context.Context, provided []float32, query string) ([]float32, error) {
	if len(provided) > 0 {
		return provided, nil
	}
	if r.embedder == nil || query == "" {
		return nil, nil
	}
	embedding, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}
	return embedding, nil
}

// temporalRetrieve 时间通道检索 / Temporal channel retrieval with distance-decay scoring
func (r *Retriever) temporalRetrieve(ctx context.Context, req *model.RetrieveRequest, plan *QueryPlan, limit int) []*model.SearchResult {
	center := *plan.TemporalCenter
	rangeD := plan.TemporalRange
	if rangeD <= 0 {
		rangeD = 7 * 24 * time.Hour
	}

	expandedRange := rangeD * 3
	after := center.Add(-expandedRange)
	before := center.Add(expandedRange)

	timelineReq := &model.TimelineRequest{
		TeamID:  req.TeamID,
		OwnerID: req.OwnerID,
		After:   &after,
		Before:  &before,
		Limit:   limit * 2,
	}

	memories, err := r.memStore.ListTimeline(ctx, timelineReq)
	if err != nil {
		logger.Warn("temporal retrieve failed", zap.Error(err))
		return nil
	}

	rangeDays := rangeD.Hours() / 24
	if rangeDays < 1 {
		rangeDays = 1
	}

	var results []*model.SearchResult
	for _, mem := range memories {
		var ts time.Time
		if mem.HappenedAt != nil && !mem.HappenedAt.IsZero() {
			ts = *mem.HappenedAt
		} else {
			ts = mem.CreatedAt
		}
		daysAway := center.Sub(ts).Hours() / 24
		if daysAway < 0 {
			daysAway = -daysAway
		}

		var score float64
		if daysAway <= rangeDays {
			score = 1.0
		} else {
			score = 1.0 / (1.0 + daysAway/rangeDays)
		}

		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  score,
			Source: "temporal",
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
