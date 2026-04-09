// Package stage 检索管线阶段实现 / Pipeline stage implementations
package stage

import (
	"context"
	"strings"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// defaultFTSLimit FTS 检索默认返回数量 / Default result limit for FTS search
const defaultFTSLimit = 30

// FTSStage 全文检索阶段 / Full-text search pipeline stage
type FTSStage struct {
	searcher FTSSearcher
	limit    int
}

// NewFTSStage 创建 FTS 阶段 / Create a new FTS stage
func NewFTSStage(searcher FTSSearcher, limit int) *FTSStage {
	if limit <= 0 {
		limit = defaultFTSLimit
	}
	return &FTSStage{
		searcher: searcher,
		limit:    limit,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *FTSStage) Name() string {
	return "fts"
}

// Execute 执行 FTS 检索 / Execute FTS search stage
func (s *FTSStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// nil searcher → 跳过 / nil searcher → skip
	if s.searcher == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "searcher is nil",
		})
		return state, nil
	}

	// 确定查询文本：优先用 Plan 中的关键词 / Determine query: prefer keywords from Plan
	query := s.resolveQuery(state)
	if query == "" {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "empty query",
		})
		return state, nil
	}

	// 执行检索 / Execute search
	results, err := s.search(ctx, query, state)
	if err != nil {
		logger.Warn("fts stage search failed",
			zap.String("query", query),
			zap.Error(err),
		)
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: 0,
			Note:        "search error: " + err.Error(),
		})
		return state, nil
	}

	// 追加结果（不替换已有候选）/ Append results (don't replace existing candidates)
	state.Candidates = append(state.Candidates, results...)

	return state, nil
}

// resolveQuery 解析查询文本 / Resolve query text from plan keywords or state query
func (s *FTSStage) resolveQuery(state *pipeline.PipelineState) string {
	if state.Plan != nil && len(state.Plan.Keywords) > 0 {
		return strings.Join(state.Plan.Keywords, " ")
	}
	return strings.TrimSpace(state.Query)
}

// search 根据是否有过滤条件选择检索方法 / Choose search method based on filters presence
func (s *FTSStage) search(ctx context.Context, query string, state *pipeline.PipelineState) ([]*model.SearchResult, error) {
	if state.Filters != nil {
		return s.searcher.SearchTextFiltered(ctx, query, state.Filters, s.limit)
	}
	return s.searcher.SearchText(ctx, query, state.Identity, s.limit)
}
