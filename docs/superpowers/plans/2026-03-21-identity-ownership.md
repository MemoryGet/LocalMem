# Identity & Ownership Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add API Key authentication, user identity injection, and memory visibility control to enable multi-tenant memory isolation.

**Architecture:** Two new middleware (Auth + Identity) intercept all `/v1/` requests, resolving API Key → team_id and X-User-ID → owner_id. Memory model gains `owner_id` + `visibility` fields. All store queries enforce a visibility rule: private=owner only, team=same team, public=everyone. Migration V6 backfills existing data.

**Tech Stack:** Go 1.25+, Gin, SQLite (modernc.org/sqlite), Qdrant

**Spec:** `docs/superpowers/specs/2026-03-21-identity-ownership-design.md`

---

## File Map

### New Files
| File | Responsibility |
|------|---------------|
| `internal/model/identity.go` | Identity struct + SystemOwnerID constant + visibility constants |
| `testing/api/auth_middleware_test.go` | Auth middleware tests |
| `testing/api/identity_middleware_test.go` | Identity middleware tests |
| `testing/store/migration_v6_test.go` | Migration V6 correctness |
| `testing/store/visibility_test.go` | Visibility filtering tests |
| `testing/report/identity_test.go` | Testreport integration tests for dashboard |

### Modified Files
| File | Changes |
|------|---------|
| `internal/model/memory.go` | +2 fields: OwnerID, Visibility |
| `internal/model/errors.go` | +2 sentinel errors: ErrUnauthorized, ErrForbidden |
| `internal/model/request.go` | +Visibility on Create/Update DTOs, +TeamID/OwnerID on SearchFilters/TimelineRequest |
| `internal/config/config.go` | +AuthConfig struct, +Auth field on Config, deprecate server.auth_enabled |
| `internal/api/middleware.go` | +AuthMiddleware, +IdentityMiddleware, +GetIdentity/SetIdentity helpers |
| `internal/api/router.go` | Register Auth+Identity middleware on /v1 group |
| `internal/store/sqlite_migration.go` | +migrateV5ToV6, latestVersion=6 |
| `internal/store/sqlite.go` | memoryColumns +2 cols, scanMemory +2 fields, Create/List/SearchText/Get visibility filtering |
| `internal/store/interfaces.go` | List/SearchText/ListByContext/ListByContextOrdered signature changes, +GetVisible |
| `internal/api/memory_handler.go` | Use GetIdentity, auto-inject owner_id/team_id/visibility |
| `internal/memory/manager.go` | Pass Identity through to store calls |
| `internal/search/retriever.go` | Pass Identity through to SearchText |

---

## Task 1: Model — Identity struct + sentinel errors

**Files:**
- Create: `internal/model/identity.go`
- Modify: `internal/model/errors.go:5-74`
- Modify: `internal/model/memory.go:42-94`

- [ ] **Step 1: Create Identity struct and visibility constants**

```go
// internal/model/identity.go
package model

// 可见性级别常量 / Visibility level constants
const (
	VisibilityPrivate = "private" // 仅创建者可见 / Owner only
	VisibilityTeam    = "team"    // 团队内可见 / Team members
	VisibilityPublic  = "public"  // 全局可见 / Everyone
)

// SystemOwnerID 系统内部操作使用的身份 / Identity for internal system operations
const SystemOwnerID = "__system__"

// Identity 请求身份信息 / Request identity context
type Identity struct {
	TeamID  string // 从 API Key 解析 / Resolved from API Key
	OwnerID string // 从 X-User-ID Header 提取 / From X-User-ID header
}

// IsSystem 是否为系统内部身份 / Whether this is a system identity
func (id *Identity) IsSystem() bool {
	return id.OwnerID == SystemOwnerID
}

// ValidVisibility 校验可见性值合法性 / Validate visibility value
func ValidVisibility(v string) bool {
	return v == VisibilityPrivate || v == VisibilityTeam || v == VisibilityPublic
}
```

- [ ] **Step 2: Add sentinel errors**

In `internal/model/errors.go`, add after line 73 (ErrDuplicateMemory):

