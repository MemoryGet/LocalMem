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

func setupFinalizeDB(t *testing.T) (*sql.DB, store.SessionStore, store.SessionFinalizeStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	tok := tokenizer.NewNoopTokenizer()
	require.NoError(t, store.Migrate(db, tok))
	// FK 需要 sessions 表中先有记录 / FK requires session to exist first (freshSchema has CASCADE)
	// 内存库走 freshSchema 路径，有 FK
	return db, store.NewSQLiteSessionStore(db), store.NewSQLiteSessionFinalizeStore(db)
}

func createTestSession(t *testing.T, ss store.SessionStore, id string) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	require.NoError(t, ss.Create(context.Background(), &model.Session{
		ID: id, State: model.SessionStateActive,
		StartedAt: now, LastSeenAt: now,
	}))
}

func TestSessionFinalizeStore_UpsertAndGet(t *testing.T) {
	db, ss, fs := setupFinalizeDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")

	st := &model.SessionFinalizeState{
		SessionID:     "sess-1",
		IngestVersion: 1,
		LastError:     "timeout",
	}
	require.NoError(t, fs.Upsert(context.Background(), st))

	got, err := fs.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, 1, got.IngestVersion)
	assert.Equal(t, "timeout", got.LastError)
	assert.False(t, got.ConversationIngested)
}

func TestSessionFinalizeStore_UpsertOverwrite(t *testing.T) {
	db, ss, fs := setupFinalizeDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")

	require.NoError(t, fs.Upsert(context.Background(), &model.SessionFinalizeState{
		SessionID: "sess-1", IngestVersion: 1,
	}))

	require.NoError(t, fs.Upsert(context.Background(), &model.SessionFinalizeState{
		SessionID: "sess-1", IngestVersion: 2, ConversationIngested: true,
	}))

	got, err := fs.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, 2, got.IngestVersion)
	assert.True(t, got.ConversationIngested)
}

func TestSessionFinalizeStore_GetNotFound(t *testing.T) {
	db, _, fs := setupFinalizeDB(t)
	defer db.Close()

	_, err := fs.Get(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, model.ErrFinalizeStateNotFound)
}

func TestSessionFinalizeStore_MarkIngested(t *testing.T) {
	db, ss, fs := setupFinalizeDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")

	require.NoError(t, fs.MarkIngested(context.Background(), "sess-1", 3))

	got, err := fs.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, 3, got.IngestVersion)
	assert.True(t, got.ConversationIngested)
}

func TestSessionFinalizeStore_MarkFinalized(t *testing.T) {
	db, ss, fs := setupFinalizeDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")

	require.NoError(t, fs.MarkFinalized(context.Background(), "sess-1", 2, "mem-summary"))

	got, err := fs.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, 2, got.FinalizeVersion)
	assert.Equal(t, "mem-summary", got.SummaryMemoryID)
}

func TestSessionFinalizeStore_ListUnfinalized(t *testing.T) {
	db, ss, fs := setupFinalizeDB(t)
	defer db.Close()

	createTestSession(t, ss, "sess-1")
	createTestSession(t, ss, "sess-2")
	createTestSession(t, ss, "sess-3")

	// sess-1: ingested but not finalized
	require.NoError(t, fs.MarkIngested(context.Background(), "sess-1", 1))
	// sess-2: fully finalized
	require.NoError(t, fs.MarkFinalized(context.Background(), "sess-2", 1, "mem-1"))
	// sess-3: not yet processed
	require.NoError(t, fs.Upsert(context.Background(), &model.SessionFinalizeState{
		SessionID: "sess-3",
	}))

	result, err := fs.ListUnfinalized(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, result, 2, "sess-1 and sess-3 should be unfinalized")

	ids := make(map[string]bool)
	for _, st := range result {
		ids[st.SessionID] = true
	}
	assert.True(t, ids["sess-1"])
	assert.True(t, ids["sess-3"])
}
