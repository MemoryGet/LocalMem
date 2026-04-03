package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrate_FreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer s.Close()

	err = s.Init(context.Background())
	require.NoError(t, err)

	// 验证 schema_version 为 16（V15→V16 derived_from → junction table）
	db := s.DB().(*sql.DB)
	var version int
	err = db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, 16, version)

	// 验证新表存在
	tables := []string{"memories", "contexts", "tags", "memory_tags", "entities", "entity_relations", "memory_entities", "documents", "async_tasks"}
	for _, tbl := range tables {
		var count int
		err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "table %s should exist", tbl)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer s.Close()

	// 第一次迁移
	err = s.Init(context.Background())
	require.NoError(t, err)

	// 第二次迁移应该是 no-op
	err = s.Init(context.Background())
	require.NoError(t, err)
}

// TestFreshSchema_MatchesIncremental 验证新库 fresh schema 与增量迁移的结果一致
// Verify fresh schema produces identical tables, columns, and indexes as incremental migration
func TestFreshSchema_MatchesIncremental(t *testing.T) {
	// --- 1. 创建 fresh schema 库 / Create fresh schema DB ---
	freshDir := t.TempDir()
	freshPath := filepath.Join(freshDir, "fresh.db")
	freshStore, err := store.NewSQLiteMemoryStore(freshPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer freshStore.Close()
	err = freshStore.Init(context.Background())
	require.NoError(t, err)
	freshDB := freshStore.DB().(*sql.DB)

	// 验证 schema_version = 16
	var freshVersion int
	err = freshDB.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&freshVersion)
	require.NoError(t, err)
	assert.Equal(t, 16, freshVersion)

	// 验证所有表存在 / Verify all tables exist
	expectedTables := []string{
		"memories", "memories_fts", "contexts", "tags", "memory_tags",
		"entities", "entity_relations", "memory_entities", "documents",
		"async_tasks", "memory_derivations", "meta", "schema_version",
	}
	for _, tbl := range expectedTables {
		var cnt int
		err = freshDB.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE (type='table' OR type='view') AND name=?", tbl,
		).Scan(&cnt)
		require.NoError(t, err)
		assert.Equal(t, 1, cnt, "fresh schema missing table: %s", tbl)
	}

	// 验证 memories 表有 35 列 / Verify memories table has 35 columns
	var colCount int
	err = freshDB.QueryRow("SELECT count(*) FROM pragma_table_info('memories')").Scan(&colCount)
	require.NoError(t, err)
	assert.Equal(t, 35, colCount, "memories table should have 35 columns")

	// 验证关键列存在 / Verify key columns exist
	keyColumns := []struct {
		table  string
		column string
	}{
		{"memories", "excerpt"},
		{"memories", "memory_class"},
		{"memories", "content_hash"},
		{"memories", "owner_id"},
		{"memories", "visibility"},
		{"memories", "consolidated_into"},
		{"contexts", "context_type"},
		{"contexts", "mission"},
		{"contexts", "directives"},
		{"contexts", "disposition"},
		{"documents", "error_msg"},
		{"documents", "stage"},
		{"documents", "parser"},
	}
	for _, kc := range keyColumns {
		var cnt int
		err = freshDB.QueryRow(
			"SELECT count(*) FROM pragma_table_info(?) WHERE name=?", kc.table, kc.column,
		).Scan(&cnt)
		require.NoError(t, err)
		assert.Equal(t, 1, cnt, "column %s.%s should exist", kc.table, kc.column)
	}

	// 验证 memories 不含已删除的列 / Verify dropped columns are absent
	droppedColumns := []string{"embedding_id", "abstract", "derived_from"}
	for _, col := range droppedColumns {
		var cnt int
		err = freshDB.QueryRow(
			"SELECT count(*) FROM pragma_table_info('memories') WHERE name=?", col,
		).Scan(&cnt)
		require.NoError(t, err)
		assert.Equal(t, 0, cnt, "dropped column %s should NOT exist", col)
	}

	// 验证索引数量 / Verify index count matches expectations
	var indexCount int
	err = freshDB.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_%'",
	).Scan(&indexCount)
	require.NoError(t, err)
	// 预期索引：memories(22) + contexts(2) + entities(1) + entity_relations(2)
	//          + memory_entities(2) + memory_tags(1) + documents(4) + async_tasks(2) + memory_derivations(1) = 37
	assert.Equal(t, 37, indexCount, "fresh schema should have 37 named indexes")

	// 验证 meta 表 tokenizer 记录 / Verify meta table has tokenizer record
	var tokName string
	err = freshDB.QueryRow("SELECT value FROM meta WHERE key='tokenizer'").Scan(&tokName)
	require.NoError(t, err)
	assert.Equal(t, "noop", tokName) // nil tokenizer defaults to noop

	// 验证 FK CASCADE 在 memory_tags / Verify FK CASCADE on memory_tags
	var memTagsDDL string
	err = freshDB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='memory_tags'").Scan(&memTagsDDL)
	require.NoError(t, err)
	assert.Contains(t, memTagsDDL, "REFERENCES")
	assert.Contains(t, memTagsDDL, "ON DELETE CASCADE")

	// 验证 CHECK 约束在 entity_relations / Verify CHECK constraint on entity_relations
	var erDDL string
	err = freshDB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='entity_relations'").Scan(&erDDL)
	require.NoError(t, err)
	assert.Contains(t, erDDL, "CHECK")
}

