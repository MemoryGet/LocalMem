// Package compliance 运行时合规测试 / Runtime compliance test suite
// 验证 bootstrap → finalize → idempotency → repair 四条关键链路
package compliance_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/runtime"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// testEnv 合规测试环境 / Compliance test environment
type testEnv struct {
	db          *sql.DB
	sessionSvc  *runtime.SessionService
	finalizeSvc *runtime.FinalizeService
	repairSvc   *runtime.RepairService
	idemStore   store.IdempotencyStore
	finalStore  store.SessionFinalizeStore
}

func setupEnv(t *testing.T, sum runtime.Summarizer) *testEnv {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	tok := tokenizer.NewNoopTokenizer()
	require.NoError(t, store.Migrate(db, tok))
	t.Cleanup(func() { db.Close() })

	ss := store.NewSQLiteSessionStore(db)
	fs := store.NewSQLiteSessionFinalizeStore(db)
	is := store.NewSQLiteIdempotencyStore(db)

	sessionSvc := runtime.NewSessionService(ss)
	finalizeSvc := runtime.NewFinalizeService(ss, fs, is, sum)
	repairSvc := runtime.NewRepairService(ss, finalizeSvc, runtime.RepairConfig{
		StaleDuration: 2 * time.Second,
		MaxAttempts:   3,
		BatchSize:     10,
	})

	return &testEnv{
		db:          db,
		sessionSvc:  sessionSvc,
		finalizeSvc: finalizeSvc,
		repairSvc:   repairSvc,
		idemStore:   is,
		finalStore:  fs,
	}
}

// =============================================================================
// L1: Bootstrap — 会话创建与状态推进 / Session creation and state progression
// =============================================================================

func TestCompliance_Bootstrap_CreatesSessionInCreatedState(t *testing.T) {
	env := setupEnv(t, nil)

	sess := &model.Session{
		ID:         "boot-1",
		ToolName:   "claude-code",
		ProjectID:  "proj-1",
		ProjectDir: "/home/user/project",
	}
	require.NoError(t, env.sessionSvc.Create(context.Background(), sess))

	got, err := env.sessionSvc.Get(context.Background(), "boot-1")
	require.NoError(t, err)
	assert.Equal(t, model.SessionStateCreated, got.State)
	assert.Equal(t, "claude-code", got.ToolName)
}

func TestCompliance_Bootstrap_FullLifecycleToActive(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{
		ID: "boot-2", ToolName: "codex",
	}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "boot-2"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "boot-2"))

	got, _ := env.sessionSvc.Get(context.Background(), "boot-2")
	assert.Equal(t, model.SessionStateActive, got.State)
}

func TestCompliance_Bootstrap_CannotSkipBootstrapped(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{
		ID: "boot-3", ToolName: "cursor",
	}))

	// created → active 非法（必须经过 bootstrapped）/ Illegal: must go through bootstrapped
	err := env.sessionSvc.MarkActive(context.Background(), "boot-3")
	assert.Error(t, err)
}

// =============================================================================
// L2: Finalize — 会话终结与摘要生成 / Session finalization and summary generation
// =============================================================================

func TestCompliance_Finalize_ActiveToFinalized(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "fin-1", ToolName: "test"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "fin-1"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "fin-1"))

	resp, err := env.finalizeSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "fin-1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:fin-1:v1",
	}, nil)
	require.NoError(t, err)
	assert.True(t, resp.Finalized)
	assert.NotNil(t, resp.FinalizedAt)

	got, _ := env.sessionSvc.Get(context.Background(), "fin-1")
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestCompliance_Finalize_WithSummaryGeneration(t *testing.T) {
	sum := &mockSummarizer{
		returnRes: &memory.SummarizeResponse{
			SemanticMemory: &model.Memory{ID: "summary-mem-1"},
			SourceCount:    10,
		},
	}
	env := setupEnv(t, sum)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "fin-2", ToolName: "test"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "fin-2"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "fin-2"))

	identity := &model.Identity{TeamID: "team-1", OwnerID: "user-1"}
	resp, err := env.finalizeSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "fin-2",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:fin-2:v1",
	}, identity)
	require.NoError(t, err)
	assert.Equal(t, "summary-mem-1", resp.SummaryMemoryID)

	// 验证 finalize state 记录了 summary / Verify finalize state records summary
	st, err := env.finalStore.Get(context.Background(), "fin-2")
	require.NoError(t, err)
	assert.Equal(t, "summary-mem-1", st.SummaryMemoryID)
}

