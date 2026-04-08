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

// 图谱遍历常量 / Graph traversal constants
const (
	defaultGraphMaxDepth    = 2
	defaultGraphLimit       = 30
	defaultGraphFTSTop      = 5
	defaultGraphEntityLimit = 10
	maxVisitedEntities      = 50
)

// GraphStage 图谱关联检索阶段 / Graph-based association retrieval stage
type GraphStage struct {
	graphStore  GraphRetriever
	ftsSearcher FTSSearcher
	maxDepth    int
	limit       int
	ftsTop      int
	entityLimit int
}

// GraphOption 图谱阶段配置选项 / Graph stage configuration option
type GraphOption func(*GraphStage)

// WithMaxDepth 设置最大遍历深度 / Set maximum traversal depth
func WithMaxDepth(depth int) GraphOption {
	return func(s *GraphStage) {
		if depth > 0 {
			s.maxDepth = depth
		}
	}
}

// WithLimit 设置结果数量上限 / Set result limit
func WithLimit(limit int) GraphOption {
	return func(s *GraphStage) {
		if limit > 0 {
			s.limit = limit
		}
	}
}

// WithFTSTop 设置 FTS 反查取 top-N 数量 / Set FTS reverse lookup top-N
func WithFTSTop(n int) GraphOption {
	return func(s *GraphStage) {
		if n > 0 {
			s.ftsTop = n
		}
	}
}

// WithEntityLimit 设置每个实体返回的记忆数上限 / Set per-entity memory limit
func WithEntityLimit(limit int) GraphOption {
	return func(s *GraphStage) {
		if limit > 0 {
			s.entityLimit = limit
		}
	}
}

// NewGraphStage 创建图谱检索阶段 / Create a new graph retrieval stage
func NewGraphStage(graphStore GraphRetriever, ftsSearcher FTSSearcher, opts ...GraphOption) *GraphStage {
	s := &GraphStage{
		graphStore:  graphStore,
		ftsSearcher: ftsSearcher,
		maxDepth:    defaultGraphMaxDepth,
		limit:       defaultGraphLimit,
		ftsTop:      defaultGraphFTSTop,
		entityLimit: defaultGraphEntityLimit,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回阶段名称 / Return stage name
func (s *GraphStage) Name() string {
	return "graph"
}

// Execute 执行图谱关联检索 / Execute graph-based association retrieval
func (s *GraphStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// nil graphStore → 跳过 / nil graphStore → skip
	if s.graphStore == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    "graph",
			Skipped: true,
			Note:    "graphStore is nil",
		})
		return state, nil
	}

	// 阶段 1: 获取种子实体 / Phase 1: Resolve seed entities
	seedEntities := s.resolveSeedEntities(ctx, state)
	if len(seedEntities) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        "graph",
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: 0,
			Note:        "no seed entities found",
		})
		return state, nil
	}

	// 阶段 2: BFS 遍历图谱 / Phase 2: BFS traversal
	visited := s.bfsTraverse(ctx, seedEntities)

	// 阶段 3: 收集关联记忆 / Phase 3: Collect associated memories
	results := s.collectMemories(ctx, visited)

	// 截断结果 / Truncate to limit
	if len(results) > s.limit {
		results = results[:s.limit]
	}

	// 追加结果（不替换已有候选）/ Append results (don't replace existing candidates)
	state.Candidates = append(state.Candidates, results...)

	state.AddTrace(pipeline.StageTrace{
		Name:        "graph",
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(results),
	})

	return state, nil
}

// resolveSeedEntities 解析种子实体：优先从 Plan.Entities，否则 FTS 反查
// Resolve seed entities: prefer Plan.Entities, fallback to FTS reverse lookup
func (s *GraphStage) resolveSeedEntities(ctx context.Context, state *pipeline.PipelineState) map[string]int {
	seeds := make(map[string]int) // entityID → depth (0 for seeds)

	// 路径 1: 从 Plan 中预提取的实体名查找 / Path 1: Look up pre-extracted entity names from Plan
	if state.Plan != nil && len(state.Plan.Entities) > 0 {
		for _, name := range state.Plan.Entities {
			scope := s.resolveScope(state)
			entities, err := s.graphStore.FindEntitiesByName(ctx, name, scope, 1)
			if err != nil {
				logger.Warn("graph: FindEntitiesByName failed",
					zap.String("name", name),
					zap.Error(err),
				)
				continue
			}
			for _, ent := range entities {
				seeds[ent.ID] = 0
			}
		}
		if len(seeds) > 0 {
			return seeds
		}
	}

	// 路径 2: FTS 反查 → 获取记忆关联的实体 / Path 2: FTS reverse lookup → get memory entities
	if s.ftsSearcher != nil && state.Query != "" {
		ftsResults, err := s.ftsSearcher.SearchText(ctx, state.Query, state.Identity, s.ftsTop)
		if err != nil {
			logger.Warn("graph: FTS reverse lookup failed", zap.Error(err))
			return seeds
		}
		for _, result := range ftsResults {
			entities, err := s.graphStore.GetMemoryEntities(ctx, result.Memory.ID)
			if err != nil {
				logger.Warn("graph: GetMemoryEntities failed",
					zap.String("memory_id", result.Memory.ID),
					zap.Error(err),
				)
				continue
			}
			for _, ent := range entities {
				seeds[ent.ID] = 0
			}
		}
	}

	return seeds
}

