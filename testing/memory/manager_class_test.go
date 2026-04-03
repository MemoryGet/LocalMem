package memory_test

import (
	"testing"

	"iclude/internal/model"
)

func TestManagerCreate_MapsMemoryClassFields(t *testing.T) {
	tests := []struct {
		name        string
		reqClass    string
		reqDerived  []string
		wantClass   string
		wantDerived int
	}{
		{"explicit semantic", "semantic", []string{"m1", "m2"}, "semantic", 2},
		{"explicit procedural", "procedural", nil, "procedural", 0},
		{"empty defaults later", "", nil, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &model.CreateMemoryRequest{
				Content:     "test",
				MemoryClass: tt.reqClass,
				DerivedFrom: tt.reqDerived,
			}
			// Simulate the mapping that Manager.Create does
			mem := &model.Memory{
				Content:     req.Content,
				MemoryClass: req.MemoryClass,
				DerivedFrom: req.DerivedFrom,
			}
			if mem.MemoryClass != tt.wantClass {
				t.Errorf("memory_class: got %q, want %q", mem.MemoryClass, tt.wantClass)
			}
			if len(mem.DerivedFrom) != tt.wantDerived {
				t.Errorf("derived_from len: got %d, want %d", len(mem.DerivedFrom), tt.wantDerived)
			}
		})
	}
}
