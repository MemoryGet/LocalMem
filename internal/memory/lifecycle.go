package memory

import (
	"fmt"
	"time"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/pkg/scoring"
)

// ValidRetentionTiers 有效的知识保留等级集合 / Valid retention tier values
var ValidRetentionTiers = map[string]bool{
	model.TierPermanent: true,
	model.TierLongTerm:  true,
	model.TierStandard:  true,
	model.TierShortTerm: true,
	model.TierEphemeral: true,
}

// ValidateRetentionTier 校验保留等级 / Validate retention tier value
func ValidateRetentionTier(tier string) error {
	if tier == "" {
		return nil
	}
	if !ValidRetentionTiers[tier] {
		return fmt.Errorf("tier %q is not valid: %w", tier, model.ErrInvalidRetentionTier)
	}
	return nil
}

// ResolveTierDefaults 根据等级填充默认衰减参数 / Resolve default decay parameters from retention tier
//
// 不变量 / Invariant: retention_tier 与 decay_rate 始终保持一致。
// decay_rate 总是从 retention_tier 重新计算，防止两列漂移。
// 调用方若需自定义 decay_rate，应在本函数之后覆盖。
//
// Invariant: retention_tier and decay_rate are always kept in sync.
// decay_rate is always recalculated from retention_tier to prevent drift.
// Callers needing a custom decay_rate should override it after calling this function.
func ResolveTierDefaults(mem *model.Memory) {
	if mem.RetentionTier == "" {
		mem.RetentionTier = model.TierStandard
	}

	// 始终从 tier 重算 decay_rate，确保一致性 / Always recalculate from tier to ensure consistency
	decayRate, expiresIn := model.DefaultDecayParams(mem.RetentionTier)
	mem.DecayRate = decayRate

	if expiresIn != nil && mem.ExpiresAt == nil {
		t := time.Now().UTC().Add(*expiresIn)
		mem.ExpiresAt = &t
	}
}

// CalculateEffectiveStrength 委托 pkg/scoring / Delegate to pkg/scoring
func CalculateEffectiveStrength(strength, decayRate float64, lastAccessedAt *time.Time, retentionTier string, accessCount int, accessAlpha float64) float64 {
	return scoring.CalculateEffectiveStrength(strength, decayRate, lastAccessedAt, retentionTier, accessCount, accessAlpha)
}

// ApplyStrengthWeighting 委托 pkg/scoring / Delegate to pkg/scoring
func ApplyStrengthWeighting(results []*model.SearchResult, accessAlpha float64) []*model.SearchResult {
	return scoring.ApplyStrengthWeighting(results, accessAlpha)
}

// tierOrder 等级升级序列 / Tier promotion order
var tierOrder = []string{
	model.TierEphemeral,
	model.TierShortTerm,
	model.TierStandard,
	model.TierLongTerm,
	model.TierPermanent,
}

// ShouldCrystallize 判断记忆是否满足自动晶化条件 / Check if memory meets auto-crystallization criteria
// 三重条件：强化次数 + 强度 + 存活时间，排除 ephemeral/conversation 类型
func ShouldCrystallize(m *model.Memory, cfg config.CrystallizationConfig) bool {
	if !cfg.Enabled {
		return false
	}
	if m.RetentionTier == model.TierPermanent {
		return false
	}
	if m.Kind == "ephemeral" || m.Kind == "conversation" {
		return false
	}
	return m.ReinforcedCount >= cfg.MinReinforceCount &&
		m.Strength >= cfg.MinStrength &&
		time.Since(m.CreatedAt) >= cfg.MinAge
}

// PromoteTier 提升记忆等级一级 / Promote memory retention tier by one level
// 返回新等级和新衰减率，若已是最高级则不变
func PromoteTier(currentTier string) (newTier string, newDecayRate float64) {
	for i, t := range tierOrder {
		if t == currentTier && i+1 < len(tierOrder) {
			next := tierOrder[i+1]
			dr, _ := model.DefaultDecayParams(next)
			return next, dr
		}
	}
	dr, _ := model.DefaultDecayParams(currentTier)
	return currentTier, dr
}

// minTierForClass returns the minimum RetentionTier for a given MemoryClass.
func minTierForClass(class string) string {
	switch class {
	case "episodic":
		return model.TierShortTerm
	case "semantic":
		return model.TierStandard
	case "procedural":
		return model.TierLongTerm
	case "core":
		return model.TierPermanent
	default:
		return model.TierStandard
	}
}

// TierIndex 返回等级的排序索引（越大越持久）/ Returns the rank of a tier (higher index = more permanent).
// 未知 tier 默认为 standard 的 rank，防止异常提升 / Unknown tier defaults to standard rank
func TierIndex(tier string) int {
	for i, t := range tierOrder {
		if t == tier {
			return i
		}
	}
	// 未知 tier 默认为 standard 的 rank，防止异常提升 / Unknown tier defaults to standard rank
	return 2
}

// ResolveTierFromClass 根据 MemoryClass 和 Kind 自动推断 RetentionTier / Auto-assigns RetentionTier from MemoryClass and Kind.
// Must be called before ResolveTierDefaults in the write path.
// If RetentionTier is already set, it is respected and not overridden.
func ResolveTierFromClass(mem *model.Memory) {
	if mem.RetentionTier != "" {
		return
	}
	if mem.Kind == "conversation" {
		mem.RetentionTier = model.TierEphemeral
		return
	}
	mem.RetentionTier = minTierForClass(mem.MemoryClass)
}
