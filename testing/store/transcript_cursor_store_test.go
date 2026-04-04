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

func setupCursorDB(t *testing.T) (*sql.DB, store.SessionStore, store.TranscriptCursorStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	tok := tokenizer.NewNoopTokenizer()
	require.NoError(t, store.Migrate(db, tok))
	return db, store.NewSQLiteSessionStore(db), store.NewSQLiteTranscriptCursorStore(db)
}

func TestTranscriptCursorStore_UpsertAndGet(t *testing.T) {
	db, ss, cs := setupCursorDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")

	cursor := &model.TranscriptCursor{
		SessionID:  "sess-1",
		SourcePath: "/tmp/transcript.jsonl",
		ByteOffset: 4096,
		LastTurnID: "turn-42",
	}
	require.NoError(t, cs.Upsert(context.Background(), cursor))

	got, err := cs.Get(context.Background(), "sess-1", "/tmp/transcript.jsonl")
	require.NoError(t, err)
	assert.Equal(t, int64(4096), got.ByteOffset)
	assert.Equal(t, "turn-42", got.LastTurnID)
}

func TestTranscriptCursorStore_UpsertOverwrite(t *testing.T) {
	db, ss, cs := setupCursorDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")

	require.NoError(t, cs.Upsert(context.Background(), &model.TranscriptCursor{
		SessionID: "sess-1", SourcePath: "/tmp/t.jsonl", ByteOffset: 100,
	}))

	require.NoError(t, cs.Upsert(context.Background(), &model.TranscriptCursor{
		SessionID: "sess-1", SourcePath: "/tmp/t.jsonl", ByteOffset: 500, LastTurnID: "turn-99",
	}))

	got, err := cs.Get(context.Background(), "sess-1", "/tmp/t.jsonl")
	require.NoError(t, err)
	assert.Equal(t, int64(500), got.ByteOffset)
	assert.Equal(t, "turn-99", got.LastTurnID)
}

func TestTranscriptCursorStore_GetNotFound(t *testing.T) {
	db, _, cs := setupCursorDB(t)
	defer db.Close()

	_, err := cs.Get(context.Background(), "sess-1", "/nonexistent")
	assert.ErrorIs(t, err, model.ErrCursorNotFound)
}

func TestTranscriptCursorStore_ListBySession(t *testing.T) {
	db, ss, cs := setupCursorDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")
	createTestSession(t, ss, "sess-2")

	require.NoError(t, cs.Upsert(context.Background(), &model.TranscriptCursor{
		SessionID: "sess-1", SourcePath: "/a.jsonl", ByteOffset: 100,
	}))
	require.NoError(t, cs.Upsert(context.Background(), &model.TranscriptCursor{
		SessionID: "sess-1", SourcePath: "/b.jsonl", ByteOffset: 200,
	}))
	require.NoError(t, cs.Upsert(context.Background(), &model.TranscriptCursor{
		SessionID: "sess-2", SourcePath: "/c.jsonl", ByteOffset: 300,
	}))

	result, err := cs.ListBySession(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Len(t, result, 2, "sess-1 should have 2 cursors")
}

func TestTranscriptCursorStore_DeleteBySession(t *testing.T) {
	db, ss, cs := setupCursorDB(t)
	defer db.Close()
	createTestSession(t, ss, "sess-1")
	now := time.Now().Truncate(time.Second)
	_ = now

	require.NoError(t, cs.Upsert(context.Background(), &model.TranscriptCursor{
		SessionID: "sess-1", SourcePath: "/a.jsonl", ByteOffset: 100,
	}))
	require.NoError(t, cs.Upsert(context.Background(), &model.TranscriptCursor{
		SessionID: "sess-1", SourcePath: "/b.jsonl", ByteOffset: 200,
	}))

	require.NoError(t, cs.DeleteBySession(context.Background(), "sess-1"))

	_, err := cs.Get(context.Background(), "sess-1", "/a.jsonl")
	assert.ErrorIs(t, err, model.ErrCursorNotFound)

	result, err := cs.ListBySession(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Len(t, result, 0)
}
