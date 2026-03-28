// Package queue 提供基于 SQLite 的持久化异步任务队列 / Package queue provides a persistent async task queue backed by SQLite.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Task 表示队列中的一个异步任务 / Task represents an async task in the queue.
type Task struct {
	// ID 任务唯一标识 / Unique task identifier
	ID string
	// Type 任务类型，用于路由到对应处理器 / Task type, used to route to the correct handler
	Type string
	// Payload 任务数据，JSON 格式 / Task data in JSON format
	Payload json.RawMessage
	// Status 任务状态：pending | processing | completed | failed / Task status
	Status string
	// RetryCount 已重试次数 / Number of retries already attempted
	RetryCount int
	// MaxRetries 最大重试次数 / Maximum number of retries allowed
	MaxRetries int
	// ErrorMsg 最后一次失败的错误信息 / Error message from the last failure
	ErrorMsg string
	// CreatedAt 任务创建时间 / Task creation time
	CreatedAt time.Time
	// UpdatedAt 任务最后更新时间 / Task last update time
	UpdatedAt time.Time
	// ScheduledAt 任务最早可被调度时间，NULL 表示立即可调度 / Earliest time the task may be scheduled; NULL means immediately
	ScheduledAt *time.Time
	// CompletedAt 任务完成时间 / Task completion time
	CompletedAt *time.Time
}

// Queue 封装 SQLite 数据库句柄，提供任务队列操作 / Queue wraps a *sql.DB and provides task queue operations.
type Queue struct {
	db *sql.DB
}

