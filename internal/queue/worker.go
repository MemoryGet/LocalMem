package queue

import (
	"context"
	"fmt"
	"time"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// Worker 从队列中消费任务并分发到注册处理器 / Worker consumes tasks from the queue and dispatches to registered handlers.
type Worker struct {
	// queue 底层任务队列 / underlying task queue
	queue *Queue
	// handlers 任务类型到处理器的映射 / mapping of task type to handler
	handlers map[string]TaskHandler
	// staleTimeout 超过该时间的 processing 任务重置为 pending / processing tasks older than this are reset to pending
	staleTimeout time.Duration
}

// NewWorker 构造 Worker 实例 / Construct a new Worker instance.
func NewWorker(q *Queue, staleTimeout time.Duration) *Worker {
	return &Worker{
		queue:        q,
		handlers:     make(map[string]TaskHandler),
		staleTimeout: staleTimeout,
	}
}

// RegisterHandler 注册任务类型对应的处理器 / Register a handler for the given task type.
func (w *Worker) RegisterHandler(taskType string, handler TaskHandler) {
	w.handlers[taskType] = handler
}

// RunOnce 执行一个处理周期：重置过期任务 + 取出一个任务 + 分发给处理器 /
// Execute one processing cycle: reset stale tasks + poll one task + dispatch to handler.
// 返回 nil 表示正常（包括队列为空）/ Returns nil on success (including empty queue).
func (w *Worker) RunOnce(ctx context.Context) error {
	// 重置超时的 processing 任务 / Reset stale processing tasks
	n, err := w.queue.ResetStale(ctx, w.staleTimeout)
	if err != nil {
		logger.Warn("worker: reset stale tasks failed", zap.Error(err))
	} else if n > 0 {
		logger.Info("worker: reset stale tasks", zap.Int("count", n))
	}

	// 取出下一个任务 / Poll the next task
	task, err := w.queue.Poll(ctx)
	if err != nil {
		return fmt.Errorf("worker poll: %w", err)
	}
	if task == nil {
		// 队列为空 / Queue is empty
		return nil
	}

	logger.Info("worker: dispatching task",
		zap.String("id", task.ID),
		zap.String("type", task.Type),
		zap.Int("retry_count", task.RetryCount),
	)

	// 查找处理器 / Look up handler
	handler, ok := w.handlers[task.Type]
	if !ok {
		errMsg := fmt.Sprintf("no handler registered for task type: %s", task.Type)
		logger.Warn("worker: unknown task type", zap.String("type", task.Type), zap.String("id", task.ID))
		if failErr := w.queue.Fail(ctx, task.ID, errMsg); failErr != nil {
			logger.Error("worker: failed to fail task", zap.String("id", task.ID), zap.Error(failErr))
		}
		return nil
	}

	// 执行处理器 / Execute the handler
	if err := handler.Handle(ctx, task.Payload); err != nil {
		logger.Warn("worker: handler returned error",
			zap.String("id", task.ID),
			zap.String("type", task.Type),
			zap.Error(err),
		)
		if failErr := w.queue.Fail(ctx, task.ID, err.Error()); failErr != nil {
			logger.Error("worker: failed to fail task", zap.String("id", task.ID), zap.Error(failErr))
		}
		return nil
	}

	// 处理成功 / Handler succeeded
	if err := w.queue.Complete(ctx, task.ID); err != nil {
		logger.Error("worker: failed to complete task", zap.String("id", task.ID), zap.Error(err))
		return fmt.Errorf("worker complete task %s: %w", task.ID, err)
	}

	logger.Info("worker: task completed", zap.String("id", task.ID), zap.String("type", task.Type))
	return nil
}

// Run 每次调度 tick 最多处理 10 个任务 / Process up to 10 tasks per scheduler tick.
// 实现 scheduler.Job 签名: func(ctx context.Context) error / Implements scheduler.Job signature.
func (w *Worker) Run(ctx context.Context) error {
	const maxPerTick = 10
	for i := 0; i < maxPerTick; i++ {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := w.RunOnce(ctx); err != nil {
			return err
		}
	}
	return nil
}
