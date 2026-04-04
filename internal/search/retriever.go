// Package search 检索业务逻辑 / Retrieval business logic
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/pkg/scoring"
	"iclude/internal/store"
	"iclude/pkg/tokenutil"

	"go.uber.org/zap"
)

// AccessTracker 访问追踪接口 / Access tracker interface
// 检索命中后异步记录访问，解耦 search 与 memory 包 / Decouples search from memory package
type AccessTracker interface {
	Track(memoryID string)
}

// Retriever 单轮检索器 / Single-round retriever
type Retriever struct {
	memStore     store.MemoryStore
	vecStore     store.VectorStore // 可为 nil / may be nil
	embedder     store.Embedder    // 可为 nil / may be nil
	graphStore   store.GraphStore  // 可为 nil / may be nil
	llm          llm.Provider      // 可为 nil / may be nil
	cfg          config.RetrievalConfig
	preprocessor *Preprocessor // 可为 nil / may be nil
	tracker      AccessTracker // 可为 nil / may be nil
}

// NewRetriever 创建检索器 / Create a new retriever
func NewRetriever(memStore store.MemoryStore, vecStore store.VectorStore, embedder store.Embedder, graphStore store.GraphStore, llm llm.Provider, cfg config.RetrievalConfig, preprocessor *Preprocessor, tracker AccessTracker) *Retriever {
	return &Retriever{
		memStore:     memStore,
		vecStore:     vecStore,
		embedder:     embedder,
		graphStore:   graphStore,
		llm:          llm,
		cfg:          cfg,
		preprocessor: preprocessor,
		tracker:      tracker,
	}
}

// resolveIdentity 从请求中构建身份，优先使用请求中的 OwnerID / Build identity from request, prefer request OwnerID
func (r *Retriever) resolveIdentity(req *model.RetrieveRequest) *model.Identity {
	ownerID := req.OwnerID
	if ownerID == "" {
		ownerID = model.SystemOwnerID
	}
	return &model.Identity{TeamID: req.TeamID, OwnerID: ownerID}
}

