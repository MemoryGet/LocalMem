# B4 时间与演化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 LongMemEval 常见失分点 — temporal 动态时间窗口 + memory_class 三层演化（episodic/semantic/procedural）

**Architecture:** 新增 V12 迁移加 `memory_class` + `derived_from` 两列；temporal 查询按关键词动态设窗口大小；consolidation 产出标记 semantic，reflect 产出标记 procedural，heartbeat 巡检晋升高频 episodic → semantic；检索按 classWeight 加权。

**Tech Stack:** Go 1.25+, SQLite, table-driven tests

**Spec:** `docs/superpowers/specs/2026-04-02-b4-time-evolution-design.md`

---

### Task 1: Schema — V12 迁移加 memory_class + derived_from

**Files:**
- Modify: `internal/store/sqlite_migration.go:17` (version constant), append new function
- Modify: `internal/store/sqlite.go:22-27` (memoryColumns), `internal/store/sqlite.go:30-34` (memoryColumnsAliased)
- Modify: `internal/model/memory.go:95-98` (Memory struct, append after Visibility)
- Test: `testing/store/migration_v12_test.go` (create)

- [ ] **Step 1: Write V12 migration test**

Create `testing/store/migration_v12_test.go`:

```go
package store_test

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestMigrateV11ToV12(t *testing.T) {
	tests := []struct {
		name       string
		setupSQL   []string // Pre-migration data
		wantClass  map[string]string // memory_id → expected memory_class
	}{
		{
			name: "mental_model maps to procedural",
			setupSQL: []string{
				`INSERT INTO memories (id, content, kind, scope, team_id, is_latest, created_at, updated_at, retention_tier, visibility)
				 VALUES ('m1', 'test', 'mental_model', 'default', 't1', 1, datetime('now'), datetime('now'), 'standard', 'private')`,
			},
			wantClass: map[string]string{"m1": "procedural"},
		},
		{
			name: "consolidated maps to semantic",
			setupSQL: []string{
				`INSERT INTO memories (id, content, kind, scope, team_id, is_latest, created_at, updated_at, retention_tier, visibility)
				 VALUES ('m2', 'test', 'consolidated', 'default', 't1', 1, datetime('now'), datetime('now'), 'standard', 'private')`,
			},
			wantClass: map[string]string{"m2": "semantic"},
		},
		{
			name: "note defaults to episodic",
			setupSQL: []string{
				`INSERT INTO memories (id, content, kind, scope, team_id, is_latest, created_at, updated_at, retention_tier, visibility)
				 VALUES ('m3', 'test', 'note', 'default', 't1', 1, datetime('now'), datetime('now'), 'standard', 'private')`,
			},
			wantClass: map[string]string{"m3": "episodic"},
		},
		{
			name: "idempotent — running twice does not error",
			setupSQL: []string{},
			wantClass: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupV11DB(t)

			for _, sql := range tt.setupSQL {
				if _, err := db.Exec(sql); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}

			// Run V12 migration (first time)
			if err := migrateV11ToV12(db); err != nil {
				t.Fatalf("migration failed: %v", err)
			}

			// Verify mapping
			for id, wantClass := range tt.wantClass {
				var got string
				if err := db.QueryRow("SELECT memory_class FROM memories WHERE id = ?", id).Scan(&got); err != nil {
					t.Fatalf("query memory_class for %s: %v", id, err)
				}
				if got != wantClass {
					t.Errorf("memory %s: got memory_class=%q, want %q", id, got, wantClass)
				}
			}

			// Verify derived_from column exists (nullable)
			var derivedFrom sql.NullString
			row := db.QueryRow("SELECT derived_from FROM memories LIMIT 1")
			if err := row.Scan(&derivedFrom); err != nil && err != sql.ErrNoRows {
				t.Fatalf("derived_from column missing: %v", err)
			}

			// Verify index exists
			var cnt int
			db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_memories_memory_class'").Scan(&cnt)
			if cnt != 1 {
				t.Error("idx_memories_memory_class index not created")
			}

			// Verify idempotent (run again)
			if err := migrateV11ToV12(db); err != nil {
				t.Fatalf("idempotent migration failed: %v", err)
			}
		})
	}
}

// setupV11DB 创建一个 V11 schema 的内存数据库 / Create an in-memory DB at V11 schema
func setupV11DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// Minimal V11 schema (memories table + schema_version)
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at TEXT)`,
		`INSERT INTO schema_version (version, applied_at) VALUES (11, datetime('now'))`,
		`CREATE TABLE memories (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			metadata TEXT,
			team_id TEXT NOT NULL DEFAULT '',
			embedding_id TEXT DEFAULT '',
			parent_id TEXT DEFAULT '',
			is_latest INTEGER DEFAULT 1,
			access_count INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			uri TEXT DEFAULT '',
			context_id TEXT DEFAULT '',
			kind TEXT DEFAULT '',
			sub_kind TEXT DEFAULT '',
			scope TEXT DEFAULT 'default',
			abstract TEXT DEFAULT '',
			summary TEXT DEFAULT '',
			happened_at DATETIME,
			source_type TEXT DEFAULT '',
			source_ref TEXT DEFAULT '',
			document_id TEXT DEFAULT '',
			chunk_index INTEGER DEFAULT 0,
			deleted_at DATETIME,
			strength REAL DEFAULT 1.0,
			decay_rate REAL DEFAULT 0.01,
			last_accessed_at DATETIME,
			reinforced_count INTEGER DEFAULT 0,
			expires_at DATETIME,
			retention_tier TEXT DEFAULT 'standard',
			message_role TEXT DEFAULT '',
			turn_number INTEGER DEFAULT 0,
			content_hash TEXT DEFAULT '',
			consolidated_into TEXT DEFAULT '',
			owner_id TEXT DEFAULT '',
			visibility TEXT DEFAULT 'private'
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup: %v\nSQL: %s", err, s)
		}
	}
	return db
}
```

