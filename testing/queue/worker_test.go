package queue_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"iclude/internal/queue"
)

// countingHandler 记录调用次数的测试处理器 / Test handler that counts invocations.
type countingHandler struct {
	count atomic.Int32
}

func (h *countingHandler) Handle(_ context.Context, _ json.RawMessage) error {
	h.count.Add(1)
	return nil
}

// TestWorkerProcessesTask worker 处理任务时应调用处理器 / Worker should invoke the handler when processing a task.
func TestWorkerProcessesTask(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	w := queue.NewWorker(q, 5*time.Minute)
	handler := &countingHandler{}
	w.RegisterHandler("send_email", handler)

	payload := json.RawMessage(`{"to":"user@example.com"}`)
	_, err := q.Enqueue(ctx, "send_email", payload)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}

	if got := handler.count.Load(); got != 1 {
		t.Errorf("handler invocation count: got %d, want 1", got)
	}
}

// TestWorkerSkipsUnknownType 没有注册处理器的任务类型应使任务走失败重试流程 /
// Unknown task type (no registered handler) should cause the task to be failed/retried.
func TestWorkerSkipsUnknownType(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	w := queue.NewWorker(q, 5*time.Minute)
	// 不注册任何处理器 / Register no handlers

	_, err := q.Enqueue(ctx, "unknown_type", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}

	// 任务应被 Fail 后重置为 pending（retry_count < max_retries），可再次 Poll /
	// Task should be reset to pending after Fail (retry_count < max_retries), so re-pollable.
	task, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll after unknown type handling failed: %v", err)
	}
	if task == nil {
		t.Fatal("expected task to be re-enqueued (pending) after unknown type failure, got nil")
	}
	// 完成该任务以免影响其他测试 / Complete the task so it doesn't interfere
	if err := q.Complete(ctx, task.ID); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
}

// TestWorkerEmptyQueue 空队列时 RunOnce 应返回 nil / RunOnce on empty queue should return nil.
func TestWorkerEmptyQueue(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	w := queue.NewWorker(q, 5*time.Minute)

	if err := w.RunOnce(ctx); err != nil {
		t.Errorf("RunOnce on empty queue returned error: %v", err)
	}
}

// TestWorkerRunProcessesMultipleTasks Run 应在一次 tick 中处理多个任务 /
// Run should process multiple tasks in a single tick.
func TestWorkerRunProcessesMultipleTasks(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	w := queue.NewWorker(q, 5*time.Minute)
	handler := &countingHandler{}
	w.RegisterHandler("batch_task", handler)

	for i := 0; i < 3; i++ {
		if _, err := q.Enqueue(ctx, "batch_task", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("Enqueue %d failed: %v", i, err)
		}
	}

	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if got := handler.count.Load(); got != 3 {
		t.Errorf("handler invocation count: got %d, want 3", got)
	}
}
