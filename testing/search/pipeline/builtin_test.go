package pipeline_test

import (
	"testing"

	"iclude/internal/config"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/pipeline/builtin"
)

// TestRegisterBuiltins_AllPipelinesRegistered 全部 6 条内置管线注册成功
// All 6 built-in pipelines registered successfully
func TestRegisterBuiltins_AllPipelinesRegistered(t *testing.T) {
	reg := pipeline.NewRegistry()
	deps := builtin.Deps{} // 全 nil，仅测试注册 / all nil, just testing registration
	postStages := builtin.RegisterBuiltins(reg, deps)

	expected := []string{"precision", "exploration", "semantic", "association", "fast", "full"}
	for _, name := range expected {
		if reg.Get(name) == nil {
			t.Errorf("pipeline %q not registered", name)
		}
	}
	if len(postStages) != 3 {
		t.Errorf("expected 3 post-stages, got %d", len(postStages))
	}
}

// TestRegisterBuiltins_FallbackChains 降级链配置正确 / Fallback chains are correct
func TestRegisterBuiltins_FallbackChains(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})

	tests := []struct {
		name     string
		fallback string
	}{
		{"precision", "exploration"},
		{"exploration", ""},
		{"semantic", "exploration"},
		{"association", "precision"},
		{"fast", ""},
		{"full", "precision"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := reg.Get(tt.name)
			if p == nil {
				t.Fatalf("pipeline %q not found", tt.name)
			}
			if p.Fallback != tt.fallback {
				t.Errorf("%s fallback = %q, want %q", tt.name, p.Fallback, tt.fallback)
			}
		})
	}
}

// TestRegisterBuiltins_PrecisionHasParallelGroup precision 第一组为并行组
// precision first group is parallel
func TestRegisterBuiltins_PrecisionHasParallelGroup(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})
	p := reg.Get("precision")
	if p == nil {
		t.Fatal("precision pipeline not found")
	}
	if len(p.Stages) == 0 {
		t.Fatal("precision has no stages")
	}
	if !p.Stages[0].Parallel {
		t.Error("precision first group should be parallel")
	}
	if len(p.Stages[0].Stages) != 2 {
		t.Errorf("precision parallel group should have 2 stages, got %d", len(p.Stages[0].Stages))
	}
}

// TestRegisterBuiltins_ExplorationNoFallback exploration 是终端降级管线，无 fallback
// exploration is the terminal fallback pipeline
func TestRegisterBuiltins_ExplorationNoFallback(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})
	p := reg.Get("exploration")
	if p == nil {
		t.Fatal("exploration pipeline not found")
	}
	if p.Fallback != "" {
		t.Errorf("exploration should have no fallback, got %q", p.Fallback)
	}
	if len(p.Stages) != 4 {
		t.Errorf("exploration should have 4 stage groups, got %d", len(p.Stages))
	}
	if !p.Stages[0].Parallel {
		t.Error("exploration first group should be parallel")
	}
}

// TestRegisterBuiltins_SemanticStructure semantic 管线结构验证
// semantic pipeline structure validation
func TestRegisterBuiltins_SemanticStructure(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})
	p := reg.Get("semantic")
	if p == nil {
		t.Fatal("semantic pipeline not found")
	}
	if p.Fallback != "exploration" {
		t.Errorf("semantic fallback = %q, want %q", p.Fallback, "exploration")
	}
	if len(p.Stages) != 4 {
		t.Errorf("semantic should have 4 stage groups, got %d", len(p.Stages))
	}
	// 第一组并行: vector + fts / First group parallel: vector + fts
	if !p.Stages[0].Parallel {
		t.Error("semantic first group should be parallel")
	}
	if len(p.Stages[0].Stages) != 2 {
		t.Errorf("semantic parallel group should have 2 stages, got %d", len(p.Stages[0].Stages))
	}
}

// TestRegisterBuiltins_AssociationStructure association 管线结构验证
// association pipeline structure validation
func TestRegisterBuiltins_AssociationStructure(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})
	p := reg.Get("association")
	if p == nil {
		t.Fatal("association pipeline not found")
	}
	if p.Fallback != "precision" {
		t.Errorf("association fallback = %q, want %q", p.Fallback, "precision")
	}
	// graph → rerank_graph → score_filter = 3 sequential groups
	if len(p.Stages) != 3 {
		t.Errorf("association should have 3 stage groups, got %d", len(p.Stages))
	}
	// 全串行，无并行组 / All sequential, no parallel group
	for i, sg := range p.Stages {
		if sg.Parallel {
			t.Errorf("association group %d should not be parallel", i)
		}
	}
}

