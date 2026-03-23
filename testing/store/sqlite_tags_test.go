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
