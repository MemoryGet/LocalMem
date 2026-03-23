// Package search 检索业务逻辑 / Retrieval business logic
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Retriever 单轮检索器 / Single-round retriever
type Retriever struct {
	memStore     store.MemoryStore
	vecStore     store.VectorStore // 可为 nil / may be nil
	embedder     store.Embedder    // 可为 nil / may be nil
	graphStore   store.GraphStore  // 可为 nil / may be nil
	llm          llm.Provider      // 可为 nil / may be nil
	cfg          config.RetrievalConfig
	preprocessor *Preprocessor // 可为 nil / may be nil
}

// NewRetriever 创建检索器 / Create a new retriever
func NewRetriever(memStore store.MemoryStore, vecStore store.VectorStore, embedder store.Embedder, graphStore store.GraphStore, llm llm.Provider, cfg config.RetrievalConfig, preprocessor *Preprocessor) *Retriever {
	return &Retriever{
		memStore:     memStore,
		vecStore:     vecStore,
		embedder:     embedder,
		graphStore:   graphStore,
		llm:          llm,
		cfg:          cfg,
		preprocessor: preprocessor,
	}
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
		// [fix] temporal 意图注入时间过滤 / Inject time filter for temporal intent
		if plan.Temporal && filters == nil {
			recent := time.Now().UTC().Add(-7 * 24 * time.Hour)
			filters = &model.SearchFilters{HappenedAfter: &recent}
		} else if plan.Temporal && filters != nil && filters.HappenedAfter == nil {
			recent := time.Now().UTC().Add(-7 * 24 * time.Hour)
			filters.HappenedAfter = &recent
		}
	}

	// 收集各路检索结果 / Collect results from each channel
	var rrfInputs []RRFInput

	// SQLite 全文检索
	if hasSQLite && ftsQuery != "" {
		var textResults []*model.SearchResult
		var err error
		if filters != nil {
			textResults, err = r.memStore.SearchTextFiltered(ctx, ftsQuery, filters, limit)
		} else {
			textResults, err = r.memStore.SearchText(ctx, ftsQuery, &model.Identity{TeamID: req.TeamID, OwnerID: model.SystemOwnerID}, limit)
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
			rrfInputs = append(rrfInputs, RRFInput{Results: textResults, Weight: ftsWeight})
		}
	}

	// Qdrant 向量检索（使用 semanticQuery 生成 embedding）
	if hasVector {
		embedding, err := r.resolveEmbedding(ctx, req.Embedding, semanticQuery)
		if err != nil {
			logger.Warn("failed to resolve embedding for search, falling back to text-only",
				zap.Error(err),
			)
			hasVector = false
		}
		if len(embedding) == 0 {
			hasVector = false
		}
		if hasVector {
			var vecResults []*model.SearchResult
			if filters != nil {
				vecResults, err = r.vecStore.SearchFiltered(ctx, embedding, filters, limit)
			} else {
				vecResults, err = r.vecStore.Search(ctx, embedding, &model.Identity{TeamID: req.TeamID, OwnerID: model.SystemOwnerID}, limit)
			}
			if err != nil {
				logger.Warn("vector search failed", zap.Error(err))
			} else if len(vecResults) > 0 {
				qdrantWeight := r.cfg.QdrantWeight
				if plan != nil {
					qdrantWeight = plan.Weights.Qdrant
				}
				if qdrantWeight == 0 {
					qdrantWeight = 1.0
				}
				rrfInputs = append(rrfInputs, RRFInput{Results: vecResults, Weight: qdrantWeight})
			}
		}
	}

	// Graph 图谱关联检索
	graphEnabled := r.cfg.GraphEnabled
	if req.GraphEnabled != nil {
		graphEnabled = *req.GraphEnabled
	}
	if graphEnabled && r.graphStore != nil && req.Query != "" {
		scope := ""
		if filters != nil {
			scope = filters.Scope
		}
		var graphResults []*model.SearchResult
		if plan != nil && len(plan.Entities) > 0 {
			// 预处理已匹配实体，跳过 FTS5 反查 / Preprocessor matched entities, skip FTS5 reverse lookup
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
			rrfInputs = append(rrfInputs, RRFInput{Results: graphResults, Weight: graphWeight})
		}
	}

	if len(rrfInputs) == 0 {
		if !hasSQLite && !hasVector {
			return nil, fmt.Errorf("no search backend available: %w", model.ErrStorageUnavailable)
		}
		return nil, fmt.Errorf("text query is required when vector store is unavailable: %w", model.ErrInvalidInput)
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

	// MMR 多样性重排（需要 VectorStore，SQLite-only 模式自动跳过）
	if r.cfg.MMR.Enabled && r.vecStore != nil {
		results = MMRRerank(ctx, results, r.vecStore, r.cfg.MMR.Lambda, limit)
	}

	return memory.ApplyStrengthWeighting(results, r.cfg.AccessAlpha), nil
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
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].depth < sorted[i].depth {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

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
	ctx, cancel := context.WithTimeout(ctx, 10*1000*1000*1000) // 10s timeout
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

	// 在 GraphStore 中匹配实体名 / Match entity names in GraphStore
	var matchedIDs []string
	allEntities, err := r.graphStore.ListEntities(ctx, scope, "", 100)
	if err != nil {
		logger.Warn("graph: ListEntities failed", zap.Error(err))
		return nil
	}

	for _, name := range output.Entities {
		for _, ent := range allEntities {
			if strings.EqualFold(ent.Name, name) {
				matchedIDs = append(matchedIDs, ent.ID)
				break
			}
		}
	}
	return matchedIDs
}

// EstimateTokens 估算文本token数 / Estimate token count for text
// 使用 rune 数作为估算：中文 1 rune ≈ 1 token，英文偏保守（安全）
func EstimateTokens(text string) int {
	return len([]rune(text))
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
