// Package heartbeat 自主巡检引擎 / Autonomous inspection engine (HEARTBEAT)
// 后台定期执行衰减审计、孤儿清理、矛盾检测，独立于 memory 包避免循环依赖
package heartbeat

import (
	"context"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Engine HEARTBEAT 巡检引擎 / HEARTBEAT inspection engine
type Engine struct {
	memStore   store.MemoryStore
	graphStore store.GraphStore  // 可为 nil / may be nil
	vecStore   store.VectorStore // 可为 nil / may be nil
	llm        llm.Provider     // 可为 nil / may be nil
}

// NewEngine 创建巡检引擎 / Create a new heartbeat engine
func NewEngine(memStore store.MemoryStore, graphStore store.GraphStore, vecStore store.VectorStore, llmProvider llm.Provider) *Engine {
	return &Engine{
		memStore:   memStore,
		graphStore: graphStore,
		vecStore:   vecStore,
		llm:        llmProvider,
	}
}

// Run 执行一轮巡检（由调度器调用）/ Execute one inspection round
func (e *Engine) Run(ctx context.Context) error {
	cfg := config.GetConfig().Heartbeat
	if !cfg.Enabled {
		return nil
	}

	logger.Info("heartbeat: starting inspection round")

	// 1. 衰减审计（始终执行，不需要 LLM）
	if err := e.runDecayAudit(ctx, cfg); err != nil {
		logger.Warn("heartbeat: decay audit failed", zap.Error(err))
	}

	// 2. 孤儿清理（需要 graphStore）
	if e.graphStore != nil {
		if err := e.runOrphanCleanup(ctx); err != nil {
			logger.Warn("heartbeat: orphan cleanup failed", zap.Error(err))
		}
	}

	// 3. 矛盾检测（需要 vecStore + llm）
	if cfg.ContradictionEnabled && e.vecStore != nil && e.llm != nil {
		if err := e.runContradictionCheck(ctx, cfg); err != nil {
			logger.Warn("heartbeat: contradiction check failed", zap.Error(err))
		}
	}

	logger.Info("heartbeat: inspection round completed")
	return nil
}
