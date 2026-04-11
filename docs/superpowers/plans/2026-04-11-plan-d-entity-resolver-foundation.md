# Plan D: Entity Resolver Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the foundation for the vector-driven entity resolver: V27 migration (confidence + candidates table), Qdrant centroid collection manager, EntityResolver skeleton with Layer 1 (tokenizer exact match), and wire into Manager as Extractor replacement.

**Architecture:** EntityResolver is a new component in `internal/memory/` that replaces the LLM-based Extractor. It uses the existing Tokenizer interface for keyword extraction and GraphStore for entity matching. V27 migration adds confidence to memory_entities and creates entity_candidates table. A CentroidManager handles the Qdrant entity_centroids collection lifecycle.

**Tech Stack:** Go 1.25+, SQLite, Qdrant, table-driven tests

**Spec:** `docs/superpowers/specs/2026-04-11-vector-entity-pipeline-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/store/sqlite_migration_v26_v29.go` | V27 migration (confidence + candidates) |
| Modify | `internal/store/sqlite_migration.go` | Register V27 |
| Modify | `internal/store/sqlite_schema.go` | Update freshSchema to V27 |
| Modify | `internal/store/interfaces.go` | Add CandidateStore interface |
| Create | `internal/store/sqlite_candidate.go` | CandidateStore SQLite implementation |
| Modify | `internal/model/graph.go` | Add EntityCandidate struct, update MemoryEntity |
| Create | `internal/memory/entity_resolver.go` | EntityResolver with Layer 1 |
| Create | `internal/memory/centroid_manager.go` | Qdrant centroid collection manager |
| Modify | `internal/memory/manager.go` | Wire EntityResolver as optional replacement |
| Modify | `internal/config/config.go` | Add ResolverConfig |
| Create | `testing/store/candidate_test.go` | CandidateStore tests |
| Create | `testing/memory/entity_resolver_test.go` | EntityResolver Layer 1 tests |

---

### Task 1: Model Layer — Candidate + Confidence

**Files:**
- Modify: `internal/model/graph.go`

- [ ] **Step 1: Add EntityCandidate struct**

After the existing EntityProfile struct in `internal/model/graph.go`, add:

```go
// EntityCandidate 候选实体（待晋升）/ Candidate entity pending promotion
type EntityCandidate struct {
	Name      string    `json:"name"`
	Scope     string    `json:"scope,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	HitCount  int       `json:"hit_count"`
	MemoryIDs []string  `json:"memory_ids"` // 关联的记忆 ID 列表 / Associated memory IDs
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

- [ ] **Step 2: Add Confidence to MemoryEntity**

Update the existing `MemoryEntity` struct:

```go
type MemoryEntity struct {
	MemoryID   string    `json:"memory_id"`
	EntityID   string    `json:"entity_id"`
	Role       string    `json:"role,omitempty"`       // subject / object / mentioned
	Confidence float64   `json:"confidence,omitempty"` // 关联置信度 / Association confidence (0-1)
	CreatedAt  time.Time `json:"created_at"`
}
```

- [ ] **Step 3: Verify build + commit**

```bash
go build ./...
git commit -m "feat(model): add EntityCandidate struct and MemoryEntity confidence field"
```

---

### Task 2: Config — ResolverConfig

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add ResolverConfig struct**

```go
// ResolverConfig 向量实体解析器配置 / Vector entity resolver configuration
type ResolverConfig struct {
	Enabled             bool    `mapstructure:"enabled"`
	CentroidCollection  string  `mapstructure:"centroid_collection"`
	CentroidThreshold   float64 `mapstructure:"centroid_threshold"`
	NeighborK           int     `mapstructure:"neighbor_k"`
	NeighborMinCount    int     `mapstructure:"neighbor_min_count"`
	CandidatePromoteMin int     `mapstructure:"candidate_promote_min"`
	SessionPropagation  bool    `mapstructure:"session_propagation"`
}
```

- [ ] **Step 2: Add to ExtractConfig**

Find `ExtractConfig` struct. Add:
```go
UseLLM   bool           `mapstructure:"use_llm"`  // LLM 抽取开关 / LLM extraction toggle
Resolver ResolverConfig `mapstructure:"resolver"`
```

- [ ] **Step 3: Add defaults**

