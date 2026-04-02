package memory_test

import (
	"testing"

	"iclude/internal/model"
)

func TestConsolidateOutput_SemanticClass(t *testing.T) {
	sourceIDs := []string{"mem_a", "mem_b", "mem_c"}
	consolidated := &model.Memory{
		MemoryClass: "semantic",
		Kind:        "consolidated",
		DerivedFrom: sourceIDs,
	}
	if consolidated.MemoryClass != "semantic" {
		t.Errorf("memory_class: got %q, want semantic", consolidated.MemoryClass)
	}
	if len(consolidated.DerivedFrom) != 3 {
		t.Fatalf("derived_from len: got %d, want 3", len(consolidated.DerivedFrom))
	}
}
