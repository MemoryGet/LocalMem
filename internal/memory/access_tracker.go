package memory

import (
	"context"

	"iclude/internal/logger"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// AccessTracker 访问计数追踪器 / Access count tracker
// 检索命中时收集 memory ID 到内存 buffer，定时批量刷新到存储
type AccessTracker struct {
	ch    chan string
	store store.MemoryStore
}

// NewAccessTracker 创建访问追踪器 / Create an access tracker
// bufSize: channel 容量，满了丢弃（best-effort）
func NewAccessTracker(memStore store.MemoryStore, bufSize int) *AccessTracker {
	if bufSize <= 0 {
		bufSize = 10000
	}
	return &AccessTracker{
		ch:    make(chan string, bufSize),
		store: memStore,
	}
}

// Track 记录一次访问（非阻塞）/ Record one access hit (non-blocking)
func (t *AccessTracker) Track(memoryID string) {
	select {
	case t.ch <- memoryID:
	default:
		// buffer 满了丢弃，best-effort
	}
}

// Flush 批量刷新访问计数到存储 / Flush buffered access counts to store
// 由调度器定时调用
func (t *AccessTracker) Flush(ctx context.Context) error {
	counts := make(map[string]int)
	for {
		select {
		case id := <-t.ch:
			counts[id]++
		default:
			goto flush
		}
	}
flush:
	if len(counts) == 0 {
		return nil
	}

	for id, delta := range counts {
		if err := t.store.IncrementAccessCount(ctx, id, delta); err != nil {
			logger.Warn("failed to increment access count",
				zap.String("memory_id", id),
				zap.Int("delta", delta),
				zap.Error(err),
			)
		}
	}
	logger.Info("access count flush completed", zap.Int("unique_memories", len(counts)))
	return nil
}
