package memory_test

import (
	"testing"
	"time"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateEffectiveStrength(t *testing.T) {
	tests := []struct {
		name           string
		strength       float64
		decayRate      float64
		lastAccessedAt *time.Time
		wantMin        float64
		wantMax        float64
	}{
		{
			name:           "nil lastAccessedAt returns raw strength",
			strength:       0.8,
			decayRate:      0.01,
			lastAccessedAt: nil,
			wantMin:        0.8,
			wantMax:        0.8,
		},
		{
			name:      "recent access has minimal decay",
			strength:  1.0,
			decayRate: 0.01,
			lastAccessedAt: func() *time.Time {
				t := time.Now().Add(-1 * time.Hour)
				return &t
			}(),
			wantMin: 0.98,
			wantMax: 1.0,
		},
		{
			name:      "old access has significant decay",
			strength:  1.0,
			decayRate: 0.01,
			lastAccessedAt: func() *time.Time {
				t := time.Now().Add(-100 * time.Hour)
				return &t
			}(),
			wantMin: 0.0,
			wantMax: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := memory.CalculateEffectiveStrength(tt.strength, tt.decayRate, tt.lastAccessedAt, "", 0, 0.0)
			assert.GreaterOrEqual(t, result, tt.wantMin)
			assert.LessOrEqual(t, result, tt.wantMax)
		})
	}
}

func TestCalculateEffectiveStrength_PermanentTier(t *testing.T) {
	// permanent 等级不受衰减影响，始终返回原始强度 / Permanent tier returns raw strength regardless of time
	tests := []struct {
		name           string
		strength       float64
		decayRate      float64
		lastAccessedAt *time.Time
	}{
		{
			name:           "permanent with nil lastAccessedAt",
			strength:       0.9,
			decayRate:      0.0,
			lastAccessedAt: nil,
		},
		{
			name:      "permanent with old access time",
			strength:  0.7,
			decayRate: 0.0,
			lastAccessedAt: func() *time.Time {
				t := time.Now().Add(-1000 * time.Hour)
				return &t
			}(),
		},
		{
			name:      "permanent ignores non-zero decay rate",
			strength:  1.0,
			decayRate: 0.5,
			lastAccessedAt: func() *time.Time {
				t := time.Now().Add(-48 * time.Hour)
				return &t
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := memory.CalculateEffectiveStrength(tt.strength, tt.decayRate, tt.lastAccessedAt, "permanent", 0, 0.0)
			assert.Equal(t, tt.strength, result, "permanent tier should return raw strength unmodified")
		})
	}
}

func TestResolveTierDefaults_AllTiers(t *testing.T) {
	// 表驱动：验证每个 tier 设置正确的 decay_rate / Table-driven test verifying each tier's default decay_rate
	tests := []struct {
		name             string
		tier             string
		wantDecayRate    float64
		wantExpiresAtSet bool
	}{
		{
			name:          "permanent tier",
			tier:          "permanent",
			wantDecayRate: 0,
		},
		{
			name:          "long_term tier",
			tier:          "long_term",
			wantDecayRate: 0.001,
		},
		{
			name:          "standard tier",
			tier:          "standard",
			wantDecayRate: 0.01,
		},
		{
			name:          "short_term tier",
			tier:          "short_term",
			wantDecayRate: 0.05,
		},
		{
			name:             "ephemeral tier",
			tier:             "ephemeral",
			wantDecayRate:    0.1,
			wantExpiresAtSet: true,
		},
		{
			name:          "empty tier defaults to standard",
			tier:          "",
			wantDecayRate: 0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &model.Memory{
				Content:       "test",
				RetentionTier: tt.tier,
			}
			memory.ResolveTierDefaults(mem)

			assert.Equal(t, tt.wantDecayRate, mem.DecayRate)

			if tt.wantExpiresAtSet {
				assert.NotNil(t, mem.ExpiresAt, "ephemeral tier should set ExpiresAt")
				assert.True(t, mem.ExpiresAt.After(time.Now()), "ExpiresAt should be in the future")
			}

			// 空 tier 应被回填为 standard
			if tt.tier == "" {
				assert.Equal(t, "standard", mem.RetentionTier)
			}
		})
	}
}

func TestValidateRetentionTier_Valid(t *testing.T) {
	// 有效等级均返回 nil 错误 / Valid tiers return nil error
	validTiers := []string{
		"permanent",
		"long_term",
		"standard",
		"short_term",
		"ephemeral",
		"", // 空字符串也是有效的（表示不指定）
	}

	for _, tier := range validTiers {
		t.Run(tier, func(t *testing.T) {
			err := memory.ValidateRetentionTier(tier)
			assert.NoError(t, err, "tier %q should be valid", tier)
		})
	}
}

func TestValidateRetentionTier_Invalid(t *testing.T) {
	// 无效等级返回 ErrInvalidRetentionTier / Invalid tier returns ErrInvalidRetentionTier
	tests := []struct {
		name string
		tier string
	}{
		{name: "unknown string", tier: "unknown"},
		{name: "uppercase", tier: "PERMANENT"},
		{name: "mixed case", tier: "Standard"},
		{name: "typo", tier: "permenant"},
		{name: "numeric", tier: "1"},
		{name: "whitespace", tier: " standard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := memory.ValidateRetentionTier(tt.tier)
			require.Error(t, err)
			assert.ErrorIs(t, err, model.ErrInvalidRetentionTier)
		})
	}
}

func TestApplyStrengthWeighting(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)
	expired := now.Add(-1 * time.Hour) // 过去的过期时间

	tests := []struct {
		name    string
		results []*model.SearchResult
		wantLen int
	}{
		{
			name: "filter expired memories",
			results: []*model.SearchResult{
				{
					Memory: &model.Memory{
						Strength: 1.0, DecayRate: 0.01,
						LastAccessedAt: &past,
					},
					Score: 1.0,
				},
				{
					Memory: &model.Memory{
						Strength: 1.0, DecayRate: 0.01,
						LastAccessedAt: &past,
						ExpiresAt:      &expired,
					},
					Score: 1.0,
				},
			},
			wantLen: 1,
		},
		{
			name:    "empty results",
			results: nil,
			wantLen: 0,
		},
		{
			name: "apply strength weighting",
			results: []*model.SearchResult{
				{
					Memory: &model.Memory{
						Strength: 0.5, DecayRate: 0.0,
						LastAccessedAt: &past,
					},
					Score: 1.0,
				},
			},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := memory.ApplyStrengthWeighting(tt.results, 0.0)
			assert.Len(t, result, tt.wantLen)

			if tt.name == "apply strength weighting" && len(result) > 0 {
				// 分数应该被强度加权（0.5 * 1.0 = 0.5）
				assert.Less(t, result[0].Score, 1.0)
			}
		})
	}
}
