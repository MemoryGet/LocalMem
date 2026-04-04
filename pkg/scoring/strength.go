// Package scoring 记忆评分计算 / Memory scoring calculations
package scoring

import (
	"math"
	"time"

	"iclude/internal/model"
)

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
func ApplyStrengthWeighting(results []*model.SearchResult, accessAlpha float64) []*model.SearchResult {
	now := time.Now().UTC()
	var filtered []*model.SearchResult

	for _, r := range results {
		if r.Memory.ExpiresAt != nil && r.Memory.ExpiresAt.Before(now) {
			continue
		}
		effective := CalculateEffectiveStrength(r.Memory.Strength, r.Memory.DecayRate, r.Memory.LastAccessedAt, r.Memory.RetentionTier, r.Memory.AccessCount, accessAlpha)
		r.Score *= effective
		filtered = append(filtered, r)
	}

	return filtered
}