```go
viper.SetDefault("extract.use_llm", true)
viper.SetDefault("extract.resolver.enabled", false)
viper.SetDefault("extract.resolver.centroid_collection", "entity_centroids")
viper.SetDefault("extract.resolver.centroid_threshold", 0.6)
viper.SetDefault("extract.resolver.neighbor_k", 10)
viper.SetDefault("extract.resolver.neighbor_min_count", 2)
viper.SetDefault("extract.resolver.candidate_promote_min", 3)
viper.SetDefault("extract.resolver.session_propagation", true)
```

- [ ] **Step 4: Verify build + commit**

```bash
go build ./...
git commit -m "feat(config): add entity resolver configuration"
```

---

### Task 3: V27 Migration

**Files:**
- Modify: `internal/store/sqlite_migration_v25_v29.go` (add migrateV26ToV27)
- Modify: `internal/store/sqlite_migration.go` (register V27)
- Modify: `internal/store/sqlite_schema.go` (update to V27)

- [ ] **Step 1: Add migrateV26ToV27 to sqlite_migration_v25_v29.go**

```go
// migrateV26ToV27 memory_entities 置信度 + entity_candidates 表
// memory_entities confidence + entity_candidates table
func migrateV26ToV27(db *sql.DB) error {
	logger.Info("executing migration V26→V27: confidence + entity_candidates")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// memory_entities: 新增 confidence / Add confidence
	if _, err := tx.Exec(`ALTER TABLE memory_entities ADD COLUMN confidence REAL DEFAULT 0.9`); err != nil {
		if !IsColumnExistsError(err) && !isNoSuchTableError(err) {
			return err
		}
	}

	// entity_candidates 表 / Create entity_candidates table
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS entity_candidates (
		name       TEXT NOT NULL,
		scope      TEXT DEFAULT '',
		first_seen DATETIME NOT NULL,
		hit_count  INTEGER DEFAULT 1,
		memory_ids TEXT DEFAULT '[]',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		UNIQUE(name, scope)
	)`); err != nil {
		return err
	}

	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_entity_candidates_hit ON entity_candidates(hit_count)`); err != nil {
		logger.Warn("V26→V27: candidate index failed (non-fatal)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (27, datetime('now'))`); err != nil {
		return err
	}

	return tx.Commit()
}
```

- [ ] **Step 2: Register in sqlite_migration.go**

After the V25→V26 block:
```go
if version < 27 {
	if err := migrateV26ToV27(db); err != nil {
		return fmt.Errorf("V26→V27 migration failed: %w", err)
	}
	version = 27
}
```

- [ ] **Step 3: Update freshSchema to V27**

Update `createFreshSchema`:
- Add `confidence REAL DEFAULT 0.9` to memory_entities CREATE TABLE
- Add entity_candidates CREATE TABLE + index
- Update schema version from 26 to 27
- Update function doc comment

- [ ] **Step 4: Update hardcoded version numbers in tests**

Change all `assert.Equal(t, 26, ...)` and `version != 26` to 27 in:
- `testing/store/sqlite_migration_test.go`
- `testing/store/context_v13_test.go`
- `testing/store/migration_v6_test.go`
- `testing/store/migration_v12_test.go`

- [ ] **Step 5: Verify build + tests + commit**

```bash
go build ./...
go test ./testing/store/ -count=1 -timeout 120s
git commit -m "feat(store): V27 migration — memory_entity confidence, entity_candidates table"
```

---

### Task 4: CandidateStore Interface + Implementation

**Files:**
- Modify: `internal/store/interfaces.go`
- Create: `internal/store/sqlite_candidate.go`
- Create: `testing/store/candidate_test.go`

- [ ] **Step 1: Add CandidateStore interface**

In `internal/store/interfaces.go`:

```go
// CandidateStore 候选实体存储 / Candidate entity store
type CandidateStore interface {
	// UpsertCandidate 创建或更新候选实体 / Create or update candidate entity
	UpsertCandidate(ctx context.Context, name, scope, memoryID string) error

	// ListPromotable 列出可晋升的候选 / List candidates ready for promotion
	ListPromotable(ctx context.Context, minHits int) ([]*model.EntityCandidate, error)

	// DeleteCandidate 删除候选 / Delete candidate after promotion
	DeleteCandidate(ctx context.Context, name, scope string) error
}
```

- [ ] **Step 2: Implement SQLiteCandidateStore**

Create `internal/store/sqlite_candidate.go`:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"iclude/internal/model"
)

var _ CandidateStore = (*SQLiteCandidateStore)(nil)

type SQLiteCandidateStore struct {
	db *sql.DB
}

func NewSQLiteCandidateStore(db *sql.DB) *SQLiteCandidateStore {
	return &SQLiteCandidateStore{db: db}
}

func (s *SQLiteCandidateStore) UpsertCandidate(ctx context.Context, name, scope, memoryID string) error {
	now := time.Now().UTC()

	// 尝试更新已有候选 / Try to update existing candidate
	result, err := s.db.ExecContext(ctx, `
		UPDATE entity_candidates
		SET hit_count = hit_count + 1,
		    memory_ids = json_insert(memory_ids, '$[#]', ?),
		    updated_at = ?
		WHERE name = ? AND scope = ?`,
		memoryID, now, name, scope,
	)
	if err != nil {
		return fmt.Errorf("update candidate: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		return nil
	}

	// 不存在，创建新候选 / Not found, create new
	idsJSON, _ := json.Marshal([]string{memoryID})
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO entity_candidates (name, scope, first_seen, hit_count, memory_ids, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)`,
		name, scope, now, string(idsJSON), now, now,
	)
	if err != nil {
		return fmt.Errorf("insert candidate: %w", err)
	}
	return nil
}

