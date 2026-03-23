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

func setupContextStore(t *testing.T) (store.ContextStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	err = s.Init(context.Background())
	require.NoError(t, err)

	db := s.DB().(*sql.DB)
	cs := store.NewSQLiteContextStore(db)

	return cs, func() { s.Close() }
}

func TestContextStore_CreateAndGet(t *testing.T) {
	cs, cleanup := setupContextStore(t)
	defer cleanup()

	c := &model.Context{
		Name:  "project-alpha",
		Scope: "user/alice",
		Kind:  "project",
	}
	err := cs.Create(context.Background(), c)
	require.NoError(t, err)
	assert.NotEmpty(t, c.ID)
	assert.Equal(t, "/project-alpha", c.Path)
	assert.Equal(t, 0, c.Depth)

	got, err := cs.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "project-alpha", got.Name)
}

func TestContextStore_GetByPath(t *testing.T) {
	cs, cleanup := setupContextStore(t)
	defer cleanup()

	c := &model.Context{Name: "test-ctx", Scope: "default"}
	err := cs.Create(context.Background(), c)
	require.NoError(t, err)

	got, err := cs.GetByPath(context.Background(), "/test-ctx")
	require.NoError(t, err)
	assert.Equal(t, c.ID, got.ID)
}

func TestContextStore_ParentChild(t *testing.T) {
	cs, cleanup := setupContextStore(t)
	defer cleanup()

	parent := &model.Context{Name: "parent"}
	err := cs.Create(context.Background(), parent)
	require.NoError(t, err)

	child := &model.Context{Name: "child", ParentID: parent.ID}
	err = cs.Create(context.Background(), child)
	require.NoError(t, err)
	assert.Equal(t, "/parent/child", child.Path)
	assert.Equal(t, 1, child.Depth)

	children, err := cs.ListChildren(context.Background(), parent.ID)
	require.NoError(t, err)
	assert.Len(t, children, 1)
}

func TestContextStore_ListSubtree(t *testing.T) {
	cs, cleanup := setupContextStore(t)
	defer cleanup()

	root := &model.Context{Name: "root"}
	cs.Create(context.Background(), root)
	child1 := &model.Context{Name: "c1", ParentID: root.ID}
	cs.Create(context.Background(), child1)
	child2 := &model.Context{Name: "c2", ParentID: root.ID}
	cs.Create(context.Background(), child2)
	grandchild := &model.Context{Name: "gc", ParentID: child1.ID}
	cs.Create(context.Background(), grandchild)

	subtree, err := cs.ListSubtree(context.Background(), "/root")
	require.NoError(t, err)
	assert.Len(t, subtree, 3) // c1, c2, gc
}

func TestContextStore_Move(t *testing.T) {
	cs, cleanup := setupContextStore(t)
	defer cleanup()

	a := &model.Context{Name: "a"}
	cs.Create(context.Background(), a)
	b := &model.Context{Name: "b"}
	cs.Create(context.Background(), b)
	c := &model.Context{Name: "c", ParentID: a.ID}
	cs.Create(context.Background(), c)

	// 移动 c 到 b 下
	err := cs.Move(context.Background(), c.ID, b.ID)
	require.NoError(t, err)

	got, err := cs.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "/b/c", got.Path)
	assert.Equal(t, b.ID, got.ParentID)
}

func TestContextStore_MemoryCount(t *testing.T) {
	cs, cleanup := setupContextStore(t)
	defer cleanup()

	c := &model.Context{Name: "counter"}
	cs.Create(context.Background(), c)

	cs.IncrementMemoryCount(context.Background(), c.ID)
	cs.IncrementMemoryCount(context.Background(), c.ID)

	got, err := cs.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.MemoryCount)

	cs.DecrementMemoryCount(context.Background(), c.ID)
	got, err = cs.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.MemoryCount)
}

func TestContextStore_NotFound(t *testing.T) {
	cs, cleanup := setupContextStore(t)
	defer cleanup()

	_, err := cs.Get(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, model.ErrContextNotFound)
}
