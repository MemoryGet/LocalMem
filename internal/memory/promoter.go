package memory

import (
	"context"
	"fmt"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// PromotionThresholds 晋升阈值 / Promotion thresholds
type PromotionThresholds struct {
	MinReinforcedCount int     // 最低强化次数 / Minimum reinforced_count to qualify
	MinStrength        float64 // 最低记忆强度 / Minimum strength to qualify
	MinAgeDays         int     // 最低存在天数 / Minimum age in days
}

// DefaultPromotionThresholds 默认晋升阈值 / Default promotion thresholds
var DefaultPromotionThresholds = PromotionThresholds{
	MinReinforcedCount: 3,
	MinStrength:        0.6,
	MinAgeDays:         1,
}

// Promoter 候选记忆晋升引擎 / Candidate memory promotion engine
type Promoter struct {
	memStore   store.MemoryStore
	thresholds PromotionThresholds
}

// NewPromoter 创建晋升引擎 / Create a new promoter
func NewPromoter(memStore store.MemoryStore, thresholds PromotionThresholds) *Promoter {
	return &Promoter{
		memStore:   memStore,
		thresholds: thresholds,
	}
}

// Run 执行一轮候选晋升扫描（由 scheduler 调用）/ Execute one promotion scan cycle
func (p *Promoter) Run(ctx context.Context) error {
	candidates, err := p.memStore.ListCandidates(ctx, 50)
	if err != nil {
		return fmt.Errorf("list candidates: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}

	promoted, skipped := 0, 0
	for _, mem := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}

		if !p.qualifies(mem) {
			skipped++
			continue
		}

		targetClass := resolveTargetClass(mem.CandidateFor)
		if targetClass == "" {
			skipped++
			continue
		}

		// 执行晋升：更新 memory_class + 清除 candidate_for / Promote: update class + clear candidate
		mem.MemoryClass = targetClass
		mem.CandidateFor = ""
		// 同步 tier：若当前 tier 低于新 class 的最低要求，升级 tier
		if minT := minTierForClass(targetClass); TierIndex(mem.RetentionTier) < TierIndex(minT) {
			mem.RetentionTier = minT
			dr, _ := model.DefaultDecayParams(minT)
			mem.DecayRate = dr
		}

		if err := p.memStore.Update(ctx, mem); err != nil {
			logger.Warn("promotion failed",
				zap.String("memory_id", mem.ID),
				zap.String("target", targetClass),
				zap.Error(err),
			)
			continue
		}

		promoted++
		logger.Info("memory promoted",
			zap.String("memory_id", mem.ID),
			zap.String("target_class", targetClass),
			zap.Int("reinforced_count", mem.ReinforcedCount),
		)
	}

	if promoted > 0 || skipped > 0 {
		logger.Info("promotion scan completed",
			zap.Int("promoted", promoted),
			zap.Int("skipped", skipped),
			zap.Int("total_candidates", len(candidates)),
		)
	}

	return nil
}

// PromoteByID 手动晋升指定记忆（由 MCP 工具调用）/ Manually promote a specific memory
func (p *Promoter) PromoteByID(ctx context.Context, memoryID, targetClass string) error {
	if targetClass != "semantic" && targetClass != "procedural" && targetClass != "core" {
		return fmt.Errorf("invalid target class %q: expected semantic, procedural, or core", targetClass)
	}

	mem, err := p.memStore.Get(ctx, memoryID)
	if err != nil {
		return fmt.Errorf("get memory: %w", err)
	}

	// core 需要额外验证 / Core requires additional validation
	if targetClass == "core" {
		if err := ValidateCoreWrite(mem); err != nil {
			return err
		}
	}

	mem.MemoryClass = targetClass
	mem.CandidateFor = ""
	// 同步 tier：若当前 tier 低于新 class 的最低要求，升级 tier
	if minT := minTierForClass(targetClass); TierIndex(mem.RetentionTier) < TierIndex(minT) {
		mem.RetentionTier = minT
		dr, _ := model.DefaultDecayParams(minT)
		mem.DecayRate = dr
	}

	if err := p.memStore.Update(ctx, mem); err != nil {
		return fmt.Errorf("promote memory: %w", err)
	}

	logger.Info("memory manually promoted",
		zap.String("memory_id", memoryID),
		zap.String("target_class", targetClass),
	)
	return nil
}

// qualifies 检查候选记忆是否达到晋升阈值 / Check if candidate qualifies for promotion
func (p *Promoter) qualifies(mem *model.Memory) bool {
	if mem.ReinforcedCount < p.thresholds.MinReinforcedCount {
		return false
	}
	if mem.Strength < p.thresholds.MinStrength {
		return false
	}
	minAge := time.Duration(p.thresholds.MinAgeDays) * 24 * time.Hour
	if time.Since(mem.CreatedAt) < minAge {
		return false
	}
	return true
}

// resolveTargetClass 从 candidate_for 解析目标 class / Resolve target class from candidate_for
func resolveTargetClass(candidateFor string) string {
	switch candidateFor {
	case "semantic_candidate":
		return "semantic"
	case "procedural_candidate":
		return "procedural"
	case "core_candidate":
		return "core"
	default:
		return ""
	}
}