```go
	// ErrUnauthorized 认证失败 / Authentication required
	ErrUnauthorized = errors.New("authentication required")

	// ErrForbidden 无权访问 / Access denied
	ErrForbidden = errors.New("access denied")
```

- [ ] **Step 3: Add fields to Memory struct**

In `internal/model/memory.go`, add after line 93 (ConsolidatedInto):

```go
	// V6: 身份与归属 / Identity & Ownership
	OwnerID    string `json:"owner_id,omitempty"`    // 创建者 ID / Creator ID
	Visibility string `json:"visibility,omitempty"`  // private / team / public
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: SUCCESS (no compilation errors)

- [ ] **Step 5: Commit**

```bash
git add internal/model/identity.go internal/model/errors.go internal/model/memory.go
git commit -m "feat(model): add Identity struct, visibility constants, and auth sentinel errors"
```

---

## Task 2: Model — Request DTO changes

**Files:**
- Modify: `internal/model/request.go:5-107`

- [ ] **Step 1: Add Visibility to CreateMemoryRequest**

After line 33 (AutoExtract field), add:

```go
	// V6: 可见性 / Visibility level
	Visibility string `json:"visibility,omitempty"` // private(default) / team / public
```

- [ ] **Step 2: Add Visibility to UpdateMemoryRequest**

After line 60 (TurnNumber field), add:

```go
	// V6: 可见性 / Visibility level
	Visibility *string `json:"visibility,omitempty"`
```

- [ ] **Step 3: Add TeamID and OwnerID to SearchFilters**

After line 97 (MessageRole field), add:

```go
	// V6: 身份过滤（API 层自动注入）/ Identity filtering (auto-injected by API layer)
	TeamID  string `json:"-"` // 不从 JSON 反序列化 / Not deserialized from JSON
	OwnerID string `json:"-"` // 不从 JSON 反序列化 / Not deserialized from JSON
```

- [ ] **Step 4: Add TeamID and OwnerID to TimelineRequest**

In `TimelineRequest` struct (line 100-106), add after Limit:

```go
	// V6: 身份过滤 / Identity filtering
	TeamID  string `json:"-"`
	OwnerID string `json:"-"`
```

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 6: Commit**

```bash
git add internal/model/request.go
git commit -m "feat(model): add visibility and identity fields to request DTOs"
```

---

## Task 3: Config — AuthConfig

**Files:**
- Modify: `internal/config/config.go:14-27` (Config struct), `71-75` (ServerConfig), `209+` (defaults)

- [ ] **Step 1: Add AuthConfig struct and Config field**

After the existing config structs (around line 75, after ServerConfig), add:

```go
// AuthConfig 认证配置 / Authentication configuration
type AuthConfig struct {
	Enabled bool         `mapstructure:"enabled"`
	APIKeys []APIKeyItem `mapstructure:"api_keys"`
}

// APIKeyItem API Key 配置项 / API Key configuration item
type APIKeyItem struct {
	Key    string `mapstructure:"key"`
	TeamID string `mapstructure:"team_id"`
	Name   string `mapstructure:"name"`
}
```

In the `Config` struct (line 14-27), add:

```go
	Auth AuthConfig `mapstructure:"auth"`
```

- [ ] **Step 2: Add defaults in LoadConfig**

In the defaults section of `LoadConfig()`, add:

```go
	viper.SetDefault("auth.enabled", false)
```

- [ ] **Step 3: Add deprecation bridge**

After config is loaded (after `viper.Unmarshal`), add logic: if `server.auth_enabled` is true but `auth.enabled` is false, bridge the value and log a warning.

```go
	// 兼容旧配置 / Backward compatibility: server.auth_enabled → auth.enabled
	if cfg.Server.AuthEnabled && !cfg.Auth.Enabled {
		logger.Warn("server.auth_enabled is deprecated, use auth.enabled instead")
		cfg.Auth.Enabled = true
	}
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add AuthConfig for API Key management"
```

---

## Task 4: Migration V6

**Files:**
- Modify: `internal/store/sqlite_migration.go:17` (latestVersion), `39-94` (Migrate func)
- Test: `testing/store/migration_v6_test.go`

- [ ] **Step 1: Write migration test**

```go
// testing/store/migration_v6_test.go
package store_test

