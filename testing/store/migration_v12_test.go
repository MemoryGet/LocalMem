package store_test

import (
	"context"
	"database/sql"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	_ "modernc.org/sqlite"
)

// TestMigrateV11ToV12_AddsColumns 验证 V12 迁移新增 memory_class 和 derived_from 列
// Verify V12 migration adds memory_class and derived_from columns
func TestMigrateV11ToV12_AddsColumns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tok := tokenizer.NewNoopTokenizer()
	if err := store.Migrate(db, tok); err != nil {
		t.Fatal(err)
	}

	// 验证新列存在：插入带 memory_class 的记录 / Verify new columns exist
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO memories (id, content, team_id, memory_class, derived_from, created_at, updated_at)
		VALUES ('test-v12', 'v12 memory', 'team-a', 'semantic', '["id-1","id-2"]', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatal("new columns should accept values: " + err.Error())
	}

	var memClass string
	var derivedFrom sql.NullString
	err = db.QueryRow(`SELECT memory_class, derived_from FROM memories WHERE id = 'test-v12'`).
		Scan(&memClass, &derivedFrom)
	if err != nil {
		t.Fatal(err)
	}
	if memClass != "semantic" {
		t.Errorf("memory_class = %q, want 'semantic'", memClass)
	}
	if !derivedFrom.Valid || derivedFrom.String != `["id-1","id-2"]` {
		t.Errorf("derived_from = %v, want '[\"id-1\",\"id-2\"]'", derivedFrom)
	}

	// 验证默认值：不指定 memory_class 的新记录 / Verify default value
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO memories (id, content, team_id, created_at, updated_at)
		VALUES ('test-default-v12', 'default memory', 'team-b', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatal(err)
	}

	err = db.QueryRow(`SELECT memory_class, derived_from FROM memories WHERE id = 'test-default-v12'`).
		Scan(&memClass, &derivedFrom)
	if err != nil {
		t.Fatal(err)
	}
	if memClass != "episodic" {
		t.Errorf("default memory_class = %q, want 'episodic'", memClass)
	}
	if derivedFrom.Valid {
		t.Errorf("default derived_from should be NULL, got %q", derivedFrom.String)
	}

	// 验证版本号 / Verify schema version
	var version int
	db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	if version != 14 {
		t.Errorf("schema version = %d, want 14", version)
	}

	// 验证索引存在 / Verify index exists
	var idxCount int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_memories_memory_class'`).Scan(&idxCount)
	if idxCount != 1 {
		t.Error("idx_memories_memory_class index should exist")
	}
}

