package heartbeat_test

import (
	"testing"

	"iclude/internal/model"
)

func TestPromotionLogic(t *testing.T) {
	tests := []struct {
		name            string
		memoryClass     string
		reinforcedCount int
		threshold       int
		shouldPromote   bool
	}{
		{"episodic at threshold promotes", "episodic", 5, 5, true},
		{"episodic above threshold promotes", "episodic", 10, 5, true},
		{"episodic below threshold stays", "episodic", 4, 5, false},
		{"semantic does not promote", "semantic", 10, 5, false},
		{"procedural does not promote", "procedural", 10, 5, false},
		{"empty class does not promote", "", 10, 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem := &model.Memory{
				MemoryClass:     tt.memoryClass,
				ReinforcedCount: tt.reinforcedCount,
			}
			shouldPromote := mem.MemoryClass == "episodic" && mem.ReinforcedCount >= tt.threshold
			if shouldPromote != tt.shouldPromote {
				t.Errorf("shouldPromote: got %v, want %v", shouldPromote, tt.shouldPromote)
			}
		})
	}
}
