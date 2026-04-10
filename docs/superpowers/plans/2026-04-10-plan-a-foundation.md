# Plan A: Storage Architecture Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add V26 schema migration (entity lifecycle fields, soft delete, source_ref index), update model/store/config layers to support the new fields.

**Architecture:** Incremental migration V25→V26 adds columns to entity_relations (mention_count, last_seen_at, updated_at) and entities (deleted_at), plus a source_ref prefix index. Model structs, store CRUD, GraphStore interface, config, and freshSchema are updated accordingly. All changes are backwards-compatible with default values.

**Tech Stack:** Go 1.25+, SQLite, table-driven tests

**Spec:** `docs/superpowers/specs/2026-04-10-storage-architecture-upgrade-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/model/graph.go` | Add fields to Entity and EntityRelation structs |
| Modify | `internal/model/request.go` | Add SourceRefPrefix to SearchFilters |
| Modify | `internal/config/config.go` | Add RetrievalConfig.RelationDecayLambda + IngestConfig |
| Modify | `internal/store/sqlite_schema.go` | Update freshSchema to V26 final state |
| Create | `internal/store/sqlite_migration_v25_v29.go` | V25→V26 incremental migration |
| Modify | `internal/store/sqlite_migration.go` | Register V26 migration step |
| Modify | `internal/store/interfaces.go` | Add SoftDeleteEntity + UpdateRelationStats to GraphStore |
| Modify | `internal/store/sqlite_graph.go` | Implement new methods + deleted_at filters |
| Create | `testing/store/graph_lifecycle_test.go` | Tests for new lifecycle features |
| Modify | `internal/store/sqlite_memory_lifecycle.go` | SearchTextFiltered source_ref prefix support |

---

### Task 1: Model Layer — Add Lifecycle Fields

**Files:**
- Modify: `internal/model/graph.go`
- Modify: `internal/model/request.go`

- [ ] **Step 1: Add fields to EntityRelation struct**

In `internal/model/graph.go`, add three lifecycle fields to `EntityRelation`:

```go
// EntityRelation 实体关系 / Entity relationship
type EntityRelation struct {
	ID           string         `json:"id"`
	SourceID     string         `json:"source_id"`
	TargetID     string         `json:"target_id"`
	RelationType string         `json:"relation_type"` // uses / knows / belongs_to / related_to
	Weight       float64        `json:"weight"`
	MentionCount int            `json:"mention_count"`                // 共现次数 / Co-occurrence count
	LastSeenAt   *time.Time     `json:"last_seen_at,omitempty"`      // 最近共现时间 / Last co-occurrence time
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`                  // 权重最后更新 / Last weight update
}
```

- [ ] **Step 2: Add DeletedAt to Entity struct**

In `internal/model/graph.go`, add soft-delete field to `Entity`:

```go
// Entity 知识图谱实体 / Knowledge graph entity
type Entity struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	EntityType  string         `json:"entity_type"` // person / org / concept / tool / location
	Scope       string         `json:"scope,omitempty"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   *time.Time     `json:"deleted_at,omitempty"` // 软删除标记 / Soft delete marker
}
```

- [ ] **Step 3: Add SourceRefPrefix to SearchFilters**

In `internal/model/request.go`, add to `SearchFilters`:

```go
// SearchFilters 检索过滤条件 / Search filter conditions
type SearchFilters struct {
	Scope          string     `json:"scope,omitempty"`
	ContextID      string     `json:"context_id,omitempty"`
	ContextPath    string     `json:"context_path,omitempty"`
	Kind           string     `json:"kind,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
	HappenedAfter  *time.Time `json:"happened_after,omitempty"`
	HappenedBefore *time.Time `json:"happened_before,omitempty"`
	SourceType     string     `json:"source_type,omitempty"`
	SourceRefPrefix string    `json:"source_ref_prefix,omitempty"` // 来源 URI 前缀匹配 / Source ref prefix match
	MinStrength    float64    `json:"min_strength,omitempty"`
	IncludeExpired bool       `json:"include_expired,omitempty"`

	// V3: 知识分级 + LLM 过滤 / Retention tier and message role filters
	RetentionTier string `json:"retention_tier,omitempty"`
	MessageRole   string `json:"message_role,omitempty"`

	// V6: 身份过滤（API 层自动注入）/ Identity filtering (auto-injected by API layer)
	TeamID  string `json:"-"` // 不从 JSON 反序列化 / Not deserialized from JSON
	OwnerID string `json:"-"` // 不从 JSON 反序列化 / Not deserialized from JSON
}
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/model/graph.go internal/model/request.go
git commit -m "feat(model): add entity lifecycle fields, soft delete, source_ref prefix filter"
```

---

### Task 2: Config Layer — Add New Config Fields

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add RelationDecayLambda to RetrievalConfig**

In `internal/config/config.go`, add the field after existing RetrievalConfig fields:

```go
type RetrievalConfig struct {
	GraphEnabled     bool             `mapstructure:"graph_enabled"`
	GraphDepth       int              `mapstructure:"graph_depth"`
	GraphWeight      float64          `mapstructure:"graph_weight"`
	FTSWeight        float64          `mapstructure:"fts_weight"`
	QdrantWeight     float64          `mapstructure:"qdrant_weight"`
	GraphFTSTop      int              `mapstructure:"graph_fts_top"`
	GraphEntityLimit int              `mapstructure:"graph_entity_limit"`
	AccessAlpha      float64          `mapstructure:"access_alpha"`
	RelationDecayLambda float64       `mapstructure:"relation_decay_lambda"` // 关系时间衰减系数 λ / Relation time decay lambda
	Rerank           RerankConfig     `mapstructure:"rerank"`
	MMR              MMRConfig        `mapstructure:"mmr"`
	Preprocess       PreprocessConfig `mapstructure:"preprocess"`
	Strategy         StrategyConfig              `mapstructure:"strategy"`
	Pipelines        map[string]PipelineOverrides `mapstructure:"pipelines"`
}
```

- [ ] **Step 2: Add IngestConfig to Config**

Add new struct and register it in Config:

```go
// IngestConfig 数据摄入配置 / Data ingestion configuration
type IngestConfig struct {
	NoiseFilter NoiseFilterConfig `mapstructure:"noise_filter"`
}

// NoiseFilterConfig 噪声过滤配置 / Noise filter configuration
type NoiseFilterConfig struct {
	MinContentLength int      `mapstructure:"min_content_length"` // 最小内容长度 / Min content length (below this: discard)
	Patterns         []string `mapstructure:"patterns"`           // 自定义噪声模式 / Custom noise patterns
}
```

In `Config` struct, add:

```go
type Config struct {
	Storage         StorageConfig         `mapstructure:"storage"`
	Server          ServerConfig          `mapstructure:"server"`
	Auth            AuthConfig            `mapstructure:"auth"`
	Partition       PartitionConfig       `mapstructure:"partitions"`
	LLM             LLMConfig             `mapstructure:"llm"`
	Reflect         ReflectConfig         `mapstructure:"reflect"`
	Extract         ExtractConfig         `mapstructure:"extract"`
	Retrieval       RetrievalConfig       `mapstructure:"retrieval"`
	Crystallization CrystallizationConfig `mapstructure:"crystallization"`
	Dedup           DedupConfig           `mapstructure:"dedup"`
	Scheduler       SchedulerConfig       `mapstructure:"scheduler"`
	Consolidation   ConsolidationConfig   `mapstructure:"consolidation"`
	Heartbeat       HeartbeatConfig       `mapstructure:"heartbeat"`
	MCP             MCPConfig             `mapstructure:"mcp"`
	Queue           QueueConfig           `mapstructure:"queue"`
	Hooks           HooksConfig           `mapstructure:"hooks"`
	Document        DocumentConfig        `mapstructure:"document"`
	Ingest          IngestConfig          `mapstructure:"ingest"`
}
```

- [ ] **Step 3: Add defaults in setDefaults()**

Find the `setDefaults` function and add:

```go
viper.SetDefault("retrieval.relation_decay_lambda", 0.015)
viper.SetDefault("ingest.noise_filter.min_content_length", 10)
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add relation_decay_lambda and ingest noise_filter config"
```

---

### Task 3: Migration V25→V26 — Schema Changes

**Files:**
- Create: `internal/store/sqlite_migration_v25_v29.go`
- Modify: `internal/store/sqlite_migration.go`
- Modify: `internal/store/sqlite_schema.go`

- [ ] **Step 1: Create migration file**

Create `internal/store/sqlite_migration_v25_v29.go`:

```go
package store

