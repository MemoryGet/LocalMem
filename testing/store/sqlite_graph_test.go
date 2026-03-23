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

func setupGraphStore(t *testing.T) (store.GraphStore, store.MemoryStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	err = s.Init(context.Background())
	require.NoError(t, err)

	db := s.DB().(*sql.DB)
	gs := store.NewSQLiteGraphStore(db)

	return gs, s, func() { s.Close() }
}

func TestGraphStore_EntityCRUD(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	entity := &model.Entity{
		Name:       "Go",
		EntityType: "concept",
		Scope:      "default",
	}
	err := gs.CreateEntity(context.Background(), entity)
	require.NoError(t, err)
	assert.NotEmpty(t, entity.ID)

	got, err := gs.GetEntity(context.Background(), entity.ID)
	require.NoError(t, err)
	assert.Equal(t, "Go", got.Name)

	entity.Description = "programming language"
	err = gs.UpdateEntity(context.Background(), entity)
	require.NoError(t, err)

	err = gs.DeleteEntity(context.Background(), entity.ID)
	require.NoError(t, err)

	_, err = gs.GetEntity(context.Background(), entity.ID)
	assert.ErrorIs(t, err, model.ErrEntityNotFound)
}

func TestGraphStore_DuplicateEntity(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	e1 := &model.Entity{Name: "dup", EntityType: "concept", Scope: "default"}
	err := gs.CreateEntity(context.Background(), e1)
	require.NoError(t, err)

	e2 := &model.Entity{Name: "dup", EntityType: "concept", Scope: "default"}
	err = gs.CreateEntity(context.Background(), e2)
	assert.ErrorIs(t, err, model.ErrConflict)
}

func TestGraphStore_ListEntities(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	gs.CreateEntity(context.Background(), &model.Entity{Name: "a", EntityType: "person", Scope: "s1"})
	gs.CreateEntity(context.Background(), &model.Entity{Name: "b", EntityType: "tool", Scope: "s1"})
	gs.CreateEntity(context.Background(), &model.Entity{Name: "c", EntityType: "person", Scope: "s2"})

	all, err := gs.ListEntities(context.Background(), "", "", 10)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	persons, err := gs.ListEntities(context.Background(), "", "person", 10)
	require.NoError(t, err)
	assert.Len(t, persons, 2)
}

func TestGraphStore_Relations(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	e1 := &model.Entity{Name: "Alice", EntityType: "person", Scope: "default"}
	gs.CreateEntity(context.Background(), e1)
	e2 := &model.Entity{Name: "Bob", EntityType: "person", Scope: "default"}
	gs.CreateEntity(context.Background(), e2)

	rel := &model.EntityRelation{
		SourceID:     e1.ID,
		TargetID:     e2.ID,
		RelationType: "knows",
		Weight:       1.0,
	}
	err := gs.CreateRelation(context.Background(), rel)
	require.NoError(t, err)
	assert.NotEmpty(t, rel.ID)

	rels, err := gs.GetEntityRelations(context.Background(), e1.ID)
	require.NoError(t, err)
	assert.Len(t, rels, 1)

	err = gs.DeleteRelation(context.Background(), rel.ID)
	require.NoError(t, err)
}

func TestGraphStore_MemoryEntity(t *testing.T) {
	gs, ms, cleanup := setupGraphStore(t)
	defer cleanup()

	mem := &model.Memory{Content: "test"}
	ms.Create(context.Background(), mem)

	entity := &model.Entity{Name: "TestEntity", EntityType: "concept", Scope: "default"}
	gs.CreateEntity(context.Background(), entity)

	me := &model.MemoryEntity{MemoryID: mem.ID, EntityID: entity.ID, Role: "subject"}
	err := gs.CreateMemoryEntity(context.Background(), me)
	require.NoError(t, err)

	memories, err := gs.GetEntityMemories(context.Background(), entity.ID, 10)
	require.NoError(t, err)
	assert.Len(t, memories, 1)

	entities, err := gs.GetMemoryEntities(context.Background(), mem.ID)
	require.NoError(t, err)
	assert.Len(t, entities, 1)

	err = gs.DeleteMemoryEntity(context.Background(), mem.ID, entity.ID)
	require.NoError(t, err)
}
