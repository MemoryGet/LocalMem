package queue_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"iclude/internal/queue"

	_ "modernc.org/sqlite"
)

// setupTestDB 创建内存 SQLite 数据库并建表 / Create in-memory SQLite DB and create table
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := queue.CreateTable(db); err != nil {
		t.Fatalf("failed to create async_tasks table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestEnqueueAndPoll 入队后 Poll 应返回任务且状态为 processing / Enqueue then Poll should return task with status processing
func TestEnqueueAndPoll(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	payload := json.RawMessage(`{"key":"value"}`)
	id, err := q.Enqueue(ctx, "send_email", payload)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if id == "" {
		t.Fatal("Enqueue returned empty ID")
	}

	task, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}
	if task == nil {
		t.Fatal("Poll returned nil task, expected a task")
	}
	if task.ID != id {
		t.Errorf("Poll returned wrong task ID: got %s, want %s", task.ID, id)
	}
	if task.Status != "processing" {
		t.Errorf("Poll task status: got %s, want processing", task.Status)
	}
	if task.Type != "send_email" {
		t.Errorf("Poll task type: got %s, want send_email", task.Type)
	}
}

// TestPollEmpty 空队列 Poll 应返回 nil 和 nil error / Poll on empty queue should return nil, nil
func TestPollEmpty(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	task, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll on empty queue returned error: %v", err)
	}
	if task != nil {
		t.Errorf("Poll on empty queue returned non-nil task: %+v", task)
	}
}

// TestComplete 完成任务后不应再被 Poll 到 / Completed task should not be polled again
func TestComplete(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	_, err := q.Enqueue(ctx, "process_data", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	task, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task from Poll")
	}

	if err := q.Complete(ctx, task.ID); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// 再次 Poll 应该返回 nil / Poll again should return nil
	next, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("second Poll failed: %v", err)
	}
	if next != nil {
		t.Errorf("expected no more tasks after Complete, got: %+v", next)
	}
}

// TestFailAndRetry 失败重试直到 max_retries 耗尽后任务应变为 failed / Fail retries until max_retries exhausted then status becomes failed
func TestFailAndRetry(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	_, err := q.Enqueue(ctx, "risky_task", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// 默认 max_retries=3，失败3次后应不可再 Poll / default max_retries=3, after 3 failures should not be pollable
	for i := 0; i < 3; i++ {
		task, err := q.Poll(ctx)
		if err != nil {
			t.Fatalf("Poll attempt %d failed: %v", i+1, err)
		}
		if task == nil {
			t.Fatalf("Poll attempt %d returned nil, expected a task", i+1)
		}
		if err := q.Fail(ctx, task.ID, "some error"); err != nil {
			t.Fatalf("Fail attempt %d failed: %v", i+1, err)
		}
	}

	// 耗尽重试次数后 Poll 应返回 nil / After exhausting retries Poll should return nil
	final, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("final Poll failed: %v", err)
	}
	if final != nil {
		t.Errorf("expected no pollable tasks after max retries exhausted, got: %+v", final)
	}
}

// TestResetStale 超时的 processing 任务应被重置为 pending / Stale processing tasks should be reset to pending
func TestResetStale(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	_, err := q.Enqueue(ctx, "slow_task", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Poll 使其变为 processing / Poll to make it processing
	task, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task")
	}

	// 手动将 updated_at 设为过去 / Manually backdate updated_at to simulate stale
	_, err = db.ExecContext(ctx,
		`UPDATE async_tasks SET updated_at = datetime('now', '-10 minutes') WHERE id = ?`,
		task.ID,
	)
	if err != nil {
		t.Fatalf("failed to backdate updated_at: %v", err)
	}

	// ResetStale 超时5分钟 / Reset tasks stale for more than 5 minutes
	count, err := q.ResetStale(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ResetStale failed: %v", err)
	}
	if count != 1 {
		t.Errorf("ResetStale returned count=%d, want 1", count)
	}

	// 应该可以再次 Poll / Should be pollable again
	repoll, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll after ResetStale failed: %v", err)
	}
	if repoll == nil {
		t.Fatal("expected task to be re-pollable after ResetStale")
	}
	if repoll.ID != task.ID {
		t.Errorf("expected same task ID after reset, got %s, want %s", repoll.ID, task.ID)
	}
}