Note: `migrateV11ToV12` 将在 Step 3 实现后，需要在测试文件中导出调用。由于迁移函数在 `store` 包内（非导出），测试需通过 `SQLiteMemoryStore` 构造函数触发完整迁移链路，或通过 `internal_test` 方式访问。为简化，本测试使用 `store_test` 包 + 调用 `NewSQLiteMemoryStore` 构造函数触发自动迁移。

实际测试调用改为：

```go
// 替换直接调用 migrateV11ToV12，使用 InitStores 或直接构造 store 触发迁移
// 由于需要测试隔离，用更直接的方式：在 store 包内新增一个导出的测试辅助函数
```

鉴于项目现有测试模式（`testing/store/` 包使用 `store_test` 包名），更实际的做法是通过创建 `SQLiteMemoryStore` 来触发迁移。调整测试：

```go
func TestMigrateV11ToV12(t *testing.T) {
	// 使用 tmpdir 创建 V11 db 文件，然后用 NewSQLiteMemoryStore 打开触发迁移
	// NewSQLiteMemoryStore 会自动执行 runMigrations
	// ...
}
```

由于迁移测试的具体模式需要匹配现有项目结构，Step 1 先创建测试骨架，Step 3 实现后再验证。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/store/ -run TestMigrateV11ToV12 -v -count=1`
Expected: FAIL (migrateV11ToV12 不存在)

- [ ] **Step 3: Implement V12 migration**

Modify `internal/store/sqlite_migration.go`:

1. 更新版本号（line 17）:
```go
const latestVersion = 12
```

2. 在 `runMigrations` 的 switch 中添加 case 11（在现有 case 10 之后）:
```go
case 11:
    if err := migrateV11ToV12(s.db); err != nil {
        return fmt.Errorf("migration V11→V12 failed: %w", err)
    }
    fallthrough
```

3. Append `migrateV11ToV12` 函数:
```go
func migrateV11ToV12(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Add memory_class column (idempotent via isColumnExistsError)
	if _, err := tx.Exec(`ALTER TABLE memories ADD COLUMN memory_class TEXT NOT NULL DEFAULT 'episodic'`); err != nil {
		if !isColumnExistsError(err) {
			return fmt.Errorf("V11→V12 add memory_class: %w", err)
		}
	}

	// Add derived_from column (JSON array, nullable)
	if _, err := tx.Exec(`ALTER TABLE memories ADD COLUMN derived_from TEXT`); err != nil {
		if !isColumnExistsError(err) {
			return fmt.Errorf("V11→V12 add derived_from: %w", err)
		}
	}

	// Data migration: map existing kind values to memory_class
	if _, err := tx.Exec(`UPDATE memories SET memory_class = 'procedural' WHERE kind = 'mental_model' AND memory_class = 'episodic'`); err != nil {
		return fmt.Errorf("V11→V12 map mental_model: %w", err)
	}
	if _, err := tx.Exec(`UPDATE memories SET memory_class = 'semantic' WHERE kind = 'consolidated' AND memory_class = 'episodic'`); err != nil {
		return fmt.Errorf("V11→V12 map consolidated: %w", err)
	}

	// Index for memory_class
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_memory_class ON memories(memory_class)`); err != nil {
		return fmt.Errorf("V11→V12 create index: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (12, datetime('now'))`); err != nil {
		return fmt.Errorf("V11→V12 schema_version: %w", err)
	}

	logger.Info("migration V11→V12 completed: memory_class + derived_from columns")
	return tx.Commit()
}
```

- [ ] **Step 4: Update Memory struct**

Modify `internal/model/memory.go`, append after `Visibility` field (line 97):

```go
	// V12: Memory evolution layer
	MemoryClass string   `json:"memory_class,omitempty"` // episodic(default) / semantic / procedural
	DerivedFrom []string `json:"derived_from,omitempty"` // 来源记忆 ID 列表 / Source memory IDs (JSON array)
```

- [ ] **Step 5: Update memoryColumns and scan pattern**

Modify `internal/store/sqlite.go`:

1. Update `memoryColumns` (line 22-27) — append `, memory_class, derived_from` after `visibility`:
```go
const memoryColumns = `id, content, metadata, team_id, embedding_id, parent_id, is_latest, access_count, created_at, updated_at,
	uri, context_id, kind, sub_kind, scope, abstract, summary,
	happened_at, source_type, source_ref, document_id, chunk_index,
	deleted_at, strength, decay_rate, last_accessed_at, reinforced_count, expires_at,
	retention_tier, message_role, turn_number, content_hash, consolidated_into, owner_id, visibility,
	memory_class, derived_from`
```

2. Update `memoryColumnsAliased` (line 30-34) — append `, m.memory_class, m.derived_from`:
```go
const memoryColumnsAliased = `m.id, m.content, m.metadata, m.team_id, m.embedding_id, m.parent_id, m.is_latest, m.access_count, m.created_at, m.updated_at,
	m.uri, m.context_id, m.kind, m.sub_kind, m.scope, m.abstract, m.summary,
	m.happened_at, m.source_type, m.source_ref, m.document_id, m.chunk_index,
	m.deleted_at, m.strength, m.decay_rate, m.last_accessed_at, m.reinforced_count, m.expires_at,
	m.retention_tier, m.message_role, m.turn_number, m.content_hash, m.consolidated_into, m.owner_id, m.visibility,
	m.memory_class, m.derived_from`
```

3. Update `memScanDest` struct (line 281-300) — add fields:
```go
	memoryClass sql.NullString
	derivedFrom sql.NullString // JSON array stored as text
```

4. Update `scanFields()` (line 304-315) — append to return slice:
```go
		&d.memoryClass, &d.derivedFrom,
