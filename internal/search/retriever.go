// Package search 检索业务逻辑 / Retrieval business logic
package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/pipeline/builtin"
	"iclude/internal/search/strategy"
	"iclude/pkg/scoring"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// minVectorSimilarity 最小向量相似度阈值 / Minimum cosine similarity threshold
const minVectorSimilarity = 0.3

// AccessTracker 访问追踪接口 / Access tracker interface
// 检索命中后异步记录访问，解耦 search 与 memory 包 / Decouples search from memory package
type AccessTracker interface {
	Track(memoryID string)
}

// Retriever 单轮检索器 / Single-round retriever
// CoreProvider 核心记忆提供者接口 / Core memory provider interface
type CoreProvider interface {
	GetCoreBlocksMultiScope(ctx context.Context, scopes []string, identity *model.Identity) ([]*model.Memory, error)
}

type Retriever struct {
	memStore     store.MemoryStore
	vecStore     store.VectorStore // 可为 nil / may be nil
	embedder     store.Embedder    // 可为 nil / may be nil
	graphStore   store.GraphStore  // 可为 nil / may be nil
	llm          llm.Provider      // 可为 nil / may be nil
	cfg          config.RetrievalConfig
	preprocessor *Preprocessor // 可为 nil / may be nil
	tracker      AccessTracker // 可为 nil / may be nil
	coreProvider CoreProvider  // 可为 nil / may be nil

	// 管线系统 / Pipeline system
	executor       *pipeline.Executor
	strategyAgent  *strategy.Agent
	ruleClassifier *strategy.RuleClassifier
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

// SetCoreProvider 设置核心记忆提供者（可选）/ Set core memory provider (optional)
func (r *Retriever) SetCoreProvider(cp CoreProvider) {
	r.coreProvider = cp
}

// InitPipeline 初始化管线系统 / Initialize pipeline system
// 在 NewRetriever 后、首次 Retrieve 前调用 / Call after NewRetriever, before first Retrieve
func (r *Retriever) InitPipeline() {
	registry := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  r.memStore,
		GraphStore:   r.graphStore,
		VectorStore:  r.vecStore,
		Embedder:     r.embedder,
		Timeline:     r.memStore,
		CoreProvider: r.coreProvider,
		LLM:          r.llm,
		Cfg:          r.cfg,
	}
	postStages := builtin.RegisterBuiltins(registry, deps)

	r.executor = pipeline.NewExecutor(registry, pipeline.WithPostStages(postStages...))

	rc := strategy.NewRuleClassifier(pipeline.PipelineExploration)
	r.ruleClassifier = rc
	r.strategyAgent = strategy.NewAgent(r.llm, rc, 5*time.Second)
}

// selectPipelineWithPlan 选择管线并返回查询计划 / Select pipeline and return query plan
func (r *Retriever) selectPipelineWithPlan(ctx context.Context, req *model.RetrieveRequest) (string, *pipeline.QueryPlan) {
	// 策略 Agent（LLM + 规则 fallback）/ Strategy agent (LLM + rule fallback)
	if r.strategyAgent != nil {
		name, plan, _ := r.strategyAgent.Select(ctx, req.Query)
		return name, plan
	}
	return pipeline.PipelineExploration, nil // 最终 fallback / ultimate fallback
}

// RetrieveResult 检索结果（含可选调试信息）/ Retrieve result with optional debug info
type RetrieveResult struct {
	Results      []*model.SearchResult
	PipelineInfo *PipelineDebugInfo // 仅 debug=true 时填充 / Only populated when debug=true
}

// PipelineDebugInfo 管线调试信息 / Pipeline debug information
type PipelineDebugInfo struct {
	PipelineName string              `json:"pipeline_name"`
	Traces       []pipeline.StageTrace `json:"traces"`
}

