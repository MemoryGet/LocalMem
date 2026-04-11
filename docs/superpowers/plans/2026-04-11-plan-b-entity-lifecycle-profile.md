# Plan B: Entity Lifecycle + Profile + Discovery

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add time decay to graph scoring, entity profile view API, search result entity enrichment, heartbeat relation cleanup, and ingest noise pre-filter.

**Architecture:** Time decay is applied at query-time in graph stage BFS scoring. Entity Profile aggregates existing store queries in GraphManager. Search results are enriched with a batch GetMemoriesEntities call post-pipeline. Heartbeat wires the cleanup methods from Plan A. Noise filter is a pre-check at ingest boundary.

**Tech Stack:** Go 1.25+, SQLite, Gin, table-driven tests

**Spec:** `docs/superpowers/specs/2026-04-10-storage-architecture-upgrade-design.md` sections 2.3–2.5, 3.3, 3.4

**Depends on:** Plan A (completed) — V26 schema, lifecycle methods, config fields

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/search/stage/graph.go` | Add time decay to depth scoring |
| Modify | `internal/search/retriever_graph.go` | Add time decay to legacy graph scoring |
| Modify | `internal/model/graph.go` | Add EntityProfile struct |
| Modify | `internal/memory/graph_manager.go` | Add GetEntityProfile method |
| Modify | `internal/api/graph_handler.go` | Add EntityProfile + EntitySearch handlers |
| Modify | `internal/api/router.go` | Register new routes |
| Modify | `internal/search/retriever.go` | Add entity enrichment to search results |
| Modify | `internal/model/search.go` or relevant | Add Entities field to SearchResult |
| Create | `internal/heartbeat/relation_cleanup.go` | Stale relation + orphan entity cleanup |
| Modify | `internal/heartbeat/engine.go` | Wire relation cleanup into Run() |
| Create | `testing/store/entity_profile_test.go` | Entity profile tests |
| Create | `testing/search/time_decay_test.go` | Time decay scoring tests |

---

### Task 1: Time Decay in Graph Stage Scoring

**Files:**
- Modify: `internal/search/stage/graph.go` (collectMemories, lines 315-324)
- Modify: `internal/search/retriever_graph.go` (graphTraverseAndCollect, line 171)

- [ ] **Step 1: Add decay helper function to graph.go**

At the top of `internal/search/stage/graph.go`, add after the imports:

```go
import "math"

// decayWeight 查询时计算关系时间衰减 / Query-time relation time decay
// effective = weight × e^(-λ × daysSinceLastSeen)
func decayWeight(weight float64, lastSeen *time.Time, lambda float64) float64 {
	if lastSeen == nil || lambda <= 0 {
		return weight
	}
	days := time.Since(*lastSeen).Hours() / 24.0
	if days < 0 {
		days = 0
	}
	return weight * math.Exp(-lambda*days)
}
```

- [ ] **Step 2: Add lambda config to GraphStage**

In the `GraphStage` struct, add a `lambda` field. In the options, add `WithDecayLambda`:

```go
type GraphStage struct {
	graphStore GraphRetriever
	ftsSearch  FTSSearcher
	maxDepth   int
	limit      int
	ftsTop     int
	entityLimit int
	lambda      float64 // 关系时间衰减系数 / Relation decay lambda
}

