package search_test

import (
	"testing"

	"iclude/internal/model"
	"iclude/internal/search"
)

func TestApplyKindAndClassWeights(t *testing.T) {
	tests := []struct {
		name        string
		memoryClass string
		kind        string
		initScore   float64
		wantScore   float64
	}{
		{"procedural 1.5x", "procedural", "note", 1.0, 1.5},
		{"semantic 1.2x", "semantic", "note", 1.0, 1.2},
		{"episodic 1.0x", "episodic", "note", 1.0, 1.0},
		{"procedural+skill capped at 2.0", "procedural", "skill", 1.0, 2.0},
		{"empty class = episodic", "", "note", 1.0, 1.0},
		{"semantic+profile", "semantic", "profile", 1.0, 1.44},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []*model.SearchResult{{
				Memory: &model.Memory{Kind: tt.kind, MemoryClass: tt.memoryClass},
				Score:  tt.initScore,
			}}
			got := search.ApplyKindAndClassWeights(results)
			const epsilon = 0.001
			if diff := got[0].Score - tt.wantScore; diff > epsilon || diff < -epsilon {
				t.Errorf("score: got %.3f, want %.3f", got[0].Score, tt.wantScore)
			}
		})
	}
}
