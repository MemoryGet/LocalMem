package stage

import (
	"context"
	"math"
	"sort"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// defaultRRFK RRF 默认 k 值 / Default RRF k parameter
const defaultRRFK = 60

// rrfScoreEpsilon 浮点比较容差 / Float64 equality tolerance for RRF score comparison
const rrfScoreEpsilon = 1e-12

// MergeStage RRF 融合阶段 / RRF merge pipeline stage
type MergeStage struct {
	strategy string
	k        int
	limit    int
}

// NewMergeStage 创建 RRF 融合阶段 / Create a new RRF merge stage
func NewMergeStage(strategy string, k int, limit int) *MergeStage {
	if strategy == "" {
		strategy = "rrf"
	}
	if k <= 0 {
		k = defaultRRFK
	}
	if limit <= 0 {
		limit = 100
	}
	return &MergeStage{
		strategy: strategy,
		k:        k,
		limit:    limit,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *MergeStage) Name() string {
	return "merge"
}

// Execute 执行 RRF 融合 / Execute RRF merge stage
func (s *MergeStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	if len(state.Candidates) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        "merge",
			Duration:    time.Since(start),
			InputCount:  0,
			OutputCount: 0,
			Note:        "no candidates to merge",
		})
		return state, nil
	}

	// 按 Source 分组 / Group candidates by source
	groups := make(map[string][]*model.SearchResult)
	for _, c := range state.Candidates {
		groups[c.Source] = append(groups[c.Source], c)
	}

	// 单源直接去重返回 / Single source: deduplicate and return
	if len(groups) == 1 {
		merged := s.dedup(state.Candidates)
		if len(merged) > s.limit {
			merged = merged[:s.limit]
		}
		state.Candidates = merged
		state.AddTrace(pipeline.StageTrace{
			Name:        "merge",
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: len(merged),
			Note:        "single source passthrough",
		})
		return state, nil
	}

	// RRF 融合 / Apply RRF fusion
	scores := make(map[string]float64)
	memMap := make(map[string]*model.Memory)

	// 每个 source 组内按 score 排序后计算 RRF / Sort each source group by score, then compute RRF
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			return group[i].Score > group[j].Score
		})
		for rank, r := range group {
			if r.Memory == nil {
				continue
			}
			id := r.Memory.ID
			scores[id] += 1.0 / float64(s.k+rank+1)
			// 保留最完整的 Memory 对象 / Keep the most complete Memory object
			if existing, ok := memMap[id]; !ok || existing.Content == "" {
				memMap[id] = r.Memory
			}
		}
	}

	// 构建融合结果 / Build merged results
	merged := make([]*model.SearchResult, 0, len(scores))
	for id, score := range scores {
		merged = append(merged, &model.SearchResult{
			Memory: memMap[id],
			Score:  score,
			Source: "hybrid",
		})
	}

	// 稳定排序：分数降序，同分按 ID 字典序 / Stable sort: score desc, tie-break by ID asc
	sort.Slice(merged, func(i, j int) bool {
		if math.Abs(merged[i].Score-merged[j].Score) > rrfScoreEpsilon {
			return merged[i].Score > merged[j].Score
		}
		return merged[i].Memory.ID < merged[j].Memory.ID
	})

	if len(merged) > s.limit {
		merged = merged[:s.limit]
	}

	state.Candidates = merged

	state.AddTrace(pipeline.StageTrace{
		Name:        "merge",
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(merged),
	})

	return state, nil
}

// dedup 按 Memory.ID 去重，保留最完整的对象 / Deduplicate by Memory.ID, keep most complete object
func (s *MergeStage) dedup(candidates []*model.SearchResult) []*model.SearchResult {
	seen := make(map[string]bool, len(candidates))
	out := make([]*model.SearchResult, 0, len(candidates))
	for _, c := range candidates {
		if c.Memory == nil {
			continue
		}
		if seen[c.Memory.ID] {
			continue
		}
		seen[c.Memory.ID] = true
		out = append(out, c)
	}
	return out
}
