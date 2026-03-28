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

	// 验证 schema_version 为 8（V7→V8 新增 async_tasks 表）
	db := s.DB().(*sql.DB)
	var version int
	err = db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, 8, version)

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

	// 验证 schema_version 为 8（V7→V8 新增 async_tasks 表）
	var version int
	err = rawDB.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, 8, version)

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
