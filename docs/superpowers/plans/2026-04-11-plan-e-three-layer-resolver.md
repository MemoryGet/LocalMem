# Plan E: Three-Layer Entity Resolver + Candidate Promotion

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the three-layer entity resolution pipeline: add Layer 2 (centroid matching) and Layer 3 (neighbor propagation) to EntityResolver, merge with confidence scoring, add candidate promotion heartbeat task, and update centroid vectors on new associations.

**Architecture:** EntityResolver gains centroidManager and vecStore dependencies. Resolve() runs all 3 layers, merges by entityID with max confidence + overlap bonus, writes associations and relations. Centroid update is triggered async after association writes. Candidate promotion runs in heartbeat.

**Tech Stack:** Go 1.25+, Qdrant, SQLite

**Spec:** `docs/superpowers/specs/2026-04-11-vector-entity-pipeline-design.md` sections 3.2, 3.3, 3.4, 5, 7

**Depends on:** Plan D (completed)

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/memory/entity_resolver.go` | Add Layer 2/3, merge logic, centroid update |
| Modify | `internal/memory/centroid_manager.go` | Add IncrementalUpdate helper |
| Create | `internal/heartbeat/candidate_promotion.go` | Candidate → Entity promotion |
| Modify | `internal/heartbeat/engine.go` | Wire candidate promotion |
| Modify | `internal/bootstrap/wiring.go` | Pass new deps to EntityResolver |

---

### Task 1: Add Layer 2 + Layer 3 to EntityResolver

**Files:**
- Modify: `internal/memory/entity_resolver.go`

- [ ] **Step 1: Add dependencies to EntityResolver**

Add `centroidMgr` and `vecStore` fields:

```go
type EntityResolver struct {
	tokenizer      tokenizer.Tokenizer
	graphStore     store.GraphStore
	candidateStore store.CandidateStore
	centroidMgr    *CentroidManager     // 质心匹配 / Centroid matching (Layer 2)
	vecStore       store.VectorStore     // 近邻传播 / Neighbor propagation (Layer 3)
	cfg            config.ResolverConfig
}
```

Update constructor to accept optional deps (nil-safe):

```go
func NewEntityResolver(
	tok tokenizer.Tokenizer,
	graphStore store.GraphStore,
	candidateStore store.CandidateStore,
	centroidMgr *CentroidManager,
	vecStore store.VectorStore,
	cfg config.ResolverConfig,
) *EntityResolver
```

- [ ] **Step 2: Implement ResolveLayer2 (centroid matching)**

```go
// ResolveLayer2 实体质心匹配（Layer 2）/ Entity centroid matching
func (r *EntityResolver) ResolveLayer2(ctx context.Context, embedding []float32) ([]EntityAssociation, error) {
	if r.centroidMgr == nil || len(embedding) == 0 {
		return nil, nil
	}

	matches, err := r.centroidMgr.SearchSimilar(ctx, embedding, 10, r.cfg.CentroidThreshold)
	if err != nil {
		return nil, err
	}

	var associations []EntityAssociation
	for _, m := range matches {
		associations = append(associations, EntityAssociation{
			EntityID:   m.EntityID,
			Confidence: 0.7,
		})
	}
	return associations, nil
}
```

- [ ] **Step 3: Implement ResolveLayer3 (neighbor propagation)**

```go
// ResolveLayer3 近邻传播（Layer 3）/ Neighbor propagation
func (r *EntityResolver) ResolveLayer3(ctx context.Context, embedding []float32) ([]EntityAssociation, error) {
	if r.vecStore == nil || len(embedding) == 0 {
		return nil, nil
	}

	// 查 Top-K 近邻记忆 / Find top-K neighbor memories
	neighbors, err := r.vecStore.Search(ctx, embedding, nil, r.cfg.NeighborK)
	if err != nil {
		return nil, err
	}
	if len(neighbors) == 0 {
		return nil, nil
	}

	// 获取近邻的实体 / Get neighbor entities
	memIDs := make([]string, 0, len(neighbors))
	for _, n := range neighbors {
		if n.Memory != nil {
			memIDs = append(memIDs, n.Memory.ID)
		}
	}

	entitiesMap, err := r.graphStore.GetMemoriesEntities(ctx, memIDs)
	if err != nil {
		return nil, err
	}

	// 统计实体出现频次 / Count entity frequency
	freq := make(map[string]int)
	for _, entities := range entitiesMap {
		for _, e := range entities {
			freq[e.ID]++
		}
	}

	// 超过阈值的传播 / Propagate entities above threshold
	var associations []EntityAssociation
	for entityID, count := range freq {
		if count >= r.cfg.NeighborMinCount {
			associations = append(associations, EntityAssociation{
				EntityID:   entityID,
				Confidence: 0.5,
			})
		}
	}
	return associations, nil
}
```

- [ ] **Step 4: Update Resolve to merge all 3 layers**

Replace the current Resolve method. It now accepts optional embeddings:

```go
// ResolveWithEmbeddings 三层解析 + 合并 / Three-layer resolution with merge
func (r *EntityResolver) ResolveWithEmbeddings(ctx context.Context, memories []*model.Memory, embeddings [][]float32) error {
	for i, mem := range memories {
		var embedding []float32
		if i < len(embeddings) {
			embedding = embeddings[i]
		}

		// 三层并行 / Three layers (Layer 1 is fast, 2/3 need embedding)
		l1, err := r.ResolveLayer1(ctx, mem)
		if err != nil {
			logger.Warn("resolver: layer1 failed", zap.String("memory_id", mem.ID), zap.Error(err))
		}

		var l2, l3 []EntityAssociation
		if len(embedding) > 0 {
			l2, _ = r.ResolveLayer2(ctx, embedding)
			l3, _ = r.ResolveLayer3(ctx, embedding)
		}

		// 合并去重 / Merge and dedup
		merged := mergeAssociations(l1, l2, l3)

		// 写入关联 / Write associations
		r.writeAssociations(ctx, mem.ID, merged)
	}
	return nil
}

