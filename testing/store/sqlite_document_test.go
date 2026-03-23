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

func setupDocumentStore(t *testing.T) (store.DocumentStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	err = s.Init(context.Background())
	require.NoError(t, err)

	db := s.DB().(*sql.DB)
	ds := store.NewSQLiteDocumentStore(db)

	return ds, func() { s.Close() }
}

func TestDocumentStore_CreateAndGet(t *testing.T) {
	ds, cleanup := setupDocumentStore(t)
	defer cleanup()

	doc := &model.Document{
		Name:    "test.md",
		DocType: "markdown",
		Scope:   "default",
	}
	err := ds.Create(context.Background(), doc)
	require.NoError(t, err)
	assert.NotEmpty(t, doc.ID)
	assert.Equal(t, "pending", doc.Status)

	got, err := ds.Get(context.Background(), doc.ID)
	require.NoError(t, err)
	assert.Equal(t, "test.md", got.Name)
}

func TestDocumentStore_DuplicateHash(t *testing.T) {
	ds, cleanup := setupDocumentStore(t)
	defer cleanup()

	doc1 := &model.Document{Name: "a.md", DocType: "markdown", ContentHash: "abc123"}
	err := ds.Create(context.Background(), doc1)
	require.NoError(t, err)

	doc2 := &model.Document{Name: "b.md", DocType: "markdown", ContentHash: "abc123"}
	err = ds.Create(context.Background(), doc2)
	assert.ErrorIs(t, err, model.ErrDuplicateDocument)
}

func TestDocumentStore_ListByStatus(t *testing.T) {
	ds, cleanup := setupDocumentStore(t)
	defer cleanup()

	ds.Create(context.Background(), &model.Document{Name: "a", DocType: "text"})
	ds.Create(context.Background(), &model.Document{Name: "b", DocType: "text"})
	ds.UpdateStatus(context.Background(), func() string {
		doc := &model.Document{Name: "c", DocType: "text"}
		ds.Create(context.Background(), doc)
		return doc.ID
	}(), "ready")

	pending, err := ds.ListByStatus(context.Background(), []string{"pending"}, 10)
	require.NoError(t, err)
	assert.Len(t, pending, 2)

	ready, err := ds.ListByStatus(context.Background(), []string{"ready"}, 10)
	require.NoError(t, err)
	assert.Len(t, ready, 1)
}

func TestDocumentStore_UpdateStatus(t *testing.T) {
	ds, cleanup := setupDocumentStore(t)
	defer cleanup()

	doc := &model.Document{Name: "test", DocType: "text"}
	ds.Create(context.Background(), doc)

	err := ds.UpdateStatus(context.Background(), doc.ID, "processing")
	require.NoError(t, err)

	got, err := ds.Get(context.Background(), doc.ID)
	require.NoError(t, err)
	assert.Equal(t, "processing", got.Status)
}

func TestDocumentStore_NotFound(t *testing.T) {
	ds, cleanup := setupDocumentStore(t)
	defer cleanup()

	_, err := ds.Get(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, model.ErrDocumentNotFound)
}

func TestDocumentStore_GetByHash(t *testing.T) {
	ds, cleanup := setupDocumentStore(t)
	defer cleanup()

	doc := &model.Document{Name: "test", DocType: "text", ContentHash: "hash123"}
	ds.Create(context.Background(), doc)

	got, err := ds.GetByHash(context.Background(), "hash123")
	require.NoError(t, err)
	assert.Equal(t, doc.ID, got.ID)

	_, err = ds.GetByHash(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, model.ErrDocumentNotFound)
}

func TestDocumentStore_Delete(t *testing.T) {
	ds, cleanup := setupDocumentStore(t)
	defer cleanup()

	doc := &model.Document{Name: "test", DocType: "text"}
	ds.Create(context.Background(), doc)

	err := ds.Delete(context.Background(), doc.ID)
	require.NoError(t, err)

	_, err = ds.Get(context.Background(), doc.ID)
	assert.ErrorIs(t, err, model.ErrDocumentNotFound)
}