func TestCompliance_Finalize_SummaryFailureDoesNotBlock(t *testing.T) {
	sum := &mockSummarizer{returnErr: assert.AnError}
	env := setupEnv(t, sum)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "fin-3", ToolName: "test"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "fin-3"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "fin-3"))

	resp, err := env.finalizeSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID:      "fin-3",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:fin-3:v1",
	}, &model.Identity{TeamID: "t", OwnerID: "u"})
	require.NoError(t, err)
	assert.True(t, resp.Finalized, "finalize should succeed even if summarizer fails")
	assert.Empty(t, resp.SummaryMemoryID)
}

// =============================================================================
// L3: Idempotency — 幂等安全 / Idempotency safety
// =============================================================================

func TestCompliance_Idempotency_RepeatedFinalizeIsSafe(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "idem-1", ToolName: "test"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "idem-1"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "idem-1"))

	req := &runtime.FinalizeRequest{
		SessionID:      "idem-1",
		ToolName:       "test",
		IdempotencyKey: "finalize:test:idem-1:v1",
	}

	r1, err := env.finalizeSvc.Finalize(context.Background(), req, nil)
	require.NoError(t, err)
	assert.True(t, r1.Finalized)
	assert.False(t, r1.AlreadyFinalized)

	r2, err := env.finalizeSvc.Finalize(context.Background(), req, nil)
	require.NoError(t, err)
	assert.True(t, r2.Finalized)
	assert.True(t, r2.AlreadyFinalized, "second call should be idempotent")
}

func TestCompliance_Idempotency_DifferentKeyNewFinalize(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "idem-2", ToolName: "test"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "idem-2"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "idem-2"))

	// v1 finalize
	_, err := env.finalizeSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID: "idem-2", ToolName: "test", IdempotencyKey: "finalize:test:idem-2:v1",
	}, nil)
	require.NoError(t, err)

	// v2 finalize（不同 key，但 session 已 finalized）/ Different key, but session already finalized
	r2, err := env.finalizeSvc.Finalize(context.Background(), &runtime.FinalizeRequest{
		SessionID: "idem-2", ToolName: "test", IdempotencyKey: "finalize:test:idem-2:v2",
	}, nil)
	require.NoError(t, err)
	assert.True(t, r2.AlreadyFinalized, "already finalized session returns idempotent response")
}

func TestCompliance_Idempotency_StoreReserveIsSafe(t *testing.T) {
	env := setupEnv(t, nil)

	r1, err := env.idemStore.Reserve(context.Background(), "retain", "event-hash-1", "memory")
	require.NoError(t, err)
	assert.True(t, r1)

	r2, err := env.idemStore.Reserve(context.Background(), "retain", "event-hash-1", "memory")
	require.NoError(t, err)
	assert.False(t, r2, "duplicate reservation must return false")
}

// =============================================================================
// L4: Repair — 修复 stale 会话 / Repair stale sessions
// =============================================================================

func TestCompliance_Repair_FixesStaleActiveSession(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "rep-1", ToolName: "codex"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "rep-1"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "rep-1"))

	// 等待超过 stale duration / Wait past stale duration
	time.Sleep(3 * time.Second)

	require.NoError(t, env.repairSvc.Run(context.Background()))

	got, _ := env.sessionSvc.Get(context.Background(), "rep-1")
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestCompliance_Repair_FixesPendingRepairSession(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "rep-2", ToolName: "cline"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "rep-2"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "rep-2"))
	require.NoError(t, env.sessionSvc.MarkPendingRepair(context.Background(), "rep-2"))

	time.Sleep(3 * time.Second)

	require.NoError(t, env.repairSvc.Run(context.Background()))

	got, _ := env.sessionSvc.Get(context.Background(), "rep-2")
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestCompliance_Repair_DoesNotTouchRecentActive(t *testing.T) {
	env := setupEnv(t, nil)

	require.NoError(t, env.sessionSvc.Create(context.Background(), &model.Session{ID: "rep-3", ToolName: "cursor"}))
	require.NoError(t, env.sessionSvc.MarkBootstrapped(context.Background(), "rep-3"))
	require.NoError(t, env.sessionSvc.MarkActive(context.Background(), "rep-3"))
	require.NoError(t, env.sessionSvc.Touch(context.Background(), "rep-3"))

	// 不等待直接 repair / Repair immediately
	require.NoError(t, env.repairSvc.Run(context.Background()))

	got, _ := env.sessionSvc.Get(context.Background(), "rep-3")
	assert.Equal(t, model.SessionStateActive, got.State, "recent session should not be repaired")
}

// =============================================================================
// Helpers
// =============================================================================

type mockSummarizer struct {
	returnRes *memory.SummarizeResponse
	returnErr error
}

func (m *mockSummarizer) SummarizeBySourceRef(_ context.Context, _ string, _ *model.Identity) (*memory.SummarizeResponse, error) {
	return m.returnRes, m.returnErr
}
