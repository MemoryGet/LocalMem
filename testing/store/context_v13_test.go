package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// TestMigrateV12ToV13_AddsColumns 验证 V13 迁移新增 mission/directives/disposition 列
// Verify V13 migration adds mission, directives, disposition columns to contexts
func TestMigrateV12ToV13_AddsColumns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	tok := tokenizer.NewNoopTokenizer()
	require.NoError(t, store.Migrate(db, tok))

	// 验证新列存在：插入带行为字段的记录 / Verify new columns exist
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO contexts (id, name, path, scope, context_type, description, mission, directives, disposition, metadata, depth, sort_order, memory_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"ctx-v13", "test-ctx", "/test-ctx", "user1", "project", "desc",
		"build the best", "be concise\nbe accurate", "friendly", "{}", 0, 0, 0)
	require.NoError(t, err, "new columns should accept values")

	var mission, directives, disposition string
	err = db.QueryRow(`SELECT mission, directives, disposition FROM contexts WHERE id = 'ctx-v13'`).
		Scan(&mission, &directives, &disposition)
	require.NoError(t, err)
	assert.Equal(t, "build the best", mission)
	assert.Equal(t, "be concise\nbe accurate", directives)
	assert.Equal(t, "friendly", disposition)

	// 验证默认值 / Verify defaults
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO contexts (id, name, path, metadata, depth, sort_order, memory_count, created_at, updated_at)
		VALUES ('ctx-v13-default', 'default-ctx', '/default-ctx', '{}', 0, 0, 0, datetime('now'), datetime('now'))`)
	require.NoError(t, err)

	err = db.QueryRow(`SELECT mission, directives, disposition FROM contexts WHERE id = 'ctx-v13-default'`).
		Scan(&mission, &directives, &disposition)
	require.NoError(t, err)
	assert.Equal(t, "", mission)
	assert.Equal(t, "", directives)
	assert.Equal(t, "", disposition)

	// 验证版本号 / Verify schema version
	var version int
	err = db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, 15, version)
}

// TestMigrateV12ToV13_Idempotent 验证 V13 迁移可安全重跑
// Verify V13 migration is idempotent (safe to run twice)
func TestMigrateV12ToV13_Idempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	tok := tokenizer.NewNoopTokenizer()

	// 第一次运行 / First run
	require.NoError(t, store.Migrate(db, tok))

	// 重置版本号强制重跑 / Reset version to force re-run
	_, err = db.Exec(`DELETE FROM schema_version WHERE version = 13`)
	require.NoError(t, err)

	// 第二次运行（应该幂等成功）/ Second run (should succeed idempotently)
	require.NoError(t, store.Migrate(db, tok))

	var version int
	err = db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, 15, version)
}

// TestContextV13_CreateGetRoundTrip 验证 Create/Get 端到端带行为字段
// Verify Create/Get round-trip with mission/directives/disposition
func TestContextV13_CreateGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Init(context.Background()))

	db := s.DB().(*sql.DB)
	cs := store.NewSQLiteContextStore(db)

	tests := []struct {
		name        string
		ctx         model.Context
		wantMission string
		wantDir     string
		wantDisp    string
	}{
		{
			name:        "all behavioral fields set",
			ctx:         model.Context{Name: "ctx-full", Mission: "help users", Directives: "be brief\nno jargon", Disposition: "professional"},
			wantMission: "help users",
			wantDir:     "be brief\nno jargon",
			wantDisp:    "professional",
		},
		{
			name:        "empty behavioral fields",
			ctx:         model.Context{Name: "ctx-empty"},
			wantMission: "",
			wantDir:     "",
			wantDisp:    "",
		},
		{
			name:        "partial fields",
			ctx:         model.Context{Name: "ctx-partial", Mission: "research assistant"},
			wantMission: "research assistant",
			wantDir:     "",
			wantDisp:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.ctx
			err := cs.Create(context.Background(), &c)
			require.NoError(t, err)

			got, err := cs.Get(context.Background(), c.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantMission, got.Mission)
			assert.Equal(t, tt.wantDir, got.Directives)
			assert.Equal(t, tt.wantDisp, got.Disposition)
		})
	}
}

// TestContextV13_UpdateRoundTrip 验证 Update 端到端带行为字段
// Verify Update round-trip with mission/directives/disposition
func TestContextV13_UpdateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Init(context.Background()))

	db := s.DB().(*sql.DB)
	cs := store.NewSQLiteContextStore(db)

	// 创建初始上下文 / Create initial context
	c := &model.Context{Name: "ctx-update", Mission: "old mission"}
	require.NoError(t, cs.Create(context.Background(), c))

	// 更新行为字段 / Update behavioral fields
	c.Mission = "new mission"
	c.Directives = "directive-1\ndirective-2"
	c.Disposition = "casual"
	require.NoError(t, cs.Update(context.Background(), c))

	// 验证更新结果 / Verify update result
	got, err := cs.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "new mission", got.Mission)
	assert.Equal(t, "directive-1\ndirective-2", got.Directives)
	assert.Equal(t, "casual", got.Disposition)
}
