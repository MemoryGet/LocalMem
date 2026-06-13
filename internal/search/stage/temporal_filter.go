package stage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// defaultTemporalFilterMin 时间过滤后至少保留此数量，否则跳过过滤 / Skip filter if fewer results would remain
const defaultTemporalFilterMin = 3

// defaultTemporalInnerRatio 内层窗口比例：内层半径 = TemporalRange × innerRatio
// Inner-window ratio: inner radius = TemporalRange × innerRatio. Tier 0 boundary.
const defaultTemporalInnerRatio = 0.5

// TemporalFilterStage 时间约束过滤阶段
// 对已有候选按时间窗口过滤，优化时间锚定查询的精度。
// 与 TemporalStage（追加新候选）互补：本 stage 在 merge 之后，负责剔除时间范围外的干扰候选。
// 只有在 plan 包含明确时间锚点（TemporalAnchor=true）时才激活，避免无时间锚的查询被错误过滤。
//
// Temporal constraint filter: removes candidates outside the temporal window.
// Complements TemporalStage (which appends new candidates) by filtering noise AFTER merge.
// Only activates when plan has an explicit temporal anchor — unanchored queries are left unchanged.
type TemporalFilterStage struct {
	minAfterFilter int
	innerRatio     float64 // 内层窗口比例，0 = 禁用重排 / Inner ratio, 0 disables rerank
}

// TemporalFilterOption 函数式选项 / Functional option for TemporalFilterStage
type TemporalFilterOption func(*TemporalFilterStage)

// WithInnerRatio 覆盖默认内层比例（传 0 可禁用 tier 重排，超出 [0,1] 会被钳制）
// Override inner ratio (0 disables tier rerank); values are clamped to [0, 1].
func WithInnerRatio(r float64) TemporalFilterOption {
	return func(s *TemporalFilterStage) {
		if r < 0 {
			r = 0
		} else if r > 1.0 {
			r = 1.0
		}
		s.innerRatio = r
	}
}

// NewTemporalFilterStage 创建时间过滤阶段 / Create temporal constraint filter stage
func NewTemporalFilterStage(minAfterFilter int, opts ...TemporalFilterOption) *TemporalFilterStage {
	if minAfterFilter <= 0 {
		minAfterFilter = defaultTemporalFilterMin
	}
	s := &TemporalFilterStage{
		minAfterFilter: minAfterFilter,
		innerRatio:     defaultTemporalInnerRatio,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回阶段名称 / Return stage name
func (s *TemporalFilterStage) Name() string { return "temporal_filter" }

// Execute 按时间窗口过滤候选，无明确时间锚时直接跳过
// Filter candidates by temporal window; skip if no explicit anchor
func (s *TemporalFilterStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// 必须有明确时间锚 / Require explicit temporal anchor from query
	if state.Plan == nil || !state.Plan.Temporal || !state.Plan.TemporalAnchor || state.Plan.TemporalCenter == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "no anchored temporal signal",
		})
		return state, nil
	}

	center := *state.Plan.TemporalCenter
	rangeD := state.Plan.TemporalRange
	if rangeD <= 0 {
		rangeD = 30 * 24 * time.Hour
	}

	after := center.Add(-rangeD)
	before := center.Add(rangeD)

	filtered := make([]*model.SearchResult, 0, len(state.Candidates))
	for _, c := range state.Candidates {
		if c == nil || c.Memory == nil {
			continue
		}
		ts := effectiveTime(c.Memory)
		if !ts.Before(after) && !ts.After(before) {
			filtered = append(filtered, c)
		}
	}

	var note string
	if len(filtered) >= s.minAfterFilter {
		state.Candidates = filtered
		note = "applied"
	} else {
		note = "skipped: too few results after filter"
	}

	// tier 重排：按到锚点中心的距离分档，档内保持原始 score 降序，不修改分数
	// Tier rerank: bucket by distance to anchor center, sort by score within tier, scores untouched.
	if s.innerRatio > 0 {
		reranked, dist := s.tierRerank(state.Candidates, center, rangeD)
		state.Candidates = reranked
		note = note + " " + dist
	}

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(state.Candidates),
		Note:        note,
	})

	return state, nil
}

// tierRerank 按到锚点中心的距离分三档重排，返回新 slice 与 tier 分布描述。
// 不修改原 slice、不修改任何 SearchResult.Score。
// tier 0: delta <= innerThreshold；tier 1: <= rangeD；tier 2: > rangeD（仅过滤跳过时存在）。
//
// Tier rerank by distance to anchor center. Returns a new slice and a tier-distribution
// note. Never mutates the input slice or any SearchResult.Score.
// Precondition: rangeD > 0 (Execute defaults it to 30 days before calling).
func (s *TemporalFilterStage) tierRerank(
	cands []*model.SearchResult, center time.Time, rangeD time.Duration,
) ([]*model.SearchResult, string) {
	innerThreshold := time.Duration(float64(rangeD) * s.innerRatio)

	tierOf := func(r *model.SearchResult) int {
		delta := effectiveTime(r.Memory).Sub(center)
		if delta < 0 {
			delta = -delta
		}
		switch {
		case delta <= innerThreshold:
			return 0
		case delta <= rangeD:
			return 1
		default:
			return 2
		}
	}

	// 复制到新 slice，预计算 tier，避免排序比较器重复计算 / Copy + precompute tiers
	type ranked struct {
		r    *model.SearchResult
		tier int
	}
	items := make([]ranked, 0, len(cands))
	var t0, t1, t2 int
	for _, c := range cands {
		if c == nil || c.Memory == nil {
			continue
		}
		tr := tierOf(c)
		switch tr {
		case 0:
			t0++
		case 1:
			t1++
		default:
			t2++
		}
		items = append(items, ranked{r: c, tier: tr})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].tier != items[j].tier {
			return items[i].tier < items[j].tier
		}
		return items[i].r.Score > items[j].r.Score
	})

	out := make([]*model.SearchResult, 0, len(items))
	for _, it := range items {
		out = append(out, it.r)
	}
	return out, fmt.Sprintf("tier0=%d tier1=%d tier2=%d", t0, t1, t2)
}
