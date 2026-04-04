package runtime_test

import (
	"context"
	"database/sql"
	"testing"

	"iclude/internal/model"
	"iclude/internal/runtime"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	tok := tokenizer.NewNoopTokenizer()
	require.NoError(t, store.Migrate(db, tok))
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSessionService_CreateAndGet(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	sess := &model.Session{
		ID:       "sess-1",
		ToolName: "claude-code",
	}
	require.NoError(t, svc.Create(context.Background(), sess))
	assert.Equal(t, model.SessionStateCreated, sess.State)

	got, err := svc.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got.ID)
	assert.Equal(t, model.SessionStateCreated, got.State)
}

func TestSessionService_CreateRequiresID(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	err := svc.Create(context.Background(), &model.Session{})
	assert.ErrorIs(t, err, model.ErrInvalidInput)
}

func TestSessionService_StateTransitions_HappyPath(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	require.NoError(t, svc.Create(context.Background(), &model.Session{ID: "s1", ToolName: "codex"}))

	require.NoError(t, svc.MarkBootstrapped(context.Background(), "s1"))
	require.NoError(t, svc.MarkActive(context.Background(), "s1"))
	require.NoError(t, svc.MarkFinalizing(context.Background(), "s1"))
	require.NoError(t, svc.MarkFinalized(context.Background(), "s1"))

	got, err := svc.Get(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestSessionService_InvalidTransition_CreatedToActive(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	require.NoError(t, svc.Create(context.Background(), &model.Session{ID: "s1"}))

	// created → active 不合法（必须先 bootstrapped）
	err := svc.MarkActive(context.Background(), "s1")
	assert.Error(t, err)
	assert.ErrorIs(t, err, model.ErrInvalidInput)
}

func TestSessionService_InvalidTransition_FinalizedToActive(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	require.NoError(t, svc.Create(context.Background(), &model.Session{ID: "s1"}))
	require.NoError(t, svc.MarkBootstrapped(context.Background(), "s1"))
	require.NoError(t, svc.MarkActive(context.Background(), "s1"))
	require.NoError(t, svc.MarkFinalizing(context.Background(), "s1"))
	require.NoError(t, svc.MarkFinalized(context.Background(), "s1"))

	// finalized 是终态，不能回到 active
	err := svc.MarkActive(context.Background(), "s1")
	assert.Error(t, err)
}

func TestSessionService_PendingRepairPath(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	require.NoError(t, svc.Create(context.Background(), &model.Session{ID: "s1"}))
	require.NoError(t, svc.MarkBootstrapped(context.Background(), "s1"))
	require.NoError(t, svc.MarkActive(context.Background(), "s1"))

	// active → pending_repair
	require.NoError(t, svc.MarkPendingRepair(context.Background(), "s1"))

	// pending_repair → finalizing → finalized
	require.NoError(t, svc.MarkFinalizing(context.Background(), "s1"))
	require.NoError(t, svc.MarkFinalized(context.Background(), "s1"))

	got, _ := svc.Get(context.Background(), "s1")
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestSessionService_Touch(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	require.NoError(t, svc.Create(context.Background(), &model.Session{ID: "s1"}))

	before, _ := svc.Get(context.Background(), "s1")
	require.NoError(t, svc.Touch(context.Background(), "s1"))
	after, _ := svc.Get(context.Background(), "s1")

	assert.True(t, !after.LastSeenAt.Before(before.LastSeenAt))
}

func TestSessionService_GetNotFound(t *testing.T) {
	db := setupDB(t)
	svc := runtime.NewSessionService(store.NewSQLiteSessionStore(db))

	_, err := svc.Get(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, model.ErrSessionNotFound)
}
