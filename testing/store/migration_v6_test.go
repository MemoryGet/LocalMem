package store_test

import (
	"context"
	"database/sql"
	"testing"

	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	_ "modernc.org/sqlite"
)

// TestMigrateV5ToV6_AddsOwnershipFields 验证 V6 迁移新增 owner_id 和 visibility 列并回填旧数据
// Verify V6 migration adds owner_id and visibility columns and backfills existing data
func TestMigrateV5ToV6_AddsOwnershipFields(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tok := tokenizer.NewNoopTokenizer()

	// 先运行全部迁移（包含 V6）来建表
	if err := store.Migrate(db, tok); err != nil {
		t.Fatal(err)
	}

	// 验证新列存在：插入带 owner_id 的记录
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO memories (id, content, team_id, owner_id, visibility, created_at, updated_at)
		VALUES ('test-new', 'new memory', 'team-a', 'alice', 'private', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatal("new columns should accept values: " + err.Error())
	}

	// 验证默认值：不指定 owner_id/visibility 的新记录
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO memories (id, content, team_id, created_at, updated_at)
		VALUES ('test-default', 'default memory', 'team-b', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatal(err)
	}

	var ownerID, visibility string
	err = db.QueryRow(`SELECT owner_id, visibility FROM memories WHERE id = 'test-default'`).
		Scan(&ownerID, &visibility)
	if err != nil {
		t.Fatal(err)
	}
	if ownerID != "" {
		t.Errorf("default owner_id = %q, want ''", ownerID)
	}
	if visibility != "private" {
		t.Errorf("default visibility = %q, want 'private'", visibility)
	}

	// 验证版本号
	var version int
	db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	if version != 9 {
		t.Errorf("schema version = %d, want 9", version)
	}

	// 验证索引存在
	var idxCount int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_memories_owner_id'`).Scan(&idxCount)
	if idxCount != 1 {
		t.Error("idx_memories_owner_id index should exist")
	}
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_memories_visibility'`).Scan(&idxCount)
	if idxCount != 1 {
		t.Error("idx_memories_visibility index should exist")
	}
}

// TestMigrateV5ToV6_BackfillsOldData 验证 V6 迁移回填旧数据
// Verify V6 migration backfills old data (team_id='' → 'default', visibility → 'team')
func TestMigrateV5ToV6_BackfillsOldData(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 手动建 V5 schema（简化版，只需 memories 表 + schema_version）
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO schema_version (version) VALUES (5)`,
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
			consolidated_into TEXT DEFAULT ''
		)`,
		// 插入模拟旧数据：team_id 为空
		`INSERT INTO memories (id, content, team_id, created_at, updated_at)
			VALUES ('old-1', 'legacy memory', '', datetime('now'), datetime('now'))`,
		`INSERT INTO memories (id, content, team_id, created_at, updated_at)
			VALUES ('old-2', 'another legacy', 'team-x', datetime('now'), datetime('now'))`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup failed: %v\nstmt: %s", err, stmt)
		}
	}

	// 运行 V6 迁移
	tok := tokenizer.NewNoopTokenizer()
	if err := store.Migrate(db, tok); err != nil {
		t.Fatal(err)
	}

	// 验证 old-1: team_id 回填为 default, visibility 回填为 team
	var teamID, visibility string
	err = db.QueryRow(`SELECT team_id, visibility FROM memories WHERE id = 'old-1'`).Scan(&teamID, &visibility)
	if err != nil {
		t.Fatal(err)
	}
	if teamID != "default" {
		t.Errorf("old-1 team_id = %q, want 'default'", teamID)
	}
	if visibility != "team" {
		t.Errorf("old-1 visibility = %q, want 'team'", visibility)
	}

	// 验证 old-2: team_id 保持 team-x（非空不回填）, visibility 回填为 team
	err = db.QueryRow(`SELECT team_id, visibility FROM memories WHERE id = 'old-2'`).Scan(&teamID, &visibility)
	if err != nil {
		t.Fatal(err)
	}
	if teamID != "team-x" {
		t.Errorf("old-2 team_id = %q, want 'team-x'", teamID)
	}
	if visibility != "team" {
		t.Errorf("old-2 visibility = %q, want 'team'", visibility)
	}
}
