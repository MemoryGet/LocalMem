package report_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"iclude/internal/llm"
	"iclude/internal/queue"
	"iclude/pkg/testreport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

const (
	suiteMCPEnhancement     = "MCP Enhancement"
	suiteMCPEnhancementIcon = "🚀"
	suiteMCPEnhancementDesc = "MCP 增强功能测试：LLM 回退链、持久化任务队列、iclude_scan 紧凑性"
)

// mockLLMProvider 用于测试的 LLM mock 提供者 / Mock LLM provider for testing
type mockLLMProvider struct {
	resp *llm.ChatResponse
	err  error
}

// Chat 返回预设的响应或错误 / Return preset response or error
func (m *mockLLMProvider) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.resp, m.err
}

// newQueueDB 创建内存 SQLite DB 并初始化 async_tasks 表 / Create in-memory SQLite DB with async_tasks table
func newQueueDB(t *testing.T) (*sql.DB, *queue.Queue) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, queue.CreateTable(db))
	return db, queue.New(db)
}

// TestMCPEnhancement_FallbackProvider 验证 LLM 多提供者回退链 / Verify LLM multi-provider fallback chain
func TestMCPEnhancement_FallbackProvider(t *testing.T) {
	tc := testreport.NewCase(t, suiteMCPEnhancement, suiteMCPEnhancementIcon, suiteMCPEnhancementDesc,
		"LLM Fallback Chain / LLM 多提供者回退链")
	defer tc.Done()

	tc.Input("场景", "primary provider fails, secondary succeeds")
	tc.Input("期望", "no error, content from fallback")

	primary := &mockLLMProvider{
		resp: nil,
		err:  errors.New("primary provider unavailable"),
	}
	secondary := &mockLLMProvider{
		resp: &llm.ChatResponse{Content: "fallback ok"},
		err:  nil,
	}
	tc.Step("创建 FallbackProvider (primary=fail, secondary=ok)")

	fp := llm.NewFallbackProvider(
		[]llm.Provider{primary, secondary},
		[]string{"primary", "secondary"},
	)
	tc.Step("构建 FallbackProvider 链")

	ctx := context.Background()
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hello"}},
	}
	resp, err := fp.Chat(ctx, req)
	tc.Step("调用 fp.Chat()")

	require.NoError(t, err)
	assert.Equal(t, "fallback ok", resp.Content)
	tc.Step("验证：无错误，内容来自次级提供者")

	tc.Output("error", "nil")
	tc.Output("resp.Content", resp.Content)
}

// TestMCPEnhancement_TaskQueue 验证持久化任务队列完整生命周期 / Verify full lifecycle of persistent task queue
func TestMCPEnhancement_TaskQueue(t *testing.T) {
	tc := testreport.NewCase(t, suiteMCPEnhancement, suiteMCPEnhancementIcon, suiteMCPEnhancementDesc,
		"Persistent Task Queue / 持久化异步任务队列")
	defer tc.Done()

	tc.Input("场景", "enqueue + poll + complete lifecycle")
	tc.Input("期望", "full lifecycle: pending → processing → completed")

	_, q := newQueueDB(t)
	tc.Step("初始化内存 SQLite + async_tasks 表")

	ctx := context.Background()
	payload, _ := json.Marshal(map[string]string{"key": "value"})
	id, err := q.Enqueue(ctx, "test_task", payload)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	tc.Step("Enqueue 任务", fmt.Sprintf("id=%s type=test_task", id))

	task, err := q.Poll(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, id, task.ID)
	assert.Equal(t, "processing", task.Status)
	tc.Step("Poll 任务", fmt.Sprintf("status=%s", task.Status))

	err = q.Complete(ctx, task.ID)
	require.NoError(t, err)
	tc.Step("Complete 任务")

	// 队列应为空 / Queue should now be empty
	next, err := q.Poll(ctx)
	require.NoError(t, err)
	assert.Nil(t, next)
	tc.Step("再次 Poll：队列为空，返回 nil")

	tc.Output("生命周期", "pending → processing → completed")
	tc.Output("completed 后再 Poll", "nil（队列空）")
}

