package stage

import (
	"context"
	"sort"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// defaultTemporalRange 默认时间范围 / Default temporal range
const defaultTemporalRange = 7 * 24 * time.Hour

// temporalExpandFactor 时间范围扩展倍数 / Temporal range expansion factor
const temporalExpandFactor = 3

// defaultTemporalLimit 时间检索默认数量 / Default temporal retrieval limit
const defaultTemporalLimit = 20

// TemporalStage 时间检索阶段 / Temporal retrieval pipeline stage
type TemporalStage struct {
	searcher TimelineSearcher
	limit    int
}

// NewTemporalStage 创建时间检索阶段 / Create a new temporal retrieval stage
func NewTemporalStage(searcher TimelineSearcher, limit int) *TemporalStage {
	if limit <= 0 {
		limit = defaultTemporalLimit
	}
	return &TemporalStage{
		searcher: searcher,
		limit:    limit,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *TemporalStage) Name() string {
	return "temporal"
}

// Execute 执行时间检索 / Execute temporal retrieval stage
func (s *TemporalStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
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

	// Plan 必须含时间信号 / Plan must contain temporal signal
	if state.Plan == nil || !state.Plan.Temporal || state.Plan.TemporalCenter == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "no temporal signal in plan",
		})
		return state, nil
	}

	center := *state.Plan.TemporalCenter
	rangeD := state.Plan.TemporalRange
	if rangeD <= 0 {
		rangeD = defaultTemporalRange
	}

	// 扩展范围查询 / Expand range for query
	expandedRange := rangeD * temporalExpandFactor
	after := center.Add(-expandedRange)
	before := center.Add(expandedRange)

	timelineReq := &model.TimelineRequest{
		After:  &after,
		Before: &before,
		Limit:  s.limit * 2,
	}

	// 从 identity 注入 / Inject from identity
	if state.Identity != nil {
		timelineReq.TeamID = state.Identity.TeamID
		timelineReq.OwnerID = state.Identity.OwnerID
	}

	memories, err := s.searcher.ListTimeline(ctx, timelineReq)
	if err != nil {
		logger.Warn("temporal stage search failed", zap.Error(err))
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: 0,
			Note:        "search error: " + err.Error(),
		})
		return state, nil
	}

	// 距离衰减评分 / Distance-decay scoring
	rangeDays := rangeD.Hours() / 24
	if rangeDays < 1 {
		rangeDays = 1
	}

	results := make([]*model.SearchResult, 0, len(memories))
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
			Source: SourceTemporal,
		})
	}

	// 按分数排序并截断 / Sort by score and truncate
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > s.limit {
		results = results[:s.limit]
	}

	// 追加结果 / Append results
	state.Candidates = append(state.Candidates, results...)

	return state, nil
}