// Resolve 兼容无 embedding 的调用 / Compatible call without embeddings
func (r *EntityResolver) Resolve(ctx context.Context, memories []*model.Memory) error {
	return r.ResolveWithEmbeddings(ctx, memories, nil)
}
```

- [ ] **Step 5: Implement mergeAssociations**

```go
// mergeAssociations 合并三层结果，同实体取 max confidence + 重叠加成 / Merge with max confidence + overlap bonus
func mergeAssociations(layers ...[]EntityAssociation) []EntityAssociation {
	byEntity := make(map[string]struct {
		maxConf    float64
		layerCount int
	})

	for _, layer := range layers {
		seen := make(map[string]bool)
		for _, a := range layer {
			if seen[a.EntityID] {
				continue
			}
			seen[a.EntityID] = true
			entry := byEntity[a.EntityID]
			if a.Confidence > entry.maxConf {
				entry.maxConf = a.Confidence
			}
			entry.layerCount++
			byEntity[a.EntityID] = entry
		}
	}

	var result []EntityAssociation
	for entityID, entry := range byEntity {
		conf := entry.maxConf
		if entry.layerCount > 1 {
			conf += 0.1 // 重叠加成 / Overlap bonus
		}
		if conf > 1.0 {
			conf = 1.0
		}
		result = append(result, EntityAssociation{
			EntityID:   entityID,
			Confidence: conf,
		})
	}
	return result
}
```

- [ ] **Step 6: Extract writeAssociations helper**

```go
// writeAssociations 写入关联 + 共现关系 / Write memory-entity associations + co-occurrence relations
func (r *EntityResolver) writeAssociations(ctx context.Context, memoryID string, associations []EntityAssociation) {
	for _, assoc := range associations {
		me := &model.MemoryEntity{
			MemoryID:   memoryID,
			EntityID:   assoc.EntityID,
			Role:       "mentioned",
			Confidence: assoc.Confidence,
		}
		if err := r.graphStore.CreateMemoryEntity(ctx, me); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				logger.Debug("resolver: create memory_entity failed", zap.Error(err))
			}
		}
	}

	// 共现关系 / Co-occurrence relations
	for i := 0; i < len(associations)-1; i++ {
		for j := i + 1; j < len(associations); j++ {
			_, _ = r.graphStore.UpdateRelationStats(ctx,
				associations[i].EntityID,
				associations[j].EntityID,
				"related_to",
			)
		}
	}
}
```

- [ ] **Step 7: Verify build + commit**

```bash
go build ./...
git commit -m "feat(memory): add Layer 2 centroid matching and Layer 3 neighbor propagation to EntityResolver"
```

---

### Task 2: Candidate Promotion Heartbeat Task

**Files:**
- Create: `internal/heartbeat/candidate_promotion.go`
- Modify: `internal/heartbeat/engine.go`

- [ ] **Step 1: Create candidate_promotion.go**

```go
package heartbeat