// Retrieve 执行检索 / Execute retrieval
// 根据配置自动选择：仅 SQLite、仅 Qdrant、或双后端 RRF 融合
func (r *Retriever) Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	if req.Query == "" && len(req.Embedding) == 0 {
		return nil, fmt.Errorf("query or embedding is required: %w", model.ErrInvalidInput)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	hasSQLite := r.memStore != nil
	hasVector := r.vecStore != nil
	filters := req.Filters

	// 预处理 / Preprocessing
	var plan *QueryPlan
	if r.preprocessor != nil && req.Query != "" {
		scope := ""
		if filters != nil {
			scope = filters.Scope
		}
		var err error
		plan, err = r.preprocessor.Process(ctx, req.Query, scope)
		if err != nil {
			logger.Warn("preprocess failed, using original query", zap.Error(err))
		}
	}

	// 确定各通道输入 / Determine per-channel inputs
	ftsQuery := req.Query
	semanticQuery := req.Query
	if plan != nil {
		if len(plan.Keywords) > 0 {
			ftsQuery = strings.Join(plan.Keywords, " ")
		}
		if plan.SemanticQuery != "" {
			semanticQuery = plan.SemanticQuery
		}
	}

	// 并行收集各路检索结果 / Collect results from each channel in parallel
	var mu sync.Mutex
	var rrfInputs []RRFInput
	var wg sync.WaitGroup

	appendInput := func(input RRFInput) {
		mu.Lock()
		rrfInputs = append(rrfInputs, input)
		mu.Unlock()
	}

	// 通道 1: SQLite FTS + HyDE（共享 SQLite，内部串行）/ Channel 1: FTS + HyDE (share SQLite, serial internally)
	if hasSQLite && ftsQuery != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// FTS5 检索 / FTS5 search
			var textResults []*model.SearchResult
			var err error
			if filters != nil {
				textResults, err = r.memStore.SearchTextFiltered(ctx, ftsQuery, filters, limit)
			} else {
				textResults, err = r.memStore.SearchText(ctx, ftsQuery, r.resolveIdentity(req), limit)
			}
			if err != nil {
				logger.Warn("text search failed", zap.Error(err))
			} else if len(textResults) > 0 {
				ftsWeight := r.cfg.FTSWeight
				if plan != nil {
					ftsWeight = plan.Weights.FTS
				}
				if ftsWeight == 0 {
					ftsWeight = 1.0
				}
				appendInput(RRFInput{Results: textResults, Weight: ftsWeight})
			}

			// HyDE 通道（串行跟在 FTS 后，共享 SQLite 连接）/ HyDE channel (serial after FTS, share SQLite)
			if plan != nil && plan.HyDEDoc != "" {
				var hydeResults []*model.SearchResult
				var hydeErr error
				if filters != nil {
					hydeResults, hydeErr = r.memStore.SearchTextFiltered(ctx, plan.HyDEDoc, filters, limit)
				} else {
					hydeResults, hydeErr = r.memStore.SearchText(ctx, plan.HyDEDoc, r.resolveIdentity(req), limit)
				}
				if hydeErr != nil {
					logger.Debug("HyDE search failed", zap.Error(hydeErr))
				} else if len(hydeResults) > 0 {
					hydeWeight := 0.8
					if plan.Weights.FTS > 0 {
						hydeWeight *= plan.Weights.FTS
					}
					appendInput(RRFInput{Results: hydeResults, Weight: hydeWeight})
				}
			}
		}()
	}

	// 通道 2: Qdrant 向量检索（独立 goroutine）/ Channel 2: Qdrant vector search (independent goroutine)
	if hasVector {
		wg.Add(1)
		go func() {
			defer wg.Done()
			embedding, err := r.resolveEmbedding(ctx, req.Embedding, semanticQuery)
			if err != nil {
				logger.Warn("failed to resolve embedding for search", zap.Error(err))
				return
			}
			if len(embedding) == 0 {
				return
			}
			var vecResults []*model.SearchResult
			if filters != nil {
				vecResults, err = r.vecStore.SearchFiltered(ctx, embedding, filters, limit)
			} else {
				vecResults, err = r.vecStore.Search(ctx, embedding, r.resolveIdentity(req), limit)
			}
			if err != nil {
				logger.Warn("vector search failed", zap.Error(err))
				return
			}
			if len(vecResults) > 0 {
				qdrantWeight := r.cfg.QdrantWeight
				if plan != nil {
					qdrantWeight = plan.Weights.Qdrant
				}
				if qdrantWeight == 0 {
					qdrantWeight = 1.0
				}
				appendInput(RRFInput{Results: vecResults, Weight: qdrantWeight})
			}
		}()
	}

	// 通道 3: Graph 图谱关联检索（独立 goroutine）/ Channel 3: Graph association retrieval (independent goroutine)
	graphEnabled := r.cfg.GraphEnabled
	if req.GraphEnabled != nil {
		graphEnabled = *req.GraphEnabled
	}
	if graphEnabled && r.graphStore != nil && req.Query != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scope := ""
			if filters != nil {
				scope = filters.Scope
			}
			var graphResults []*model.SearchResult
			if plan != nil && len(plan.Entities) > 0 {
				graphResults = r.graphRetrieveByEntities(ctx, plan.Entities, limit)
			} else {
				graphResults = r.graphRetrieve(ctx, req.Query, req.TeamID, scope, limit)
			}
			if len(graphResults) > 0 {
				graphWeight := r.cfg.GraphWeight
				if plan != nil {
					graphWeight = plan.Weights.Graph
				}
				if graphWeight == 0 {
					graphWeight = 0.8
				}
				appendInput(RRFInput{Results: graphResults, Weight: graphWeight})
			}
		}()
	}

	// 通道 4: 时间通道（独立 goroutine）/ Channel 4: Temporal channel (independent goroutine)
	if plan != nil && plan.Temporal && plan.TemporalCenter != nil && hasSQLite {
		wg.Add(1)
		go func() {
			defer wg.Done()
			temporalResults := r.temporalRetrieve(ctx, req, plan, limit)
			if len(temporalResults) > 0 {
				appendInput(RRFInput{Results: temporalResults, Weight: 1.2})
			}
		}()
	}

	wg.Wait()

	if len(rrfInputs) == 0 {
		if !hasSQLite && !hasVector {
			return nil, fmt.Errorf("no search backend available: %w", model.ErrStorageUnavailable)
		}
		return []*model.SearchResult{}, nil
	}

	// 单路直接返回，多路加权 RRF 融合 / Single channel returns directly, multi-channel uses weighted RRF
	var results []*model.SearchResult
	if len(rrfInputs) == 1 {
		results = rrfInputs[0].Results
		if len(results) > limit {
			results = results[:limit]
		}
	} else {
		results = MergeWeightedRRF(rrfInputs, defaultRRFK, limit)
	}

	// 回填空壳 Memory（Qdrant 返回的 Memory 只有 ID，需从 SQLite 补全）
	// Backfill incomplete Memory objects (Qdrant results only contain ID)
	results = r.backfillMemories(ctx, results)

	// 精排（在类型/强度加权前执行，先提升文本相关性）
	// Re-rank before kind/strength weighting to refine semantic/textual ordering first
	if reranker := r.resolveReranker(req); reranker != nil && req.Query != "" {
		results = reranker.Rerank(ctx, req.Query, results)
	}

	// 类型+层级权重（skill/决策类提权 + memory_class 加权）/ Kind + class weighting
	results = ApplyKindAndClassWeights(results)

	// Filter by memory_class if specified / 按 memory_class 过滤
	if req.MemoryClass != "" {
		filtered := make([]*model.SearchResult, 0, len(results))
		for _, r := range results {
			if r.Memory != nil && r.Memory.MemoryClass == req.MemoryClass {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	// 强度加权（在 MMR 前执行，使过期/弱记忆在多样性选择前降分）
	// Apply strength weighting before MMR so expired/weak memories are scored down before diversity selection
	results = scoring.ApplyStrengthWeighting(results, r.cfg.AccessAlpha)

	// 重排序：classWeight + strengthWeight 修改了分数，需要重新按分数排序
	// Re-sort after score modifications to ensure ranking reflects updated scores
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// MMR 多样性重排（需要 VectorStore，SQLite-only 模式自动跳过）/ MMR diversity re-ranking
	// per-request 覆盖全局配置 / Per-request fields override global config
	mmrEnabled := r.cfg.MMR.Enabled
	if req.MmrEnabled != nil {
		mmrEnabled = *req.MmrEnabled
	}
	mmrLambda := r.cfg.MMR.Lambda
	if req.MmrLambda != nil {
		mmrLambda = *req.MmrLambda
	}
	if mmrEnabled && r.vecStore != nil {
		results = MMRRerank(ctx, results, r.vecStore, mmrLambda, limit)
	}

	// 自适应重试：置信度低时放宽条件重查 / Adaptive retry: relax constraints when confidence is low
	if len(results) > 0 && results[0].Score < 0.3 && req.Query != "" && filters != nil && !req.NoRetry {
		logger.Debug("adaptive retry: low confidence, retrying without time filter",
			zap.Float64("top_score", results[0].Score),
		)
		retryReq := *req
		retryFilters := *filters
		retryFilters.HappenedAfter = nil
		retryFilters.HappenedBefore = nil
		if retryFilters.MinStrength > 0 {
			retryFilters.MinStrength *= 0.6
		}
		retryReq.Filters = &retryFilters
		retryReq.NoRetry = true
		retryResults, err := r.Retrieve(ctx, &retryReq)
		if err == nil && len(retryResults) > 0 && retryResults[0].Score > results[0].Score {
			results = retryResults
		}
	}

	// 异步记录访问 / Async-track access hits
	if r.tracker != nil {
		for _, res := range results {
			if res.Memory != nil {
				r.tracker.Track(res.Memory.ID)
			}
		}
	}

	return results, nil
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

// Timeline 时间线查询 / Timeline query
func (r *Retriever) Timeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	return r.memStore.ListTimeline(ctx, req)
}

// graphRetrieve 图谱关联检索 / Graph-based association retrieval
// 通过 FTS5 反查实体，遍历图谱关系，获取关联记忆
func (r *Retriever) graphRetrieve(ctx context.Context, query string, teamID string, scope string, limit int) []*model.SearchResult {
	// 阶段 1: FTS5 反查实体 / Phase 1: Reverse entity lookup from FTS5 hits
	ftsTop := r.cfg.GraphFTSTop
	if ftsTop <= 0 {
		ftsTop = 5
	}

	entityIDs := make(map[string]bool)
	ftsResults, err := r.memStore.SearchText(ctx, query, &model.Identity{TeamID: teamID, OwnerID: model.SystemOwnerID}, ftsTop)
	if err != nil {
		logger.Warn("graph: FTS5 search failed", zap.Error(err))
	} else {
		for _, result := range ftsResults {
			entities, err := r.graphStore.GetMemoryEntities(ctx, result.Memory.ID)
			if err != nil {
				logger.Warn("graph: GetMemoryEntities failed", zap.String("memory_id", result.Memory.ID), zap.Error(err))
				continue
			}
			for _, ent := range entities {
				entityIDs[ent.ID] = true
			}
		}
	}

	// 阶段 1.5: LLM fallback（FTS5 无实体命中时）/ LLM fallback when no entities found
	if len(entityIDs) == 0 && r.llm != nil {
		llmEntities := r.llmExtractEntities(ctx, query, scope)
		for _, id := range llmEntities {
			entityIDs[id] = true
		}
	}

	if len(entityIDs) == 0 {
		return nil
	}

	return r.graphTraverseAndCollect(ctx, entityIDs, limit)
}

// graphRetrieveByEntities 从预匹配的实体 ID 开始图谱检索 / Graph retrieval from pre-matched entity IDs
func (r *Retriever) graphRetrieveByEntities(ctx context.Context, entityIDs []string, limit int) []*model.SearchResult {
	if len(entityIDs) == 0 {
		return nil
	}
	seedIDs := make(map[string]bool, len(entityIDs))
	for _, id := range entityIDs {
		seedIDs[id] = true
	}
	return r.graphTraverseAndCollect(ctx, seedIDs, limit)
}

// graphTraverseAndCollect 从已知实体 ID 遍历图谱并收集关联记忆 / Traverse graph from entity IDs and collect associated memories
func (r *Retriever) graphTraverseAndCollect(ctx context.Context, seedEntityIDs map[string]bool, limit int) []*model.SearchResult {
	depth := r.cfg.GraphDepth
	if depth <= 0 {
		depth = 1
	}

	visited := make(map[string]int) // entityID → depth level
	currentEntities := make([]string, 0, len(seedEntityIDs))
	for id := range seedEntityIDs {
		visited[id] = 0
		currentEntities = append(currentEntities, id)
	}

	for d := 1; d <= depth; d++ {
		var nextEntities []string
		for _, entityID := range currentEntities {
			relations, err := r.graphStore.GetEntityRelations(ctx, entityID)
			if err != nil {
				logger.Warn("graph: GetEntityRelations failed", zap.String("entity_id", entityID), zap.Error(err))
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
		if len(currentEntities) == 0 {
			break
		}
	}

	entityLimit := r.cfg.GraphEntityLimit
	if entityLimit <= 0 {
		entityLimit = 10
	}

	memoryMap := make(map[string]*model.Memory)
	memoryDepth := make(map[string]int)
	for entityID, d := range visited {
		memories, err := r.graphStore.GetEntityMemories(ctx, entityID, entityLimit)
		if err != nil {
			logger.Warn("graph: GetEntityMemories failed", zap.String("entity_id", entityID), zap.Error(err))
			continue
		}
		for _, mem := range memories {
			if _, exists := memoryMap[mem.ID]; !exists {
				memoryMap[mem.ID] = mem
				memoryDepth[mem.ID] = d
			} else if d < memoryDepth[mem.ID] {
				memoryDepth[mem.ID] = d
			}
		}
	}

	type depthMem struct {
		mem   *model.Memory
		depth int
	}
	var sorted []depthMem
	for id, mem := range memoryMap {
		sorted = append(sorted, depthMem{mem: mem, depth: memoryDepth[id]})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].depth < sorted[j].depth
	})

	results := make([]*model.SearchResult, 0, len(sorted))
	for _, dm := range sorted {
		// [fix] 深度衰减评分: depth 0 → 1.0, depth 1 → 0.5, depth 2 → 0.33 ...
		depthScore := 1.0 / float64(dm.depth+1)
		results = append(results, &model.SearchResult{
			Memory: dm.mem,
			Score:  depthScore,
			Source: "graph",
		})
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// llmExtractEntities LLM 从查询中抽取实体名 / LLM extract entity names from query
func (r *Retriever) llmExtractEntities(ctx context.Context, query string, scope string) []string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	temp := 0.1
	resp, err := r.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "Extract entity names from the query. Output JSON: {\"entities\": [\"name1\", \"name2\"]}"},
			{Role: "user", Content: query},
		},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	})
	if err != nil {
		logger.Warn("graph: LLM entity extraction failed", zap.Error(err))
		return nil
	}

	var output struct {
		Entities []string `json:"entities"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &output); err != nil {
		logger.Warn("graph: LLM entity parse failed", zap.String("raw", resp.Content))
		return nil
	}

	// 在 GraphStore 中按名称精确匹配实体（索引查询，替代全量扫描）
	// Match entity names via indexed query (replaces O(N) full scan)
	var matchedIDs []string
	for _, name := range output.Entities {
		entities, err := r.graphStore.FindEntitiesByName(ctx, name, scope, 1)
		if err != nil {
			logger.Debug("graph: FindEntitiesByName failed", zap.String("name", name), zap.Error(err))
			continue
		}
		if len(entities) > 0 {
			matchedIDs = append(matchedIDs, entities[0].ID)
		}
	}
	return matchedIDs
}

// kindWeights 记忆类型权重 / Memory kind weights
var kindWeights = map[string]float64{
	"skill":   1.5,
	"profile": 1.2,
	"fact":    1.0,
	"note":    1.0,
}

// subKindWeights 子类型权重加成 / Sub-kind weight boost
var subKindWeights = map[string]float64{
	"pattern": 1.3,
	"case":    1.3,
}

// classWeights 记忆层级权重 / Memory class weights
var classWeights = map[string]float64{
	"procedural": 1.5,
	"semantic":   1.2,
	"episodic":   1.0,
}

// weightCap 最大权重上限，防止叠乘过度放大 / Max weight cap to prevent over-amplification
const weightCap = 2.0

// ApplyKindAndClassWeights 按 kind + memory_class 加权 / Weight results by kind and memory class
func ApplyKindAndClassWeights(results []*model.SearchResult) []*model.SearchResult {
	for _, r := range results {
		if r.Memory == nil {
			continue
		}
		w := 1.0
		if kw, ok := kindWeights[r.Memory.Kind]; ok {
			w = kw
		}
		if sw, ok := subKindWeights[r.Memory.SubKind]; ok {
			w *= sw
		}
		if cw, ok := classWeights[r.Memory.MemoryClass]; ok {
			w *= cw
		}
		if w > weightCap {
			w = weightCap
		}
		r.Score *= w
	}
	return results
}

// EstimateTokens 估算文本token数 / Estimate token count for text
// 委托给 pkg/tokenutil 统一实现 / Delegates to shared tokenutil package
func EstimateTokens(text string) int {
	return tokenutil.EstimateTokens(text)
}

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
