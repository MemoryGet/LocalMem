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
