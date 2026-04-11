package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCandidateStore(t *testing.T) (store.CandidateStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	err = s.Init(context.Background())
	require.NoError(t, err)

	db := s.DB().(*sql.DB)
	cs := store.NewSQLiteCandidateStore(db)

	return cs, func() { s.Close() }
}

func TestUpsertCandidate_CreateAndIncrement(t *testing.T) {
	cs, cleanup := setupCandidateStore(t)
	defer cleanup()

	ctx := context.Background()

	// 第一次 upsert 应创建候选 / First upsert creates candidate
	err := cs.UpsertCandidate(ctx, "Go语言", "default", "mem-001")
	require.NoError(t, err)

	// 验证创建 / Verify creation
	candidates, err := cs.ListPromotable(ctx, 1)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "Go语言", candidates[0].Name)
	assert.Equal(t, "default", candidates[0].Scope)
	assert.Equal(t, 1, candidates[0].HitCount)
	assert.Equal(t, []string{"mem-001"}, candidates[0].MemoryIDs)

	// 第二次 upsert 应递增 hit_count 并追加 memoryID / Second upsert increments and appends
	err = cs.UpsertCandidate(ctx, "Go语言", "default", "mem-002")
	require.NoError(t, err)

	candidates, err = cs.ListPromotable(ctx, 1)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, 2, candidates[0].HitCount)
	assert.Equal(t, []string{"mem-001", "mem-002"}, candidates[0].MemoryIDs)
}

func TestListPromotable_FilterByMinHits(t *testing.T) {
	cs, cleanup := setupCandidateStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建不同 hit_count 的候选 / Create candidates with different hit counts
	// "Alpha" = 1 hit
	require.NoError(t, cs.UpsertCandidate(ctx, "Alpha", "default", "m1"))

	// "Beta" = 3 hits
	require.NoError(t, cs.UpsertCandidate(ctx, "Beta", "default", "m2"))
	require.NoError(t, cs.UpsertCandidate(ctx, "Beta", "default", "m3"))
	require.NoError(t, cs.UpsertCandidate(ctx, "Beta", "default", "m4"))

	// "Gamma" = 5 hits
	for i := 0; i < 5; i++ {
		require.NoError(t, cs.UpsertCandidate(ctx, "Gamma", "default", "g"+string(rune('0'+i))))
	}

	// minHits=3 应只返回 Beta 和 Gamma / minHits=3 should return only Beta and Gamma
	candidates, err := cs.ListPromotable(ctx, 3)
	require.NoError(t, err)
	require.Len(t, candidates, 2)

	// 按 hit_count DESC 排序，Gamma 在前 / Ordered by hit_count DESC, Gamma first
	assert.Equal(t, "Gamma", candidates[0].Name)
	assert.Equal(t, 5, candidates[0].HitCount)
	assert.Equal(t, "Beta", candidates[1].Name)
	assert.Equal(t, 3, candidates[1].HitCount)

	// minHits=6 应返回空 / minHits=6 should return empty
	candidates, err = cs.ListPromotable(ctx, 6)
	require.NoError(t, err)
	assert.Empty(t, candidates)
}

func TestDeleteCandidate_Basic(t *testing.T) {
	cs, cleanup := setupCandidateStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建候选 / Create candidate
	require.NoError(t, cs.UpsertCandidate(ctx, "ToDelete", "default", "m1"))
	require.NoError(t, cs.UpsertCandidate(ctx, "ToDelete", "default", "m2"))

	// 确认存在 / Verify exists
	candidates, err := cs.ListPromotable(ctx, 1)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "ToDelete", candidates[0].Name)

	// 删除 / Delete
	err = cs.DeleteCandidate(ctx, "ToDelete", "default")
	require.NoError(t, err)

	// 确认不存在 / Verify gone
	candidates, err = cs.ListPromotable(ctx, 1)
	require.NoError(t, err)
	assert.Empty(t, candidates)

	// 删除不存在的候选不应报错 / Deleting non-existent should not error
	err = cs.DeleteCandidate(ctx, "NonExistent", "default")
	require.NoError(t, err)
}