func TestMigrate_V2ToV3(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// 手动构造 V2 数据库：V1 表结构 + V2 ALTER 列，但不含 V3 列
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = db.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)

	// V1 基础表
	_, err = db.Exec(`CREATE TABLE memories (
		id TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		metadata TEXT,
		team_id TEXT DEFAULT '',
		embedding_id TEXT DEFAULT '',
		parent_id TEXT DEFAULT '',
		is_latest INTEGER DEFAULT 1,
		access_count INTEGER DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
		content, abstract, summary,
		content=memories, content_rowid=rowid
	)`)
	require.NoError(t, err)

	// V2 ALTER 列（不含 retention_tier / message_role / turn_number）
	v2Columns := []string{
		`ALTER TABLE memories ADD COLUMN uri TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN context_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN kind TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN sub_kind TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN scope TEXT DEFAULT 'default'`,
		`ALTER TABLE memories ADD COLUMN abstract TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN summary TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN happened_at DATETIME`,
		`ALTER TABLE memories ADD COLUMN source_type TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN source_ref TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN document_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN chunk_index INTEGER DEFAULT 0`,
		`ALTER TABLE memories ADD COLUMN deleted_at DATETIME`,
		`ALTER TABLE memories ADD COLUMN strength REAL DEFAULT 1.0`,
		`ALTER TABLE memories ADD COLUMN decay_rate REAL DEFAULT 0.01`,
		`ALTER TABLE memories ADD COLUMN last_accessed_at DATETIME`,
		`ALTER TABLE memories ADD COLUMN reinforced_count INTEGER DEFAULT 0`,
		`ALTER TABLE memories ADD COLUMN expires_at DATETIME`,
	}
	for _, stmt := range v2Columns {
		_, err = db.Exec(stmt)
		require.NoError(t, err)
	}

	// schema_version 表 + V2 标记
	_, err = db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO schema_version (version) VALUES (2)`)
	require.NoError(t, err)

	// 插入两条记录：一条 decay_rate=0（应被回填为 permanent），一条 decay_rate=0.01（应为 standard）
	_, err = db.Exec(`INSERT INTO memories (id, content, scope, strength, decay_rate, created_at, updated_at)
		VALUES ('perm1', 'permanent content', 'default', 1.0, 0, datetime('now'), datetime('now'))`)
	require.NoError(t, err)

	_, err = db.Exec(`INSERT INTO memories (id, content, scope, strength, decay_rate, created_at, updated_at)
		VALUES ('std1', 'standard content', 'default', 1.0, 0.01, datetime('now'), datetime('now'))`)
	require.NoError(t, err)

	db.Close()

	// 通过 store 重新打开并执行 V2→V3 迁移
	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer s.Close()

	err = s.Init(context.Background())
	require.NoError(t, err)

	rawDB := s.DB().(*sql.DB)

	// 验证 schema_version 为 16（V15→V16 derived_from → junction table）
	var version int
	err = rawDB.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, 16, version)

	// 验证 V3 新增列存在
	v3Columns := []string{"retention_tier", "message_role", "turn_number"}
	for _, col := range v3Columns {
		var colCount int
		err = rawDB.QueryRow(
			"SELECT count(*) FROM pragma_table_info('memories') WHERE name = ?", col,
		).Scan(&colCount)
		require.NoError(t, err)
		assert.Equal(t, 1, colCount, "column %s should exist", col)
	}

	// 验证 decay_rate=0 的记忆被回填为 permanent
	var tier1 string
	err = rawDB.QueryRow("SELECT retention_tier FROM memories WHERE id = 'perm1'").Scan(&tier1)
	require.NoError(t, err)
	assert.Equal(t, "permanent", tier1, "decay_rate=0 memory should be backfilled to permanent tier")

	// 验证 decay_rate=0.01 的记忆保持 standard
	var tier2 string
	err = rawDB.QueryRow("SELECT retention_tier FROM memories WHERE id = 'std1'").Scan(&tier2)
	require.NoError(t, err)
	assert.Equal(t, "standard", tier2, "decay_rate=0.01 memory should have standard tier")
}

func TestMigrate_V1ToV2(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// 手动创建 V1 schema
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = db.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)

	// V1 表结构
	_, err = db.Exec(`CREATE TABLE memories (
		id TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		metadata TEXT,
		team_id TEXT DEFAULT '',
		embedding_id TEXT DEFAULT '',
		parent_id TEXT DEFAULT '',
		is_latest INTEGER DEFAULT 1,
		access_count INTEGER DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(content, content=memories, content_rowid=rowid)`)
	require.NoError(t, err)

	// 插入一条 V1 记忆
	_, err = db.Exec(`INSERT INTO memories (id, content, team_id, created_at, updated_at) VALUES ('test1', 'hello world', 'team1', datetime('now'), datetime('now'))`)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO schema_version (version) VALUES (1)`)
	require.NoError(t, err)

	db.Close()

	// 现在通过 store 打开并迁移到 V2
	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer s.Close()

	err = s.Init(context.Background())
	require.NoError(t, err)

	// 验证旧记忆保留
	mem, err := s.Get(context.Background(), "test1")
	require.NoError(t, err)
	assert.Equal(t, "hello world", mem.Content)
	assert.Equal(t, "default", mem.Scope) // 回填的默认值
	assert.Equal(t, 1.0, mem.Strength)    // 回填的默认值
}
