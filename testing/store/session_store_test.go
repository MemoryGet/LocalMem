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

func setupSessionDB(t *testing.T) (*sql.DB, store.SessionStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	tok := tokenizer.NewNoopTokenizer()
	require.NoError(t, store.Migrate(db, tok))
	return db, store.NewSQLiteSessionStore(db)
}

func TestSessionStore_CreateAndGet(t *testing.T) {
	db, ss := setupSessionDB(t)
	defer db.Close()

	now := time.Now().Truncate(time.Second)
	sess := &model.Session{
		ID:         "sess-1",
		ContextID:  "ctx-1",
		UserID:     "user-1",
		ToolName:   "claude-code",
		ProjectID:  "proj-1",
		ProjectDir: "/home/user/project",
		Profile:    "A",
		State:      model.SessionStateCreated,
		StartedAt:  now,
		LastSeenAt: now,
		Metadata:   map[string]any{"version": "1.0"},
	}

	require.NoError(t, ss.Create(context.Background(), sess))

	got, err := ss.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got.ID)
	assert.Equal(t, "ctx-1", got.ContextID)
	assert.Equal(t, "claude-code", got.ToolName)
	assert.Equal(t, model.SessionStateCreated, got.State)
	assert.Equal(t, "1.0", got.Metadata["version"])
	assert.Nil(t, got.FinalizedAt)
}

func TestSessionStore_GetNotFound(t *testing.T) {
	db, ss := setupSessionDB(t)
	defer db.Close()

	_, err := ss.Get(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, model.ErrSessionNotFound)
}

func TestSessionStore_UpdateState(t *testing.T) {
	db, ss := setupSessionDB(t)
	defer db.Close()

	now := time.Now().Truncate(time.Second)
	sess := &model.Session{
		ID: "sess-1", State: model.SessionStateCreated,
		StartedAt: now, LastSeenAt: now,
	}
	require.NoError(t, ss.Create(context.Background(), sess))

	require.NoError(t, ss.UpdateState(context.Background(), "sess-1", model.SessionStateActive))

	got, err := ss.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, model.SessionStateActive, got.State)
}

func TestSessionStore_UpdateStateNotFound(t *testing.T) {
	db, ss := setupSessionDB(t)
	defer db.Close()

	err := ss.UpdateState(context.Background(), "nonexistent", model.SessionStateActive)
	assert.ErrorIs(t, err, model.ErrSessionNotFound)
}

func TestSessionStore_Touch(t *testing.T) {
	db, ss := setupSessionDB(t)
	defer db.Close()

	now := time.Now().Truncate(time.Second)
	sess := &model.Session{
		ID: "sess-1", State: model.SessionStateActive,
		StartedAt: now, LastSeenAt: now,
	}
	require.NoError(t, ss.Create(context.Background(), sess))

	later := now.Add(10 * time.Minute)
	require.NoError(t, ss.Touch(context.Background(), "sess-1", later))

	got, err := ss.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.True(t, got.LastSeenAt.After(now) || got.LastSeenAt.Equal(later))
}

func TestSessionStore_ListPendingFinalize(t *testing.T) {
	db, ss := setupSessionDB(t)
	defer db.Close()

	old := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	recent := time.Now().Truncate(time.Second)

	// 旧的 active 会话 / Old active session
	require.NoError(t, ss.Create(context.Background(), &model.Session{
		ID: "old-active", State: model.SessionStateActive,
		StartedAt: old, LastSeenAt: old,
	}))
	// 旧的 pending_repair 会话 / Old pending_repair session
	require.NoError(t, ss.Create(context.Background(), &model.Session{
		ID: "old-repair", State: model.SessionStatePendingRepair,
		StartedAt: old, LastSeenAt: old,
	}))
	// 新的 active 会话（不应被选中）/ Recent active session (should not be selected)
	require.NoError(t, ss.Create(context.Background(), &model.Session{
		ID: "recent-active", State: model.SessionStateActive,
		StartedAt: recent, LastSeenAt: recent,
	}))
	// 已 finalized 的旧会话（不应被选中）/ Old finalized session (should not be selected)
	require.NoError(t, ss.Create(context.Background(), &model.Session{
		ID: "old-finalized", State: model.SessionStateFinalized,
		StartedAt: old, LastSeenAt: old,
	}))

	result, err := ss.ListPendingFinalize(context.Background(), 1*time.Hour, 10)
	require.NoError(t, err)
	assert.Len(t, result, 2)

	ids := make(map[string]bool)
	for _, s := range result {
		ids[s.ID] = true
	}
	assert.True(t, ids["old-active"])
	assert.True(t, ids["old-repair"])
}

func TestSessionStore_MetadataNil(t *testing.T) {
	db, ss := setupSessionDB(t)
	defer db.Close()

	now := time.Now().Truncate(time.Second)
	require.NoError(t, ss.Create(context.Background(), &model.Session{
		ID: "sess-no-meta", State: model.SessionStateCreated,
		StartedAt: now, LastSeenAt: now,
	}))

	got, err := ss.Get(context.Background(), "sess-no-meta")
	require.NoError(t, err)
	assert.Nil(t, got.Metadata)
}
