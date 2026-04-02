package memory

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/mathutil"

	"go.uber.org/zap"
)

// Consolidator 记忆归纳引擎 / Memory consolidation engine
// 定期找到相似记忆簇，用 LLM 归纳为浓缩版永久记忆
type Consolidator struct {
	memStore store.MemoryStore
	vecStore store.VectorStore          // 可为 nil / may be nil
	llm      llm.Provider               // 可为 nil / may be nil
	cfg      config.ConsolidationConfig // 注入配置 / injected config
}

// NewConsolidator 创建归纳引擎 / Create a new consolidator
func NewConsolidator(memStore store.MemoryStore, vecStore store.VectorStore, llmProvider llm.Provider, cfg config.ConsolidationConfig) *Consolidator {
	return &Consolidator{
		memStore: memStore,
		vecStore: vecStore,
		llm:      llmProvider,
		cfg:      cfg,
	}
}

// Run 执行一次归纳（由调度器调用）/ Execute one consolidation run
func (c *Consolidator) Run(ctx context.Context) error {
	if c.vecStore == nil || c.llm == nil {
		logger.Debug("consolidation: skipped (vecStore or llm unavailable)")
		return nil
	}

	// 获取候选记忆
	candidates, err := c.selectCandidates(ctx, c.cfg)
	if err != nil {
		return fmt.Errorf("consolidation: failed to select candidates: %w", err)
	}
	if len(candidates) < 2 {
		logger.Debug("consolidation: not enough candidates", zap.Int("count", len(candidates)))
		return nil
	}

	// 获取向量
	ids := make([]string, len(candidates))
	for i, m := range candidates {
		ids[i] = m.ID
	}
	vectors, err := c.vecStore.GetVectors(ctx, ids)
	if err != nil {
		return fmt.Errorf("consolidation: failed to get vectors: %w", err)
	}

	// 层次聚类
	clusters := agglomerativeClustering(candidates, vectors, c.cfg.SimilarityThreshold, c.cfg.MinClusterSize)
	if len(clusters) == 0 {
		logger.Debug("consolidation: no clusters found")
		return nil
	}

	logger.Info("consolidation: clusters found", zap.Int("clusters", len(clusters)))

	// 对每个簇执行归纳
	for i, cluster := range clusters {
		if err := c.consolidateCluster(ctx, cluster, i); err != nil {
			logger.Warn("consolidation: cluster failed",
				zap.Int("cluster", i),
				zap.Error(err),
			)
		}
	}

	return nil
}

// selectCandidates 时间轮转 + 随机采样选取候选 / Select candidates via time-window rotation + random sampling
// 将 [minAgeDays, now] 区间分为多个窗口，每个窗口随机采样，确保新旧记忆都有机会被归纳
func (c *Consolidator) selectCandidates(ctx context.Context, cfg config.ConsolidationConfig) ([]*model.Memory, error) {
	now := time.Now()
	cutoff := now.AddDate(0, 0, -cfg.MinAgeDays)
	maxPerRun := cfg.MaxMemoriesPerRun
	if maxPerRun <= 0 {
		maxPerRun = 100
	}

	// 将时间范围分为 windows（每窗口 7 天）/ Split time range into 7-day windows
	windowSize := 7 * 24 * time.Hour
	totalDuration := cutoff.Sub(now.AddDate(-1, 0, 0)) // 最远回溯 1 年 / Look back 1 year max
	if totalDuration < windowSize {
		totalDuration = windowSize
	}

	perWindow := maxPerRun / 4 // 每窗口采样数 / Samples per window
	if perWindow < 10 {
		perWindow = 10
	}

	var allCandidates []*model.Memory

	// 从最近的合格时间往回遍历窗口 / Iterate windows from most recent eligible backward
	windowEnd := cutoff
	windowStart := cutoff.Add(-windowSize)
	oldest := now.AddDate(-1, 0, 0)

	for windowEnd.After(oldest) && len(allCandidates) < maxPerRun {
		if windowStart.Before(oldest) {
			windowStart = oldest
		}

		memories, err := c.memStore.ListTimeline(ctx, &model.TimelineRequest{
			After:  &windowStart,
			Before: &windowEnd,
			Limit:  perWindow * 3, // 多拉一些用于过滤后采样 / Over-fetch for filtering
		})
		if err != nil {
			logger.Warn("consolidation: window query failed",
				zap.Time("start", windowStart),
				zap.Time("end", windowEnd),
				zap.Error(err),
			)
			windowEnd = windowStart
			windowStart = windowStart.Add(-windowSize)
			continue
		}

		// 过滤不合格记忆 / Filter ineligible memories
		var eligible []*model.Memory
		for _, m := range memories {
			if m.RetentionTier == model.TierPermanent || m.RetentionTier == model.TierEphemeral {
				continue
			}
			if m.ConsolidatedInto != "" || m.Kind == "consolidated" {
				continue
			}
			eligible = append(eligible, m)
		}

		// 随机采样 / Random sample from eligible
		if len(eligible) > perWindow {
			rand.Shuffle(len(eligible), func(i, j int) {
				eligible[i], eligible[j] = eligible[j], eligible[i]
			})
			eligible = eligible[:perWindow]
		}

		allCandidates = append(allCandidates, eligible...)

		// 滑动窗口 / Slide window
		windowEnd = windowStart
		windowStart = windowStart.Add(-windowSize)
	}

	// 最终截断 / Final cap
	if len(allCandidates) > maxPerRun {
		allCandidates = allCandidates[:maxPerRun]
	}

	logger.Info("consolidation: candidates selected",
		zap.Int("total", len(allCandidates)),
		zap.Int("max_per_run", maxPerRun),
	)
	return allCandidates, nil
}