// CreateTable 幂等建表：创建 async_tasks 表及相关索引 / Idempotent table creation: creates async_tasks table and indexes.
func CreateTable(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS async_tasks (
			id          TEXT PRIMARY KEY,
			type        TEXT NOT NULL,
			payload     TEXT NOT NULL DEFAULT '{}',
			status      TEXT NOT NULL DEFAULT 'pending',
			retry_count INTEGER NOT NULL DEFAULT 0,
			max_retries INTEGER NOT NULL DEFAULT 3,
			error_msg   TEXT NOT NULL DEFAULT '',
			created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at  DATETIME NOT NULL DEFAULT (datetime('now')),
			scheduled_at DATETIME,
			completed_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_async_tasks_status_created ON async_tasks(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_async_tasks_scheduled_at ON async_tasks(scheduled_at) WHERE scheduled_at IS NOT NULL`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create async_tasks: %w", err)
		}
	}
	return nil
}

// New 构造 Queue 实例 / Construct a new Queue instance.
func New(db *sql.DB) *Queue {
	return &Queue{db: db}
}

// Enqueue 将新任务加入队列，返回任务 ID / Enqueue a new task, returning its ID.
// 任务初始状态为 pending，scheduled_at 为 NULL（立即可调度） / Initial status is pending; scheduled_at is NULL (immediately schedulable).
func (q *Queue) Enqueue(ctx context.Context, taskType string, payload json.RawMessage) (string, error) {
	id := uuid.New().String()
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	_, err := q.db.ExecContext(ctx,
		`INSERT INTO async_tasks (id, type, payload, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'pending', datetime('now'), datetime('now'))`,
		id, taskType, string(payload),
	)
	if err != nil {
		return "", fmt.Errorf("enqueue task: %w", err)
	}
	return id, nil
}

// Poll 原子地取出下一个可处理的 pending 任务并标记为 processing / Atomically dequeue the next pending task and mark it processing.
// 返回 nil, nil 表示队列为空 / Returns nil, nil when the queue is empty.
// 尊重 scheduled_at：仅返回 scheduled_at IS NULL 或 scheduled_at <= now 的任务 / Respects scheduled_at: only returns tasks where scheduled_at IS NULL or <= now.
func (q *Queue) Poll(ctx context.Context) (*Task, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("poll begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var task Task
	var payloadStr, errorMsg string
	var scheduledAtStr, completedAtStr sql.NullString
	var createdAtStr, updatedAtStr string

	err = tx.QueryRowContext(ctx,
		`SELECT id, type, payload, status, retry_count, max_retries, error_msg,
		        created_at, updated_at, scheduled_at, completed_at
		 FROM async_tasks
		 WHERE status = 'pending'
		   AND (scheduled_at IS NULL OR scheduled_at <= datetime('now'))
		 ORDER BY created_at ASC
		 LIMIT 1`,
	).Scan(
		&task.ID, &task.Type, &payloadStr, &task.Status,
		&task.RetryCount, &task.MaxRetries, &errorMsg,
		&createdAtStr, &updatedAtStr, &scheduledAtStr, &completedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("poll select: %w", err)
	}

	task.Payload = json.RawMessage(payloadStr)
	task.ErrorMsg = errorMsg

	// 解析时间字符串 / Parse time strings
	parseTime := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02 15:04:05", s)
		return t
	}
	task.CreatedAt = parseTime(createdAtStr)
	task.UpdatedAt = parseTime(updatedAtStr)

	if scheduledAtStr.Valid && scheduledAtStr.String != "" {
		t := parseTime(scheduledAtStr.String)
		task.ScheduledAt = &t
	}
	if completedAtStr.Valid && completedAtStr.String != "" {
		t := parseTime(completedAtStr.String)
		task.CompletedAt = &t
	}

	// 标记为 processing / Mark as processing
	_, err = tx.ExecContext(ctx,
		`UPDATE async_tasks SET status = 'processing', updated_at = datetime('now') WHERE id = ?`,
		task.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("poll update: %w", err)
	}
	task.Status = "processing"

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("poll commit: %w", err)
	}
	return &task, nil
}

// Complete 将任务标记为已完成 / Mark a task as completed.
func (q *Queue) Complete(ctx context.Context, id string) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE async_tasks
		 SET status = 'completed', completed_at = datetime('now'), updated_at = datetime('now')
		 WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("complete task %s: %w", id, err)
	}
	return nil
}

// Fail 记录任务失败：retry_count++；若未超过 max_retries 则重置为 pending，否则标记为 failed /
// Record task failure: increment retry_count; reset to pending if under max_retries, else mark failed.
func (q *Queue) Fail(ctx context.Context, id, errMsg string) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fail begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var retryCount, maxRetries int
	if err := tx.QueryRowContext(ctx,
		`SELECT retry_count, max_retries FROM async_tasks WHERE id = ?`, id,
	).Scan(&retryCount, &maxRetries); err != nil {
		return fmt.Errorf("fail query task %s: %w", id, err)
	}

	retryCount++
	var newStatus string
	if retryCount < maxRetries {
		// 还有重试次数，重置为 pending / Still has retries, reset to pending
		newStatus = "pending"
	} else {
		// 重试耗尽，标记为 failed / Retries exhausted, mark as failed
		newStatus = "failed"
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE async_tasks
		 SET status = ?, retry_count = ?, error_msg = ?, updated_at = datetime('now')
		 WHERE id = ?`,
		newStatus, retryCount, errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("fail update task %s: %w", id, err)
	}

	return tx.Commit()
}

// ResetStale 将超时的 processing 任务重置为 pending，返回重置数量 /
// Reset processing tasks whose updated_at is older than timeout back to pending; returns the reset count.
func (q *Queue) ResetStale(ctx context.Context, timeout time.Duration) (int, error) {
	cutoff := time.Now().Add(-timeout)
	cutoffStr := cutoff.UTC().Format("2006-01-02 15:04:05")

	res, err := q.db.ExecContext(ctx,
		`UPDATE async_tasks
		 SET status = 'pending', updated_at = datetime('now')
		 WHERE status = 'processing' AND updated_at < ?`,
		cutoffStr,
	)
	if err != nil {
		return 0, fmt.Errorf("reset stale tasks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reset stale rows affected: %w", err)
	}
	return int(n), nil
}