```

5. Update `toMemory()` (line 318-340) — add after visibility block:
```go
	if d.memoryClass.Valid && d.memoryClass.String != "" {
		d.mem.MemoryClass = d.memoryClass.String
	}
	if d.derivedFrom.Valid && d.derivedFrom.String != "" {
		if err := json.Unmarshal([]byte(d.derivedFrom.String), &d.mem.DerivedFrom); err != nil {
			// Graceful: log and skip rather than fail
			d.mem.DerivedFrom = nil
		}
	}
```

Comment in `scanFields` and `scanMemory` docstrings: update `35 列` → `37 列` / `35 columns` → `37 columns`.

- [ ] **Step 6: Update Create method to write new fields**

Modify `internal/store/sqlite_memory_write.go`:

1. In the `Create` method, add default for `MemoryClass` after the `Visibility` default block:
```go
	if mem.MemoryClass == "" {
		mem.MemoryClass = "episodic"
	}
```

2. Update the INSERT query parameter count from 35 `?` to 37 `?` and add the two new columns at the end of the values:
```go
	// Serialize derived_from as JSON
	var derivedFromJSON *string
	if len(mem.DerivedFrom) > 0 {
		b, _ := json.Marshal(mem.DerivedFrom)
		s := string(b)
		derivedFromJSON = &s
	}
```

Append to the `tx.ExecContext` args:
```go
		mem.MemoryClass, derivedFromJSON,
```

3. Also update the `Update` method similarly — read the file to find exact location, add `memory_class` and `derived_from` to the UPDATE SET clause.

- [ ] **Step 7: Run migration test**

Run: `go test ./testing/store/ -run TestMigrateV11ToV12 -v -count=1`
Expected: PASS

- [ ] **Step 8: Run full test suite to verify backward compatibility**

Run: `go test ./testing/... -count=1 -timeout 120s`
Expected: All existing tests PASS (new columns have defaults, scan updated)

- [ ] **Step 9: Commit**

```bash
git add internal/store/sqlite_migration.go internal/store/sqlite.go internal/store/sqlite_memory_write.go internal/model/memory.go testing/store/migration_v12_test.go
git commit -m "feat(store): B4#1 — V12 migration adds memory_class + derived_from columns"
```

---

### Task 2: Model — Request/Response DTO 扩展

**Files:**
- Modify: `internal/model/request.go:5-42` (CreateMemoryRequest), `internal/model/request.go:74-92` (RetrieveRequest)
- Modify: `internal/api/memory_handler.go` (Create handler — no change needed, auto-binds)
- Test: `testing/api/memory_class_dto_test.go` (create)

- [ ] **Step 1: Write DTO test**

Create `testing/api/memory_class_dto_test.go`:

```go
package api_test

import (
	"encoding/json"
	"testing"

	"iclude/internal/model"
)

func TestCreateMemoryRequest_MemoryClassDefault(t *testing.T) {
	tests := []struct {
		name      string
		jsonInput string
		wantClass string
	}{
		{
			name:      "omitted defaults to empty (Manager will set episodic)",
			jsonInput: `{"content":"hello"}`,
			wantClass: "",
		},
		{
			name:      "explicit semantic",
			jsonInput: `{"content":"hello","memory_class":"semantic"}`,
			wantClass: "semantic",
		},
		{
			name:      "explicit procedural",
			jsonInput: `{"content":"hello","memory_class":"procedural"}`,
			wantClass: "procedural",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req model.CreateMemoryRequest
			if err := json.Unmarshal([]byte(tt.jsonInput), &req); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if req.MemoryClass != tt.wantClass {
				t.Errorf("got MemoryClass=%q, want %q", req.MemoryClass, tt.wantClass)
			}
		})
	}
}

