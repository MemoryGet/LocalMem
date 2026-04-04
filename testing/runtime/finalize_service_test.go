package runtime_test

import (
	"context"
	"database/sql"
	"testing"

	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/runtime"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSummarizer 测试用摘要生成器 / Test summarizer mock
type mockSummarizer struct {
	called    bool
	returnErr error
	returnRes *memory.SummarizeResponse
}

func (m *mockSummarizer) SummarizeBySourceRef(_ context.Context, _ string, _ *model.Identity) (*memory.SummarizeResponse, error) {
	m.called = true
	return m.returnRes, m.returnErr
}

func setupFinalizeEnv(t *testing.T, sum runtime.Summarizer) (*sql.DB, *runtime.SessionService, *runtime.FinalizeService) {
	t.Helper()
	db := setupDB(t)
	ss := store.NewSQLiteSessionStore(db)
	fs := store.NewSQLiteSessionFinalizeStore(db)
	is := store.NewSQLiteIdempotencyStore(db)

	sessionSvc := runtime.NewSessionService(ss)
	finalizeSvc := runtime.NewFinalizeService(ss, fs, is, sum)
	return db, sessionSvc, finalizeSvc
}

func createActiveSession(t *testing.T, svc *runtime.SessionService, id string) {
	t.Helper()
	require.NoError(t, svc.Create(context.Background(), &model.Session{ID: id, ToolName: "test"}))
	require.NoError(t, svc.MarkBootstrapped(context.Background(), id))
	require.NoError(t, svc.MarkActive(context.Background(), id))
}

func TestFinalizeService_BasicFinalize(t *testing.T) {
	_, sessSvc, finSvc := setupFinalizeEnv(t, nil)
	createActiveSession(t, sessSvc, "s1")

	resp, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "s1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:s1:v1",
	}, nil)
	require.NoError(t, err)
	assert.True(t, resp.Finalized)
	assert.NotNil(t, resp.FinalizedAt)
	assert.False(t, resp.AlreadyFinalized)

	// 验证 session 状态 / Verify session state
	got, _ := sessSvc.Get(context.Background(), "s1")
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestFinalizeService_Idempotent(t *testing.T) {
	_, sessSvc, finSvc := setupFinalizeEnv(t, nil)
	createActiveSession(t, sessSvc, "s1")

	req := &runtime.FinalizeRequest{
		SessionID:      "s1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:s1:v1",
	}

	// 第一次 finalize / First finalize
	resp1, err := finSvc.Finalize(context.Background(), req, nil)
	require.NoError(t, err)
	assert.True(t, resp1.Finalized)
	assert.False(t, resp1.AlreadyFinalized)

	// 第二次 finalize（幂等）/ Second finalize (idempotent)
	resp2, err := finSvc.Finalize(context.Background(), req, nil)
	require.NoError(t, err)
	assert.True(t, resp2.Finalized)
	assert.True(t, resp2.AlreadyFinalized)
}

func TestFinalizeService_WithSummarizer(t *testing.T) {
	sum := &mockSummarizer{
		returnRes: &memory.SummarizeResponse{
			SemanticMemory: &model.Memory{ID: "mem-summary-1"},
			SourceCount:    5,
		},
	}
	_, sessSvc, finSvc := setupFinalizeEnv(t, sum)
	createActiveSession(t, sessSvc, "s1")

	identity := &model.Identity{TeamID: "team-1", OwnerID: "user-1"}
	resp, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "s1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:s1:v1",
	}, identity)
	require.NoError(t, err)
	assert.True(t, resp.Finalized)
	assert.Equal(t, "mem-summary-1", resp.SummaryMemoryID)
	assert.True(t, sum.called)
}

func TestFinalizeService_SummarizerFailureContinues(t *testing.T) {
	sum := &mockSummarizer{
		returnErr: assert.AnError,
	}
	_, sessSvc, finSvc := setupFinalizeEnv(t, sum)
	createActiveSession(t, sessSvc, "s1")

	identity := &model.Identity{TeamID: "t", OwnerID: "u"}
	resp, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "s1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:s1:v1",
	}, identity)

	// 摘要失败不中止 finalize / Summary failure doesn't abort finalize
	require.NoError(t, err)
	assert.True(t, resp.Finalized)
	assert.Empty(t, resp.SummaryMemoryID)
}

func TestFinalizeService_RequiresSessionID(t *testing.T) {
	_, _, finSvc := setupFinalizeEnv(t, nil)

	_, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		IdempotencyKey: "k",
	}, nil)
	assert.ErrorIs(t, err, model.ErrInvalidInput)
}

func TestFinalizeService_RequiresIdempotencyKey(t *testing.T) {
	_, _, finSvc := setupFinalizeEnv(t, nil)

	_, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID: "s1",
	}, nil)
	assert.ErrorIs(t, err, model.ErrInvalidInput)
}

func TestFinalizeService_SessionNotFound(t *testing.T) {
	_, _, finSvc := setupFinalizeEnv(t, nil)

	_, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "nonexistent",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:nonexistent:v1",
	}, nil)
	assert.Error(t, err)
}

func TestFinalizeService_AlreadyFinalizedSession(t *testing.T) {
	_, sessSvc, finSvc := setupFinalizeEnv(t, nil)
	createActiveSession(t, sessSvc, "s1")

	// 先 finalize / First finalize
	_, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "s1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:s1:v1",
	}, nil)
	require.NoError(t, err)

	// 用不同 idempotency key 再次 finalize（session 已 finalized）
	resp, err := finSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "s1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:s1:v2",
	}, nil)
	require.NoError(t, err)
	assert.True(t, resp.AlreadyFinalized)
}
