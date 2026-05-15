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

// defaultExhaustiveMax 默认最大返回数 / Default maximum results for exhaustive stage
const defaultExhaustiveMax = 200

// ExhaustiveStage 穷举召回阶段：获取所有实体关联记忆，去重并按时间排序
// ExhaustiveStage fetches ALL entity-linked memories, deduplicates, and sorts chronologically
// for aggregation queries (totals, counts, averages).
// When no entities are resolved, falls back to full timeline scan if timeline is provided.
type ExhaustiveStage struct {
	graphStore GraphRetriever
	timeline   TimelineSearcher
	maxResults int
}

// NewExhaustiveStage 创建穷举召回阶段 / Create a new exhaustive retrieval stage
func NewExhaustiveStage(graphStore GraphRetriever, timeline TimelineSearcher, maxResults int) *ExhaustiveStage {
	if maxResults <= 0 {
		maxResults = defaultExhaustiveMax
	}
	return &ExhaustiveStage{graphStore: graphStore, timeline: timeline, maxResults: maxResults}
}

// Name 返回阶段名称 / Return stage name
func (s *ExhaustiveStage) Name() string { return SourceExhaustive }

// Execute 执行穷举召回 / Execute exhaustive recall
func (s *ExhaustiveStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// 0. 始终标记聚合上下文类型（即使数据不可用也要语义正确）
	// Always mark aggregation context type — AggregationPipeline always signals aggregation,
	// regardless of graphStore availability or whether entities were resolved.
	state.ContextType = model.RetrievalContextAggregation

	// 1. nil guard: both graphStore and timeline absent → nothing to do
	if s.graphStore == nil && s.timeline == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "graphStore is nil",
		})
		return state, nil
	}

	// 2. resolve entity IDs from plan (only when graphStore available)
	var entityIDs []string
	if s.graphStore != nil {
		entityIDs = s.resolveEntities(ctx, state)
	}

	// 3. no entities (or no graphStore) → fall back to full timeline scan if available
	if len(entityIDs) == 0 {
		return s.timelineFallback(ctx, state, start, inputCount)
	}

	// 4. fetch memories for each entity, dedup by ID
	memoryMap := make(map[string]*model.Memory)
	for _, entityID := range entityIDs {
		memories, err := s.graphStore.GetEntityMemories(ctx, entityID, s.maxResults)
		if err != nil {
			logger.Warn("exhaustive: GetEntityMemories failed",
				zap.String("entity_id", entityID),
				zap.Error(err),
			)
			continue
		}
		for _, mem := range memories {
			if _, exists := memoryMap[mem.ID]; !exists {
				memoryMap[mem.ID] = mem
			}
		}
	}

	// 5. filter expired memories
	now := time.Now()
	filtered := make([]*model.Memory, 0, len(memoryMap))
	for _, mem := range memoryMap {
		if mem.ExpiresAt != nil && mem.ExpiresAt.Before(now) {
			continue
		}
		filtered = append(filtered, mem)
	}

	// 6. sort by effectiveTime ascending (oldest first)
	sort.Slice(filtered, func(i, j int) bool {
		return effectiveTime(filtered[i]).Before(effectiveTime(filtered[j]))
	})

	// 7. cap at maxResults
	if len(filtered) > s.maxResults {
		filtered = filtered[:s.maxResults]
	}

	// 8. build SearchResult slice with Score=1.0, Source=SourceExhaustive
	results := make([]*model.SearchResult, 0, len(filtered))
	for _, mem := range filtered {
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  1.0,
			Source: SourceExhaustive,
		})
	}

	// 9. append to state.Candidates
	state.Candidates = append(state.Candidates, results...)

	// 10. add trace (ContextType already set at step 0)
	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(results),
	})

	return state, nil
}

// timelineFallback 无实体时回退到全量时序扫描 / Fall back to full timeline scan when no entities
func (s *ExhaustiveStage) timelineFallback(ctx context.Context, state *pipeline.PipelineState, start time.Time, inputCount int) (*pipeline.PipelineState, error) {
	if s.timeline == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: 0,
			Note:        "no entities in plan",
		})
		return state, nil
	}

	req := &model.TimelineRequest{Limit: s.maxResults}
	if state.Identity != nil {
		req.TeamID = state.Identity.TeamID
		req.OwnerID = state.Identity.OwnerID
	}

	memories, err := s.timeline.ListTimeline(ctx, req)
	if err != nil {
		logger.Warn("exhaustive: timeline fallback failed", zap.Error(err))
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: 0,
			Note:        "timeline fallback error: " + err.Error(),
		})
		return state, nil
	}

	results := make([]*model.SearchResult, 0, len(memories))
	for _, mem := range memories {
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  1.0,
			Source: SourceExhaustive,
		})
	}
	state.Candidates = append(state.Candidates, results...)
	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(results),
		Note:        "timeline fallback",
	})
	return state, nil
}

// resolveEntities 从 plan.Entities 解析实体 ID 列表（通过 FindEntitiesByName 查找）
// resolveEntities resolves entity names from plan to entity IDs via FindEntitiesByName
func (s *ExhaustiveStage) resolveEntities(ctx context.Context, state *pipeline.PipelineState) []string {
	if state.Plan == nil || len(state.Plan.Entities) == 0 {
		return nil
	}

	// 从 Filters 或 Identity 获取 scope / Resolve scope from Filters or Identity
	var scope string
	if state.Filters != nil && state.Filters.Scope != "" {
		scope = state.Filters.Scope
	} else if state.Identity != nil {
		scope = state.Identity.TeamID
	}

	seen := make(map[string]bool)
	ids := make([]string, 0)

	for _, name := range state.Plan.Entities {
		entities, err := s.graphStore.FindEntitiesByName(ctx, name, scope, 3)
		if err != nil {
			logger.Warn("exhaustive: FindEntitiesByName failed",
				zap.String("name", name),
				zap.Error(err),
			)
			continue
		}
		for _, ent := range entities {
			if !seen[ent.ID] {
				seen[ent.ID] = true
				ids = append(ids, ent.ID)
			}
		}
	}

	return ids
}

// effectiveTime 返回用于排序的有效时间（优先 HappenedAt，回退 CreatedAt）
// effectiveTime returns the effective time for sorting: HappenedAt preferred, fallback CreatedAt
func effectiveTime(m *model.Memory) time.Time {
	if m.HappenedAt != nil && !m.HappenedAt.IsZero() {
		return *m.HappenedAt
	}
	return m.CreatedAt
}
