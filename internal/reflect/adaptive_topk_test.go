package reflect

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdaptiveTopK(t *testing.T) {
	tests := []struct {
		name        string
		round       int
		maxRounds   int
		usedTokens  int
		tokenBudget int
		priorRounds []priorRoundSummary
		expectMin   int
		expectMax   int
	}{
		{"round 1 wide search", 1, 5, 0, 10000, nil, 15, 15},
		{"round 2 narrows", 2, 5, 1000, 10000, []priorRoundSummary{{}}, 8, 8},
		{"last round minimal", 5, 5, 5000, 10000, nil, 5, 5},
		{"low budget reduces", 2, 5, 8000, 10000, []priorRoundSummary{{}}, 3, 6},
		{"very low budget", 2, 5, 9500, 10000, []priorRoundSummary{{}}, 3, 4},
		{"no budget set", 1, 3, 0, 0, nil, 15, 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adaptiveTopK(tt.round, tt.maxRounds, tt.usedTokens, tt.tokenBudget, tt.priorRounds)
			assert.GreaterOrEqual(t, got, tt.expectMin, "topK should be >= %d", tt.expectMin)
			assert.LessOrEqual(t, got, tt.expectMax, "topK should be <= %d", tt.expectMax)
		})
	}
}
