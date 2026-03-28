package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTagStore(t *testing.T) (store.TagStore, store.MemoryStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	err = s.Init(context.Background())
	require.NoError(t, err)

	db := s.DB().(*sql.DB)
	ts := store.NewSQLiteTagStore(db)

	return ts, s, func() { s.Close() }
}

func TestTagStore_CreateAndGet(t *testing.T) {
	ts, _, cleanup := setupTagStore(t)
	defer cleanup()

	tag := &model.Tag{Name: "important", Scope: "default"}
	err := ts.CreateTag(context.Background(), tag)
	require.NoError(t, err)
	assert.NotEmpty(t, tag.ID)

	got, err := ts.GetTag(context.Background(), tag.ID)
	require.NoError(t, err)
	assert.Equal(t, "important", got.Name)
}

func TestTagStore_DuplicateName(t *testing.T) {
	ts, _, cleanup := setupTagStore(t)
	defer cleanup()

	tag1 := &model.Tag{Name: "dup", Scope: "default"}
	err := ts.CreateTag(context.Background(), tag1)
	require.NoError(t, err)

	tag2 := &model.Tag{Name: "dup", Scope: "default"}
	err = ts.CreateTag(context.Background(), tag2)
	assert.ErrorIs(t, err, model.ErrConflict)
}

func TestTagStore_ListTags(t *testing.T) {
	ts, _, cleanup := setupTagStore(t)
	defer cleanup()

	ts.CreateTag(context.Background(), &model.Tag{Name: "a", Scope: "s1"})
	ts.CreateTag(context.Background(), &model.Tag{Name: "b", Scope: "s1"})
	ts.CreateTag(context.Background(), &model.Tag{Name: "c", Scope: "s2"})

	all, err := ts.ListTags(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	scoped, err := ts.ListTags(context.Background(), "s1")
	require.NoError(t, err)
	assert.Len(t, scoped, 2)
}

func TestTagStore_TagMemory(t *testing.T) {
	ts, ms, cleanup := setupTagStore(t)
	defer cleanup()

	// 创建记忆
	mem := &model.Memory{Content: "test memory"}
	err := ms.Create(context.Background(), mem)
	require.NoError(t, err)

	// 创建标签
	tag := &model.Tag{Name: "test-tag", Scope: "default"}
	err = ts.CreateTag(context.Background(), tag)
	require.NoError(t, err)

	// 打标签
	err = ts.TagMemory(context.Background(), mem.ID, tag.ID)
	require.NoError(t, err)

	// 获取标签
	tags, err := ts.GetMemoryTags(context.Background(), mem.ID)
	require.NoError(t, err)
	assert.Len(t, tags, 1)
	assert.Equal(t, "test-tag", tags[0].Name)

	// 获取记忆
	memories, err := ts.GetMemoriesByTag(context.Background(), tag.ID, 10)
	require.NoError(t, err)
	assert.Len(t, memories, 1)

	// 移除标签
	err = ts.UntagMemory(context.Background(), mem.ID, tag.ID)
	require.NoError(t, err)

	tags, err = ts.GetMemoryTags(context.Background(), mem.ID)
	require.NoError(t, err)
	assert.Len(t, tags, 0)
}

func TestTagStore_DeleteTag(t *testing.T) {
	ts, _, cleanup := setupTagStore(t)
	defer cleanup()

	tag := &model.Tag{Name: "to-delete", Scope: "default"}
	ts.CreateTag(context.Background(), tag)

	err := ts.DeleteTag(context.Background(), tag.ID)
	require.NoError(t, err)

	_, err = ts.GetTag(context.Background(), tag.ID)
	assert.ErrorIs(t, err, model.ErrTagNotFound)
}

func TestGetTagNamesByMemoryIDs(t *testing.T) {
	ts, ms, cleanup := setupTagStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建两条记忆
	mem1 := &model.Memory{Content: "memory one"}
	mem2 := &model.Memory{Content: "memory two"}
	require.NoError(t, ms.Create(ctx, mem1))
	require.NoError(t, ms.Create(ctx, mem2))

	// 创建标签
	tagA := &model.Tag{Name: "alpha", Scope: "default"}
	tagB := &model.Tag{Name: "beta", Scope: "default"}
	tagC := &model.Tag{Name: "gamma", Scope: "default"}
	require.NoError(t, ts.CreateTag(ctx, tagA))
	require.NoError(t, ts.CreateTag(ctx, tagB))
	require.NoError(t, ts.CreateTag(ctx, tagC))

	// mem1 → alpha, beta; mem2 → gamma
	require.NoError(t, ts.TagMemory(ctx, mem1.ID, tagA.ID))
	require.NoError(t, ts.TagMemory(ctx, mem1.ID, tagB.ID))
	require.NoError(t, ts.TagMemory(ctx, mem2.ID, tagC.ID))

	tests := []struct {
		name      string
		ids       []string
		wantKeys  []string
		wantTags  map[string][]string
		wantEmpty bool
	}{
		{
			name:    "both memories return correct tag names",
			ids:     []string{mem1.ID, mem2.ID},
			wantKeys: []string{mem1.ID, mem2.ID},
			wantTags: map[string][]string{
				mem1.ID: {"alpha", "beta"},
				mem2.ID: {"gamma"},
			},
		},
		{
			name:    "single memory query",
			ids:     []string{mem1.ID},
			wantKeys: []string{mem1.ID},
			wantTags: map[string][]string{
				mem1.ID: {"alpha", "beta"},
			},
		},
		{
			name:      "empty ids returns empty map",
			ids:       []string{},
			wantEmpty: true,
		},
		{
			name:      "non-existent id returns empty map",
			ids:       []string{"does-not-exist"},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ts.GetTagNamesByMemoryIDs(ctx, tt.ids)
			require.NoError(t, err)

			if tt.wantEmpty {
				assert.Empty(t, got)
				return
			}

			assert.Len(t, got, len(tt.wantKeys))
			for memID, expectedTags := range tt.wantTags {
				assert.ElementsMatch(t, expectedTags, got[memID])
			}
		})
	}
}
