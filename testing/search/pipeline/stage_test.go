package pipeline_test

import (
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// TestNewState 验证初始状态字段 / Verify initial state fields
func TestNewState(t *testing.T) {
	identity := &model.Identity{TeamID: "team-1", OwnerID: "owner-1"}
	state := pipeline.NewState("test query", identity)

	if state.Query != "test query" {
		t.Errorf("Query = %q, want %q", state.Query, "test query")
	}
	if state.Identity != identity {
		t.Errorf("Identity = %v, want %v", state.Identity, identity)
	}
	if len(state.Candidates) != 0 {
		t.Errorf("Candidates len = %d, want 0", len(state.Candidates))
	}
	if len(state.Traces) != 0 {
		t.Errorf("Traces len = %d, want 0", len(state.Traces))
	}
	if state.Metadata == nil {
		t.Error("Metadata is nil, want non-nil map")
	}
}

// TestPipelineState_AddTrace 追加执行记录 / Append stage trace
func TestPipelineState_AddTrace(t *testing.T) {
	state := pipeline.NewState("q", nil)
	trace := pipeline.StageTrace{
		Name:        "fts",
		Duration:    50 * time.Millisecond,
		InputCount:  0,
		OutputCount: 5,
	}

	state.AddTrace(trace)

	if len(state.Traces) != 1 {
		t.Fatalf("Traces len = %d, want 1", len(state.Traces))
	}
	got := state.Traces[0]
	if got.Name != "fts" {
		t.Errorf("Name = %q, want %q", got.Name, "fts")
	}
	if got.Duration != 50*time.Millisecond {
		t.Errorf("Duration = %v, want %v", got.Duration, 50*time.Millisecond)
	}
	if got.InputCount != 0 {
		t.Errorf("InputCount = %d, want 0", got.InputCount)
	}
	if got.OutputCount != 5 {
		t.Errorf("OutputCount = %d, want 5", got.OutputCount)
	}
}

// TestPipelineState_Clone 克隆状态独立性 / Clone state independence
func TestPipelineState_Clone(t *testing.T) {
	state := pipeline.NewState("original", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Score: 0.9, Source: "sqlite"},
		{Score: 0.7, Source: "qdrant"},
	}
	state.AddTrace(pipeline.StageTrace{Name: "step1", OutputCount: 2})
	state.Metadata["key"] = "value"

	cloned := state.Clone()

	// Clone has empty candidates / 克隆后 Candidates 为空
	if len(cloned.Candidates) != 0 {
		t.Errorf("cloned Candidates len = %d, want 0", len(cloned.Candidates))
	}

	// Original candidates unchanged / 原始 Candidates 不变
	if len(state.Candidates) != 2 {
		t.Errorf("original Candidates len = %d, want 2", len(state.Candidates))
	}

	// Traces are copied / Traces 已复制
	if len(cloned.Traces) != 1 {
		t.Fatalf("cloned Traces len = %d, want 1", len(cloned.Traces))
	}
	if cloned.Traces[0].Name != "step1" {
		t.Errorf("cloned trace Name = %q, want %q", cloned.Traces[0].Name, "step1")
	}

	// Traces are independent / Traces 互相独立
	cloned.AddTrace(pipeline.StageTrace{Name: "step2"})
	if len(state.Traces) != 1 {
		t.Errorf("original Traces len = %d after clone mutation, want 1", len(state.Traces))
	}

	// Metadata is independent copy / Metadata 独立副本
	cloned.Metadata["new_key"] = "new_value"
	if _, ok := state.Metadata["new_key"]; ok {
		t.Error("original Metadata should not have 'new_key' after clone mutation")
	}
	if cloned.Metadata["key"] != "value" {
		t.Errorf("cloned Metadata[key] = %v, want %q", cloned.Metadata["key"], "value")
	}

	// Query and Identity are shared / Query 和 Identity 共享
	if cloned.Query != "original" {
		t.Errorf("cloned Query = %q, want %q", cloned.Query, "original")
	}
	if cloned.Identity != state.Identity {
		t.Error("cloned Identity should be same pointer as original")
	}
}
