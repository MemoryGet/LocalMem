// Package search 图谱关联检索 / Graph-based association retrieval
package search

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// maxVisitedEntities 图谱遍历最大实体数，防止高连接度实体指数级扇出
// Maximum visited entities during graph traversal to prevent exponential fan-out
const maxVisitedEntities = 50

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

	// 阶段 1.5: 关键词直接匹配实体表（零 LLM，替代原 LLM fallback）
	// Keyword direct match against entity table (zero LLM, replaces LLM fallback)
	if len(entityIDs) == 0 {
		for _, kw := range strings.Fields(query) {
			if len([]rune(kw)) < 2 {
				continue
			}
			entities, err := r.graphStore.FindEntitiesByName(ctx, kw, scope, 3)
			if err != nil {
				continue
			}
			for _, ent := range entities {
				entityIDs[ent.ID] = true
			}
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
			// 扇出限制：已访问实体数超过上限时提前终止 / Fan-out limit: stop early when visited entities exceed cap
			if len(visited) >= maxVisitedEntities {
				logger.Info("graph: traversal truncated at entity cap",
					zap.Int("visited", len(visited)),
					zap.Int("max", maxVisitedEntities),
					zap.Int("depth", d),
				)
				break
			}
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
		if len(currentEntities) == 0 || len(visited) >= maxVisitedEntities {
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