// resolveScope 从 Identity 或 Metadata 解析 scope / Resolve scope from Identity or Metadata
func (s *GraphStage) resolveScope(state *pipeline.PipelineState) string {
	if filters, ok := state.Metadata["filters"].(*model.SearchFilters); ok && filters != nil && filters.Scope != "" {
		return filters.Scope
	}
	if state.Identity != nil {
		return state.Identity.TeamID
	}
	return ""
}

// bfsTraverse BFS 遍历图谱，返回 entityID → depth 映射
// BFS traverse graph, returns entityID → depth mapping
func (s *GraphStage) bfsTraverse(ctx context.Context, seeds map[string]int) map[string]int {
	visited := make(map[string]int, len(seeds))
	currentEntities := make([]string, 0, len(seeds))
	for id := range seeds {
		visited[id] = 0
		currentEntities = append(currentEntities, id)
	}

	for d := 1; d <= s.maxDepth; d++ {
		var nextEntities []string
		for _, entityID := range currentEntities {
			// 扇出限制 / Fan-out cap
			if len(visited) >= maxVisitedEntities {
				logger.Info("graph: traversal truncated at entity cap",
					zap.Int("visited", len(visited)),
					zap.Int("max", maxVisitedEntities),
					zap.Int("depth", d),
				)
				break
			}
			relations, err := s.graphStore.GetEntityRelations(ctx, entityID)
			if err != nil {
				logger.Warn("graph: GetEntityRelations failed",
					zap.String("entity_id", entityID),
					zap.Error(err),
				)
				continue
			}
			for _, rel := range relations {
				for _, targetID := range []string{rel.SourceID, rel.TargetID} {
					if targetID == entityID {
						continue
					}
					if _, seen := visited[targetID]; !seen {
						visited[targetID] = d
						nextEntities = append(nextEntities, targetID)
					}
				}
			}
		}
		currentEntities = nextEntities
		if len(currentEntities) == 0 || len(visited) >= maxVisitedEntities {
			break
		}
	}

	return visited
}

// depthMemory 深度-记忆对（用于排序）/ Depth-memory pair for sorting
type depthMemory struct {
	mem   *model.Memory
	depth int
}

// collectMemories 收集所有已访问实体的关联记忆，去重并按深度排序
// Collect memories for all visited entities, deduplicate and sort by depth
func (s *GraphStage) collectMemories(ctx context.Context, visited map[string]int) []*model.SearchResult {
	memoryMap := make(map[string]*model.Memory)
	memoryDepth := make(map[string]int)

	for entityID, d := range visited {
		memories, err := s.graphStore.GetEntityMemories(ctx, entityID, s.entityLimit)
		if err != nil {
			logger.Warn("graph: GetEntityMemories failed",
				zap.String("entity_id", entityID),
				zap.Error(err),
			)
			continue
		}
		for _, mem := range memories {
			if _, exists := memoryMap[mem.ID]; !exists {
				memoryMap[mem.ID] = mem
				memoryDepth[mem.ID] = d
			} else if d < memoryDepth[mem.ID] {
				// 用更浅的深度（更高分数）/ Use shallower depth (higher score)
				memoryDepth[mem.ID] = d
			}
		}
	}

	// 按深度排序 / Sort by depth ascending
	sorted := make([]depthMemory, 0, len(memoryMap))
	for id, mem := range memoryMap {
		sorted = append(sorted, depthMemory{mem: mem, depth: memoryDepth[id]})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].depth < sorted[j].depth
	})

	// 构建结果，深度衰减评分 / Build results with depth-decay scoring
	results := make([]*model.SearchResult, 0, len(sorted))
	for _, dm := range sorted {
		score := 1.0 / float64(dm.depth+1)
		results = append(results, &model.SearchResult{
			Memory: dm.mem,
			Score:  score,
			Source: "graph",
		})
	}

	return results
}
