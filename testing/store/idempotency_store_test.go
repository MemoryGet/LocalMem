package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupIdempotencyDB(t *testing.T) (*sql.DB, store.IdempotencyStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	tok := tokenizer.NewNoopTokenizer()
	require.NoError(t, store.Migrate(db, tok))
	return db, store.NewSQLiteIdempotencyStore(db)
}

func TestIdempotencyStore_ReserveFirstTime(t *testing.T) {
	db, is := setupIdempotencyDB(t)
	defer db.Close()

	reserved, err := is.Reserve(context.Background(), "retain", "key-1", "memory")
	require.NoError(t, err)
	assert.True(t, reserved, "first reservation should succeed")
}

func TestIdempotencyStore_ReserveDuplicate(t *testing.T) {
	db, is := setupIdempotencyDB(t)
	defer db.Close()

	_, err := is.Reserve(context.Background(), "retain", "key-1", "memory")
	require.NoError(t, err)

	reserved, err := is.Reserve(context.Background(), "retain", "key-1", "memory")
	require.NoError(t, err)
	assert.False(t, reserved, "duplicate reservation should return false")
}

func TestIdempotencyStore_DifferentScopes(t *testing.T) {
	db, is := setupIdempotencyDB(t)
	defer db.Close()

	r1, err := is.Reserve(context.Background(), "retain", "key-1", "memory")
	require.NoError(t, err)
	assert.True(t, r1)

	r2, err := is.Reserve(context.Background(), "ingest", "key-1", "session")
	require.NoError(t, err)
	assert.True(t, r2, "same key in different scope should succeed")
}

func TestIdempotencyStore_BindResource(t *testing.T) {
	db, is := setupIdempotencyDB(t)
	defer db.Close()

	_, err := is.Reserve(context.Background(), "retain", "key-1", "memory")
	require.NoError(t, err)

	require.NoError(t, is.BindResource(context.Background(), "retain", "key-1", "mem-123"))

	rec, err := is.Get(context.Background(), "retain", "key-1")
	require.NoError(t, err)
	assert.Equal(t, "mem-123", rec.ResourceID)
	assert.Equal(t, "memory", rec.ResourceType)
}

func TestIdempotencyStore_BindResourceNotFound(t *testing.T) {
	db, is := setupIdempotencyDB(t)
	defer db.Close()

	err := is.BindResource(context.Background(), "retain", "nonexistent", "res-1")
	assert.ErrorIs(t, err, model.ErrIdempotencyNotFound)
}

func TestIdempotencyStore_GetNotFound(t *testing.T) {
	db, is := setupIdempotencyDB(t)
	defer db.Close()

	_, err := is.Get(context.Background(), "retain", "nonexistent")
	assert.ErrorIs(t, err, model.ErrIdempotencyNotFound)
}

func TestIdempotencyStore_PurgeExpired(t *testing.T) {
	db, is := setupIdempotencyDB(t)
	defer db.Close()

	// 直接插入旧记录 / Insert old records directly
	_, err := db.Exec(`INSERT INTO idempotency_keys (scope, idem_key, resource_type, created_at)
		VALUES ('retain', 'old-1', 'memory', datetime('now', '-48 hours'))`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO idempotency_keys (scope, idem_key, resource_type, created_at)
		VALUES ('retain', 'old-2', 'memory', datetime('now', '-48 hours'))`)
	require.NoError(t, err)

	// 新记录 / Fresh record
	_, err = is.Reserve(context.Background(), "retain", "fresh-1", "memory")
	require.NoError(t, err)

	purged, err := is.PurgeExpired(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, purged, "should purge 2 old records")

	// 新记录应保留 / Fresh record should remain
	_, err = is.Get(context.Background(), "retain", "fresh-1")
	require.NoError(t, err)
}