// TestRegisterBuiltins_FastStructure fast 管线结构验证
// fast pipeline structure validation
func TestRegisterBuiltins_FastStructure(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})
	p := reg.Get("fast")
	if p == nil {
		t.Fatal("fast pipeline not found")
	}
	if p.Fallback != "" {
		t.Errorf("fast should have no fallback, got %q", p.Fallback)
	}
	// fts → score_filter = 2 sequential groups
	if len(p.Stages) != 2 {
		t.Errorf("fast should have 2 stage groups, got %d", len(p.Stages))
	}
}

// TestRegisterBuiltins_FullStructure full 管线结构验证
// full pipeline structure validation
func TestRegisterBuiltins_FullStructure(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})
	p := reg.Get("full")
	if p == nil {
		t.Fatal("full pipeline not found")
	}
	if p.Fallback != "precision" {
		t.Errorf("full fallback = %q, want %q", p.Fallback, "precision")
	}
	if len(p.Stages) != 4 {
		t.Errorf("full should have 4 stage groups, got %d", len(p.Stages))
	}
	// 第一组并行: graph + fts + vector / First group parallel: graph + fts + vector
	if !p.Stages[0].Parallel {
		t.Error("full first group should be parallel")
	}
	if len(p.Stages[0].Stages) != 3 {
		t.Errorf("full parallel group should have 3 stages, got %d", len(p.Stages[0].Stages))
	}
}

// TestRegisterBuiltins_PostStageNames 后处理 stage 名称正确
// Post-processing stage names are correct
func TestRegisterBuiltins_PostStageNames(t *testing.T) {
	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		Cfg: config.RetrievalConfig{
			AccessAlpha: 0.5,
			MMR:         config.MMRConfig{Lambda: 0.7},
		},
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	expectedNames := []string{"mmr", "core", "trim"}
	if len(postStages) != len(expectedNames) {
		t.Fatalf("expected %d post-stages, got %d", len(expectedNames), len(postStages))
	}
	for i, name := range expectedNames {
		if postStages[i].Name() != name {
			t.Errorf("post-stage[%d] name = %q, want %q", i, postStages[i].Name(), name)
		}
	}
}

// TestRegisterBuiltins_StageNames 每条管线中 stage 名称验证
// Verify stage names within each pipeline
func TestRegisterBuiltins_StageNames(t *testing.T) {
	reg := pipeline.NewRegistry()
	builtin.RegisterBuiltins(reg, builtin.Deps{})

	tests := []struct {
		pipeline string
		// 每组中第一个 stage 的名称 / Name of first stage in each group
		firstStageNames []string
	}{
		{"precision", []string{"graph", "merge", "filter", "rerank_graph"}},
		{"exploration", []string{"graph", "merge", "filter", "rerank_overlap"}},
		{"semantic", []string{"vector", "merge", "filter", "rerank_overlap"}},
		{"association", []string{"graph", "rerank_graph", "filter"}},
		{"fast", []string{"fts", "filter"}},
		{"full", []string{"graph", "merge", "filter", "rerank_overlap"}},
	}

	for _, tt := range tests {
		t.Run(tt.pipeline, func(t *testing.T) {
			p := reg.Get(tt.pipeline)
			if p == nil {
				t.Fatalf("pipeline %q not found", tt.pipeline)
			}
			if len(p.Stages) != len(tt.firstStageNames) {
				t.Fatalf("%s: expected %d groups, got %d", tt.pipeline, len(tt.firstStageNames), len(p.Stages))
			}
			for i, expectedName := range tt.firstStageNames {
				if len(p.Stages[i].Stages) == 0 {
					t.Fatalf("%s group[%d] has no stages", tt.pipeline, i)
				}
				got := p.Stages[i].Stages[0].Name()
				if got != expectedName {
					t.Errorf("%s group[%d] first stage = %q, want %q", tt.pipeline, i, got, expectedName)
				}
			}
		})
	}
}