import (
	"context"
	"database/sql"
	"testing"

	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	_ "modernc.org/sqlite"
)

func TestMigrateV5ToV6_AddsOwnershipFields(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 先执行到 V5
	tok := tokenizer.NewNoopTokenizer()
	if err := store.Migrate(db, tok); err != nil {
		t.Fatal(err)
	}

	// 插入一条老数据（无 owner_id，team_id 为空）
	_, err = db.Exec(`INSERT INTO memories (id, content, team_id, created_at, updated_at)
		VALUES ('test-1', 'old memory', '', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatal(err)
	}

	// 验证新列存在
	var ownerID, visibility, teamID string
	err = db.QueryRow(`SELECT owner_id, visibility, team_id FROM memories WHERE id = 'test-1'`).
		Scan(&ownerID, &visibility, &teamID)
	if err != nil {
		t.Fatalf("new columns should exist: %v", err)
	}

	// 验证老数据迁移
	if visibility != "team" {
		t.Errorf("old data visibility = %q, want 'team'", visibility)
	}
	if teamID != "default" {
		t.Errorf("old data team_id = %q, want 'default'", teamID)
	}

	// 验证版本号
	var version int
	db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	if version != 6 {
		t.Errorf("schema version = %d, want 6", version)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/store/ -run TestMigrateV5ToV6 -v`
Expected: FAIL (migration V6 not yet implemented, columns won't exist)

- [ ] **Step 3: Implement migration V6**

In `internal/store/sqlite_migration.go`:

Change line 17: `const latestVersion = 6`

In `Migrate()` func, after the V4→V5 block (around line 91), add:

```go
	// V5→V6: 身份与归属 / Identity & Ownership
	if version < 6 {
		if err := migrateV5ToV6(db); err != nil {
			return fmt.Errorf("migration V5→V6 failed: %w", err)
		}
	}
```

Add the migration function:

```go
// migrateV5ToV6 身份与归属字段
func migrateV5ToV6(db *sql.DB) error {
	logger.Info("executing migration V5→V6")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 新增 owner_id 和 visibility 列
	alterColumns := []string{
		`ALTER TABLE memories ADD COLUMN owner_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN visibility TEXT DEFAULT 'private'`,
	}
	for _, stmt := range alterColumns {
		if _, err := tx.Exec(stmt); err != nil {
			if isColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to alter table: %w", err)
		}
	}

	// 索引
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_owner_id ON memories(owner_id)`); err != nil {
		return fmt.Errorf("failed to create owner_id index: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_visibility ON memories(visibility)`); err != nil {
		return fmt.Errorf("failed to create visibility index: %w", err)
	}

	// 老数据迁移：visibility 设为 team，空 team_id 回填 default
	if _, err := tx.Exec(`UPDATE memories SET visibility = 'team' WHERE owner_id = ''`); err != nil {
		return fmt.Errorf("failed to backfill visibility: %w", err)
	}
	if _, err := tx.Exec(`UPDATE memories SET team_id = 'default' WHERE team_id = ''`); err != nil {
		return fmt.Errorf("failed to backfill team_id: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_version (version) VALUES (6)`); err != nil {
		return fmt.Errorf("failed to update schema version: %w", err)
	}

	return tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/store/ -run TestMigrateV5ToV6 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite_migration.go testing/store/migration_v6_test.go
git commit -m "feat(store): add migration V6 — owner_id + visibility columns with data backfill"
```

---

## Task 5: Store — Update memoryColumns, scanMemory, Create

**Files:**
- Modify: `internal/store/sqlite.go:22-34` (columns), `100-159` (Create), `700-810` (scan helpers)

- [ ] **Step 1: Update memoryColumns constant**

Change `memoryColumns` (line 22-27) to append `, owner_id, visibility` at the end:

```go
const memoryColumns = `id, content, metadata, team_id, embedding_id, parent_id, is_latest, access_count, created_at, updated_at,
	uri, context_id, kind, sub_kind, scope, abstract, summary,
	happened_at, source_type, source_ref, document_id, chunk_index,
	deleted_at, strength, decay_rate, last_accessed_at, reinforced_count, expires_at,
	retention_tier, message_role, turn_number, content_hash, consolidated_into, owner_id, visibility`
```

Update `memoryColumnsAliased` (line 29-34) similarly, adding `m.owner_id, m.visibility`.

- [ ] **Step 2: Update scanMemory — add owner_id and visibility to Scan**

In `scanMemory` (line 702), add variables:

```go
	ownerID    sql.NullString
	visibility sql.NullString
```

In `row.Scan(...)` (line 723-731), append `&ownerID, &visibility` after `&consolidatedInto`.

After the consolidatedInto block (line 748-749), add:

```go
	if ownerID.Valid {
		mem.OwnerID = ownerID.String
	}
	if visibility.Valid {
		mem.Visibility = visibility.String
	}
```

- [ ] **Step 3: Update scanMemoryFromRows and scanMemoryWithRank identically**

Apply the same changes to `scanMemoryFromRows` (line 754+) and `scanMemoryWithRank` (line 806+).

- [ ] **Step 4: Update Create method — add owner_id and visibility to INSERT**

In `Create` (line 100-159):

Add default visibility after the RetentionTier default (line 128-129):

```go
	if mem.Visibility == "" {
		mem.Visibility = model.VisibilityPrivate
	}
```

Update the INSERT query (line 136-137) — add 2 more `?` placeholders (total 35):

```go
	query := `INSERT INTO memories (` + memoryColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
```

In the ExecContext args (line 139-148), append `mem.OwnerID, mem.Visibility` after `mem.ConsolidatedInto`.

- [ ] **Step 5: Update CreateBatch method identically**

In `CreateBatch` (line 161+):

Add default visibility in the per-memory loop (after RetentionTier default):

```go
	if mem.Visibility == "" {
		mem.Visibility = model.VisibilityPrivate
	}
```

Update the INSERT query (line 173-174) — add 2 more `?` placeholders (total 35):

```go
	insertQuery := `INSERT INTO memories (` + memoryColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
```

In the ExecContext args (line 210+), append `mem.OwnerID, mem.Visibility` after `mem.ConsolidatedInto`.

**Also update the Update method** (line 263+): add `owner_id = ?, visibility = ?` to the SET clause, and append the corresponding args. Note: `owner_id` should NOT be updatable via the API (enforced at handler level), but the store-level Update should still persist it.

- [ ] **Step 6: Verify build + existing tests still pass**

Run: `go build ./... && go test ./testing/store/ -v -count=1`
Expected: BUILD SUCCESS, tests PASS (migration test creates new schema with all columns)

- [ ] **Step 6: Commit**

```bash
git add internal/store/sqlite.go
git commit -m "feat(store): add owner_id + visibility to columns, scan helpers, and Create"
```

---

## Task 6: Store — Visibility filtering on List, SearchText, Get

**Files:**
- Modify: `internal/store/interfaces.go:12-82` (MemoryStore interface)
- Modify: `internal/store/sqlite.go:360-460` (List, SearchText, ListByContext)
- Test: `testing/store/visibility_test.go`

- [ ] **Step 1: Write visibility filtering tests**

```go
// testing/store/visibility_test.go
package store_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
)

func setupVisibilityStore(t *testing.T) store.MemoryStore {
	t.Helper()
	s, err := store.NewSQLiteMemoryStore(":memory:", [3]float64{10, 5, 3}, tokenizer.NewNoopTokenizer())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// 插入测试数据
	memories := []*model.Memory{
		{Content: "alice private", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityPrivate},
		{Content: "alice team", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityTeam},
		{Content: "bob private", TeamID: "team-a", OwnerID: "bob", Visibility: model.VisibilityPrivate},
		{Content: "public knowledge", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityPublic},
		{Content: "other team", TeamID: "team-b", OwnerID: "carol", Visibility: model.VisibilityTeam},
	}
	for _, m := range memories {
		if err := s.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestList_VisibilityFiltering(t *testing.T) {
	s := setupVisibilityStore(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		identity *model.Identity
		wantMin  int // 至少能看到几条
		wantMax  int // 最多能看到几条
	}{
		{
			name:     "alice sees own private + team + public",
			identity: &model.Identity{TeamID: "team-a", OwnerID: "alice"},
			wantMin:  3, // alice private + alice team + public
			wantMax:  3,
		},
		{
			name:     "bob sees own private + team + public, not alice private",
			identity: &model.Identity{TeamID: "team-a", OwnerID: "bob"},
			wantMin:  3, // bob private + alice team + public
			wantMax:  3,
		},
		{
			name:     "team-b user sees only public from team-a",
			identity: &model.Identity{TeamID: "team-b", OwnerID: "carol"},
			wantMin:  2, // public + other team
			wantMax:  2,
		},
		{
			name:     "system sees team + public, not private",
			identity: &model.Identity{TeamID: "team-a", OwnerID: model.SystemOwnerID},
			wantMin:  2, // alice team + public
			wantMax:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := s.List(ctx, tt.identity, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) < tt.wantMin || len(results) > tt.wantMax {
				t.Errorf("got %d results, want %d-%d", len(results), tt.wantMin, tt.wantMax)
				for _, r := range results {
					t.Logf("  - %s (owner=%s, vis=%s, team=%s)", r.Content, r.OwnerID, r.Visibility, r.TeamID)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/store/ -run TestList_VisibilityFiltering -v`
Expected: FAIL (List signature doesn't accept Identity yet)

- [ ] **Step 3: Update MemoryStore interface signatures**

> **Atomicity note:** Steps 3-9 form a single atomic unit. The interface change in Step 3 will break all callers. Steps 4-9 fix those callers. The code will NOT compile between Steps 3 and 9. This is expected — commit only after Step 12.

In `internal/store/interfaces.go`:

Change `List` (line 32):
```go
	List(ctx context.Context, identity *model.Identity, offset, limit int) ([]*model.Memory, error)
```

Change `SearchText` (line 35):
```go
	SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error)
```

Change `ListByContext` (line 41):
```go
	ListByContext(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error)
```

Change `ListByContextOrdered` (line 74):
```go
	ListByContextOrdered(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error)
```

Add new method after `Get` (line 28):
```go
	// GetVisible 带可见性校验获取记忆 / Get memory with visibility check
	GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error)
```

- [ ] **Step 4: Implement visibility SQL helper**

In `internal/store/sqlite.go`, add a helper function:

```go
// visibilityCondition 返回可见性 WHERE 子句和参数 / Return visibility WHERE clause and args
// 前缀 p 为表别名（如 "m." 或 ""）
func visibilityCondition(prefix string, identity *model.Identity) (string, []interface{}) {
	return fmt.Sprintf(`(%[1]svisibility = 'public'
		OR (%[1]steam_id = ? AND %[1]svisibility = 'team')
		OR (%[1]steam_id = ? AND %[1]svisibility = 'private' AND %[1]sowner_id = ?))`,
		prefix), []interface{}{identity.TeamID, identity.TeamID, identity.OwnerID}
}
```

- [ ] **Step 5: Update List method**

Change signature and query in `List` (line 360-378):

```go
func (s *SQLiteMemoryStore) List(ctx context.Context, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE deleted_at IS NULL AND ` + visCond + `
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?`

	args := append(visArgs, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}
```

- [ ] **Step 6: Update SearchText method**

Change signature and query in `SearchText` (line 380-432):

```go
func (s *SQLiteMemoryStore) SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
```

Replace the WHERE clause to use `visibilityCondition("m.", identity)` instead of `(m.team_id = ? OR ? = '')`.

- [ ] **Step 7: Implement GetVisible**

```go
func (s *SQLiteMemoryStore) GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error) {
	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE id = ? AND deleted_at IS NULL AND ` + visCond

	args := append([]interface{}{id}, visArgs...)
	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get visible memory: %w", err)
	}
	return mem, nil
}
```

- [ ] **Step 8: Update ListByContext and ListByContextOrdered similarly**

Add Identity parameter, use `visibilityCondition`.

- [ ] **Step 9: Fix all compilation errors in callers**

Update callers that pass `teamID string` to now pass `*model.Identity`:

- `internal/memory/manager.go` — create a helper to build Identity from context or use system Identity
- `internal/search/retriever.go` — pass Identity from RetrieveRequest
- `internal/memory/consolidation.go` — use `&model.Identity{TeamID: teamID, OwnerID: model.SystemOwnerID}`
- `cmd/test-dashboard/testenv.go` — use `&model.Identity{TeamID: "default", OwnerID: model.SystemOwnerID}`
- All handler files — will be updated in Task 8, for now use placeholder Identity to compile

- [ ] **Step 10: Run test to verify it passes**

Run: `go test ./testing/store/ -run TestList_VisibilityFiltering -v`
Expected: PASS

- [ ] **Step 11: Run full test suite**

Run: `go test ./testing/... -v -count=1`
Expected: PASS (all existing tests should still work)

- [ ] **Step 12: Commit**

```bash
git add internal/store/interfaces.go internal/store/sqlite.go internal/memory/ internal/search/ cmd/ testing/store/visibility_test.go
git commit -m "feat(store): enforce visibility filtering on List, SearchText, Get, ListByContext"
```

---

## Task 7: Middleware — AuthMiddleware + IdentityMiddleware

**Files:**
- Modify: `internal/api/middleware.go:1-48`
- Modify: `internal/api/router.go:26-38`
- Test: `testing/api/auth_middleware_test.go`
- Test: `testing/api/identity_middleware_test.go`

- [ ] **Step 1: Write auth middleware test**

```go
// testing/api/auth_middleware_test.go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"iclude/internal/api"
	"iclude/internal/config"

	"github.com/gin-gonic/gin"
)

func TestAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	authCfg := config.AuthConfig{
		Enabled: true,
		APIKeys: []config.APIKeyItem{
			{Key: "sk-test-key", TeamID: "team-abc", Name: "test"},
		},
	}

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"valid key", "Bearer sk-test-key", http.StatusOK},
		{"invalid key", "Bearer sk-wrong", http.StatusUnauthorized},
		{"missing header", "", http.StatusUnauthorized},
		{"malformed header", "sk-test-key", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(api.AuthMiddleware(authCfg))
			r.GET("/test", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest("GET", "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestAuthMiddleware_Disabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	authCfg := config.AuthConfig{Enabled: false}

	r := gin.New()
	r.Use(api.AuthMiddleware(authCfg))
	r.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("disabled auth should pass, got status %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/api/ -run TestAuthMiddleware -v`
Expected: FAIL (AuthMiddleware not yet defined)

- [ ] **Step 3: Implement middleware**

In `internal/api/middleware.go`, add:

```go
import (
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const identityKey = "iclude_identity"

// AuthMiddleware API Key 认证中间件 / API Key authentication middleware
func AuthMiddleware(cfg config.AuthConfig) gin.HandlerFunc {
	// 构建内存 map / Build in-memory lookup map
	keyMap := make(map[string]string, len(cfg.APIKeys))
	for _, item := range cfg.APIKeys {
		keyMap[item.Key] = item.TeamID
	}

	return func(c *gin.Context) {
		if !cfg.Enabled {
			// 开发模式：注入默认 team_id / Dev mode: inject default
			c.Set("team_id", "default")
			c.Next()
			return
		}

		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		teamID, ok := keyMap[token]
		if !ok {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid api key"})
			return
		}

		c.Set("team_id", teamID)
		c.Next()
	}
}

// IdentityMiddleware 身份注入中间件 / Identity injection middleware
func IdentityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		teamIDVal, exists := c.Get("team_id")
		if !exists {
			c.AbortWithStatusJSON(500, gin.H{"error": "team_id not set by auth middleware"})
			return
		}
		teamID, ok := teamIDVal.(string)
		if !ok {
			c.AbortWithStatusJSON(500, gin.H{"error": "team_id has invalid type"})
			return
		}

		ownerID := c.GetHeader("X-User-ID")
		if ownerID == "" {
			ownerID = "anonymous"
		}

		identity := &model.Identity{
			TeamID:  teamID,
			OwnerID: ownerID,
		}
		SetIdentity(c, identity)
		c.Next()
	}
}

// SetIdentity 将身份信息写入请求上下文 / Set identity into request context
func SetIdentity(c *gin.Context, id *model.Identity) {
	c.Set(identityKey, id)
}

// GetIdentity 从请求上下文获取身份 / Get identity from request context
func GetIdentity(c *gin.Context) *model.Identity {
	val, exists := c.Get(identityKey)
	if !exists {
		return nil
	}
	id, ok := val.(*model.Identity)
	if !ok {
		return nil
	}
	return id
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/api/ -run TestAuthMiddleware -v`
Expected: PASS

- [ ] **Step 5: Register middleware on router**

In `internal/api/router.go`, update `SetupRouter`:

The `RouterDeps` struct needs an `AuthConfig` field. Add:

```go
type RouterDeps struct {
	// ... existing fields ...
	AuthConfig config.AuthConfig
}
```

In `SetupRouter`, after `r.Use(LoggerMiddleware())` (line 32), add auth middleware to the v1 group:

```go
	v1 := r.Group("/v1")
	v1.Use(AuthMiddleware(deps.AuthConfig))
	v1.Use(IdentityMiddleware())
```

Update `cmd/server/main.go` to pass `AuthConfig` in `RouterDeps`.

- [ ] **Step 6: Verify build**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 7: Commit**

```bash
git add internal/api/middleware.go internal/api/router.go cmd/server/main.go testing/api/
git commit -m "feat(api): add AuthMiddleware + IdentityMiddleware with API Key validation"
```

---

## Task 8: Handlers — Identity injection

**Files:**
- Modify: `internal/api/memory_handler.go:24-171`
- Modify: all other handler files

- [ ] **Step 1: Update Create handler to inject identity**

In `memory_handler.go` Create method, after binding the request, add:

```go
	identity := GetIdentity(c)
	if identity == nil {
		ErrorResponse(c, 500, "identity not found in context")
		return
	}

	// 强制覆盖 owner_id 和 team_id / Force override from identity
	// (请求体中即使传了也会被覆盖)
```

When building the Memory struct, set:
```go
	mem.OwnerID = identity.OwnerID
	mem.TeamID = identity.TeamID
```

Validate and set visibility:
```go
	if req.Visibility == "" {
		mem.Visibility = model.VisibilityPrivate
	} else if !model.ValidVisibility(req.Visibility) {
		ErrorResponse(c, 400, "invalid visibility: must be private, team, or public")
		return
	} else {
		mem.Visibility = req.Visibility
	}
```

- [ ] **Step 2: Update List handler**

Replace `teamID := c.Query("team_id")` with identity from context. Pass `identity` to `manager.List()`.

- [ ] **Step 3: Update Get handler**

Use `GetVisible` instead of `Get` for API-facing requests.

- [ ] **Step 4: Update all other handlers**

For each handler file (conversation, document, graph, tag, context), replace query param `team_id` usage with `GetIdentity(c)`.

- [ ] **Step 5: Verify build + full test suite**

Run: `go build ./... && go test ./testing/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/
git commit -m "feat(api): inject Identity in all handlers, enforce visibility on CRUD"
```

---

## Task 9: SearchTextFiltered + VectorStore updates

**Files:**
- Modify: `internal/store/sqlite.go:480-555` (SearchTextFiltered)
- Modify: `internal/store/interfaces.go:84-103` (VectorStore)
- Modify: `internal/store/qdrant.go` (if exists)

- [ ] **Step 1: Update SearchTextFiltered to use SearchFilters.TeamID/OwnerID**

In `SearchTextFiltered`, replace the existing `team_id` filter with `visibilityCondition` using `filters.TeamID` and `filters.OwnerID`.

- [ ] **Step 2: Update VectorStore.Search signature**

Change `Search(ctx, embedding, teamID string, limit int)` to `Search(ctx, embedding []float32, identity *model.Identity, limit int)`.

Update `internal/store/qdrant.go` implementation to add `owner_id` + `visibility` payload filters.

- [ ] **Step 3: Update Retriever to pass Identity through**

In `internal/search/retriever.go`, ensure the Identity from `RetrieveRequest` is threaded into both SQLite and Qdrant search calls.

- [ ] **Step 4: Verify build + tests**

Run: `go build ./... && go test ./testing/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/ internal/search/
git commit -m "feat(search): enforce visibility in SearchTextFiltered and VectorStore"
```

---

## Task 10: Testreport integration tests

**Files:**
- Create: `testing/report/identity_test.go`

- [ ] **Step 1: Write testreport-integrated test**

```go
// testing/report/identity_test.go
package report_test

import (
	"context"
	"fmt"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"
)

func TestIdentity_VisibilityIsolation(t *testing.T) {
	// 使用 newTestStore（定义在 main_test.go 中）
	s := newTestStore(t, tokenizer.NewNoopTokenizer())
	defer s.Close()

	tc := testreport.NewCase("visibility-isolation", "记忆可见性隔离 / Memory visibility isolation")
	tc.Input("scenario", "3 memories: alice-private, alice-team, bob-private in team-a")

	ctx := context.Background()

	// 创建测试数据
	tc.Step("create memories with different visibility")
	for _, m := range []*model.Memory{
		{Content: "alice secret", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityPrivate},
		{Content: "team knowledge", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityTeam},
		{Content: "bob secret", TeamID: "team-a", OwnerID: "bob", Visibility: model.VisibilityPrivate},
	} {
		if err := s.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	// alice 查询
	tc.Step("alice lists memories")
	aliceID := &model.Identity{TeamID: "team-a", OwnerID: "alice"}
	aliceResults, err := s.List(ctx, aliceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	tc.Output("alice visible count", fmt.Sprintf("%d (expect 2: own private + team)", len(aliceResults)))
	if len(aliceResults) != 2 {
		t.Errorf("alice should see 2 memories, got %d", len(aliceResults))
	}

	// bob 查询
	tc.Step("bob lists memories")
	bobID := &model.Identity{TeamID: "team-a", OwnerID: "bob"}
	bobResults, err := s.List(ctx, bobID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	tc.Output("bob visible count", fmt.Sprintf("%d (expect 2: own private + team)", len(bobResults)))
	if len(bobResults) != 2 {
		t.Errorf("bob should see 2 memories, got %d", len(bobResults))
	}

	tc.Done()
}
```

- [ ] **Step 2: Run test**

Run: `go test ./testing/report/ -run TestIdentity -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add testing/report/identity_test.go
git commit -m "test: add testreport integration tests for visibility isolation"
```

---

## Task 11: Update deploy config + .env.example

**Files:**
- Modify: `deploy/config.yaml` (if exists)
- Modify: `.env.example`

- [ ] **Step 1: Add auth section to config.yaml**

```yaml
auth:
  enabled: false
  api_keys: []
```

- [ ] **Step 2: Update .env.example with auth notes**

Add comment explaining API Key configuration.

- [ ] **Step 3: Commit**

```bash
git add deploy/ .env.example
git commit -m "docs: add auth config section to deploy config and .env.example"
```

---

## Task 12: Final verification

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 2: Full test suite**

Run: `go test ./testing/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 3: Generate test report**

Run: `go test ./testing/report/ -v -count=1`
Verify: `testing/report/report.html` includes identity test cases

- [ ] **Step 4: Manual smoke test**

```bash
# 启动服务（auth disabled）
go run ./cmd/server/

# 创建记忆（无需 auth）
curl -X POST http://localhost:8080/v1/memories \
  -H "Content-Type: application/json" \
  -H "X-User-ID: alice" \
  -d '{"content":"test memory","visibility":"team"}'

# 列出记忆
curl http://localhost:8080/v1/memories
```

- [ ] **Step 5: Final commit if any cleanup needed**