func WithDecayLambda(lambda float64) GraphOption {
	return func(s *GraphStage) { s.lambda = lambda }
}
```

- [ ] **Step 3: Apply time decay in collectMemories scoring**

In `collectMemories()`, modify the score calculation (around line 315-324). The depth score needs to incorporate relation weight decay. Currently the function just has `depth` info. To get relation weights, we need to modify the BFS traversal to track the relation weight at each hop.

Instead of tracking just `visited map[string]int` (entityID → depth), change the scoring to combine depth with a time-based decay on the **memory's** own age:

```go
for _, dm := range sorted {
	depthScore := 1.0 / float64(dm.depth+1)
	// 记忆自身的时间衰减 / Memory age decay
	memAge := dm.mem.CreatedAt
	if dm.mem.HappenedAt != nil {
		memAge = *dm.mem.HappenedAt
	}
	ageDecay := decayWeight(1.0, &memAge, s.lambda)
	score := depthScore * ageDecay
	results = append(results, &model.SearchResult{
		Memory: dm.mem,
		Score:  score,
		Source: SourceGraph,
	})
}
```

- [ ] **Step 4: Pass lambda in pipeline builtin registration**

In `internal/search/pipeline/builtin/builtin.go`, wherever `NewGraphStage` is called, add `stage.WithDecayLambda(deps.Cfg.RelationDecayLambda)`. This affects `buildPrecision`, `buildExploration`, `buildAssociation`, `buildFull`.

- [ ] **Step 5: Apply same decay in legacy retriever_graph.go**

In `internal/search/retriever_graph.go`, in `graphTraverseAndCollect` (around line 171), apply the same pattern:

```go
memAge := dm.mem.CreatedAt
if dm.mem.HappenedAt != nil {
	memAge = *dm.mem.HappenedAt
}
days := time.Since(memAge).Hours() / 24.0
if days < 0 {
	days = 0
}
ageDecay := math.Exp(-r.cfg.RelationDecayLambda * days)
depthScore := 1.0 / float64(dm.depth+1) * ageDecay
```

- [ ] **Step 6: Verify build**

Run: `go build ./...`

- [ ] **Step 7: Commit**

```bash
git commit -m "feat(search): add time decay scoring to graph stage and legacy retriever"
```

---

### Task 2: EntityProfile Model + GraphManager Method

**Files:**
- Modify: `internal/model/graph.go` (add EntityProfile struct)
- Modify: `internal/memory/graph_manager.go` (add GetEntityProfile)

- [ ] **Step 1: Add EntityProfile struct**

In `internal/model/graph.go`, add after Tag struct:

```go
// EntityProfile 实体聚合视图 / Entity profile aggregation view
type EntityProfile struct {
	Entity     *Entity                   `json:"entity"`
	Relations  []*EntityRelation          `json:"relations"`
	BySource   map[string][]*Memory       `json:"by_source"`   // source_type:source_ref → memories
	ByTimeline map[string][]*Memory       `json:"by_timeline"` // YYYY-MM → memories
	ByScope    map[string]int             `json:"by_scope"`    // scope → count
	TotalMemories int                     `json:"total_memories"`
}
```

- [ ] **Step 2: Implement GetEntityProfile in GraphManager**

In `internal/memory/graph_manager.go`, add:

```go
// GetEntityProfile 获取实体聚合视图 / Get entity profile aggregation
func (m *GraphManager) GetEntityProfile(ctx context.Context, entityID string, limit int) (*model.EntityProfile, error) {
	if entityID == "" {
		return nil, fmt.Errorf("entity id is required: %w", model.ErrInvalidInput)
	}
	if limit <= 0 {
		limit = 50
	}

	// 并行查询 / Parallel queries
	type entityResult struct {
		entity *model.Entity
		err    error
	}
	type relResult struct {
		relations []*model.EntityRelation
		err       error
	}
	type memResult struct {
		memories []*model.Memory
		err      error
	}

	eCh := make(chan entityResult, 1)
	rCh := make(chan relResult, 1)
	mCh := make(chan memResult, 1)

	go func() {
		e, err := m.graphStore.GetEntity(ctx, entityID)
		eCh <- entityResult{e, err}
	}()
	go func() {
		r, err := m.graphStore.GetEntityRelations(ctx, entityID)
		rCh <- relResult{r, err}
	}()
	go func() {
		mem, err := m.graphStore.GetEntityMemories(ctx, entityID, limit)
		mCh <- memResult{mem, err}
	}()

	eRes := <-eCh
	if eRes.err != nil {
		return nil, eRes.err
	}
	rRes := <-rCh
	if rRes.err != nil {
		return nil, fmt.Errorf("failed to get entity relations: %w", rRes.err)
	}
	mRes := <-mCh
	if mRes.err != nil {
		return nil, fmt.Errorf("failed to get entity memories: %w", mRes.err)
	}

	// Go 层分组 / Group in Go
	bySource := make(map[string][]*model.Memory)
	byTimeline := make(map[string][]*model.Memory)
	byScope := make(map[string]int)

	for _, mem := range mRes.memories {
		// 按来源分组 / Group by source
		srcKey := mem.SourceType + ":" + mem.SourceRef
		bySource[srcKey] = append(bySource[srcKey], mem)

		// 按月份分组 / Group by month
		ts := mem.CreatedAt
		if mem.HappenedAt != nil {
			ts = *mem.HappenedAt
		}
		monthKey := ts.Format("2006-01")
		byTimeline[monthKey] = append(byTimeline[monthKey], mem)

		// 按 scope 计数 / Count by scope
		byScope[mem.Scope]++
	}

	return &model.EntityProfile{
		Entity:        eRes.entity,
		Relations:     rRes.relations,
		BySource:      bySource,
		ByTimeline:    byTimeline,
		ByScope:       byScope,
		TotalMemories: len(mRes.memories),
	}, nil
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(memory): add EntityProfile model and GraphManager.GetEntityProfile"
```

---

### Task 3: Entity Profile + Search API Endpoints

**Files:**
- Modify: `internal/api/graph_handler.go` (add 2 handlers)
- Modify: `internal/api/router.go` (register routes)

- [ ] **Step 1: Add GetEntityProfile handler**

In `internal/api/graph_handler.go`, add:

```go
// GetEntityProfile 获取实体聚合视图 / Get entity profile aggregation
func (h *GraphHandler) GetEntityProfile(c *gin.Context, identity *model.Identity) {
	id := c.Param("id")
	if id == "" {
		Error(c, fmt.Errorf("entity id is required: %w", model.ErrInvalidInput))
		return
	}
	limitStr := c.DefaultQuery("limit", "50")
	limit := 50
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}

	profile, err := h.manager.GetEntityProfile(c.Request.Context(), id, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, profile)
}
```

- [ ] **Step 2: Add SearchEntities handler**

```go
// SearchEntities 按名称搜索实体 / Search entities by name
func (h *GraphHandler) SearchEntities(c *gin.Context, identity *model.Identity) {
	q := c.Query("q")
	if q == "" {
		Error(c, fmt.Errorf("query parameter 'q' is required: %w", model.ErrInvalidInput))
		return
	}
	scope := c.Query("scope")
	limitStr := c.DefaultQuery("limit", "20")
	limit := 20
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}

	entities, err := h.manager.FindEntitiesByName(c.Request.Context(), q, scope, limit)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, entities)
}
```

- [ ] **Step 3: Register routes in router.go**

In `internal/api/router.go`, inside the `if deps.GraphManager != nil` block, add:

```go
v1.GET("/entities/search", withIdentity(graphHandler.SearchEntities))
v1.GET("/entities/:id/profile", withIdentity(graphHandler.GetEntityProfile))
```

IMPORTANT: `/entities/search` must be registered BEFORE `/entities/:id` to avoid route conflict (`:id` would match "search" as a param).

- [ ] **Step 4: Add strconv import if missing**

Ensure `"strconv"` is in the import block of `graph_handler.go`.

- [ ] **Step 5: Verify build**

Run: `go build ./...`

- [ ] **Step 6: Commit**

```bash
git commit -m "feat(api): add entity profile and entity search endpoints"
```

---

### Task 4: Search Result Entity Enrichment

**Files:**
- Modify: `internal/model/search.go` or wherever SearchResult is defined
- Modify: `internal/search/retriever.go` (add enrichment step)

- [ ] **Step 1: Find and update SearchResult struct**

Search for `type SearchResult struct` in the codebase. Add an `Entities` field:

```go
type SearchResult struct {
	Memory   *Memory   `json:"memory"`
	Score    float64   `json:"score"`
	Source   string    `json:"source"`
	Entities []*Entity `json:"entities,omitempty"` // 关联实体 / Associated entities
}
```

- [ ] **Step 2: Add entity enrichment in Retriever**

In `internal/search/retriever.go`, find the main retrieve method that returns results. After results are assembled but before returning, add entity enrichment:

```go
// 实体发现：批量加载命中记忆的实体 / Entity discovery: batch-load entities for result memories
func (r *Retriever) enrichWithEntities(ctx context.Context, results []*model.SearchResult) {
	if r.graphStore == nil || len(results) == 0 {
		return
	}
	memIDs := make([]string, 0, len(results))
	for _, sr := range results {
		if sr.Memory != nil {
			memIDs = append(memIDs, sr.Memory.ID)
		}
	}
	entitiesMap, err := r.graphStore.GetMemoriesEntities(ctx, memIDs)
	if err != nil {
		// 非阻塞：实体加载失败不影响搜索结果 / Non-blocking: entity load failure doesn't affect search
		return
	}
	for _, sr := range results {
		if sr.Memory != nil {
			sr.Entities = entitiesMap[sr.Memory.ID]
		}
	}
}
```

Call this method at the appropriate point in the retrieve flow (before returning results to the caller).

- [ ] **Step 3: Verify Retriever has graphStore dependency**

Check if `Retriever` struct has a `graphStore store.GraphStore` field. If not, add it and update the constructor `NewRetriever(...)`.

- [ ] **Step 4: Verify build**

Run: `go build ./...`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): enrich search results with associated entities"
```