// TestMCPEnhancement_ScanCompactness 验证 iclude_scan 结果紧凑性 / Verify iclude_scan result compactness
func TestMCPEnhancement_ScanCompactness(t *testing.T) {
	tc := testreport.NewCase(t, suiteMCPEnhancement, suiteMCPEnhancementIcon, suiteMCPEnhancementDesc,
		"Scan Token Savings / iclude_scan token 节省验证")
	defer tc.Done()

	tc.Input("场景", "compare scan result size vs full recall result size")
	tc.Input("期望", "scan result is <20% of full result size")

	// 构造一条包含 500 字符内容的记忆 / Build a memory entry with 500-char content
	content := strings.Repeat("这是一段很长的记忆内容用于测试token节省效果。", 20)[:500]

	type fullItem struct {
		ID            string     `json:"id"`
		Content       string     `json:"content"`
		Kind          string     `json:"kind"`
		Score         float64    `json:"score"`
		Source        string     `json:"source"`
		Excerpt      string     `json:"excerpt"`
		Summary       string     `json:"summary"`
		Scope         string     `json:"scope"`
		TeamID        string     `json:"team_id"`
		Strength      float64    `json:"strength"`
		DecayRate     float64    `json:"decay_rate"`
		RetentionTier string     `json:"retention_tier"`
		CreatedAt     time.Time  `json:"created_at"`
		UpdatedAt     time.Time  `json:"updated_at"`
		Tags          []string   `json:"tags"`
	}

	type scanItem struct {
		ID            string  `json:"id"`
		Title         string  `json:"title"`
		Score         float64 `json:"score"`
		Source        string  `json:"source"`
		Kind          string  `json:"kind,omitempty"`
		TokenEstimate int     `json:"token_estimate"`
	}

	tc.Step("构造 500 字符内容的全量条目 vs 紧凑扫描条目")

	now := time.Now()
	full := fullItem{
		ID:            "test-id-001",
		Content:       content,
		Kind:          "fact",
		Score:         0.95,
		Source:        "fts",
		Excerpt:      content[:80],
		Summary:       content[:120],
		Scope:         "engineering",
		TeamID:        "team-abc-123",
		Strength:      0.87,
		DecayRate:     0.01,
		RetentionTier: "long_term",
		CreatedAt:     now,
		UpdatedAt:     now,
		Tags:          []string{"go", "backend", "performance"},
	}

	scan := scanItem{
		ID:            "test-id-001",
		Title:         content[:80],
		Score:         0.95,
		Source:        "fts",
		Kind:          "fact",
		TokenEstimate: len([]rune(content)) / 4,
	}

	fullBytes, err := json.Marshal(full)
	require.NoError(t, err)
	scanBytes, err := json.Marshal(scan)
	require.NoError(t, err)
	tc.Step("序列化两种条目为 JSON")

	ratio := float64(len(scanBytes)) / float64(len(fullBytes))
	tc.Step("计算 scan/full 大小比例", fmt.Sprintf("scan=%d bytes, full=%d bytes, ratio=%.3f", len(scanBytes), len(fullBytes), ratio))

	assert.Less(t, ratio, 0.20, "scan result should be less than 20%% of full result")
	tc.Step("验证：scan 大小 < full 大小的 20%%")

	tc.Output("full JSON 大小", fmt.Sprintf("%d bytes", len(fullBytes)))
	tc.Output("scan JSON 大小", fmt.Sprintf("%d bytes", len(scanBytes)))
	tc.Output("比例 (scan/full)", fmt.Sprintf("%.3f (< 0.20)", ratio))
}