// TestMigrateV11ToV12_BackfillsKind 验证 V12 迁移根据 kind 回填 memory_class
// Verify V12 migration backfills memory_class based on kind
func TestMigrateV11ToV12_BackfillsKind(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 手动建 V11 schema（简化版）/ Build V11 schema manually
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO schema_version (version) VALUES (11)`,
		`CREATE TABLE memories (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			metadata TEXT,
			team_id TEXT DEFAULT '',
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
			scope TEXT DEFAULT '',
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
			retention_tier TEXT DEFAULT '',
			message_role TEXT DEFAULT '',
			turn_number INTEGER DEFAULT 0,
			content_hash TEXT DEFAULT '',
			consolidated_into TEXT DEFAULT '',
			owner_id TEXT DEFAULT '',
			visibility TEXT DEFAULT 'private'
		)`,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
		// 插入不同 kind 的旧数据 / Insert old data with different kinds
		`INSERT INTO memories (id, content, kind, created_at, updated_at)
			VALUES ('m-mental', 'mental model memory', 'mental_model', datetime('now'), datetime('now'))`,
		`INSERT INTO memories (id, content, kind, created_at, updated_at)
			VALUES ('m-consolidated', 'consolidated memory', 'consolidated', datetime('now'), datetime('now'))`,
		`INSERT INTO memories (id, content, kind, created_at, updated_at)
			VALUES ('m-note', 'regular note', 'note', datetime('now'), datetime('now'))`,
		`INSERT INTO memories (id, content, kind, created_at, updated_at)
			VALUES ('m-empty', 'no kind', '', datetime('now'), datetime('now'))`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup failed: %v\nstmt: %s", err, stmt)
		}
	}

	tok := tokenizer.NewNoopTokenizer()
	if err := store.Migrate(db, tok); err != nil {
		t.Fatal(err)
	}

	// 表驱动验证 / Table-driven verification
	tests := []struct {
		id          string
		wantClass   string
		description string
	}{
		{"m-mental", "procedural", "mental_model kind should become procedural"},
		{"m-consolidated", "semantic", "consolidated kind should become semantic"},
		{"m-note", "episodic", "note kind should remain episodic"},
		{"m-empty", "episodic", "empty kind should remain episodic"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			var memClass string
			err := db.QueryRow(`SELECT memory_class FROM memories WHERE id = ?`, tt.id).Scan(&memClass)
			if err != nil {
				t.Fatalf("failed to query memory %s: %v", tt.id, err)
			}
			if memClass != tt.wantClass {
				t.Errorf("memory %s: memory_class = %q, want %q", tt.id, memClass, tt.wantClass)
			}
		})
	}
}

// TestMigrateV11ToV12_Idempotent 验证 V12 迁移可安全重跑
// Verify V12 migration is idempotent (safe to run twice)
func TestMigrateV11ToV12_Idempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tok := tokenizer.NewNoopTokenizer()

	// 第一次运行 / First run
	if err := store.Migrate(db, tok); err != nil {
		t.Fatal("first migration failed: " + err.Error())
	}

	// 重置版本号强制重跑 / Reset version to force re-run
	if _, err := db.Exec(`DELETE FROM schema_version WHERE version = 12`); err != nil {
		t.Fatal(err)
	}

	// 第二次运行（应该幂等成功）/ Second run (should succeed idempotently)
	if err := store.Migrate(db, tok); err != nil {
		t.Fatal("idempotent migration failed: " + err.Error())
	}

	var version int
	db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	if version != 14 {
		t.Errorf("schema version = %d, want 14", version)
	}
}

// TestMigrateV12_CreateWithMemoryClass 验证 Create 方法正确写入 memory_class 和 derived_from
// Verify Create method correctly writes memory_class and derived_from
func TestMigrateV12_CreateWithMemoryClass(t *testing.T) {
	s, err := store.NewSQLiteMemoryStore(":memory:", [3]float64{10, 5, 3}, tokenizer.NewNoopTokenizer())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		mem         model.Memory
		wantClass   string
		wantDerived []string
	}{
		{
			name:        "default episodic",
			mem:         model.Memory{Content: "test default", TeamID: "t1"},
			wantClass:   "episodic",
			wantDerived: nil,
		},
		{
			name:        "explicit semantic with derived_from",
			mem:         model.Memory{Content: "test semantic", TeamID: "t1", MemoryClass: "semantic", DerivedFrom: []string{"src-1", "src-2"}},
			wantClass:   "semantic",
			wantDerived: []string{"src-1", "src-2"},
		},
		{
			name:        "explicit procedural no derived",
			mem:         model.Memory{Content: "test procedural", TeamID: "t1", MemoryClass: "procedural"},
			wantClass:   "procedural",
			wantDerived: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := tt.mem
			if err := s.Create(ctx, &mem); err != nil {
				t.Fatal(err)
			}

			got, err := s.Get(ctx, mem.ID)
			if err != nil {
				t.Fatal(err)
			}

			if got.MemoryClass != tt.wantClass {
				t.Errorf("MemoryClass = %q, want %q", got.MemoryClass, tt.wantClass)
			}

			if len(got.DerivedFrom) != len(tt.wantDerived) {
				t.Errorf("DerivedFrom len = %d, want %d", len(got.DerivedFrom), len(tt.wantDerived))
			} else {
				for i, v := range got.DerivedFrom {
					if v != tt.wantDerived[i] {
						t.Errorf("DerivedFrom[%d] = %q, want %q", i, v, tt.wantDerived[i])
					}
				}
			}
		})
	}
}
