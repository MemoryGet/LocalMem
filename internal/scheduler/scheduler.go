// Package scheduler 后台任务调度器 / Background task scheduler
// 进程内 goroutine + ticker 模式，支持优雅关机和重叠防护
package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// Task 定时任务定义 / Scheduled task definition
type Task struct {
	Name     string                             // 任务名称 / Task name
	Interval time.Duration                      // 执行间隔 / Execution interval
	Fn       func(ctx context.Context) error    // 任务函数 / Task function
	running  atomic.Bool                        // 防重叠执行 / Overlap prevention
}

// Scheduler 后台任务调度器 / Background task scheduler
type Scheduler struct {
	tasks  []*Task
	wg     sync.WaitGroup
	logger *zap.Logger
}

// New 创建调度器 / Create a new scheduler
func New() *Scheduler {
	return &Scheduler{
		logger: logger.GetLogger(),
	}
}

// Register 注册定时任务 / Register a periodic task
// 必须在 Run 之前调用
func (s *Scheduler) Register(name string, interval time.Duration, fn func(ctx context.Context) error) {
	s.tasks = append(s.tasks, &Task{
		Name:     name,
		Interval: interval,
		Fn:       fn,
	})
	s.logger.Info("scheduler: task registered",
		zap.String("task", name),
		zap.Duration("interval", interval),
	)
}

// Run 启动所有定时任务 / Start all scheduled tasks
// 阻塞直到 ctx 被取消
func (s *Scheduler) Run(ctx context.Context) {
	if len(s.tasks) == 0 {
		s.logger.Info("scheduler: no tasks registered, exiting")
		return
	}

	s.logger.Info("scheduler: starting", zap.Int("tasks", len(s.tasks)))

	for _, t := range s.tasks {
		s.wg.Add(1)
		go s.runTask(ctx, t)
	}

	s.wg.Wait()
	s.logger.Info("scheduler: all tasks stopped")
}

// Wait 等待所有任务完成（带超时）/ Wait for all tasks to finish with timeout
func (s *Scheduler) Wait(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		s.logger.Warn("scheduler: wait timeout, some tasks may still be running",
			zap.Duration("timeout", timeout),
		)
	}
}

// runTask 运行单个定时任务 / Run a single periodic task
func (s *Scheduler) runTask(ctx context.Context, t *Task) {
	defer s.wg.Done()

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	s.logger.Info("scheduler: task started", zap.String("task", t.Name))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler: task stopping", zap.String("task", t.Name))
			return
		case <-ticker.C:
			if !t.running.CompareAndSwap(false, true) {
				s.logger.Debug("scheduler: task still running, skipping",
					zap.String("task", t.Name),
				)
				continue
			}

			start := time.Now()
			if err := t.Fn(ctx); err != nil {
				s.logger.Warn("scheduler: task failed",
					zap.String("task", t.Name),
					zap.Error(err),
					zap.Duration("duration", time.Since(start)),
				)
			} else {
				s.logger.Debug("scheduler: task completed",
					zap.String("task", t.Name),
					zap.Duration("duration", time.Since(start)),
				)
			}
			t.running.Store(false)
		}
	}
}
