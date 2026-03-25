// Package scheduler_test 后台调度器测试 / Background scheduler tests
package scheduler_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"iclude/internal/scheduler"

	"github.com/stretchr/testify/assert"
)

// TestScheduler_NoTasks 无任务时 Run 立即返回 / Run exits immediately with no tasks
func TestScheduler_NoTasks(t *testing.T) {
	s := scheduler.New()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// OK: returned immediately
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run with no tasks should return immediately")
	}
}

// TestScheduler_TaskExecutes 注册任务在触发间隔后执行 / Registered task executes after interval
func TestScheduler_TaskExecutes(t *testing.T) {
	s := scheduler.New()
	var callCount atomic.Int32

	s.Register("test-task", 50*time.Millisecond, func(ctx context.Context) error {
		callCount.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go s.Run(ctx)
	<-ctx.Done()

	assert.GreaterOrEqual(t, int(callCount.Load()), 1, "task should have run at least once")
}

// TestScheduler_GracefulShutdown ctx 取消后任务停止 / Tasks stop after ctx cancellation
func TestScheduler_GracefulShutdown(t *testing.T) {
	s := scheduler.New()
	var running atomic.Bool

	s.Register("long-task", 20*time.Millisecond, func(ctx context.Context) error {
		running.Store(true)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// 等任务跑起来
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK: scheduler shut down
	case <-time.After(500 * time.Millisecond):
		t.Fatal("scheduler should shut down within 500ms after context cancel")
	}
}

// TestScheduler_OverlapPrevention 同一任务不重叠执行 / Same task never runs concurrently
func TestScheduler_OverlapPrevention(t *testing.T) {
	s := scheduler.New()
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	s.Register("concurrent-task", 10*time.Millisecond, func(ctx context.Context) error {
		n := concurrent.Add(1)
		// 记录最大并发数
		for {
			old := maxConcurrent.Load()
			if n <= old || maxConcurrent.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond) // 故意慢于间隔，触发重叠保护
		concurrent.Add(-1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	go s.Run(ctx)
	<-ctx.Done()

	assert.Equal(t, int32(1), maxConcurrent.Load(), "task should never run concurrently")
}

// TestScheduler_TaskError 任务报错后调度器继续运行 / Scheduler continues after task error
func TestScheduler_TaskError(t *testing.T) {
	s := scheduler.New()
	var callCount atomic.Int32

	s.Register("error-task", 30*time.Millisecond, func(ctx context.Context) error {
		callCount.Add(1)
		return errors.New("simulated task failure")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	go s.Run(ctx)
	<-ctx.Done()

	assert.GreaterOrEqual(t, int(callCount.Load()), 2, "scheduler should keep running after task error")
}

// TestScheduler_MultipleTasksIndependent 多任务独立调度互不影响 / Multiple tasks run independently
func TestScheduler_MultipleTasksIndependent(t *testing.T) {
	s := scheduler.New()
	var countA, countB atomic.Int32

	s.Register("task-a", 40*time.Millisecond, func(ctx context.Context) error {
		countA.Add(1)
		return nil
	})
	s.Register("task-b", 60*time.Millisecond, func(ctx context.Context) error {
		countB.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go s.Run(ctx)
	<-ctx.Done()

	assert.GreaterOrEqual(t, int(countA.Load()), 2, "task-a should run at least twice")
	assert.GreaterOrEqual(t, int(countB.Load()), 1, "task-b should run at least once")
}

// TestScheduler_Wait Wait 超时正常返回 / Wait returns after timeout
func TestScheduler_Wait(t *testing.T) {
	s := scheduler.New()
	start := time.Now()
	s.Wait(50 * time.Millisecond) // no tasks → wg.Wait() returns immediately
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 200*time.Millisecond)
}
