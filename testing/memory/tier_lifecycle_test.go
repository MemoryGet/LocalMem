package memory_test

import (
	"testing"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
)

func TestResolveTierFromClass(t *testing.T) {
	tests := []struct {
		name         string
		class        string
		kind         string
		explicitTier string
		wantTier     string
	}{
		{"explicit tier not overridden", "episodic", "", "long_term", "long_term"},
		{"conversation kind → ephemeral", "episodic", "conversation", "", "ephemeral"},
		{"episodic class → short_term", "episodic", "", "", "short_term"},
		{"semantic class → standard", "semantic", "", "", "standard"},
		{"procedural class → long_term", "procedural", "", "", "long_term"},
		{"core class → permanent", "core", "", "", "permanent"},
		{"empty class → standard", "", "", "", "standard"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &model.Memory{
				MemoryClass:   tt.class,
				Kind:          tt.kind,
				RetentionTier: tt.explicitTier,
			}
			memory.ResolveTierFromClass(mem)
			assert.Equal(t, tt.wantTier, mem.RetentionTier)
		})
	}
}

func TestTierIndex(t *testing.T) {
	assert.Equal(t, 0, memory.TierIndex("ephemeral"))
	assert.Equal(t, 1, memory.TierIndex("short_term"))
	assert.Equal(t, 2, memory.TierIndex("standard"))
	assert.Equal(t, 3, memory.TierIndex("long_term"))
	assert.Equal(t, 4, memory.TierIndex("permanent"))
	assert.Equal(t, 2, memory.TierIndex("unknown_tier"))
}

func TestResolveTierFromClass_DecayRateSync(t *testing.T) {
	tests := []struct {
		name          string
		class         string
		wantTier      string
		wantDecayRate float64
	}{
		{"episodic gets short_term decay", "episodic", "short_term", 0.05},
		{"semantic gets standard decay", "semantic", "standard", 0.01},
		{"procedural gets long_term decay", "procedural", "long_term", 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &model.Memory{MemoryClass: tt.class}
			memory.ResolveTierFromClass(mem)
			memory.ResolveTierDefaults(mem)
			assert.Equal(t, tt.wantTier, mem.RetentionTier)
			assert.Equal(t, tt.wantDecayRate, mem.DecayRate)
		})
	}
}
