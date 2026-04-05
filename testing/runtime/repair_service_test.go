package runtime_test

import (
	"context"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/runtime"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRepairEnv(t *testing.T) (*runtime.SessionService, *runtime.RepairService) {
	t.Helper()
	db := setupDB(t)
	ss := store.NewSQLiteSessionStore(db)
	fs := store.NewSQLiteSessionFinalizeStore(db)
	is := store.NewSQLiteIdempotencyStore(db)

	sessionSvc := runtime.NewSessionService(ss)
	finalizeSvc := runtime.NewFinalizeService(ss, fs, is, nil)
	repairSvc := runtime.NewRepairService(ss, finalizeSvc, runtime.RepairConfig{
		StaleDuration: 3 * time.Second, // 测试用短窗口 / Short window for testing
		MaxAttempts:   3,
		BatchSize:     10,
	})
	return sessionSvc, repairSvc
}

func TestRepairService_RepairsStaleActiveSessions(t *testing.T) {
	sessSvc, repairSvc := setupRepairEnv(t)

	// 创建一个旧的 active 会话 / Create an old active session
	require.NoError(t, sessSvc.Create(context.Background(), &model.Session{
		ID:       "stale-1",
		ToolName: "codex",
	}))
	require.NoError(t, sessSvc.MarkBootstrapped(context.Background(), "stale-1"))
	require.NoError(t, sessSvc.MarkActive(context.Background(), "stale-1"))

	// 等待超过 stale duration / Wait past stale duration
	time.Sleep(4 * time.Second)

	require.NoError(t, repairSvc.Run(context.Background()))

	// 验证已 finalized / Verify finalized
	got, err := sessSvc.Get(context.Background(), "stale-1")
	require.NoError(t, err)
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestRepairService_SkipsRecentSessions(t *testing.T) {
	sessSvc, repairSvc := setupRepairEnv(t)

	// 创建新的 active 会话 / Create a fresh active session
	require.NoError(t, sessSvc.Create(context.Background(), &model.Session{
		ID:       "fresh-1",
		ToolName: "claude-code",
	}))
	require.NoError(t, sessSvc.MarkBootstrapped(context.Background(), "fresh-1"))
	require.NoError(t, sessSvc.MarkActive(context.Background(), "fresh-1"))

	// Touch 确保 last_seen_at 是当前时间 / Touch to ensure last_seen_at is now
	require.NoError(t, sessSvc.Touch(context.Background(), "fresh-1"))

	// 立即 repair（session 不够 stale）/ Repair immediately (session not stale enough)
	require.NoError(t, repairSvc.Run(context.Background()))

	// 应该仍然是 active / Should still be active
	got, err := sessSvc.Get(context.Background(), "fresh-1")
	require.NoError(t, err)
	assert.Equal(t, model.SessionStateActive, got.State)
}

func TestRepairService_SkipsAlreadyFinalized(t *testing.T) {
	sessSvc, repairSvc := setupRepairEnv(t)

	require.NoError(t, sessSvc.Create(context.Background(), &model.Session{
		ID:       "done-1",
		ToolName: "test",
	}))
	require.NoError(t, sessSvc.MarkBootstrapped(context.Background(), "done-1"))
	require.NoError(t, sessSvc.MarkActive(context.Background(), "done-1"))
	require.NoError(t, sessSvc.MarkFinalizing(context.Background(), "done-1"))
	require.NoError(t, sessSvc.MarkFinalized(context.Background(), "done-1"))

	time.Sleep(2 * time.Second)

	// 已 finalized 的不会出现在 ListPendingFinalize 中 / Finalized sessions won't appear
	require.NoError(t, repairSvc.Run(context.Background()))

	got, _ := sessSvc.Get(context.Background(), "done-1")
	assert.Equal(t, model.SessionStateFinalized, got.State)
}

func TestRepairService_NoSessionsIsNoop(t *testing.T) {
	_, repairSvc := setupRepairEnv(t)

	// 空库不报错 / Empty DB runs without error
	require.NoError(t, repairSvc.Run(context.Background()))
}

func TestRepairService_AbandonsAfterMaxAttempts(t *testing.T) {
	db := setupDB(t)
	ss := store.NewSQLiteSessionStore(db)
	fs := store.NewSQLiteSessionFinalizeStore(db)
	is := store.NewSQLiteIdempotencyStore(db)

	sessSvc := runtime.NewSessionService(ss)

	// 使用一个永远失败的 summarizer 让 finalize 产出 Finalized=false
	// Use MaxAttempts=2 for faster test
	finalizeSvc := runtime.NewFinalizeService(ss, fs, is, nil)
	repairSvc := runtime.NewRepairService(ss, finalizeSvc, runtime.RepairConfig{
		StaleDuration: 1 * time.Second,
		MaxAttempts:   2,
		BatchSize:     10,
	})

	// 创建 session 并预设 repair_attempts 已达上限 / Pre-set repair_attempts at max
	require.NoError(t, sessSvc.Create(context.Background(), &model.Session{
		ID:       "max-retry-1",
		ToolName: "codex",
	}))
	require.NoError(t, sessSvc.MarkBootstrapped(context.Background(), "max-retry-1"))
	require.NoError(t, sessSvc.MarkActive(context.Background(), "max-retry-1"))

	// 写入 metadata: repair_attempts = 2 (= MaxAttempts) / Set attempts to max
	require.NoError(t, ss.UpdateMetadata(context.Background(), "max-retry-1", map[string]any{
		"repair_attempts": 2,
	}))

	// 等待超过 stale duration / Wait past stale duration
	time.Sleep(2 * time.Second)

	require.NoError(t, repairSvc.Run(context.Background()))

	// 验证已 abandoned / Verify abandoned
	got, err := sessSvc.Get(context.Background(), "max-retry-1")
	require.NoError(t, err)
	assert.Equal(t, model.SessionStateAbandoned, got.State)
}

func TestRepairService_IncreasesAttemptCount(t *testing.T) {
	db := setupDB(t)
	ss := store.NewSQLiteSessionStore(db)
	fs := store.NewSQLiteSessionFinalizeStore(db)
	is := store.NewSQLiteIdempotencyStore(db)

	sessSvc := runtime.NewSessionService(ss)
	finalizeSvc := runtime.NewFinalizeService(ss, fs, is, nil)
	repairSvc := runtime.NewRepairService(ss, finalizeSvc, runtime.RepairConfig{
		StaleDuration: 1 * time.Second,
		MaxAttempts:   5, // 足够高，不会 abandon / High enough to not abandon
		BatchSize:     10,
	})

	require.NoError(t, sessSvc.Create(context.Background(), &model.Session{
		ID:       "inc-1",
		ToolName: "claude-code",
	}))
	require.NoError(t, sessSvc.MarkBootstrapped(context.Background(), "inc-1"))
	require.NoError(t, sessSvc.MarkActive(context.Background(), "inc-1"))

	time.Sleep(2 * time.Second)

	// 第一次 repair — finalize 会成功（nil summarizer = 跳过摘要）
	// First repair — finalize succeeds (nil summarizer = skip summary)
	require.NoError(t, repairSvc.Run(context.Background()))

	// 验证 session 已 finalized（因为 nil summarizer 不会失败）/ Verify finalized
	got, err := sessSvc.Get(context.Background(), "inc-1")
	require.NoError(t, err)
	assert.Equal(t, model.SessionStateFinalized, got.State)

	// 验证 metadata 中 repair_attempts 为 1 / Verify repair_attempts = 1
	assert.Equal(t, 1, metadataAttempts(got))
}

// metadataAttempts 从 session metadata 提取 repair_attempts / Extract repair_attempts from metadata
func metadataAttempts(s *model.Session) int {
	if s.Metadata == nil {
		return 0
	}
	v, ok := s.Metadata["repair_attempts"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