// retrieveViaPipeline 通过管线执行检索 / Execute retrieval via pipeline
func (r *Retriever) retrieveViaPipeline(ctx context.Context, req *model.RetrieveRequest) (*RetrieveResult, error) {
	// 1. 选择管线：请求级 override > 策略 Agent / Select pipeline: per-request override > strategy agent
	pipelineName := req.Pipeline
	var plan *pipeline.QueryPlan
	if pipelineName == "" {
		pipelineName, plan = r.selectPipelineWithPlan(ctx, req)
	}

	// 2. 构建初始状态 / Build initial state
	state := pipeline.NewState(req.Query, r.resolveIdentity(req))
	state.Plan = plan
	state.Metadata["request"] = req
	state.Filters = req.Filters
	if len(req.Embedding) > 0 {
		state.Embedding = req.Embedding
	}

	// 3. 执行管线 / Execute pipeline
	result, err := r.executor.Execute(ctx, pipelineName, state)
	if err != nil {
		return nil, fmt.Errorf("pipeline %q execution failed: %w", pipelineName, err)
	}

	// 4. 异步记录访问（与旧逻辑一致）/ Async access tracking (same as legacy)
	if r.tracker != nil {
		for _, res := range result.Candidates {
			if res.Memory != nil {
				r.tracker.Track(res.Memory.ID)
			}
		}
	}

	out := &RetrieveResult{Results: result.Candidates}
	// 5. 填充调试信息 / Populate debug info if requested
	if req.Debug {
		out.PipelineInfo = &PipelineDebugInfo{
			PipelineName: result.PipelineName,
			Traces:       result.Traces,
		}
	}

	return out, nil
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
// 管线模式优先，未初始化时走旧逻辑 / Pipeline mode preferred, falls back to legacy if not initialized
func (r *Retriever) Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	if req.Query == "" && len(req.Embedding) == 0 {
		return nil, fmt.Errorf("query or embedding is required: %w", model.ErrInvalidInput)
	}

	// 管线模式 / Pipeline mode
	if r.executor != nil {
		result, err := r.retrieveViaPipeline(ctx, req)
		if err != nil {
			return nil, err
		}
		return result.Results, nil
	}

	// 兼容模式：管线未初始化时走旧逻辑 / Legacy mode: fall back to old logic if pipeline not initialized
	return r.retrieveLegacy(ctx, req)
}

// RetrieveWithDebug 执行检索并返回调试信息 / Execute retrieval with debug info
// 管线模式时返回 trace，旧逻辑模式返回空调试信息 / Returns trace in pipeline mode, empty debug in legacy mode
func (r *Retriever) RetrieveWithDebug(ctx context.Context, req *model.RetrieveRequest) (*RetrieveResult, error) {
	if req.Query == "" && len(req.Embedding) == 0 {
		return nil, fmt.Errorf("query or embedding is required: %w", model.ErrInvalidInput)
	}

	if r.executor != nil {
		return r.retrieveViaPipeline(ctx, req)
	}

	results, err := r.retrieveLegacy(ctx, req)
	if err != nil {
		return nil, err
	}
	return &RetrieveResult{Results: results}, nil
}

// retrieveLegacy 旧检索逻辑（管线未初始化时的 fallback）/ Legacy retrieval logic (fallback when pipeline not initialized)
// 根据配置自动选择：仅 SQLite、仅 Qdrant、或双后端 RRF 融合
func (r *Retriever) retrieveLegacy(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
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
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("retrieval channel 1 (FTS+HyDE) panic recovered", zap.Any("panic", rv))
				}
			}()
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
					hydeWeight := r.cfg.Preprocess.ResolvedHyDEWeight()
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
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("retrieval channel 2 (Qdrant) panic recovered", zap.Any("panic", rv))
				}
			}()
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
			// 过滤低相似度结果 / Filter out low-similarity results
			if len(vecResults) > 0 {
				filtered := make([]*model.SearchResult, 0, len(vecResults))
				for _, vr := range vecResults {
					if vr.Score >= minVectorSimilarity {
						filtered = append(filtered, vr)
					}
				}
				vecResults = filtered
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
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("retrieval channel 3 (Graph) panic recovered", zap.Any("panic", rv))
				}
			}()
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
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("retrieval channel 4 (Temporal) panic recovered", zap.Any("panic", rv))
				}
			}()
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

	// Scope 优先级加权（session > project > user/core > other）/ Scope priority weighting
	results = ApplyScopePriority(results)

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

	// Core memory 注入（默认开启，IncludeCore=false 时跳过）/ Core memory injection
	includeCore := req.IncludeCore == nil || *req.IncludeCore
	if includeCore && r.coreProvider != nil {
		results = r.injectCoreMemories(ctx, req, results)
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

// Timeline 时间线查询 / Timeline query
func (r *Retriever) Timeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	return r.memStore.ListTimeline(ctx, req)
}