import (
	"context"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// runCandidatePromotion 候选实体晋升 / Promote candidate entities to real entities
func (e *Engine) runCandidatePromotion(ctx context.Context, minHits int) error {
	if e.candidateStore == nil || e.graphStore == nil {
		return nil
	}

	candidates, err := e.candidateStore.ListPromotable(ctx, minHits)
	if err != nil {
		return err
	}

	for _, c := range candidates {
		// 创建正式实体 / Create real entity
		entity := &model.Entity{
			Name:       c.Name,
			EntityType: "concept", // 默认类型 / Default type
			Scope:      c.Scope,
		}
		if err := e.graphStore.CreateEntity(ctx, entity); err != nil {
			logger.Warn("promotion: create entity failed", zap.String("name", c.Name), zap.Error(err))
			continue
		}

		// 回溯关联历史记忆 / Backfill memory-entity associations
		for _, memID := range c.MemoryIDs {
			me := &model.MemoryEntity{
				MemoryID:   memID,
				EntityID:   entity.ID,
				Role:       "mentioned",
				Confidence: 0.9,
			}
			if err := e.graphStore.CreateMemoryEntity(ctx, me); err != nil {
				// 忽略已存在 / Ignore duplicates
				continue
			}
		}

		// 删除候选 / Delete candidate
		if err := e.candidateStore.DeleteCandidate(ctx, c.Name, c.Scope); err != nil {
			logger.Warn("promotion: delete candidate failed", zap.String("name", c.Name), zap.Error(err))
		}

		logger.Info("promoted candidate to entity",
			zap.String("name", c.Name),
			zap.String("entity_id", entity.ID),
			zap.Int("backfilled_memories", len(c.MemoryIDs)),
		)
	}

	return nil
}
```

- [ ] **Step 2: Add candidateStore to Engine**

Read `internal/heartbeat/engine.go`. Check if Engine already has a `candidateStore` field. If not, add:
```go
candidateStore store.CandidateStore // 可为 nil / May be nil
```

Update `NewEngine` to accept it. Check the existing constructor signature and add the parameter.

- [ ] **Step 3: Wire into Run()**

In the `Run()` method, add after the relation cleanup block:

```go
// 候选实体晋升 / Candidate entity promotion
if e.candidateStore != nil {
	promoteMin := 3 // 默认值，后续可配置化 / Default, can be configurable later
	if err := e.runCandidatePromotion(ctx, promoteMin); err != nil {
		logger.Warn("heartbeat: candidate promotion error", zap.Error(err))
	}
}
```

- [ ] **Step 4: Update bootstrap wiring**

Read `internal/bootstrap/wiring.go`. Find where `heartbeat.NewEngine` is called. Add `stores.CandidateStore` as the new parameter.

- [ ] **Step 5: Verify build + commit**

```bash
go build ./...
git commit -m "feat(heartbeat): add candidate entity promotion task"
```

---

### Task 3: Update Bootstrap Wiring for Resolver Dependencies

**Files:**
- Modify: `internal/bootstrap/wiring.go`

- [ ] **Step 1: Update EntityResolver construction**

Find where `memory.NewEntityResolver` is called. Update to pass the new dependencies:

```go
if cfg.Extract.Resolver.Enabled && stores.GraphStore != nil && stores.CandidateStore != nil {
	var centroidMgr *memory.CentroidManager
	if stores.VectorStore != nil && cfg.Storage.Qdrant.Enabled {
		var err error
		centroidMgr, err = memory.NewCentroidManager(
			cfg.Storage.Qdrant.URL,
			cfg.Extract.Resolver.CentroidCollection,
			cfg.Storage.Qdrant.Dimension,
		)
		if err != nil {
			logger.Warn("centroid manager init failed, Layer 2 disabled", zap.Error(err))
		}
	}

	resolver := memory.NewEntityResolver(
		tok,
		stores.GraphStore,
		stores.CandidateStore,
		centroidMgr,    // may be nil (Layer 2 disabled)
		stores.VectorStore, // may be nil (Layer 3 disabled)
		cfg.Extract.Resolver,
	)
	manager.SetResolver(resolver)
	logger.Info("entity resolver enabled",
		zap.Bool("layer2_centroid", centroidMgr != nil),
		zap.Bool("layer3_neighbor", stores.VectorStore != nil),
	)
}
```

- [ ] **Step 2: Verify build + commit**

```bash
go build ./...
git commit -m "feat(bootstrap): wire centroid manager and vector store into EntityResolver"
```

---

### Task 4: Full Verification

- [ ] **Step 1: Build + vet**

```bash
go build ./... && go vet ./...
```

- [ ] **Step 2: Run all tests**

```bash
go test ./testing/store/ ./testing/memory/ ./testing/search/... ./testing/heartbeat/ -count=1 -timeout 120s
```

- [ ] **Step 3: Push**

```bash
git push origin main
```

---

## Summary

| Task | What | Key Files |
|------|------|-----------|
| 1 | Layer 2/3 + merge + writeAssociations | entity_resolver.go |
| 2 | Candidate promotion heartbeat | candidate_promotion.go, engine.go |
| 3 | Bootstrap wiring for new deps | wiring.go |
| 4 | Full verification + push | — |
