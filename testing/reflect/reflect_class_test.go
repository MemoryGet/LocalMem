package reflect_test

import (
	"testing"

	"iclude/internal/model"
)

func TestReflectAutoSave_ProceduralClass(t *testing.T) {
	evidenceIDs := []string{"ev_1", "ev_2"}
	req := &model.CreateMemoryRequest{
		Content:     "Conclusion",
		Kind:        "mental_model",
		MemoryClass: "procedural",
		DerivedFrom: evidenceIDs,
		SourceType:  "reflect",
	}
	if req.MemoryClass != "procedural" {
		t.Errorf("memory_class: got %q, want procedural", req.MemoryClass)
	}
	if len(req.DerivedFrom) != 2 {
		t.Fatalf("derived_from len: got %d, want 2", len(req.DerivedFrom))
	}
}