func (s *SQLiteCandidateStore) ListPromotable(ctx context.Context, minHits int) ([]*model.EntityCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, scope, first_seen, hit_count, memory_ids, created_at, updated_at
		 FROM entity_candidates WHERE hit_count >= ? ORDER BY hit_count DESC`,
		minHits,
	)
	if err != nil {
		return nil, fmt.Errorf("list promotable: %w", err)
	}
	defer rows.Close()

	var candidates []*model.EntityCandidate
	for rows.Next() {
		var c model.EntityCandidate
		var idsJSON string
		if err := rows.Scan(&c.Name, &c.Scope, &c.FirstSeen, &c.HitCount, &idsJSON, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		if err := json.Unmarshal([]byte(idsJSON), &c.MemoryIDs); err != nil {
			c.MemoryIDs = nil
		}
		candidates = append(candidates, &c)
	}
	return candidates, rows.Err()
}

func (s *SQLiteCandidateStore) DeleteCandidate(ctx context.Context, name, scope string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM entity_candidates WHERE name = ? AND scope = ?`,
		name, scope,
	)
	if err != nil {
		return fmt.Errorf("delete candidate: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: Write tests**

Create `testing/store/candidate_test.go` with tests for:
- UpsertCandidate creates new, increments on duplicate
- ListPromotable returns only candidates >= minHits
- DeleteCandidate removes the record

- [ ] **Step 4: Verify + commit**

```bash
go build ./...
go test ./testing/store/ -run TestCandidate -v
git commit -m "feat(store): add CandidateStore interface and SQLite implementation"
```

---

### Task 5: EntityResolver — Layer 1 (Tokenizer Exact Match)

**Files:**
- Create: `internal/memory/entity_resolver.go`

- [ ] **Step 1: Create EntityResolver with Layer 1**

```go
package memory

import (
	"context"
	"strings"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// EntityResolver 向量驱动实体解析器 / Vector-driven entity resolver
type EntityResolver struct {
	tokenizer      tokenizer.Tokenizer
	graphStore     store.GraphStore
	candidateStore store.CandidateStore
	cfg            config.ResolverConfig
}

// NewEntityResolver 创建实体解析器 / Create entity resolver
func NewEntityResolver(
	tok tokenizer.Tokenizer,
	graphStore store.GraphStore,
	candidateStore store.CandidateStore,
	cfg config.ResolverConfig,
) *EntityResolver {
	return &EntityResolver{
		tokenizer:      tok,
		graphStore:     graphStore,
		candidateStore: candidateStore,
		cfg:            cfg,
	}
}

// EntityAssociation 实体关联结果 / Entity association result
type EntityAssociation struct {
	EntityID   string
	Confidence float64
}

// ResolveLayer1 分词精确匹配 / Tokenizer exact match (Layer 1)
func (r *EntityResolver) ResolveLayer1(ctx context.Context, mem *model.Memory) ([]EntityAssociation, error) {
	if r.tokenizer == nil {
		return nil, nil
	}

	// 分词 / Tokenize
	tokenized, err := r.tokenizer.Tokenize(ctx, mem.Content)
	if err != nil {
		return nil, err
	}

	terms := strings.Fields(tokenized)
	seen := make(map[string]bool)
	var associations []EntityAssociation

	for _, term := range terms {
		// 过滤短词 / Filter short terms
		termRunes := []rune(term)
		if len(termRunes) < 2 {
			continue
		}
		// 去重 / Deduplicate
		lower := strings.ToLower(term)
		if seen[lower] {
			continue
		}
		seen[lower] = true

		// 匹配已知实体 / Match known entities
		entities, err := r.graphStore.FindEntitiesByName(ctx, term, mem.Scope, 1)
		if err != nil {
			logger.Debug("layer1: entity lookup failed", zap.String("term", term), zap.Error(err))
			continue
		}

		if len(entities) > 0 {
			associations = append(associations, EntityAssociation{
				EntityID:   entities[0].ID,
				Confidence: 0.9,
			})
		} else {
			// 未命中 → 候选 / No match → candidate
			if r.candidateStore != nil {
				if err := r.candidateStore.UpsertCandidate(ctx, term, mem.Scope, mem.ID); err != nil {
					logger.Debug("layer1: upsert candidate failed", zap.String("term", term), zap.Error(err))
				}
			}
		}
	}

	return associations, nil
}

// Resolve 执行实体解析（当前仅 Layer 1，后续加 Layer 2/3）/ Execute entity resolution
func (r *EntityResolver) Resolve(ctx context.Context, memories []*model.Memory) error {
	for _, mem := range memories {
		associations, err := r.ResolveLayer1(ctx, mem)
		if err != nil {
			logger.Warn("resolver: layer1 failed", zap.String("memory_id", mem.ID), zap.Error(err))
			continue
		}

		// 写入关联 / Write associations
		for _, assoc := range associations {
			me := &model.MemoryEntity{
				MemoryID:   mem.ID,
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

		// 共现关系更新 / Co-occurrence relation update
		if len(associations) >= 2 {
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
	}
	return nil
}
```

- [ ] **Step 2: Update CreateMemoryEntity to accept confidence**

In `internal/store/sqlite_graph.go`, update `CreateMemoryEntity` to use the new `Confidence` field:

```go
func (s *SQLiteGraphStore) CreateMemoryEntity(ctx context.Context, me *model.MemoryEntity) error {
	now := time.Now().UTC()
	me.CreatedAt = now
	if me.Confidence == 0 {
		me.Confidence = 0.9
	}

	query := `INSERT INTO memory_entities (memory_id, entity_id, role, confidence, created_at)
		VALUES (?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query, me.MemoryID, me.EntityID, me.Role, me.Confidence, me.CreatedAt)
	// ...existing error handling...
}
```

- [ ] **Step 3: Verify build + commit**

```bash
go build ./...
git commit -m "feat(memory): add EntityResolver with Layer 1 tokenizer exact match"
```

---

### Task 6: Wire EntityResolver into Manager

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/memory/manager_create_helpers.go`

- [ ] **Step 1: Add resolver field to Manager**

In `Manager` struct, add:
```go
resolver *EntityResolver // 向量实体解析器 / Vector entity resolver (optional)
```

In `ManagerConfig` or constructor, accept the resolver as an optional dependency.

- [ ] **Step 2: Update handleAutoExtract**

In `manager_create_helpers.go`, find `handleAutoExtract`. Add a branch: if resolver is available and `cfg.Extract.Resolver.Enabled`, use resolver instead of extractor:

```go
func (m *Manager) handleAutoExtract(ctx context.Context, mem *model.Memory) {
	// 优先使用向量解析器 / Prefer vector resolver
	if m.resolver != nil {
		go func() {
			rCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := m.resolver.Resolve(rCtx, []*model.Memory{mem}); err != nil {
				logger.Warn("resolver failed", zap.Error(err))
			}
		}()
		return
	}

	// fallback: LLM extractor（原有逻辑）/ Fallback: LLM extractor (existing logic)
	if m.extractor != nil {
		// ...existing code...
	}
}
```

- [ ] **Step 3: Wire in bootstrap/wiring.go**

Find where Manager is constructed. If `cfg.Extract.Resolver.Enabled`, create EntityResolver and pass to Manager. The resolver needs: tokenizer, graphStore, candidateStore, cfg.

- [ ] **Step 4: Verify build + commit**

```bash
go build ./...
git commit -m "feat(memory): wire EntityResolver into Manager as Extractor replacement"
```

---

### Task 7: CentroidManager Skeleton

**Files:**
- Create: `internal/memory/centroid_manager.go`

- [ ] **Step 1: Create CentroidManager**

```go
package memory

import (
	"context"
	"fmt"

	"iclude/internal/logger"
	"iclude/pkg/qdrant"

	"go.uber.org/zap"
)

// CentroidManager 实体质心向量管理 / Entity centroid vector manager
type CentroidManager struct {
	client     *qdrant.Client
	collection string
}

// NewCentroidManager 创建质心管理器 / Create centroid manager
func NewCentroidManager(baseURL, collection string, dimension int) (*CentroidManager, error) {
	client := qdrant.NewClient(baseURL, collection, dimension)

	ctx := context.Background()
	if err := client.EnsureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure centroid collection: %w", err)
	}
	// 确保 entity_id payload 字段有索引 / Ensure entity_id payload index
	if err := client.EnsureFieldIndex(ctx, "entity_id"); err != nil {
		logger.Warn("centroid: field index failed (non-fatal)", zap.Error(err))
	}

	return &CentroidManager{
		client:     client,
		collection: collection,
	}, nil
}

// UpsertCentroid 更新实体质心向量 / Update entity centroid vector
func (m *CentroidManager) UpsertCentroid(ctx context.Context, entityID, entityName, scope string, vector []float32, memoryCount int) error {
	point := qdrant.PointStruct{
		ID:     entityID,
		Vector: vector,
		Payload: map[string]any{
			"entity_id":    entityID,
			"entity_name":  entityName,
			"scope":        scope,
			"memory_count": memoryCount,
		},
	}
	return m.client.UpsertPoints(ctx, []qdrant.PointStruct{point})
}

// SearchSimilar 查找与向量相似的实体 / Find entities similar to a vector
func (m *CentroidManager) SearchSimilar(ctx context.Context, vector []float32, limit int, minScore float64) ([]CentroidMatch, error) {
	results, err := m.client.Search(ctx, qdrant.SearchRequest{
		Vector:      vector,
		Limit:       limit,
		WithPayload: true,
	})
	if err != nil {
		return nil, fmt.Errorf("centroid search: %w", err)
	}

	var matches []CentroidMatch
	for _, r := range results {
		if r.Score < minScore {
			continue
		}
		entityID, _ := r.Payload["entity_id"].(string)
		if entityID == "" {
			continue
		}
		matches = append(matches, CentroidMatch{
			EntityID: entityID,
			Score:    r.Score,
		})
	}
	return matches, nil
}

// DeleteCentroid 删除实体质心 / Delete entity centroid
func (m *CentroidManager) DeleteCentroid(ctx context.Context, entityID string) error {
	return m.client.DeletePoints(ctx, []string{entityID})
}

// CentroidMatch 质心匹配结果 / Centroid match result
type CentroidMatch struct {
	EntityID string
	Score    float64
}
```

- [ ] **Step 2: Verify build + commit**

```bash
go build ./...
git commit -m "feat(memory): add CentroidManager for entity centroid vector lifecycle"
```

---

### Task 8: Full Verification

- [ ] **Step 1: Build + vet**

```bash
go build ./... && go vet ./...
```

- [ ] **Step 2: Run all tests**

```bash
go test ./testing/store/ ./testing/memory/ ./testing/search/... -count=1 -timeout 120s
```

- [ ] **Step 3: Commit fixups if needed**

---

## Summary

| Task | What | Key Files |
|------|------|-----------|
| 1 | Model: EntityCandidate + confidence | model/graph.go |
| 2 | Config: ResolverConfig | config/config.go |
| 3 | V27 migration | migration files + schema |
| 4 | CandidateStore interface + impl | interfaces.go, sqlite_candidate.go |
| 5 | EntityResolver Layer 1 | entity_resolver.go |
| 6 | Wire into Manager | manager.go, wiring.go |
| 7 | CentroidManager skeleton | centroid_manager.go |
| 8 | Full verification | — |