// consolidateLLMTimeout 单次归纳 LLM 超时 / Per-call timeout for consolidation LLM
const consolidateLLMTimeout = 30 * time.Second

// consolidateCluster 归纳一个簇 / Consolidate a single cluster
func (c *Consolidator) consolidateCluster(ctx context.Context, cluster []*model.Memory, idx int) error {
	// 收集内容和元数据 / Collect content and metadata
	var memLines string
	maxStrength := 0.0
	totalReinforced := 0
	// 取首个非空 scope/kind 作为归纳记忆的元数据 / Inherit scope/kind from first non-empty member
	var inheritScope, inheritKind, inheritTeamID string
	for i, m := range cluster {
		// 带编号和 kind 前缀，保留结构化上下文 / Numbered entries with kind prefix preserve context
		kindTag := ""
		if m.Kind != "" {
			kindTag = fmt.Sprintf("[%s] ", m.Kind)
		}
		memLines += fmt.Sprintf("%d. %s%s\n", i+1, kindTag, m.Content)
		if m.Strength > maxStrength {
			maxStrength = m.Strength
		}
		totalReinforced += m.ReinforcedCount
		if inheritScope == "" && m.Scope != "" {
			inheritScope = m.Scope
		}
		if inheritKind == "" && m.Kind != "" && m.Kind != "consolidated" {
			inheritKind = m.Kind
		}
		if inheritTeamID == "" && m.TeamID != "" {
			inheritTeamID = m.TeamID
		}
	}

	sysPrompt := "You are a memory consolidation engine. Merge the numbered memories into one concise, accurate memory. Preserve all unique facts and key details. Remove redundancy. Output ONLY the consolidated memory text — no prefixes, no explanations, no numbering."
	prompt := fmt.Sprintf("Merge these %d related memories into one comprehensive memory:\n\n%s", len(cluster), memLines)

	// 独立超时防止 LLM hang / Per-call timeout
	llmCtx, cancel := context.WithTimeout(ctx, consolidateLLMTimeout)
	defer cancel()

	resp, err := c.llm.Chat(llmCtx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return fmt.Errorf("LLM consolidation failed: %w", err)
	}

	// 输出验证：结果不得为空，且长度不得短于最短原始记忆的 10% / Validate output is non-trivially short
	consolidatedContent := strings.TrimSpace(resp.Content)
	if consolidatedContent == "" {
		return fmt.Errorf("LLM returned empty consolidation for cluster %d", idx)
	}
	shortestSource := len(cluster[0].Content)
	for _, m := range cluster[1:] {
		if len(m.Content) < shortestSource {
			shortestSource = len(m.Content)
		}
	}
	if len(consolidatedContent) < shortestSource/10 {
		logger.Warn("consolidation: LLM output suspiciously short, skipping cluster",
			zap.Int("cluster", idx),
			zap.Int("output_len", len(consolidatedContent)),
			zap.Int("min_source_len", shortestSource),
		)
		return fmt.Errorf("consolidation output too short (cluster %d), skipping to preserve data integrity", idx)
	}

	// Collect source IDs for derived_from tracing / 收集来源 ID 用于溯源
	sourceIDs := make([]string, len(cluster))
	for i, m := range cluster {
		sourceIDs[i] = m.ID
	}

	// 创建归纳记忆 / Create consolidated memory
	consolidated := &model.Memory{
		Content:       consolidatedContent,
		RetentionTier: model.TierPermanent,
		Kind:          inheritKind,
		MemoryClass:   "semantic",
		DerivedFrom:   sourceIDs,
		Strength:      math.Min(maxStrength*1.1, 1.0),
		SourceType:    "consolidation",
		Scope:         inheritScope,
		TeamID:        inheritTeamID,
	}
	ResolveTierDefaults(consolidated)

	if err := c.memStore.Create(ctx, consolidated); err != nil {
		return fmt.Errorf("failed to create consolidated memory: %w", err)
	}

	// soft-delete 原始记忆并记录归纳目标 / Soft-delete sources and set consolidated_into
	for _, m := range cluster {
		m.ConsolidatedInto = consolidated.ID
		if err := c.memStore.Update(ctx, m); err != nil {
			logger.Warn("consolidation: failed to set consolidated_into",
				zap.String("memory_id", m.ID),
				zap.Error(err),
			)
		}
		if err := c.memStore.SoftDelete(ctx, m.ID); err != nil {
			logger.Warn("consolidation: failed to soft-delete source",
				zap.String("memory_id", m.ID),
				zap.Error(err),
			)
		}
	}

	logger.Info("consolidation: cluster merged",
		zap.Int("cluster", idx),
		zap.Int("source_count", len(cluster)),
		zap.String("consolidated_id", consolidated.ID),
		zap.String("scope", inheritScope),
	)
	return nil
}

