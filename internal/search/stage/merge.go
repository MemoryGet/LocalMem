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
			Name:        s.Name(),
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
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: len(merged),
			Note:        "single source passthrough",
		})
		return state, nil
	}

	// 按策略分支融合 / Branch by merge strategy
	var merged []*model.SearchResult
	switch s.strategy {
	case "graph_aware":
		merged = s.mergeGraphAware(groups)
	default: // "rrf"
		merged = s.mergeRRF(groups)
	}

	if len(merged) > s.limit {
		merged = merged[:s.limit]
	}

	state.Candidates = merged

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(merged),
	})

	return state, nil
}

// mergeRRF 标准 RRF 融合 / Standard RRF fusion
func (s *MergeStage) mergeRRF(groups map[string][]*model.SearchResult) []*model.SearchResult {
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

	return merged
}

// trustFactorCrossValidated 交叉验证信任因子（graph + 其他源）/ Cross-validated trust factor
const trustFactorCrossValidated = 1.5

// trustFactorGraphOrVector graph/vector 单源信任因子 / Single-source trust for graph or vector
const trustFactorGraphOrVector = 1.0

// trustFactorFTSOrTemporal fts/temporal 单源信任因子 / Single-source trust for fts or temporal
const trustFactorFTSOrTemporal = 0.8

// mergeGraphAware 图感知 RRF 融合，按源信任加权 / Graph-aware RRF merge with source trust weighting
func (s *MergeStage) mergeGraphAware(groups map[string][]*model.SearchResult) []*model.SearchResult {
	// 1. 构建 memSources: 每个 memID 出现在哪些源 / Build memSources: which sources each memID appeared in
	memSources := make(map[string]map[string]bool)
	for source, group := range groups {
		for _, r := range group {
			if r.Memory == nil {
				continue
			}
			id := r.Memory.ID
			if memSources[id] == nil {
				memSources[id] = make(map[string]bool)
			}
			memSources[id][source] = true
		}
	}

	// 2. 计算每个 memID 的信任因子 / Determine trust factor per memID
	trustFactors := make(map[string]float64, len(memSources))
	for id, sources := range memSources {
		trustFactors[id] = computeTrustFactor(sources)
	}

	// 3. 加权 RRF / Weighted RRF with trust factors
	scores := make(map[string]float64)
	memMap := make(map[string]*model.Memory)

	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			return group[i].Score > group[j].Score
		})
		for rank, r := range group {
			if r.Memory == nil {
				continue
			}
			id := r.Memory.ID
			trust := trustFactors[id]
			scores[id] += trust * 1.0 / float64(s.k+rank+1)
			if existing, ok := memMap[id]; !ok || existing.Content == "" {
				memMap[id] = r.Memory
			}
		}
	}

	// 4. 构建结果并排序 / Build results and sort
	merged := make([]*model.SearchResult, 0, len(scores))
	for id, score := range scores {
		merged = append(merged, &model.SearchResult{
			Memory: memMap[id],
			Score:  score,
			Source: "hybrid",
		})
	}

	sort.Slice(merged, func(i, j int) bool {
		if math.Abs(merged[i].Score-merged[j].Score) > rrfScoreEpsilon {
			return merged[i].Score > merged[j].Score
		}
		return merged[i].Memory.ID < merged[j].Memory.ID
	})

	return merged
}

// computeTrustFactor 根据候选出现的源集合计算信任因子
// Compute trust factor based on which sources a candidate appeared in
func computeTrustFactor(sources map[string]bool) float64 {
	hasGraph := sources["graph"]
	hasOtherThanGraph := false
	for src := range sources {
		if src != "graph" {
			hasOtherThanGraph = true
			break
		}
	}

	// 出现在 graph + 至少一个其他源 → 交叉验证 / Appears in graph AND any other source → cross-validated
	if hasGraph && hasOtherThanGraph {
		return trustFactorCrossValidated
	}

	// 仅出现在 graph 或 vector → 标准信任 / Only in graph or vector → standard trust
	for src := range sources {
		if src == "graph" || src == "vector" {
			return trustFactorGraphOrVector
		}
	}

	// 仅出现在 fts 或 temporal → 降低信任 / Only in fts or temporal → reduced trust
	return trustFactorFTSOrTemporal
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