import (
	"database/sql"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV25ToV26 实体关系生命周期字段 + 实体软删除 + source_ref 前缀索引
// Entity relation lifecycle fields + entity soft delete + source_ref prefix index
func migrateV25ToV26(db *sql.DB) error {
	logger.Info("executing migration V25→V26: entity lifecycle + source_ref index")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// entity_relations: 新增 mention_count / Add mention_count
	if _, err := tx.Exec(`ALTER TABLE entity_relations ADD COLUMN mention_count INTEGER DEFAULT 1`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: mention_count column already exists")
	}

	// entity_relations: 新增 last_seen_at / Add last_seen_at
	if _, err := tx.Exec(`ALTER TABLE entity_relations ADD COLUMN last_seen_at DATETIME`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: last_seen_at column already exists")
	}

	// entity_relations: 新增 updated_at / Add updated_at
	if _, err := tx.Exec(`ALTER TABLE entity_relations ADD COLUMN updated_at DATETIME`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: updated_at column already exists")
	}

	// entities: 新增 deleted_at / Add soft delete
	if _, err := tx.Exec(`ALTER TABLE entities ADD COLUMN deleted_at DATETIME DEFAULT NULL`); err != nil {
		if !IsColumnExistsError(err) {
			return err
		}
		logger.Debug("V25→V26: deleted_at column already exists")
	}

	// 回填已有 entity_relations 的 last_seen_at 和 updated_at / Backfill existing rows
	if _, err := tx.Exec(`UPDATE entity_relations SET last_seen_at = created_at, updated_at = created_at WHERE last_seen_at IS NULL`); err != nil {
		logger.Warn("V25→V26: backfill last_seen_at failed (non-fatal)", zap.Error(err))
	}

	// 新增索引 / Add indexes
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_source_ref_prefix ON memories(source_ref)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_deleted_at ON entities(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_entity_relations_last_seen ON entity_relations(last_seen_at)`,
	}
	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			logger.Warn("V25→V26: index creation failed (non-fatal)", zap.Error(err), zap.String("sql", idx))
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (26, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Info("migration V25→V26 completed: entity lifecycle fields + source_ref index")
	return nil
}
```

- [ ] **Step 2: Register migration in sqlite_migration.go**

In `internal/store/sqlite_migration.go`, after the V24→V25 block (around line 260), add:

```go
	// V25→V26: entity lifecycle fields + entity soft delete + source_ref prefix index
	if version < 26 {
		if err := migrateV25ToV26(db); err != nil {
			return fmt.Errorf("V25→V26 migration failed: %w", err)
		}
		version = 26
	}
```

- [ ] **Step 3: Update freshSchema to V26**

In `internal/store/sqlite_schema.go`, update the `entities` CREATE TABLE to include `deleted_at`:

```sql
CREATE TABLE entities (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    scope       TEXT DEFAULT '',
    description TEXT DEFAULT '',
    metadata    TEXT,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL,
    deleted_at  DATETIME DEFAULT NULL,
    UNIQUE(name, entity_type, scope)
)
```

Update the `entity_relations` CREATE TABLE to include new fields:

```sql
CREATE TABLE entity_relations (
    id            TEXT PRIMARY KEY,
    source_id     TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    target_id     TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL,
    weight        REAL DEFAULT 1.0 CHECK (weight >= 0),
    mention_count INTEGER DEFAULT 1,
    last_seen_at  DATETIME,
    metadata      TEXT,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME,
    CHECK (source_id != target_id),
    UNIQUE(source_id, target_id, relation_type)
)
```

Add new indexes in the index section:

```go
`CREATE INDEX IF NOT EXISTS idx_memories_source_ref_prefix ON memories(source_ref)`,
`CREATE INDEX IF NOT EXISTS idx_entities_deleted_at ON entities(deleted_at)`,
`CREATE INDEX IF NOT EXISTS idx_entity_relations_last_seen ON entity_relations(last_seen_at)`,
```

Update the final schema version from 25 to 26:

```go
if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (26, datetime('now'))`); err != nil {
```

Also update the function doc comment from V25 to V26.

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite_migration_v25_v29.go internal/store/sqlite_migration.go internal/store/sqlite_schema.go
git commit -m "feat(store): V26 migration — entity lifecycle fields, soft delete, source_ref index"
```

---

### Task 4: Store Layer — Update GraphStore Interface

**Files:**
- Modify: `internal/store/interfaces.go`

- [ ] **Step 1: Add new methods to GraphStore interface**

In `internal/store/interfaces.go`, add these methods to the `GraphStore` interface (before the closing brace):

```go
	// SoftDeleteEntity 软删除实体 / Soft delete an entity
	SoftDeleteEntity(ctx context.Context, id string) error

	// RestoreEntity 恢复软删除的实体 / Restore a soft-deleted entity
	RestoreEntity(ctx context.Context, id string) error

	// UpdateRelationStats 更新关系共现统计 / Update relation co-occurrence stats
	// 若关系已存在则 mention_count++ 并更新 last_seen_at；否则创建新关系
	// If relation exists: increment mention_count and update last_seen_at; otherwise create new
	UpdateRelationStats(ctx context.Context, sourceID, targetID, relationType string) (*model.EntityRelation, error)

	// CleanupStaleRelations 清理过期弱关系 / Cleanup stale weak relations
	// 条件：mention_count < minMentions AND last_seen_at < cutoff
	CleanupStaleRelations(ctx context.Context, minMentions int, cutoff time.Time) (int64, error)

	// CleanupOrphanEntities 软删除无关系的孤儿实体 / Soft-delete orphan entities with no active relations
	CleanupOrphanEntities(ctx context.Context) (int64, error)

	// PurgeDeletedEntities 硬删除已超期的软删除实体 / Hard-delete entities soft-deleted before cutoff
	PurgeDeletedEntities(ctx context.Context, cutoff time.Time) (int64, error)
```

Add `"time"` to the import block if not already present.

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: FAIL — SQLiteGraphStore doesn't implement new methods yet (expected, will fix in Task 5)

- [ ] **Step 3: Commit**

```bash
git add internal/store/interfaces.go
git commit -m "feat(store): extend GraphStore interface with lifecycle methods"
```

---

### Task 5: Store Layer — Implement New Methods + Update Existing

**Files:**
- Modify: `internal/store/sqlite_graph.go`
- Test: `testing/store/graph_lifecycle_test.go`

- [ ] **Step 1: Write tests for new lifecycle methods**

Create `testing/store/graph_lifecycle_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/store"
)

// setupGraphTest 创建测试数据库和 GraphStore / Create test DB and GraphStore
func setupGraphTest(t *testing.T) (store.GraphStore, func()) {
	t.Helper()
	db := setupTestDB(t)
	gs := store.NewSQLiteGraphStore(db)
	return gs, func() { db.Close() }
}

func TestSoftDeleteEntity_Basic(t *testing.T) {
	gs, cleanup := setupGraphTest(t)
	defer cleanup()
	ctx := context.Background()

	// 创建实体 / Create entity
	entity := &model.Entity{Name: "Python", EntityType: "tool", Scope: "test"}
	if err := gs.CreateEntity(ctx, entity); err != nil {
		t.Fatalf("create entity: %v", err)
	}

	// 软删除 / Soft delete
	if err := gs.SoftDeleteEntity(ctx, entity.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	// GetEntity 应该返回 not found / Should return not found
	_, err := gs.GetEntity(ctx, entity.ID)
	if err != model.ErrEntityNotFound {
		t.Fatalf("expected ErrEntityNotFound after soft delete, got: %v", err)
	}

	// ListEntities 不应包含 / Should not appear in list
	entities, err := gs.ListEntities(ctx, "test", "", 100)
	if err != nil {
		t.Fatalf("list entities: %v", err)
	}
	for _, e := range entities {
		if e.ID == entity.ID {
			t.Fatal("soft-deleted entity should not appear in ListEntities")
		}
	}
}

func TestRestoreEntity_Basic(t *testing.T) {
	gs, cleanup := setupGraphTest(t)
	defer cleanup()
	ctx := context.Background()

	entity := &model.Entity{Name: "Python", EntityType: "tool", Scope: "test"}
	if err := gs.CreateEntity(ctx, entity); err != nil {
		t.Fatalf("create entity: %v", err)
	}

	if err := gs.SoftDeleteEntity(ctx, entity.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	// 恢复 / Restore
	if err := gs.RestoreEntity(ctx, entity.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// 应该能查到 / Should be visible again
	got, err := gs.GetEntity(ctx, entity.ID)
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if got.Name != "Python" {
		t.Fatalf("expected Python, got %s", got.Name)
	}
}

func TestUpdateRelationStats_CreateAndIncrement(t *testing.T) {
	gs, cleanup := setupGraphTest(t)
	defer cleanup()
	ctx := context.Background()

	// 创建两个实体 / Create two entities
	e1 := &model.Entity{Name: "Alice", EntityType: "person", Scope: "test"}
	e2 := &model.Entity{Name: "Python", EntityType: "tool", Scope: "test"}
	if err := gs.CreateEntity(ctx, e1); err != nil {
		t.Fatalf("create e1: %v", err)
	}
	if err := gs.CreateEntity(ctx, e2); err != nil {
		t.Fatalf("create e2: %v", err)
	}

	// 第一次 UpdateRelationStats → 创建新关系 / First call → creates relation
	rel1, err := gs.UpdateRelationStats(ctx, e1.ID, e2.ID, "uses")
	if err != nil {
		t.Fatalf("first UpdateRelationStats: %v", err)
	}
	if rel1.MentionCount != 1 {
		t.Fatalf("expected mention_count=1, got %d", rel1.MentionCount)
	}

	// 第二次 → mention_count++ / Second call → increment
	rel2, err := gs.UpdateRelationStats(ctx, e1.ID, e2.ID, "uses")
	if err != nil {
		t.Fatalf("second UpdateRelationStats: %v", err)
	}
	if rel2.MentionCount != 2 {
		t.Fatalf("expected mention_count=2, got %d", rel2.MentionCount)
	}
	if rel2.ID != rel1.ID {
		t.Fatal("should return same relation ID")
	}
}

func TestCleanupStaleRelations_Basic(t *testing.T) {
	gs, cleanup := setupGraphTest(t)
	defer cleanup()
	ctx := context.Background()

	e1 := &model.Entity{Name: "A", EntityType: "concept", Scope: "test"}
	e2 := &model.Entity{Name: "B", EntityType: "concept", Scope: "test"}
	if err := gs.CreateEntity(ctx, e1); err != nil {
		t.Fatalf("create e1: %v", err)
	}
	if err := gs.CreateEntity(ctx, e2); err != nil {
		t.Fatalf("create e2: %v", err)
	}

	// 创建一个弱关系 / Create a weak relation
	if _, err := gs.UpdateRelationStats(ctx, e1.ID, e2.ID, "related_to"); err != nil {
		t.Fatalf("create relation: %v", err)
	}

	// 清理 cutoff 设为未来 → 应该清理掉 / Cutoff in future → should clean up
	deleted, err := gs.CleanupStaleRelations(ctx, 3, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// 确认关系已删除 / Confirm deleted
	rels, err := gs.GetEntityRelations(ctx, e1.ID)
	if err != nil {
		t.Fatalf("get relations: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected 0 relations, got %d", len(rels))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./testing/store/ -run "TestSoftDelete|TestRestore|TestUpdateRelation|TestCleanupStale" -v`
Expected: FAIL — methods not implemented yet

- [ ] **Step 3: Update relationScanDest for new fields**

In `internal/store/sqlite_graph.go`, update `relationScanDest` and related code:

```go
// relationScanDest EntityRelation 扫描目标（10列）/ EntityRelation scan destination (10 columns)
type relationScanDest struct {
	rel        model.EntityRelation
	metaStr    sql.NullString
	lastSeenAt sql.NullTime
	updatedAt  sql.NullTime
}

// scanFields 返回扫描目标字段列表 / Returns scan destination fields
func (d *relationScanDest) scanFields() []any {
	return []any{
		&d.rel.ID, &d.rel.SourceID, &d.rel.TargetID, &d.rel.RelationType,
		&d.rel.Weight, &d.rel.MentionCount, &d.lastSeenAt, &d.metaStr,
		&d.rel.CreatedAt, &d.updatedAt,
	}
}

// toRelation 将扫描结果转为 EntityRelation / Convert scan result to EntityRelation
func (d *relationScanDest) toRelation() (*model.EntityRelation, error) {
	if d.metaStr.Valid {
		if err := json.Unmarshal([]byte(d.metaStr.String), &d.rel.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal relation metadata: %w", err)
		}
	}
	if d.lastSeenAt.Valid {
		d.rel.LastSeenAt = &d.lastSeenAt.Time
	}
	if d.updatedAt.Valid {
		d.rel.UpdatedAt = d.updatedAt.Time
	}
	return &d.rel, nil
}
```

Update all relation SELECT queries to include the new columns. Replace all occurrences of:

```sql
SELECT id, source_id, target_id, relation_type, weight, metadata, created_at
    FROM entity_relations
```

with:

```sql
SELECT id, source_id, target_id, relation_type, weight, mention_count, last_seen_at, metadata, created_at, updated_at
    FROM entity_relations
```

This applies to: `CreateRelation` (return query if any), `GetRelation`, `GetEntityRelations`, and `scanRelation`.

- [ ] **Step 4: Update entityScanDest for deleted_at**

Update `entityScanDest`:

```go
// entityScanDest Entity 扫描目标（9列）/ Entity scan destination (9 columns)
type entityScanDest struct {
	entity    model.Entity
	metaStr   sql.NullString
	deletedAt sql.NullTime
}

// scanFields 返回扫描目标字段列表 / Returns scan destination fields
func (d *entityScanDest) scanFields() []any {
	return []any{
		&d.entity.ID, &d.entity.Name, &d.entity.EntityType, &d.entity.Scope,
		&d.entity.Description, &d.metaStr, &d.entity.CreatedAt, &d.entity.UpdatedAt,
		&d.deletedAt,
	}
}

// toEntity 将扫描结果转为 Entity / Convert scan result to Entity
func (d *entityScanDest) toEntity() (*model.Entity, error) {
	if d.metaStr.Valid {
		if err := json.Unmarshal([]byte(d.metaStr.String), &d.entity.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal entity metadata: %w", err)
		}
	}
	if d.deletedAt.Valid {
		d.entity.DeletedAt = &d.deletedAt.Time
	}
	return &d.entity, nil
}
```

Update all entity SELECT queries to include `deleted_at`:

```sql
SELECT id, name, entity_type, scope, description, metadata, created_at, updated_at, deleted_at
    FROM entities
```

Add `AND deleted_at IS NULL` to: `GetEntity`, `ListEntities`, `FindEntitiesByName`, `GetEntityMemories` (the JOIN on entities), `GetMemoryEntities`, `GetMemoriesEntities`.

- [ ] **Step 5: Update CreateRelation to set new fields**

In `CreateRelation`, set lifecycle fields:

```go
func (s *SQLiteGraphStore) CreateRelation(ctx context.Context, rel *model.EntityRelation) error {
	now := time.Now().UTC()
	rel.ID = uuid.New().String()
	rel.CreatedAt = now
	rel.UpdatedAt = now
	lastSeen := now
	rel.LastSeenAt = &lastSeen

	if rel.Weight == 0 {
		rel.Weight = 1.0
	}
	if rel.MentionCount == 0 {
		rel.MentionCount = 1
	}

	metadataJSON, err := marshalMetadata(rel.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal relation metadata: %w", err)
	}

	query := `INSERT INTO entity_relations (id, source_id, target_id, relation_type, weight, mention_count, last_seen_at, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, query,
		rel.ID, rel.SourceID, rel.TargetID, rel.RelationType, rel.Weight,
		rel.MentionCount, rel.LastSeenAt, metadataJSON, rel.CreatedAt, rel.UpdatedAt,
	)
	if err != nil {
		if IsUniqueConstraintError(err) {
			return fmt.Errorf("relation already exists: %w", model.ErrConflict)
		}
		return fmt.Errorf("failed to insert relation: %w", err)
	}

	return nil
}
```

- [ ] **Step 6: Implement SoftDeleteEntity**

```go
// SoftDeleteEntity 软删除实体 / Soft delete an entity by setting deleted_at
func (s *SQLiteGraphStore) SoftDeleteEntity(ctx context.Context, id string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE entities SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("failed to soft delete entity: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrEntityNotFound
	}

	return nil
}
```

- [ ] **Step 7: Implement RestoreEntity**

```go
// RestoreEntity 恢复软删除的实体 / Restore a soft-deleted entity
func (s *SQLiteGraphStore) RestoreEntity(ctx context.Context, id string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE entities SET deleted_at = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("failed to restore entity: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrEntityNotFound
	}

	return nil
}
```

- [ ] **Step 8: Implement UpdateRelationStats**

```go
// UpdateRelationStats 更新关系共现统计（upsert）/ Update relation co-occurrence stats (upsert)
func (s *SQLiteGraphStore) UpdateRelationStats(ctx context.Context, sourceID, targetID, relationType string) (*model.EntityRelation, error) {
	now := time.Now().UTC()

	// 尝试更新已有关系 / Try to update existing relation
	result, err := s.db.ExecContext(ctx,
		`UPDATE entity_relations SET mention_count = mention_count + 1, last_seen_at = ?, updated_at = ?
		 WHERE source_id = ? AND target_id = ? AND relation_type = ?`,
		now, now, sourceID, targetID, relationType,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update relation stats: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows > 0 {
		// 已更新，查询返回 / Updated, query and return
		var d relationScanDest
		query := `SELECT id, source_id, target_id, relation_type, weight, mention_count, last_seen_at, metadata, created_at, updated_at
			FROM entity_relations WHERE source_id = ? AND target_id = ? AND relation_type = ?`
		if err := s.db.QueryRowContext(ctx, query, sourceID, targetID, relationType).Scan(d.scanFields()...); err != nil {
			return nil, fmt.Errorf("failed to read updated relation: %w", err)
		}
		return d.toRelation()
	}

	// 不存在，创建新关系 / Doesn't exist, create new
	rel := &model.EntityRelation{
		SourceID:     sourceID,
		TargetID:     targetID,
		RelationType: relationType,
		Weight:       1.0,
	}
	if err := s.CreateRelation(ctx, rel); err != nil {
		return nil, fmt.Errorf("failed to create relation via stats: %w", err)
	}
	return rel, nil
}
```

- [ ] **Step 9: Implement CleanupStaleRelations**

```go
// CleanupStaleRelations 清理过期弱关系 / Cleanup stale weak relations
func (s *SQLiteGraphStore) CleanupStaleRelations(ctx context.Context, minMentions int, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM entity_relations WHERE mention_count < ? AND last_seen_at < ?`,
		minMentions, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup stale relations: %w", err)
	}
	return result.RowsAffected()
}
```

- [ ] **Step 10: Implement CleanupOrphanEntities**

```go
// CleanupOrphanEntities 软删除无关系的孤儿实体 / Soft-delete orphan entities
func (s *SQLiteGraphStore) CleanupOrphanEntities(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE entities SET deleted_at = ?, updated_at = ?
		WHERE deleted_at IS NULL
		  AND id NOT IN (SELECT DISTINCT entity_id FROM memory_entities)
		  AND id NOT IN (SELECT DISTINCT source_id FROM entity_relations)
		  AND id NOT IN (SELECT DISTINCT target_id FROM entity_relations)`,
		now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup orphan entities: %w", err)
	}
	return result.RowsAffected()
}
```

- [ ] **Step 11: Implement PurgeDeletedEntities**

```go
// PurgeDeletedEntities 硬删除已超期的软删除实体 / Hard-delete entities soft-deleted before cutoff
func (s *SQLiteGraphStore) PurgeDeletedEntities(ctx context.Context, cutoff time.Time) (int64, error) {
	// CASCADE 会自动清理 entity_relations 和 memory_entities / CASCADE auto-cleans related rows
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM entities WHERE deleted_at IS NOT NULL AND deleted_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to purge deleted entities: %w", err)
	}
	return result.RowsAffected()
}
```

- [ ] **Step 12: Run tests**

Run: `go test ./testing/store/ -run "TestSoftDelete|TestRestore|TestUpdateRelation|TestCleanupStale" -v`
Expected: ALL PASS

- [ ] **Step 13: Verify full build**

Run: `go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 14: Commit**

```bash
git add internal/store/sqlite_graph.go testing/store/graph_lifecycle_test.go
git commit -m "feat(store): implement entity lifecycle methods — soft delete, relation stats, cleanup"
```

---

### Task 6: Store Layer — SearchTextFiltered Source Ref Prefix

**Files:**
- Modify: `internal/store/sqlite_memory_lifecycle.go`
- Test: `testing/store/graph_lifecycle_test.go` (append)

- [ ] **Step 1: Write test for source_ref prefix filtering**

Append to `testing/store/graph_lifecycle_test.go`:

```go
func TestSearchTextFiltered_SourceRefPrefix(t *testing.T) {
	ms, cleanup := setupMemoryStoreTest(t)
	defer cleanup()
	ctx := context.Background()

	// 创建两条不同来源的记忆 / Create memories from different sources
	m1 := &model.Memory{Content: "Alice uses Python for data analysis", SourceType: "feishu", SourceRef: "feishu://chat/group-eng/msg/001", Scope: "test"}
	m2 := &model.Memory{Content: "Bob uses Python for web scraping", SourceType: "wechat", SourceRef: "wechat://contact/bob/msg/001", Scope: "test"}

	if err := ms.Create(ctx, m1); err != nil {
		t.Fatalf("create m1: %v", err)
	}
	if err := ms.Create(ctx, m2); err != nil {
		t.Fatalf("create m2: %v", err)
	}

	// 按 feishu 前缀过滤 / Filter by feishu prefix
	filters := &model.SearchFilters{SourceRefPrefix: "feishu://chat/group-eng/"}
	results, err := ms.SearchTextFiltered(ctx, "Python", filters, 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Memory.SourceRef != "feishu://chat/group-eng/msg/001" {
		t.Fatalf("unexpected source_ref: %s", results[0].Memory.SourceRef)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/store/ -run TestSearchTextFiltered_SourceRefPrefix -v`
Expected: FAIL — SourceRefPrefix not implemented

- [ ] **Step 3: Add SourceRefPrefix condition to SearchTextFiltered**

In `internal/store/sqlite_memory_lifecycle.go`, in the `SearchTextFiltered` method, find where filters are applied to build WHERE conditions. Add:

```go
if filters.SourceRefPrefix != "" {
	conditions = append(conditions, "m.source_ref LIKE ?")
	args = append(args, filters.SourceRefPrefix+"%")
}
```

This should be placed alongside existing filter conditions (SourceType, Scope, etc.).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/store/ -run TestSearchTextFiltered_SourceRefPrefix -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite_memory_lifecycle.go testing/store/graph_lifecycle_test.go
git commit -m "feat(store): add source_ref prefix filtering to SearchTextFiltered"
```

---

### Task 7: Verify All Tests Pass

**Files:** None (verification only)

- [ ] **Step 1: Run full test suite**

Run: `go test ./testing/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 2: Run vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 3: Verify migration on fresh DB**

Run: `rm -f /tmp/test_v26.db && go test ./testing/store/ -run TestMigration -v`
Or manually verify: the server starts clean with a new DB path.

- [ ] **Step 4: Final commit (if any fixups needed)**

```bash
git add -A
git commit -m "fix(store): test suite fixups for V26 migration"
```

---

## Summary

| Task | What | Files | Commit |
|------|------|-------|--------|
| 1 | Model: lifecycle fields + SourceRefPrefix | model/graph.go, model/request.go | `feat(model)` |
| 2 | Config: decay lambda + noise filter | config/config.go | `feat(config)` |
| 3 | Migration V25→V26 + freshSchema | migration files + schema | `feat(store): V26` |
| 4 | Interface: new GraphStore methods | interfaces.go | `feat(store): interface` |
| 5 | Implement: soft delete, relation stats, cleanup | sqlite_graph.go + tests | `feat(store): implement` |
| 6 | SearchTextFiltered: source_ref prefix | sqlite_memory_lifecycle.go + test | `feat(store): prefix` |
| 7 | Verify all tests pass | — | fixup if needed |