---

### Task 5: Heartbeat Relation Cleanup

**Files:**
- Create: `internal/heartbeat/relation_cleanup.go`
- Modify: `internal/heartbeat/engine.go` (wire into Run)

- [ ] **Step 1: Create relation_cleanup.go**

```go
package heartbeat

import (
	"context"
	"time"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// runRelationCleanup 清理过期弱关系 + 软删孤儿实体 / Cleanup stale relations + soft-delete orphan entities
func (e *Engine) runRelationCleanup(ctx context.Context) error {
	if e.graphStore == nil {
		return nil
	}

	// 清理弱关系：mention_count < 3 且 last_seen_at 超过 90 天 / Stale relations: low mention + old
	cutoff := time.Now().AddDate(0, 0, -90)
	deleted, err := e.graphStore.CleanupStaleRelations(ctx, 3, cutoff)
	if err != nil {
		logger.Warn("heartbeat: relation cleanup failed", zap.Error(err))
	} else if deleted > 0 {
		logger.Info("heartbeat: cleaned stale relations", zap.Int64("deleted", deleted))
	}

	// 软删孤儿实体 / Soft-delete orphan entities
	orphans, err := e.graphStore.CleanupOrphanEntities(ctx)
	if err != nil {
		logger.Warn("heartbeat: orphan entity cleanup failed", zap.Error(err))
	} else if orphans > 0 {
		logger.Info("heartbeat: soft-deleted orphan entities", zap.Int64("count", orphans))
	}

	// 硬删 30 天前软删的实体 / Purge entities soft-deleted over 30 days ago
	purgeCutoff := time.Now().AddDate(0, 0, -30)
	purged, err := e.graphStore.PurgeDeletedEntities(ctx, purgeCutoff)
	if err != nil {
		logger.Warn("heartbeat: entity purge failed", zap.Error(err))
	} else if purged > 0 {
		logger.Info("heartbeat: purged deleted entities", zap.Int64("count", purged))
	}

	return nil
}
```

