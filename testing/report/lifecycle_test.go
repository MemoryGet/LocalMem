package report_test

import (
	"fmt"
	"testing"
	"time"

	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/pkg/testreport"

	"github.com/stretchr/testify/assert"
)

const (
	suiteLifecycle     = "访问频率加权 (Access Frequency)"
	suiteLifecycleIcon = "\U0001F4C8"
	suiteLifecycleDesc = "P0: CalculateEffectiveStrength 增加 log2(accessCount+1) 访问频率加成，α=0.15 阻尼"
)

func TestAccessFrequency_ZeroAccessCount(t *testing.T) {
	tc := testreport.NewCase(t, suiteLifecycle, suiteLifecycleIcon, suiteLifecycleDesc,
		"accessCount=0 时乘数为 1.0")
	defer tc.Done()

	tc.Input("strength", "1.0")
	tc.Input("accessCount", "0")
	tc.Input("alpha", "0.15")

	past := time.Now().Add(-1 * time.Hour)
	result := memory.CalculateEffectiveStrength(1.0, 0.01, &past, "standard", 0, 0.15)
	tc.Step("计算 effectiveStrength (accessCount=0)")

	// accessCount=0 → boost = 1 + 0.15*log2(1) = 1.0
	// 所以结果只受衰减影响，不受访问加成影响
	assert.InDelta(t, 1.0*0.99*1.0, result, 0.01, "accessCount=0 时访问加成应为 1.0")
	tc.Step("验证: 加成乘数 = 1.0（无惩罚）")

	tc.Output("effectiveStrength", fmt.Sprintf("%.4f", result))
	tc.Output("accessBoost", "1.0")
}

func TestAccessFrequency_HighAccessCount(t *testing.T) {
	tc := testreport.NewCase(t, suiteLifecycle, suiteLifecycleIcon, suiteLifecycleDesc,
		"accessCount=100 时乘数约 2.0")
	defer tc.Done()

	tc.Input("strength", "1.0")
	tc.Input("accessCount", "100")
	tc.Input("alpha", "0.15")

	past := time.Now().Add(-1 * time.Hour)
	withAccess := memory.CalculateEffectiveStrength(1.0, 0.01, &past, "standard", 100, 0.15)
	withoutAccess := memory.CalculateEffectiveStrength(1.0, 0.01, &past, "standard", 0, 0.15)
	tc.Step("计算 effectiveStrength (accessCount=100 vs 0)")

	ratio := withAccess / withoutAccess
	// 1 + 0.15*log2(101) ≈ 1 + 0.15*6.66 ≈ 2.0
	assert.InDelta(t, 2.0, ratio, 0.1, "accessCount=100 时加成约 2.0x")
	tc.Step("验证: 加成比率约 2.0x")

	tc.Output("withAccess", fmt.Sprintf("%.4f", withAccess))
	tc.Output("withoutAccess", fmt.Sprintf("%.4f", withoutAccess))
	tc.Output("ratio", fmt.Sprintf("%.2fx", ratio))
}

func TestAccessFrequency_VeryHighAccessCount(t *testing.T) {
	tc := testreport.NewCase(t, suiteLifecycle, suiteLifecycleIcon, suiteLifecycleDesc,
		"accessCount=1000 时乘数约 2.5（有界）")
	defer tc.Done()

	tc.Input("accessCount", "1000")
	tc.Input("alpha", "0.15")

	past := time.Now().Add(-1 * time.Hour)
	withAccess := memory.CalculateEffectiveStrength(1.0, 0.01, &past, "standard", 1000, 0.15)
	withoutAccess := memory.CalculateEffectiveStrength(1.0, 0.01, &past, "standard", 0, 0.15)
	tc.Step("计算 effectiveStrength (accessCount=1000 vs 0)")

	ratio := withAccess / withoutAccess
	assert.InDelta(t, 2.5, ratio, 0.15, "accessCount=1000 时加成约 2.5x（不会失控）")
	tc.Step("验证: 加成有界，不会压倒衰减")

	tc.Output("ratio", fmt.Sprintf("%.2fx", ratio))
}

func TestAccessFrequency_PermanentTierIgnored(t *testing.T) {
	tc := testreport.NewCase(t, suiteLifecycle, suiteLifecycleIcon, suiteLifecycleDesc,
		"permanent 等级不衰减也不加成")
	defer tc.Done()

	past := time.Now().Add(-100 * time.Hour)
	result := memory.CalculateEffectiveStrength(0.8, 0.01, &past, "permanent", 50, 0.15)
	tc.Step("计算 permanent tier (accessCount=50)")

	assert.Equal(t, 0.8, result, "permanent tier 直接返回原始 strength")
	tc.Step("验证: strength 未变化")

	tc.Output("effectiveStrength", fmt.Sprintf("%.1f", result))
}

func TestAccessFrequency_ApplyStrengthWeighting(t *testing.T) {
	tc := testreport.NewCase(t, suiteLifecycle, suiteLifecycleIcon, suiteLifecycleDesc,
		"ApplyStrengthWeighting 端到端")
	defer tc.Done()

	past := time.Now().Add(-1 * time.Hour)
	results := []*model.SearchResult{
		{
			Memory: &model.Memory{
				Strength: 1.0, DecayRate: 0.01,
				LastAccessedAt: &past, RetentionTier: "standard",
				AccessCount: 50,
			},
			Score: 1.0,
		},
		{
			Memory: &model.Memory{
				Strength: 1.0, DecayRate: 0.01,
				LastAccessedAt: &past, RetentionTier: "standard",
				AccessCount: 0,
			},
			Score: 1.0,
		},
	}
	tc.Input("记忆数", "2")
	tc.Input("accessCount", "50 vs 0")

	weighted := memory.ApplyStrengthWeighting(results, 0.15)
	tc.Step("执行 ApplyStrengthWeighting(alpha=0.15)")

	assert.Len(t, weighted, 2)
	assert.Greater(t, weighted[0].Score, weighted[1].Score, "高访问量记忆分数应更高")
	tc.Step("验证: accessCount=50 的记忆分数 > accessCount=0")

	tc.Output("score[0] (access=50)", fmt.Sprintf("%.4f", weighted[0].Score))
	tc.Output("score[1] (access=0)", fmt.Sprintf("%.4f", weighted[1].Score))
}