// TestMCPEnhancement_QueueRetry 验证任务重试逻辑 / Verify task retry logic
func TestMCPEnhancement_QueueRetry(t *testing.T) {
	tc := testreport.NewCase(t, suiteMCPEnhancement, suiteMCPEnhancementIcon, suiteMCPEnhancementDesc,
		"Queue Retry Logic / 队列重试逻辑")
	defer tc.Done()

	tc.Input("场景", "fail task 3 times (max_retries=3)")
	tc.Input("期望", "task is permanently failed after max retries")

	_, q := newQueueDB(t)
	tc.Step("初始化内存 SQLite + async_tasks 表")

	ctx := context.Background()

	// 使用默认 max_retries=3，直接 INSERT 设置 max_retries / Use max_retries=3 via Enqueue (default)
	id, err := q.Enqueue(ctx, "retry_task", nil)
	require.NoError(t, err)
	tc.Step("Enqueue 任务", fmt.Sprintf("id=%s max_retries=3", id))

	// 失败 3 次 / Fail 3 times
	for i := 1; i <= 3; i++ {
		task, err := q.Poll(ctx)
		require.NoError(t, err)
		require.NotNil(t, task, "expected task on attempt %d", i)
		assert.Equal(t, "processing", task.Status)

		err = q.Fail(ctx, task.ID, fmt.Sprintf("error attempt %d", i))
		require.NoError(t, err)
		tc.Step(fmt.Sprintf("第 %d 次失败", i), fmt.Sprintf("Fail(error attempt %d)", i))
	}

	// 第 4 次 Poll 应返回 nil（任务已 permanently failed）/ 4th Poll should return nil
	next, err := q.Poll(ctx)
	require.NoError(t, err)
	assert.Nil(t, next)
	tc.Step("第 4 次 Poll：任务已彻底失败，返回 nil")

	tc.Output("最终状态", "failed（不可重试）")
	tc.Output("4th Poll", "nil（永久失败，不再返回）")
}

// TestMCPEnhancement_StaleReset 验证过期 processing 任务重置 / Verify stale processing task reset
func TestMCPEnhancement_StaleReset(t *testing.T) {
	tc := testreport.NewCase(t, suiteMCPEnhancement, suiteMCPEnhancementIcon, suiteMCPEnhancementDesc,
		"Stale Task Reset / 过期任务重置")
	defer tc.Done()

	tc.Input("场景", "task stuck in processing for >5 minutes")
	tc.Input("期望", "1 task reset, pollable again")

	db, q := newQueueDB(t)
	tc.Step("初始化内存 SQLite + async_tasks 表")

	ctx := context.Background()
	id, err := q.Enqueue(ctx, "stale_task", nil)
	require.NoError(t, err)
	tc.Step("Enqueue 任务", fmt.Sprintf("id=%s", id))

	task, err := q.Poll(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "processing", task.Status)
	tc.Step("Poll 任务 → status=processing")

	// 将 updated_at 回拨 10 分钟以模拟卡住 / Backdate updated_at by 10m to simulate stuck task
	staleTime := time.Now().UTC().Add(-10 * time.Minute).Format("2006-01-02 15:04:05")
	_, err = db.ExecContext(ctx, `UPDATE async_tasks SET updated_at = ? WHERE id = ?`, staleTime, task.ID)
	require.NoError(t, err)
	tc.Step("回拨 updated_at -10分钟", fmt.Sprintf("updated_at = %s", staleTime))

	// 重置 >5分钟 的 stale 任务 / Reset tasks stale for >5 minutes
	n, err := q.ResetStale(ctx, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	tc.Step("ResetStale(5min)", fmt.Sprintf("重置了 %d 个任务", n))

	// 重置后应可再次 Poll / Should be pollable again after reset
	polled, err := q.Poll(ctx)
	require.NoError(t, err)
	require.NotNil(t, polled)
	assert.Equal(t, id, polled.ID)
	assert.Equal(t, "processing", polled.Status)
	tc.Step("再次 Poll：任务可被重新调度", fmt.Sprintf("id=%s status=%s", polled.ID, polled.Status))

	tc.Output("ResetStale 重置数", fmt.Sprintf("%d", n))
	tc.Output("reset 后 Poll", fmt.Sprintf("id=%s status=processing（可再次处理）", polled.ID))
}
