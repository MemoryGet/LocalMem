// Package heartbeat 自主巡检引擎 / Autonomous inspection engine (HEARTBEAT)
// 后台定期执行衰减审计、孤儿清理、矛盾检测，独立于 memory 包避免循环依赖
package heartbeat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Engine HEARTBEAT 巡检引擎 / HEARTBEAT inspection engine
type Engine struct {
	memStore       store.MemoryStore
	graphStore     store.GraphStore     // 可为 nil / may be nil
	vecStore       store.VectorStore    // 可为 nil / may be nil
	candidateStore store.CandidateStore // 可为 nil / may be nil
	llm            llm.Provider         // 可为 nil / may be nil
	hbCfg          config.HeartbeatConfig // 注入配置 / injected config
}

// NewEngine 创建巡检引擎 / Create a new heartbeat engine
func NewEngine(memStore store.MemoryStore, graphStore store.GraphStore, vecStore store.VectorStore, candidateStore store.CandidateStore, llmProvider llm.Provider, hbCfg config.HeartbeatConfig) *Engine {
	return &Engine{
		memStore:       memStore,
		graphStore:     graphStore,
		vecStore:       vecStore,
		candidateStore: candidateStore,
		llm:            llmProvider,
		hbCfg:          hbCfg,
	}
}

// Run 执行一轮巡检（由调度器调用）/ Execute one inspection round
func (e *Engine) Run(ctx context.Context) error {
	cfg := e.hbCfg
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

	// 4. 摘要补漏（需要 LLM）/ Excerpt backfill (requires LLM)
	if e.llm != nil {
		if err := e.runExcerptBackfill(ctx); err != nil {
			logger.Warn("heartbeat: excerpt backfill failed", zap.Error(err))
		}
	}

	// 5. 晋升高频强化的记忆 / Promotion: episodic → semantic when reinforced_count >= threshold
	if cfg.PromotionEnabled {
		if err := e.runPromotion(ctx, cfg); err != nil {
			logger.Warn("heartbeat: promotion check failed", zap.Error(err))
		}
	}

	// 6. 关系清理 / Relation cleanup
	if e.graphStore != nil {
		if err := e.runRelationCleanup(ctx); err != nil {
			logger.Warn("heartbeat: relation cleanup error", zap.Error(err))
		}
	}

	// 7. 候选实体晋升 / Candidate entity promotion
	if e.candidateStore != nil {
		minHits := e.hbCfg.CandidatePromoteMinHits
		if minHits <= 0 {
			minHits = 3
		}
		if err := e.runCandidatePromotion(ctx, minHits); err != nil {
			logger.Warn("heartbeat: candidate promotion error", zap.Error(err))
		}
	}

	// 8. 保留层级晋升 / Retention tier promotion (short_term→standard, standard→long_term)
	if cfg.PromotionEnabled {
		if err := e.runTierPromotion(ctx); err != nil {
			logger.Warn("heartbeat: tier promotion failed", zap.Error(err))
		}
	}

	// 9. 过期清理 / Expiry cleanup: soft-delete ephemeral past expires_at
	if err := e.runExpiryCleanup(ctx); err != nil {
		logger.Warn("heartbeat: expiry cleanup failed", zap.Error(err))
	}

	logger.Info("heartbeat: inspection round completed")
	return nil
}

// runExcerptBackfill 补充缺少摘要的记忆 / Backfill memories missing excerpt
func (e *Engine) runExcerptBackfill(ctx context.Context) error {
	if e.llm == nil {
		return nil
	}

	const batchLimit = 20
	memories, err := e.memStore.ListMissingExcerpt(ctx, batchLimit)
	if err != nil {
		return fmt.Errorf("list missing excerpt: %w", err)
	}
	if len(memories) == 0 {
		return nil
	}

	logger.Info("heartbeat: backfilling excerpts", zap.Int("count", len(memories)))

	filled := 0
	for _, mem := range memories {
		if len([]rune(mem.Content)) <= 50 {
			mem.Excerpt = mem.Content
		} else {
			excerpt, err := e.generateExcerpt(ctx, mem.Content)
			if err != nil {
				logger.Warn("heartbeat: excerpt generation failed, skipping",
					zap.String("memory_id", mem.ID),
					zap.Error(err),
				)
				continue
			}
			mem.Excerpt = excerpt
		}

		if err := e.memStore.Update(ctx, mem); err != nil {
			logger.Warn("heartbeat: excerpt update failed",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
			continue
		}
		filled++
	}

	logger.Info("heartbeat: excerpt backfill completed", zap.Int("filled", filled))
	return nil
}

// generateExcerpt 调用 LLM 生成摘要 / Generate excerpt via LLM
func (e *Engine) generateExcerpt(ctx context.Context, content string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	temp := 0.1
	resp, err := e.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "用一句话（≤100字）概括以下内容的核心信息，直接输出摘要，不加前缀。"},
			{Role: "user", Content: content},
		},
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("llm chat failed: %w", err)
	}
	excerpt := strings.TrimSpace(resp.Content)
	if len([]rune(excerpt)) > 150 {
		excerpt = string([]rune(excerpt)[:150])
	}
	return excerpt, nil
}

// runPromotion 晋升高频强化的 episodic 记忆为 semantic / Promote highly reinforced episodic memories to semantic
func (e *Engine) runPromotion(ctx context.Context, cfg config.HeartbeatConfig) error {
	threshold := cfg.PromotionThreshold
	if threshold <= 0 {
		threshold = 5
	}

	// 查询候选记忆 / List recent memories to check for promotion candidates
	memories, err := e.memStore.List(ctx, nil, 0, 200)
	if err != nil {
		return fmt.Errorf("list memories for promotion: %w", err)
	}

	promoted := 0
	for _, mem := range memories {
		if mem.MemoryClass != "episodic" || mem.ReinforcedCount < threshold {
			continue
		}
		mem.MemoryClass = "semantic"
		if err := e.memStore.Update(ctx, mem); err != nil {
			logger.Warn("heartbeat: promotion update failed",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
			continue
		}
		promoted++
	}

	if promoted > 0 {
		logger.Info("heartbeat: promoted episodic → semantic",
			zap.Int("count", promoted),
			zap.Int("threshold", threshold),
		)
	}
	return nil
}