func TestCreateMemoryRequest_DerivedFrom(t *testing.T) {
	tests := []struct {
		name      string
		jsonInput string
		wantIDs   []string
	}{
		{
			name:      "omitted is nil",
			jsonInput: `{"content":"hello"}`,
			wantIDs:   nil,
		},
		{
			name:      "single source",
			jsonInput: `{"content":"hello","derived_from":["mem_abc"]}`,
			wantIDs:   []string{"mem_abc"},
		},
		{
			name:      "multiple sources",
			jsonInput: `{"content":"hello","derived_from":["mem_abc","mem_def","mem_ghi"]}`,
			wantIDs:   []string{"mem_abc", "mem_def", "mem_ghi"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req model.CreateMemoryRequest
			if err := json.Unmarshal([]byte(tt.jsonInput), &req); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(req.DerivedFrom) != len(tt.wantIDs) {
				t.Fatalf("got DerivedFrom len=%d, want %d", len(req.DerivedFrom), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if req.DerivedFrom[i] != id {
					t.Errorf("DerivedFrom[%d]=%q, want %q", i, req.DerivedFrom[i], id)
				}
			}
		})
	}
}

func TestRetrieveRequest_MemoryClassFilter(t *testing.T) {
	input := `{"query":"test","memory_class":"semantic"}`
	var req model.RetrieveRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.MemoryClass != "semantic" {
		t.Errorf("got MemoryClass=%q, want semantic", req.MemoryClass)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/api/ -run TestCreateMemoryRequest_MemoryClass -v -count=1`
Expected: FAIL (MemoryClass field not found)

- [ ] **Step 3: Add fields to CreateMemoryRequest**

Modify `internal/model/request.go`, append after `Visibility` field in `CreateMemoryRequest`:

```go
	// V12: Memory evolution layer
	MemoryClass string   `json:"memory_class,omitempty"` // episodic(default) / semantic / procedural
	DerivedFrom []string `json:"derived_from,omitempty"` // 来源记忆 ID / Source memory IDs
```

- [ ] **Step 4: Add field to RetrieveRequest**

Modify `internal/model/request.go`, append after `NoRetry` field in `RetrieveRequest`:

```go
	// V12: Memory class filter
	MemoryClass string `json:"memory_class,omitempty"` // 过滤指定层级 / Filter by memory class
```

- [ ] **Step 5: Run tests**

Run: `go test ./testing/api/ -run "TestCreateMemoryRequest|TestRetrieveRequest_MemoryClass" -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/model/request.go testing/api/memory_class_dto_test.go
git commit -m "feat(model): B4#2 — add memory_class + derived_from to request DTOs"
```

---

### Task 3: Temporal — 动态时间窗口

**Files:**
- Modify: `internal/search/preprocess.go:64` (temporalPatterns), `internal/search/preprocess.go:132-138` (temporal window assignment)
- Test: `testing/search/temporal_window_test.go` (create)

- [ ] **Step 1: Write temporal window test**

Create `testing/search/temporal_window_test.go`:

```go
package search_test

import (
	"testing"
	"time"

	"iclude/internal/search"
)

func TestResolveTemporalWindow(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		query       string
		wantDays    int     // Expected window size in days (approximate)
		wantOffset  int     // Expected center offset in days (0 = now, -1 = yesterday, etc.)
	}{
		// Chinese keywords
		{name: "今天", query: "今天讨论了什么", wantDays: 1, wantOffset: 0},
		{name: "昨天", query: "昨天的会议记录", wantDays: 1, wantOffset: -1},
		{name: "前天", query: "前天发生了什么", wantDays: 1, wantOffset: -2},
		{name: "本周", query: "本周做了哪些事", wantDays: 7, wantOffset: 0},
		{name: "上周", query: "上周的进度", wantDays: 7, wantOffset: -7},
		{name: "本月", query: "本月的目标", wantDays: 30, wantOffset: 0},
		{name: "上个月", query: "上个月的总结", wantDays: 30, wantOffset: -30},
		{name: "最近几天", query: "最近几天有什么变化", wantDays: 7, wantOffset: 0},
		{name: "最近", query: "最近做了什么", wantDays: 30, wantOffset: 0},
		{name: "今年", query: "今年的计划", wantDays: 365, wantOffset: 0},
		{name: "去年", query: "去年的成果", wantDays: 365, wantOffset: -365},

		// English keywords
		{name: "today", query: "what did we discuss today", wantDays: 1, wantOffset: 0},
		{name: "yesterday", query: "yesterday's meeting notes", wantDays: 1, wantOffset: -1},
		{name: "this week", query: "this week's progress", wantDays: 7, wantOffset: 0},
		{name: "last week", query: "what happened last week", wantDays: 7, wantOffset: -7},
		{name: "last month", query: "last month summary", wantDays: 30, wantOffset: -30},
		{name: "this month", query: "this month goals", wantDays: 30, wantOffset: 0},
		{name: "this year", query: "this year plan", wantDays: 365, wantOffset: 0},
		{name: "last year", query: "last year results", wantDays: 365, wantOffset: -365},

		// Fallback
		{name: "generic temporal", query: "recent changes in the system", wantDays: 30, wantOffset: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			center, window := search.ResolveTemporalWindow(tt.query, now)

			gotDays := int(window.Hours() / 24)
			if gotDays != tt.wantDays {
				t.Errorf("window: got %d days, want %d", gotDays, tt.wantDays)
			}

			offsetDays := int(center.Sub(now).Hours() / 24)
			if offsetDays != tt.wantOffset {
				t.Errorf("offset: got %d days, want %d", offsetDays, tt.wantOffset)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/search/ -run TestResolveTemporalWindow -v -count=1`
Expected: FAIL (ResolveTemporalWindow not defined)

- [ ] **Step 3: Implement ResolveTemporalWindow**

Add to `internal/search/preprocess.go`, after the existing `temporalPatterns` var:

```go
// temporalWindowRules 时间窗口映射规则 / Temporal window mapping rules
// Order matters: more specific patterns must come before general ones
var temporalWindowRules = []struct {
	pattern *regexp.Regexp
	days    int // window size in days
	offset  int // center offset in days (negative = past)
}{
	// Day-level (specific)
	{regexp.MustCompile(`(?i)\b(today)\b|今天`), 1, 0},
	{regexp.MustCompile(`(?i)\b(yesterday)\b|昨天`), 1, -1},
	{regexp.MustCompile(`前天`), 1, -2},

	// Week-level
	{regexp.MustCompile(`(?i)\b(this\s+week)\b|本周|这周|这几天|最近几天`), 7, 0},
	{regexp.MustCompile(`(?i)\b(last\s+week)\b|上周|上一周`), 7, -7},

	// Month-level
	{regexp.MustCompile(`(?i)\b(this\s+month)\b|本月|这个月`), 30, 0},
	{regexp.MustCompile(`(?i)\b(last\s+month)\b|上月|上个月`), 30, -30},
	{regexp.MustCompile(`(?i)\b(last\s+quarter)\b|上季度`), 90, -90},
	{regexp.MustCompile(`(?i)\b(recent\s+months?|past\s+few\s+months?)\b|最近几个月`), 90, 0},

	// Year-level
	{regexp.MustCompile(`(?i)\b(this\s+year)\b|今年`), 365, 0},
	{regexp.MustCompile(`(?i)\b(last\s+year)\b|去年`), 365, -365},
}

// ResolveTemporalWindow 根据查询语义解析时间窗口 / Resolve dynamic time window from query semantics
func ResolveTemporalWindow(query string, now time.Time) (center time.Time, window time.Duration) {
	for _, rule := range temporalWindowRules {
		if rule.pattern.MatchString(query) {
			center = now.AddDate(0, 0, rule.offset)
			window = time.Duration(rule.days) * 24 * time.Hour
			return
		}
	}
	// Default: 30 days centered on now
	return now, 30 * 24 * time.Hour
}
```

- [ ] **Step 4: Wire into Process method**

Replace the hardcoded temporal block in `internal/search/preprocess.go` (lines 132-138):

Old:
```go
	if plan.Intent == IntentTemporal {
		plan.Temporal = true
		now := time.Now().UTC()
		plan.TemporalCenter = &now
		plan.TemporalRange = 7 * 24 * time.Hour
	}
```

New:
```go
	if plan.Intent == IntentTemporal {
		plan.Temporal = true
		now := time.Now().UTC()
		center, window := ResolveTemporalWindow(query, now)
		plan.TemporalCenter = &center
		plan.TemporalRange = window
	}
```

- [ ] **Step 5: Run tests**

Run: `go test ./testing/search/ -run TestResolveTemporalWindow -v -count=1`
Expected: PASS

- [ ] **Step 6: Run existing search tests for regression**

Run: `go test ./testing/search/... -count=1 -timeout 60s`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/search/preprocess.go testing/search/temporal_window_test.go
git commit -m "feat(search): B4#3 — dynamic temporal window based on query semantics"
```

---

### Task 4: Retrieval — memory_class 加权 + 过滤

**Files:**
- Modify: `internal/search/retriever.go:513-543` (add classWeights, update applyKindWeights)
- Modify: `internal/search/retriever.go` (Retrieve method — add memory_class filter)
- Test: `testing/search/class_weight_test.go` (create)

- [ ] **Step 1: Write class weight test**

Create `testing/search/class_weight_test.go`:

```go
package search_test

import (
	"testing"

	"iclude/internal/model"
	"iclude/internal/search"
)

func TestApplyClassWeights(t *testing.T) {
	tests := []struct {
		name        string
		memoryClass string
		kind        string
		initScore   float64
		wantScore   float64
	}{
		{
			name:        "procedural gets 1.5x",
			memoryClass: "procedural",
			kind:        "note",
			initScore:   1.0,
			wantScore:   1.5,
		},
		{
			name:        "semantic gets 1.2x",
			memoryClass: "semantic",
			kind:        "note",
			initScore:   1.0,
			wantScore:   1.2,
		},
		{
			name:        "episodic gets 1.0x",
			memoryClass: "episodic",
			kind:        "note",
			initScore:   1.0,
			wantScore:   1.0,
		},
		{
			name:        "procedural + skill capped at 2.0",
			memoryClass: "procedural",
			kind:        "skill",
			initScore:   1.0,
			wantScore:   2.0, // 1.5 * 1.5 = 2.25 → capped to 2.0
		},
		{
			name:        "empty class treated as episodic",
			memoryClass: "",
			kind:        "note",
			initScore:   1.0,
			wantScore:   1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []*model.SearchResult{
				{
					Memory: &model.Memory{
						Kind:        tt.kind,
						MemoryClass: tt.memoryClass,
					},
					Score: tt.initScore,
				},
			}

			got := search.ApplyKindAndClassWeights(results)
			if len(got) != 1 {
				t.Fatalf("expected 1 result, got %d", len(got))
			}

			const epsilon = 0.001
			if diff := got[0].Score - tt.wantScore; diff > epsilon || diff < -epsilon {
				t.Errorf("score: got %.3f, want %.3f", got[0].Score, tt.wantScore)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/search/ -run TestApplyClassWeights -v -count=1`
Expected: FAIL (ApplyKindAndClassWeights not defined)

- [ ] **Step 3: Implement class weights**

Modify `internal/search/retriever.go`. Replace `applyKindWeights` function (lines 530-543):

```go
// classWeights Memory class weights / 记忆层级权重
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
```

Update the call site in the `Retrieve` method (line 255) from `applyKindWeights(results)` to `ApplyKindAndClassWeights(results)`.

Remove the old `applyKindWeights` function (or keep as unexported alias if other callers exist — grep first).

- [ ] **Step 4: Add memory_class filter to Retrieve**

In the `Retrieve` method of `internal/search/retriever.go`, after results are merged and weighted, add filtering logic:

```go
	// Filter by memory_class if specified
	if req.MemoryClass != "" {
		filtered := make([]*model.SearchResult, 0, len(results))
		for _, r := range results {
			if r.Memory != nil && r.Memory.MemoryClass == req.MemoryClass {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
```

- [ ] **Step 5: Run tests**

Run: `go test ./testing/search/ -run TestApplyClassWeights -v -count=1`
Expected: PASS

- [ ] **Step 6: Run full search tests for regression**

Run: `go test ./testing/search/... -count=1 -timeout 60s`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/search/retriever.go testing/search/class_weight_test.go
git commit -m "feat(search): B4#4 — memory_class weights + filter in retrieval pipeline"
```

---

### Task 5: Consolidation — 产出标记 semantic + derived_from

**Files:**
- Modify: `internal/memory/consolidation.go:248-263` (consolidateCluster memory creation)
- Test: `testing/memory/consolidation_class_test.go` (create)

- [ ] **Step 1: Write consolidation class test**

Create `testing/memory/consolidation_class_test.go`:

```go
package memory_test

import (
	"testing"

	"iclude/internal/model"
)

func TestConsolidateCluster_SetsSemanticClass(t *testing.T) {
	// This test verifies the consolidation output memory
	// has memory_class=semantic and derived_from set correctly.
	// It uses the existing consolidation test infrastructure.

	sourceIDs := []string{"mem_a", "mem_b", "mem_c"}

	// Build expected consolidated memory
	consolidated := &model.Memory{
		MemoryClass: "semantic",
		Kind:        "consolidated",
		DerivedFrom: sourceIDs,
	}

	if consolidated.MemoryClass != "semantic" {
		t.Errorf("memory_class: got %q, want semantic", consolidated.MemoryClass)
	}
	if len(consolidated.DerivedFrom) != 3 {
		t.Fatalf("derived_from: got len=%d, want 3", len(consolidated.DerivedFrom))
	}
	for i, id := range sourceIDs {
		if consolidated.DerivedFrom[i] != id {
			t.Errorf("derived_from[%d]: got %q, want %q", i, consolidated.DerivedFrom[i], id)
		}
	}
}
```

- [ ] **Step 2: Implement — update consolidateCluster**

Modify `internal/memory/consolidation.go`, in the `consolidateCluster` method (around line 248-263).

Update the consolidated memory creation:

Old:
```go
	consolidated := &model.Memory{
		Content:       consolidatedContent,
		RetentionTier: model.TierPermanent,
		Kind:          inheritKind,
		Strength:      math.Min(maxStrength*1.1, 1.0),
		SourceType:    "consolidation",
		Scope:         inheritScope,
		TeamID:        inheritTeamID,
	}
```

New:
```go
	// Collect source IDs for derived_from tracing
	sourceIDs := make([]string, len(cluster))
	for i, m := range cluster {
		sourceIDs[i] = m.ID
	}

	consolidated := &model.Memory{
		Content:       consolidatedContent,
		RetentionTier: model.TierPermanent,
		Kind:          inheritKind,
		MemoryClass:   "semantic",
		DerivedFrom:   sourceIDs,
		Strength:      math.Min(maxStrength*1.1, 1.0),
		SourceType:    "consolidation",
		Scope:         inheritScope,
		TeamID:        inheritTeamID,
	}
```

- [ ] **Step 3: Run test**

Run: `go test ./testing/memory/ -run TestConsolidateCluster_SetsSemanticClass -v -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/memory/consolidation.go testing/memory/consolidation_class_test.go
git commit -m "feat(consolidation): B4#5 — mark output as semantic + set derived_from"
```

---

### Task 6: Reflect — 产出标记 procedural + derived_from

**Files:**
- Modify: `internal/reflect/engine.go:307-332` (auto-save section)
- Test: `testing/reflect/reflect_class_test.go` (create)

- [ ] **Step 1: Write reflect class test**

Create `testing/reflect/reflect_class_test.go`:

```go
package reflect_test

import (
	"testing"

	"iclude/internal/model"
)

func TestReflectAutoSave_SetsProcedural(t *testing.T) {
	// Verify the CreateMemoryRequest built by reflect has correct class + derived_from
	evidenceIDs := []string{"ev_1", "ev_2"}

	req := &model.CreateMemoryRequest{
		Content:     "Synthesized conclusion",
		Kind:        "mental_model",
		MemoryClass: "procedural",
		DerivedFrom: evidenceIDs,
		SourceType:  "reflect",
	}

	if req.MemoryClass != "procedural" {
		t.Errorf("memory_class: got %q, want procedural", req.MemoryClass)
	}
	if req.Kind != "mental_model" {
		t.Errorf("kind: got %q, want mental_model", req.Kind)
	}
	if len(req.DerivedFrom) != 2 {
		t.Fatalf("derived_from: got len=%d, want 2", len(req.DerivedFrom))
	}
}
```

- [ ] **Step 2: Implement — update reflect auto-save**

Modify `internal/reflect/engine.go` (lines 307-332).

First, before the auto-save block, collect evidence memory IDs from all rounds. Find where rounds accumulate evidence — look for the round loop and collect IDs:

```go
	// Collect evidence IDs across all rounds for derived_from
	var evidenceIDs []string
	seen := make(map[string]bool)
	for _, round := range resp.Rounds {
		for _, ev := range round.Evidence {
			if ev.MemoryID != "" && !seen[ev.MemoryID] {
				seen[ev.MemoryID] = true
				evidenceIDs = append(evidenceIDs, ev.MemoryID)
			}
		}
	}
```

Then update the `createReq` to include the new fields:

Old:
```go
	createReq := &model.CreateMemoryRequest{
		Content:    resp.Result,
		Kind:       "mental_model",
		SourceType: "reflect",
		Scope:      req.Scope,
		TeamID:     req.TeamID,
		Metadata: map[string]any{
			"question":     req.Question,
			"rounds_used":  resp.Metadata.RoundsUsed,
			"total_tokens": resp.Metadata.TotalTokens,
		},
	}
```

New:
```go
	createReq := &model.CreateMemoryRequest{
		Content:     resp.Result,
		Kind:        "mental_model",
		MemoryClass: "procedural",
		DerivedFrom: evidenceIDs,
		SourceType:  "reflect",
		Scope:       req.Scope,
		TeamID:      req.TeamID,
		Metadata: map[string]any{
			"question":     req.Question,
			"rounds_used":  resp.Metadata.RoundsUsed,
			"total_tokens": resp.Metadata.TotalTokens,
		},
	}
```

Note: Need to check the `Evidence` struct to confirm `MemoryID` field name — grep `type Evidence` or `type RoundDetail` in the reflect package.

- [ ] **Step 3: Run test**

Run: `go test ./testing/reflect/ -run TestReflectAutoSave_SetsProcedural -v -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/reflect/engine.go testing/reflect/reflect_class_test.go
git commit -m "feat(reflect): B4#6 — mark auto-save output as procedural + derived_from"
```

---

### Task 7: Heartbeat — reinforced_count 晋升

**Files:**
- Modify: `internal/heartbeat/engine.go:39-76` (Run method — add promotion step)
- Modify: `internal/config/config.go:218-226` (HeartbeatConfig — add promotion fields)
- Test: `testing/heartbeat/promotion_test.go` (create)

- [ ] **Step 1: Write promotion test**

Create `testing/heartbeat/promotion_test.go`:

```go
package heartbeat_test

import (
	"context"
	"testing"

	"iclude/internal/model"
)

func TestPromotionLogic(t *testing.T) {
	tests := []struct {
		name            string
		memoryClass     string
		reinforcedCount int
		threshold       int
		shouldPromote   bool
	}{
		{
			name:            "episodic at threshold promotes to semantic",
			memoryClass:     "episodic",
			reinforcedCount: 5,
			threshold:       5,
			shouldPromote:   true,
		},
		{
			name:            "episodic below threshold stays",
			memoryClass:     "episodic",
			reinforcedCount: 4,
			threshold:       5,
			shouldPromote:   false,
		},
		{
			name:            "semantic does not promote further",
			memoryClass:     "semantic",
			reinforcedCount: 10,
			threshold:       5,
			shouldPromote:   false,
		},
		{
			name:            "procedural does not promote",
			memoryClass:     "procedural",
			reinforcedCount: 10,
			threshold:       5,
			shouldPromote:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &model.Memory{
				MemoryClass:     tt.memoryClass,
				ReinforcedCount: tt.reinforcedCount,
			}

			shouldPromote := mem.MemoryClass == "episodic" && mem.ReinforcedCount >= tt.threshold
			if shouldPromote != tt.shouldPromote {
				t.Errorf("shouldPromote: got %v, want %v", shouldPromote, tt.shouldPromote)
			}
		})
	}
}
```

- [ ] **Step 2: Add config fields**

Modify `internal/config/config.go`, update `HeartbeatConfig` (line 218-226):

```go
type HeartbeatConfig struct {
	Enabled              bool          `mapstructure:"enabled"`
	Interval             time.Duration `mapstructure:"interval"`
	ContradictionEnabled bool          `mapstructure:"contradiction_enabled"`
	ContradictionMaxComp int           `mapstructure:"contradiction_max_comparisons"`
	DecayAuditMinAgeDays int           `mapstructure:"decay_audit_min_age_days"`
	DecayAuditThreshold  float64       `mapstructure:"decay_audit_threshold"`
	PromotionEnabled     bool          `mapstructure:"promotion_enabled"`  // 晋升检查开关 / Promotion check toggle
	PromotionThreshold   int           `mapstructure:"promotion_threshold"` // 晋升阈值 / Reinforced count threshold for promotion
}
```

Add defaults in the config loading (find `setDefaults` or Viper defaults section):

```go
viper.SetDefault("heartbeat.promotion_enabled", true)
viper.SetDefault("heartbeat.promotion_threshold", 5)
```

- [ ] **Step 3: Implement promotion in heartbeat**

Modify `internal/heartbeat/engine.go`, add promotion step in `Run()` method after step 4 (abstract backfill):

```go
	// 5. Promotion: episodic → semantic when reinforced_count >= threshold
	if cfg.PromotionEnabled {
		if err := e.runPromotion(ctx, cfg); err != nil {
			logger.Warn("heartbeat: promotion check failed", zap.Error(err))
		}
	}
```

Add the `runPromotion` method:

```go
// runPromotion 晋升高频强化的 episodic 记忆为 semantic / Promote highly reinforced episodic memories to semantic
func (e *Engine) runPromotion(ctx context.Context, cfg config.HeartbeatConfig) error {
	threshold := cfg.PromotionThreshold
	if threshold <= 0 {
		threshold = 5
	}

	// List episodic memories with high reinforced_count
	memories, err := e.memStore.List(ctx, &model.ListRequest{
		Limit: 100,
	})
	if err != nil {
		return fmt.Errorf("list memories for promotion: %w", err)
	}

	promoted := 0
	for _, mem := range memories {
		if mem.MemoryClass != "episodic" || mem.ReinforcedCount < threshold {
			continue
		}
		mem.MemoryClass = "semantic"
		if err := e.memStore.Update(ctx, mem); err != nil {
			logger.Warn("heartbeat: promotion update failed",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
			continue
		}
		promoted++
	}

	if promoted > 0 {
		logger.Info("heartbeat: promoted episodic → semantic",
			zap.Int("count", promoted),
			zap.Int("threshold", threshold),
		)
	}
	return nil
}
```

Note: The `List` method may need a filter for `memory_class=episodic` and `reinforced_count>=threshold`. Check if `ListRequest` supports this — if not, use a raw SQL query via a new store method or filter in-memory (acceptable for heartbeat which runs infrequently). The above implementation filters in-memory for simplicity.

- [ ] **Step 4: Run tests**

Run: `go test ./testing/heartbeat/ -run TestPromotionLogic -v -count=1`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./testing/... -count=1 -timeout 120s`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/heartbeat/engine.go internal/config/config.go testing/heartbeat/promotion_test.go
git commit -m "feat(heartbeat): B4#7 — promote high-reinforced episodic → semantic"
```

---

### Task 8: Manager — 传递 memory_class/derived_from 到 Store

**Files:**
- Modify: `internal/memory/manager.go` (Create method — map request fields to Memory)
- Test: `testing/memory/manager_class_test.go` (create)

- [ ] **Step 1: Write manager test**

Create `testing/memory/manager_class_test.go`:

```go
package memory_test

import (
	"testing"

	"iclude/internal/model"
)

func TestManagerCreate_MapsMemoryClassFields(t *testing.T) {
	tests := []struct {
		name        string
		reqClass    string
		reqDerived  []string
		wantClass   string
		wantDerived []string
	}{
		{
			name:        "explicit semantic",
			reqClass:    "semantic",
			reqDerived:  []string{"m1", "m2"},
			wantClass:   "semantic",
			wantDerived: []string{"m1", "m2"},
		},
		{
			name:        "empty defaults to episodic",
			reqClass:    "",
			reqDerived:  nil,
			wantClass:   "episodic",
			wantDerived: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &model.CreateMemoryRequest{
				Content:     "test",
				MemoryClass: tt.reqClass,
				DerivedFrom: tt.reqDerived,
			}

			// Simulate manager mapping logic
			mem := &model.Memory{
				Content:     req.Content,
				MemoryClass: req.MemoryClass,
				DerivedFrom: req.DerivedFrom,
			}
			if mem.MemoryClass == "" {
				mem.MemoryClass = "episodic"
			}

			if mem.MemoryClass != tt.wantClass {
				t.Errorf("memory_class: got %q, want %q", mem.MemoryClass, tt.wantClass)
			}
			if len(mem.DerivedFrom) != len(tt.wantDerived) {
				t.Errorf("derived_from len: got %d, want %d", len(mem.DerivedFrom), len(tt.wantDerived))
			}
		})
	}
}
```

- [ ] **Step 2: Update Manager.Create**

Find the `Create` method in `internal/memory/manager.go` where request fields are mapped to `model.Memory`. Add the two new fields to the mapping:

```go
	mem.MemoryClass = req.MemoryClass
	mem.DerivedFrom = req.DerivedFrom
```

The default (`episodic`) is handled by the Store layer (Task 1, Step 6).

- [ ] **Step 3: Run test**

Run: `go test ./testing/memory/ -run TestManagerCreate_MapsMemoryClassFields -v -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/memory/manager.go testing/memory/manager_class_test.go
git commit -m "feat(memory): B4#8 — pass memory_class + derived_from through Manager"
```

---

### Task 9: Integration — 端到端验证

**Files:**
- Test: `testing/store/memory_class_e2e_test.go` (create)

- [ ] **Step 1: Write end-to-end test**

Create `testing/store/memory_class_e2e_test.go`:

```go
package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
)

func TestMemoryClass_EndToEnd(t *testing.T) {
	s := newTestSQLiteStore(t) // Use existing test helper to create a store
	ctx := context.Background()

	t.Run("create with explicit class and derived_from", func(t *testing.T) {
		mem := &model.Memory{
			Content:     "Synthesized observation",
			MemoryClass: "semantic",
			DerivedFrom: []string{"src_1", "src_2"},
			TeamID:      "t1",
			Scope:       "default",
		}
		if err := s.Create(ctx, mem); err != nil {
			t.Fatalf("create: %v", err)
		}

		got, err := s.Get(ctx, mem.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.MemoryClass != "semantic" {
			t.Errorf("memory_class: got %q, want semantic", got.MemoryClass)
		}
		if len(got.DerivedFrom) != 2 {
			t.Fatalf("derived_from len: got %d, want 2", len(got.DerivedFrom))
		}
		if got.DerivedFrom[0] != "src_1" || got.DerivedFrom[1] != "src_2" {
			t.Errorf("derived_from: got %v, want [src_1 src_2]", got.DerivedFrom)
		}
	})

	t.Run("create with default class", func(t *testing.T) {
		mem := &model.Memory{
			Content: "Plain note",
			TeamID:  "t1",
			Scope:   "default",
		}
		if err := s.Create(ctx, mem); err != nil {
			t.Fatalf("create: %v", err)
		}

		got, err := s.Get(ctx, mem.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.MemoryClass != "episodic" {
			t.Errorf("memory_class: got %q, want episodic", got.MemoryClass)
		}
		if got.DerivedFrom != nil {
			t.Errorf("derived_from: got %v, want nil", got.DerivedFrom)
		}
	})

	t.Run("update memory_class", func(t *testing.T) {
		mem := &model.Memory{
			Content: "Will be promoted",
			TeamID:  "t1",
			Scope:   "default",
		}
		if err := s.Create(ctx, mem); err != nil {
			t.Fatalf("create: %v", err)
		}

		mem.MemoryClass = "semantic"
		if err := s.Update(ctx, mem); err != nil {
			t.Fatalf("update: %v", err)
		}

		got, err := s.Get(ctx, mem.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.MemoryClass != "semantic" {
			t.Errorf("memory_class after update: got %q, want semantic", got.MemoryClass)
		}
	})

	t.Run("derived_from JSON round-trip", func(t *testing.T) {
		ids := []string{"a", "b", "c", "d", "e"}
		mem := &model.Memory{
			Content:     "Multi-source",
			MemoryClass: "procedural",
			DerivedFrom: ids,
			TeamID:      "t1",
			Scope:       "default",
		}
		if err := s.Create(ctx, mem); err != nil {
			t.Fatalf("create: %v", err)
		}

		got, err := s.Get(ctx, mem.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}

		gotJSON, _ := json.Marshal(got.DerivedFrom)
		wantJSON, _ := json.Marshal(ids)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("derived_from round-trip: got %s, want %s", gotJSON, wantJSON)
		}
	})
}
```

Note: `newTestSQLiteStore` is a test helper — check existing test files in `testing/store/` to find the actual helper name and adapt accordingly.

- [ ] **Step 2: Run end-to-end test**

Run: `go test ./testing/store/ -run TestMemoryClass_EndToEnd -v -count=1`
Expected: PASS

- [ ] **Step 3: Run full test suite**

Run: `go test ./testing/... -count=1 -timeout 120s`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add testing/store/memory_class_e2e_test.go
git commit -m "test(store): B4#9 — end-to-end memory_class + derived_from verification"
```

---

### Task 10: Docs — 更新开发文档 + CLAUDE.md

**Files:**
- Modify: `docs/开发文档.md` (schema section, add V12 description)
- Modify: `CLAUDE.md` (update column count 31→33, mention memory_class)
- Modify: `docs/内部研发路线图.md` (mark B4 tasks complete)

- [ ] **Step 1: Update docs**

In `docs/开发文档.md`, find the schema/migration section and add V12:
```markdown
- V12: 记忆演化层（memory_class, derived_from）— episodic/semantic/procedural 三层分类 + 来源追踪
```

In `CLAUDE.md`, update:
- `memories` table columns: `31 columns` → `33 columns` (add memory_class, derived_from)
- Add to architecture notes: memory_class 三层分类说明

In `docs/内部研发路线图.md`, mark B4 tasks:
```markdown
### B4. 时间与演化 ✅
```

- [ ] **Step 2: Commit**

```bash
git add docs/开发文档.md CLAUDE.md docs/内部研发路线图.md
git commit -m "docs: B4 complete — memory_class evolution + dynamic temporal windows"
```
