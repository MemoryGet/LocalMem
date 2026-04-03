package memory

import (
	"fmt"
	"math"
	"time"

	"iclude/internal/config"
	"iclude/internal/model"
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

// CalculateEffectiveStrength 计算有效记忆强度（含访问频率加成）/ Calculate effective memory strength with decay and access boost
// accessAlpha 为访问频率阻尼系数，推荐值 0.15
func CalculateEffectiveStrength(strength, decayRate float64, lastAccessedAt *time.Time, retentionTier string, accessCount int, accessAlpha float64) float64 {
	// permanent 等级不衰减
	if retentionTier == model.TierPermanent {
		return strength
	}
	if lastAccessedAt == nil {
		return strength
	}
	hours := time.Since(*lastAccessedAt).Hours()
	if hours < 0 {
		hours = 0
	}
	decay := strength * math.Exp(-decayRate*hours)
	accessBoost := 1.0 + accessAlpha*math.Log2(float64(accessCount)+1.0)
	if accessBoost > 3.0 {
		accessBoost = 3.0
	}
	return decay * accessBoost
}

// ApplyStrengthWeighting 应用记忆强度加权 / Apply strength weighting to search results
// 过滤已过期记忆，并用有效强度（含访问频率）加权分数
// accessAlpha: 访问频率阻尼系数，从 RetrievalConfig.AccessAlpha 传入
func ApplyStrengthWeighting(results []*model.SearchResult, accessAlpha float64) []*model.SearchResult {
	now := time.Now().UTC()
	var filtered []*model.SearchResult

	for _, r := range results {
		// 过滤已过期记忆
		if r.Memory.ExpiresAt != nil && r.Memory.ExpiresAt.Before(now) {
			continue
		}

		// 计算有效强度并加权
		effective := CalculateEffectiveStrength(r.Memory.Strength, r.Memory.DecayRate, r.Memory.LastAccessedAt, r.Memory.RetentionTier, r.Memory.AccessCount, accessAlpha)
		r.Score *= effective

		filtered = append(filtered, r)
	}

	return filtered
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