// agglomerativeClustering 层次聚类 / Agglomerative clustering with average linkage
func agglomerativeClustering(memories []*model.Memory, vectors map[string][]float32, simThreshold float64, minSize int) [][]*model.Memory {
	n := len(memories)
	if n < 2 {
		return nil
	}

	// 初始：每个记忆一个簇
	type cluster struct {
		members []*model.Memory
	}
	clusters := make([]*cluster, n)
	for i, m := range memories {
		clusters[i] = &cluster{members: []*model.Memory{m}}
	}

	// 计算平均链接距离
	avgDistance := func(a, b *cluster) float64 {
		total := 0.0
		count := 0
		for _, ma := range a.members {
			va := vectors[ma.ID]
			if len(va) == 0 {
				continue
			}
			for _, mb := range b.members {
				vb := vectors[mb.ID]
				if len(vb) == 0 {
					continue
				}
				total += 1.0 - mathutil.CosineSimilarity(va, vb)
				count++
			}
		}
		if count == 0 {
			return 1.0
		}
		return total / float64(count)
	}

	distThreshold := 1.0 - simThreshold

	// 贪心合并
	for {
		bestDist := 2.0
		bestI, bestJ := -1, -1

		for i := 0; i < len(clusters); i++ {
			if clusters[i] == nil {
				continue
			}
			for j := i + 1; j < len(clusters); j++ {
				if clusters[j] == nil {
					continue
				}
				d := avgDistance(clusters[i], clusters[j])
				if d < bestDist {
					bestDist = d
					bestI = i
					bestJ = j
				}
			}
		}

		if bestI < 0 || bestDist > distThreshold {
			break
		}

		// 合并 j 到 i
		clusters[bestI].members = append(clusters[bestI].members, clusters[bestJ].members...)
		clusters[bestJ] = nil
	}

	// 收集满足最小大小的簇
	var result [][]*model.Memory
	for _, c := range clusters {
		if c != nil && len(c.members) >= minSize {
			result = append(result, c.members)
		}
	}
	return result
}

