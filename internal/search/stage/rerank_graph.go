package stage

import (
	"context"
	"sort"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// defaultGraphScoreWeight 图距离分在混合分中的默认权重 / Default graph score weight in blend
const defaultGraphScoreWeight = 0.6

// defaultMinGraphScore 图距离分最低阈值（低于此值过滤） / Default minimum graph score threshold
const defaultMinGraphScore = 0.2

// graphScoreDirect 直接匹配得分 / Direct match graph score (distance 0)
const graphScoreDirect = 1.0

// graphScore1Hop 1-hop 邻居得分 / 1-hop neighbor graph score (distance 1)
const graphScore1Hop = 0.7

// graphScore2Hop 2-hop 邻居得分 / 2-hop neighbor graph score (distance 2)
const graphScore2Hop = 0.4

// RerankGraphStage 图距离精排阶段 / Graph distance reranking stage
type RerankGraphStage struct {
	graphStore    GraphRetriever
	scoreWeight   float64 // 图距离分在混合分中的权重 / graph score weight in blend
	minGraphScore float64 // 过滤阈值 / filter threshold
}

// NewRerankGraphStage 创建图距离精排阶段 / Create a new graph distance reranker stage
func NewRerankGraphStage(graphStore GraphRetriever, scoreWeight, minGraphScore float64) *RerankGraphStage {
	if scoreWeight <= 0 {
		scoreWeight = defaultGraphScoreWeight
	}
	if scoreWeight > 1 {
		scoreWeight = 1
	}
	if minGraphScore <= 0 {
		minGraphScore = defaultMinGraphScore
	}
	return &RerankGraphStage{
		graphStore:    graphStore,
		scoreWeight:   scoreWeight,
		minGraphScore: minGraphScore,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *RerankGraphStage) Name() string {
	return "rerank_graph"
}

// Execute 执行图距离精排 / Execute graph distance reranking
func (s *RerankGraphStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// graphStore nil → 跳过 / nil graphStore → skip
	if s.graphStore == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Skipped:     true,
			Note:        "skipped: nil graph store",
		})
		return state, nil
	}

	// 无查询实体 → 跳过 / No query entities → skip
	queryEntities := extractQueryEntities(state)
	if len(queryEntities) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Skipped:     true,
			Note:        "skipped: no query entities",
		})
		return state, nil
	}

	// 无候选 → 直接返回 / No candidates → return as-is
	if len(state.Candidates) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  0,
			OutputCount: 0,
		})
		return state, nil
	}

	// 预构建查询实体邻居映射 / Pre-build query entity neighbor maps
	queryEntitySet, hop1Set, hop2Set := s.buildNeighborSets(ctx, queryEntities)

	// 批量预获取所有候选记忆的实体映射（消除 N+1）/ Batch pre-fetch entity mappings for all candidates (eliminate N+1)
	memIDs := make([]string, 0, len(state.Candidates))
	for _, cand := range state.Candidates {
		if cand != nil && cand.Memory != nil {
			memIDs = append(memIDs, cand.Memory.ID)
		}
	}
	allMemEntities, err := s.graphStore.GetMemoriesEntities(ctx, memIDs)
	if err != nil {
		// 批量查询失败时降级跳过 / Degrade gracefully on batch query failure
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Skipped:     true,
			Note:        "skipped: batch GetMemoriesEntities failed",
		})
		return state, nil
	}

	// 计算每个候选的图距离分并混合 / Compute graph distance score for each candidate and blend
	maxBaseScore := findMaxBaseScore(state.Candidates)

	type scoredCandidate struct {
		res        *model.SearchResult
		graphScore float64
		finalScore float64
	}
	scored := make([]scoredCandidate, 0, len(state.Candidates))

	for _, cand := range state.Candidates {
		if cand == nil || cand.Memory == nil {
			continue
		}

		// 从预获取映射中查找实体 / Look up entities from pre-fetched map
		memEnts := allMemEntities[cand.Memory.ID]

		// 取候选所有实体中最高的图距离分 / Take max graph score across all candidate's entities
		graphScore := 0.0
		for _, ent := range memEnts {
			es := entityGraphScore(ent.ID, queryEntitySet, hop1Set, hop2Set)
			if es > graphScore {
				graphScore = es
			}
		}

		// 混合分数 / Blend scores
		normalizedBase := cand.Score / maxBaseScore
		finalScore := (1-s.scoreWeight)*normalizedBase + s.scoreWeight*graphScore

		scored = append(scored, scoredCandidate{
			res:        cand,
			graphScore: graphScore,
			finalScore: finalScore,
		})
	}

	// 过滤低图距离分候选 / Filter candidates with low graph score
	filtered := make([]scoredCandidate, 0, len(scored))
	for _, sc := range scored {
		if sc.graphScore >= s.minGraphScore {
			filtered = append(filtered, sc)
		}
	}

	// 按最终分数降序排序 / Sort by final score descending
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].finalScore > filtered[j].finalScore
	})

	// 创建副本避免修改原始输入 / Create copies to avoid mutating original input
	result := make([]*model.SearchResult, len(filtered))
	for i, sc := range filtered {
		resCopy := *sc.res
		resCopy.Score = sc.finalScore
		result[i] = &resCopy
	}

	state.Candidates = result

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(result),
	})

	return state, nil
}

