package stage

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// decayWeight 查询时计算时间衰减 / Query-time age decay
func decayWeight(lambda float64, t time.Time) float64 {
	if lambda <= 0 {
		return 1.0
	}
	days := time.Since(t).Hours() / 24.0
	if days < 0 {
		days = 0
	}
	return math.Exp(-lambda * days)
}

// edgeConfidence 由关系共现次数推导边置信度，饱和到 [0.5, 1)。
// mentionCount <= 0 视为 1（与 store 层默认一致），返回 0.5。
// Edge confidence from co-occurrence count, saturating in [0.5, 1).
// mentionCount <= 0 is treated as 1 (matching store-layer default), returning 0.5.
func edgeConfidence(mentionCount int) float64 {
	if mentionCount <= 0 {
		mentionCount = 1
	}
	return 1.0 - 1.0/float64(1+mentionCount)
}

// 图谱遍历常量 / Graph traversal constants
const (
	defaultGraphMaxDepth    = 2
	defaultGraphLimit       = 30
	defaultGraphFTSTop      = 5
	defaultGraphEntityLimit = 10
	defaultMaxVisited       = 50
)

// GraphStage 图谱关联检索阶段 / Graph-based association retrieval stage
type GraphStage struct {
	graphStore       GraphRetriever
	ftsSearcher      FTSSearcher
	embedder         Embedder        // 可选：用于边证据向量化 / Optional: embeds query for edge evidence scoring
	vectorSearcher   VectorSearcher  // 可选：向量证据匹配集来源 / Optional: source for vector-based evidence matched set
	salienceChecker  SalienceChecker // 可选：hub 节点检测 / Optional: hub node detector
	salienceMaxCount int             // hub 阈值（记忆数），0 = 禁用 / Hub threshold (memory count), 0 = disabled
	ftsFirstSeeds    bool            // Option B: FTS 优先种子策略（过滤 hub 后以 FTS 结果实体为 BFS 起点）
	maxDepth         int
	limit            int
	ftsTop           int
	entityLimit      int
	maxVisited       int     // BFS 最大访问实体数 / Max entities visited during BFS traversal
	lambda           float64 // 时间衰减系数 / Time decay lambda
	pathConfAlpha    float64 // 路径置信度权重 α∈[0,1]，0=禁用 / Path confidence weight, 0 disables
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

// WithMaxVisited 设置 BFS 遍历最大实体数（控制截断阈值）/ Set max entities visited during BFS traversal
func WithMaxVisited(n int) GraphOption {
	return func(s *GraphStage) {
		if n > 0 {
			s.maxVisited = n
		}
	}
}

// WithDecayLambda 设置时间衰减系数 / Set time decay lambda
func WithDecayLambda(lambda float64) GraphOption {
	return func(s *GraphStage) { s.lambda = lambda }
}

// WithPathConfidence 启用多跳路径置信度加权（基于边 MentionCount，min 瓶颈聚合）。
// alpha 钳制到 [0,1]；alpha=0（默认）完全禁用，得分不受影响。
// Enable multi-hop path-confidence weighting (edge MentionCount, min aggregation).
// alpha is clamped to [0,1]; alpha=0 (default) fully disables — scores unaffected.
func WithPathConfidence(alpha float64) GraphOption {
	return func(s *GraphStage) {
		if alpha < 0 {
			alpha = 0
		} else if alpha > 1 {
			alpha = 1
		}
		s.pathConfAlpha = alpha
	}
}

// WithSalienceFilter 注入 hub 节点检测器，在 BFS 中跳过高频实体的扩张
// Inject hub node detector; BFS skips expanding from entities that appear in too many memories.
// maxCount: entities appearing in >= maxCount memories are treated as hubs. 0 = disabled.
func WithSalienceFilter(checker SalienceChecker, maxCount int) GraphOption {
	return func(s *GraphStage) {
		if checker != nil && maxCount > 0 {
			s.salienceChecker = checker
			s.salienceMaxCount = maxCount
		}
	}
}

// WithVectorEvidence 注入向量化工具，启用基于语义的边证据过滤
// Inject embedder + vector searcher to enable semantic edge evidence filtering.
// When set, edges whose source_memory_id is not in the vector matched set are skipped.
// When not set (default), BFS traversal behaves as before (no edge filtering).
func WithVectorEvidence(embedder Embedder, searcher VectorSearcher) GraphOption {
	return func(s *GraphStage) {
		s.embedder = embedder
		s.vectorSearcher = searcher
	}
}

// WithFTSFirstSeeds 启用 FTS 优先种子策略（Option B）。
// 以 FTS 检索 top-K 记忆的关联实体作为 BFS 种子，并在种子层面过滤 hub 实体（需同时配置 WithSalienceFilter）。
// 适合超级节点密集的图谱：BFS 从语义相关实体出发，而非从查询关键词直接匹配的 hub 节点出发。
// Enable FTS-first seed strategy (Option B). BFS seeds are derived from entities linked to FTS
// top-K results, filtered at seed level when SalienceFilter is also configured.
func WithFTSFirstSeeds() GraphOption {
	return func(s *GraphStage) { s.ftsFirstSeeds = true }
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
		maxVisited:  defaultMaxVisited,
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
			Name:    s.Name(),
			Skipped: true,
			Note:    "graphStore is nil",
		})
		return state, nil
	}

	// 提前构建 hub 集合：既用于种子层面过滤（Option B），也用于 BFS 扩张跳过（Option A）
	// Build hub set early: used for seed-level filtering (Option B) and BFS skip (Option A).
	hubEntities := s.buildHubSet(ctx)

	// 阶段 1: 获取种子实体 / Phase 1: Resolve seed entities
	seedEntities := s.resolveSeedEntities(ctx, state, hubEntities)
	if len(seedEntities) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: 0,
			Note:        "no seed entities found",
		})
		return state, nil
	}

	// 阶段 2: BFS 遍历图谱（可选：向量边证据过滤 + hub 节点跳过）
	// Phase 2: BFS with optional vector edge evidence filtering and hub entity skip.
	vecMatchedIDs := s.buildVectorMatchedSet(ctx, state)
	visited := s.bfsTraverse(ctx, seedEntities, vecMatchedIDs, hubEntities)

	// 阶段 3: 收集关联记忆 / Phase 3: Collect associated memories
	results := s.collectMemories(ctx, visited)

	// 截断结果 / Truncate to limit
	if len(results) > s.limit {
		results = results[:s.limit]
	}

	// 追加结果（不替换已有候选）/ Append results (don't replace existing candidates)
	state.Candidates = append(state.Candidates, results...)

	return state, nil
}