- [ ] **Step 2: Wire into engine.go Run()**

In `internal/heartbeat/engine.go`, in the `Run()` method, add after existing cleanup tasks:

```go
// 关系清理 / Relation cleanup
if e.graphStore != nil {
	if err := e.runRelationCleanup(ctx); err != nil {
		logger.Warn("heartbeat: relation cleanup error", zap.Error(err))
	}
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(heartbeat): add relation cleanup — stale relations, orphan entities, purge"
```

---

### Task 6: Ingest Noise Pre-Filter

**Files:**
- Find the conversation ingest entry point (likely `internal/memory/manager.go` or `internal/mcp/tools/ingest.go`)
- Add noise filter check

- [ ] **Step 1: Locate the ingest entry point**

Search for where conversation messages are ingested (the `IngestConversation` or similar method). The noise filter should be the first check before any processing.

- [ ] **Step 2: Add noise filter function**

Create a helper (in the appropriate package, likely `internal/memory/`):

```go
// IsNoiseContent 检查内容是否为噪声 / Check if content is noise
func IsNoiseContent(content string, cfg config.NoiseFilterConfig) bool {
	// 长度检查 / Length check
	contentRunes := []rune(content)
	if cfg.MinContentLength > 0 && len(contentRunes) < cfg.MinContentLength {
		return true
	}
	// 自定义模式匹配 / Custom pattern match
	for _, pattern := range cfg.Patterns {
		if strings.TrimSpace(content) == pattern {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Apply filter at ingest boundary**

At the beginning of the ingest method, add:

```go
if IsNoiseContent(msg.Content, cfg.Ingest.NoiseFilter) {
	continue // 跳过噪声消息 / Skip noise message
}
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(memory): add ingest noise pre-filter based on content length and patterns"
```

---

### Task 7: Verify All Tests Pass

- [ ] **Step 1: Run full store tests**

Run: `go test ./testing/store/ -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 2: Run search tests**

Run: `go test ./testing/search/... -count=1 -timeout 60s`
Expected: ALL PASS

- [ ] **Step 3: Run full build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean

- [ ] **Step 4: Commit any fixups**

```bash
git commit -m "fix: test suite fixups for Plan B"
```

---

## Summary

| Task | What | Key Files |
|------|------|-----------|
| 1 | Time decay in graph scoring | stage/graph.go, retriever_graph.go, builtin.go |
| 2 | EntityProfile model + method | model/graph.go, graph_manager.go |
| 3 | Entity Profile + Search API | graph_handler.go, router.go |
| 4 | Search result entity enrichment | retriever.go, model (SearchResult) |
| 5 | Heartbeat relation cleanup | heartbeat/relation_cleanup.go, engine.go |
| 6 | Ingest noise pre-filter | memory/ (manager or ingest) |
| 7 | Full verification | — |