// extractQueryEntities 从状态中提取查询实体 ID / Extract query entity IDs from pipeline state
func extractQueryEntities(state *pipeline.PipelineState) []string {
	if state.Plan == nil {
		return nil
	}
	return state.Plan.Entities
}

// buildNeighborSets 预构建查询实体的邻居集合（0-hop、1-hop、2-hop）
// Pre-build neighbor sets for query entities (0-hop, 1-hop, 2-hop)
func (s *RerankGraphStage) buildNeighborSets(ctx context.Context, queryEntities []string) (
	queryEntitySet map[string]bool,
	hop1Set map[string]bool,
	hop2Set map[string]bool,
) {
	queryEntitySet = make(map[string]bool, len(queryEntities))
	for _, id := range queryEntities {
		queryEntitySet[id] = true
	}

	hop1Set = make(map[string]bool)
	for _, qID := range queryEntities {
		relations, err := s.graphStore.GetEntityRelations(ctx, qID)
		if err != nil {
			continue
		}
		for _, rel := range relations {
			neighbor := otherEnd(rel, qID)
			if neighbor != "" && !queryEntitySet[neighbor] {
				hop1Set[neighbor] = true
			}
		}
	}

	hop2Set = make(map[string]bool)
	for hop1ID := range hop1Set {
		relations, err := s.graphStore.GetEntityRelations(ctx, hop1ID)
		if err != nil {
			continue
		}
		for _, rel := range relations {
			neighbor := otherEnd(rel, hop1ID)
			if neighbor != "" && !queryEntitySet[neighbor] && !hop1Set[neighbor] {
				hop2Set[neighbor] = true
			}
		}
	}

	return queryEntitySet, hop1Set, hop2Set
}

// otherEnd 获取关系中的另一端实体 ID / Get the other end entity ID in a relation
func otherEnd(rel *model.EntityRelation, entityID string) string {
	if rel.SourceID == entityID {
		return rel.TargetID
	}
	if rel.TargetID == entityID {
		return rel.SourceID
	}
	return ""
}

// entityGraphScore 计算单个实体的图距离分 / Compute graph score for a single entity
func entityGraphScore(entityID string, querySet, hop1Set, hop2Set map[string]bool) float64 {
	if querySet[entityID] {
		return graphScoreDirect
	}
	if hop1Set[entityID] {
		return graphScore1Hop
	}
	if hop2Set[entityID] {
		return graphScore2Hop
	}
	return 0.0
}