// resolveSeedEntities 解析种子实体。
// Option B (ftsFirstSeeds=true): FTS 优先 → 结果实体过滤 hub 后为种子；若无结果则降级到关键词路径。
// 默认路径：Plan 实体 → 关键词匹配 → FTS 反查（三路径，零 LLM）。
// hubEntities: 由 buildHubSet 提前构建，Option B 时在种子层面过滤 hub；nil 表示不过滤。
//
// Resolve seed entities.
// Option B (ftsFirstSeeds=true): FTS first — entities from FTS results minus hubs become seeds;
// falls through to keyword paths when no non-hub seeds are found.
// Default: Plan → keyword direct match → FTS reverse lookup (zero LLM).
// hubEntities: pre-built by buildHubSet; when non-nil, hub entities are filtered from seeds (Option B only).
func (s *GraphStage) resolveSeedEntities(ctx context.Context, state *pipeline.PipelineState, hubEntities map[string]struct{}) map[string]int {
	seeds := make(map[string]int) // entityID → depth (0 for seeds)
	scope := s.resolveScope(state)

	// Option B: FTS 优先种子策略 — 以 FTS top-K 结果中的实体（过滤 hub 后）作为 BFS 起点。
	// 相比关键词匹配，FTS 返回的记忆在语义上与 query 更相关，其关联实体更有代表性。
	// Option B: FTS-first seed strategy — entities from FTS top-K results (hub-filtered) become BFS roots.
	// More semantically relevant than keyword matching, and avoids seeding from hub super-nodes.
	if s.ftsFirstSeeds && s.ftsSearcher != nil && state.Query != "" {
		ftsResults, err := s.ftsSearcher.SearchText(ctx, state.Query, state.Identity, s.ftsTop)
		if err != nil {
			logger.Warn("graph: FTS-first seed lookup failed", zap.Error(err))
		} else {
			for _, result := range ftsResults {
				entities, err := s.graphStore.GetMemoryEntities(ctx, result.Memory.ID)
				if err != nil {
					continue
				}
				for _, ent := range entities {
					if hubEntities != nil {
						if _, isHub := hubEntities[ent.ID]; isHub {
							continue // hub 节点不作为种子，防止根部爆炸 / Skip hub entities as seeds
						}
					}
					seeds[ent.ID] = 0
				}
			}
		}
		if len(seeds) > 0 {
			return seeds
		}
		// FTS 未返回可用非 hub 种子，降级到关键词路径 / No non-hub seeds from FTS, fall through
	}

	// 路径 1: 从 Plan 中预提取的实体名查找 / Path 1: Look up pre-extracted entity names from Plan
	if state.Plan != nil && len(state.Plan.Entities) > 0 {
		for _, name := range state.Plan.Entities {
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

	// 路径 2: query 关键词直接匹配实体表（纯索引查询，零 LLM）
	// Path 2: match query keywords directly against entity table (index-only, zero LLM)
	if state.Query != "" {
		keywords := strings.Fields(state.Query)
		for _, kw := range keywords {
			if len([]rune(kw)) < 2 {
				continue // 跳过单字符词 / Skip single-char words
			}
			entities, err := s.graphStore.FindEntitiesByName(ctx, kw, scope, 3)
			if err != nil {
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

	// 路径 3: FTS 反查 → 获取记忆关联的实体 / Path 3: FTS reverse lookup → get memory entities
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

// buildHubSet 查询高频实体集合（出现在 >= salienceMaxCount 条记忆中的 hub 节点）
// Builds the set of hub entity IDs to skip during BFS expansion.
// Returns nil when salience filter is not configured.
func (s *GraphStage) buildHubSet(ctx context.Context) map[string]struct{} {
	if s.salienceChecker == nil || s.salienceMaxCount <= 0 {
		return nil
	}
	hubs, err := s.salienceChecker.GetHighFrequencyEntities(ctx, s.salienceMaxCount)
	if err != nil {
		logger.Warn("graph: buildHubSet failed", zap.Error(err))
		return nil
	}
	logger.Debug("graph: hub set built",
		zap.Int("hubs", len(hubs)),
		zap.Int("threshold", s.salienceMaxCount),
	)
	return hubs
}

// buildVectorMatchedSet 嵌入 query，通过向量检索构建证据记忆 ID 集合
// Embed the query and vector-search for semantically similar memories.
// Returns nil when embedder/vectorSearcher are not configured — disables edge filtering.
func (s *GraphStage) buildVectorMatchedSet(ctx context.Context, state *pipeline.PipelineState) map[string]struct{} {
	if s.embedder == nil || s.vectorSearcher == nil || state.Query == "" {
		return nil // 无向量支持，不过滤 / No vector support, skip filtering
	}
	queryVec, err := s.embedder.Embed(ctx, state.Query)
	if err != nil {
		logger.Warn("graph: embed query for edge evidence failed", zap.Error(err))
		return nil
	}
	const vectorEvidenceLimit = 200
	results, err := s.vectorSearcher.Search(ctx, queryVec, state.Identity, vectorEvidenceLimit)
	if err != nil {
		logger.Warn("graph: vector search for edge evidence failed", zap.Error(err))
		return nil
	}
	ids := make(map[string]struct{}, len(results))
	for _, r := range results {
		ids[r.Memory.ID] = struct{}{}
	}
	logger.Debug("graph: vector evidence matched set built",
		zap.Int("matched_memories", len(ids)),
	)
	return ids
}

// resolveScope 从 Identity 或 Metadata 解析 scope / Resolve scope from Identity or Metadata
func (s *GraphStage) resolveScope(state *pipeline.PipelineState) string {
	if state.Filters != nil && state.Filters.Scope != "" {
		return state.Filters.Scope
	}
	if state.Identity != nil {
		return state.Identity.TeamID
	}
	return ""
}

// bfsTraverse BFS 遍历图谱，返回 entityID → entityReach（含最浅深度与最优路径置信度）
// BFS traverse graph, returns entityID → entityReach (shallowest depth + best path confidence).
// vecMatchedIDs: semantic evidence set; edges with source_memory_id not in set are skipped (nil = no filter).
// hubEntities: high-frequency entities to skip expansion from (nil = no filter).
// 路径置信度：种子 conf=1.0，子实体 conf=min(父 conf, edgeConfidence(边 MentionCount))；同深取 max。
// Path confidence: seeds conf=1.0; child conf=min(parent conf, edge confidence); same-depth keeps max.
func (s *GraphStage) bfsTraverse(ctx context.Context, seeds map[string]int, vecMatchedIDs map[string]struct{}, hubEntities map[string]struct{}) map[string]entityReach {
	visited := make(map[string]entityReach, len(seeds))
	currentEntities := make([]string, 0, len(seeds))
	for id := range seeds {
		visited[id] = entityReach{depth: 0, conf: 1.0}
		currentEntities = append(currentEntities, id)
	}

	for d := 1; d <= s.maxDepth; d++ {
		var nextEntities []string
		for _, entityID := range currentEntities {
			// 扇出限制 / Fan-out cap
			if len(visited) >= s.maxVisited {
				logger.Info("graph: traversal truncated at entity cap",
					zap.Int("visited", len(visited)),
					zap.Int("max", s.maxVisited),
					zap.Int("depth", d),
				)
				break
			}
			// Hub 节点跳过扩张（已访问，记忆仍会被收集；仅跳过向外展开）
			// Skip expanding from hub entities — still visited; memories still collected.
			if hubEntities != nil {
				if _, isHub := hubEntities[entityID]; isHub {
					continue
				}
			}
			parentConf := visited[entityID].conf
			relations, err := s.graphStore.GetEntityRelations(ctx, entityID)
			if err != nil {
				logger.Warn("graph: GetEntityRelations failed",
					zap.String("entity_id", entityID),
					zap.Error(err),
				)
				continue
			}
			for _, rel := range relations {
				// 边证据过滤：有 source_memory_id 但不匹配 query 时跳过此边
				// Skip edges whose evidence memory is not relevant to the current query.
				if rel.SourceMemoryID != "" && vecMatchedIDs != nil {
					if _, ok := vecMatchedIDs[rel.SourceMemoryID]; !ok {
						continue
					}
				}
				childConf := min(parentConf, edgeConfidence(rel.MentionCount))
				for _, targetID := range []string{rel.SourceID, rel.TargetID} {
					if targetID == entityID {
						continue
					}
					existing, seen := visited[targetID]
					if !seen {
						// 首次访问：记录深度与 conf，入队 / First visit: record and enqueue
						visited[targetID] = entityReach{depth: d, conf: childConf}
						nextEntities = append(nextEntities, targetID)
					} else if existing.depth == d && childConf > existing.conf {
						// 同深更优路径：仅更新 conf，不重复入队 / Same depth better path: update conf only
						existing.conf = childConf
						visited[targetID] = existing
					}
				}
			}
		}
		currentEntities = nextEntities
		if len(currentEntities) == 0 || len(visited) >= s.maxVisited {
			break
		}
	}

	return visited
}

// entityReach BFS 到达某实体的最优路径信息 / Best-path reach info for an entity during BFS
type entityReach struct {
	depth int     // 最浅到达深度 / Shallowest reach depth
	conf  float64 // 该深度下的最优路径置信度 / Best path confidence at that depth
}

// depthMemory 深度-记忆对（用于排序）/ Depth-memory pair for sorting
type depthMemory struct {
	mem   *model.Memory
	depth int
}

// collectMemories 收集所有已访问实体的关联记忆，去重并按深度排序，应用路径置信度加权
// Collect memories for all visited entities, deduplicate, sort by depth, apply path-confidence weighting.
func (s *GraphStage) collectMemories(ctx context.Context, visited map[string]entityReach) []*model.SearchResult {
	memoryMap := make(map[string]*model.Memory)
	memoryDepth := make(map[string]int)
	memoryConf := make(map[string]float64)

	for entityID, reach := range visited {
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
				memoryDepth[mem.ID] = reach.depth
				memoryConf[mem.ID] = reach.conf
			} else if reach.depth < memoryDepth[mem.ID] {
				// 更浅深度：同时更新深度与 conf / Shallower: update both depth and conf
				memoryDepth[mem.ID] = reach.depth
				memoryConf[mem.ID] = reach.conf
			} else if reach.depth == memoryDepth[mem.ID] && reach.conf > memoryConf[mem.ID] {
				// 同深更优路径：仅更新 conf / Same depth better path: update conf only
				memoryConf[mem.ID] = reach.conf
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

	// 构建结果：深度衰减 × 时间衰减 × 路径置信度因子
	// Build results: depth-decay × time-decay × path-confidence factor.
	results := make([]*model.SearchResult, 0, len(sorted))
	for _, dm := range sorted {
		depthScore := 1.0 / float64(dm.depth+1)
		memTime := dm.mem.CreatedAt
		if dm.mem.HappenedAt != nil {
			memTime = *dm.mem.HappenedAt
		}
		pathConfFactor := (1.0 - s.pathConfAlpha) + s.pathConfAlpha*memoryConf[dm.mem.ID]
		score := depthScore * decayWeight(s.lambda, memTime) * pathConfFactor
		results = append(results, &model.SearchResult{
			Memory: dm.mem,
			Score:  score,
			Source: SourceGraph,
		})
	}

	return results
}
