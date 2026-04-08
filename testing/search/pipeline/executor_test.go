package pipeline_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// mockStage 测试用 mock stage / Mock stage for testing
type mockStage struct {
	name    string
	results []*model.SearchResult
	err     error
}

func (m *mockStage) Name() string { return m.name }

func (m *mockStage) Execute(_ context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	if m.err != nil {
		return state, m.err
	}
	state.Candidates = append(state.Candidates, m.results...)
	return state, nil
}

// panicStage 测试用 panic stage / Mock stage that panics
type panicStage struct {
	name string
}

func (p *panicStage) Name() string { return p.name }

func (p *panicStage) Execute(_ context.Context, _ *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	panic("intentional panic for test")
}

// TestExecutor_Sequential 顺序执行两个 stage / Sequential execution of two stages
func TestExecutor_Sequential(t *testing.T) {
	reg := pipeline.NewRegistry()
	reg.Register(&pipeline.Pipeline{
		Name: "seq",
		Stages: []pipeline.StageGroup{
			{Parallel: false, Stages: []pipeline.Stage{
				&mockStage{name: "s1", results: []*model.SearchResult{
					{Score: 0.9, Source: "sqlite"},
				}},
			}},
			{Parallel: false, Stages: []pipeline.Stage{
				&mockStage{name: "s2", results: []*model.SearchResult{
					{Score: 0.8, Source: "qdrant"},
				}},
			}},
		},
	})

	exec := pipeline.NewExecutor(reg)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})

	result, err := exec.Execute(context.Background(), "seq", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证两个 stage 都贡献了候选结果 / Verify both stages contributed candidates
	if len(result.Candidates) != 2 {
		t.Errorf("Candidates len = %d, want 2", len(result.Candidates))
	}

	// 验证 trace 记录 / Verify traces recorded
	traceNames := traceNameSet(result.Traces)
	if !traceNames["s1"] {
		t.Error("missing trace for s1")
	}
	if !traceNames["s2"] {
		t.Error("missing trace for s2")
	}
}

// TestExecutor_Parallel 并行组执行 / Parallel group execution
func TestExecutor_Parallel(t *testing.T) {
	reg := pipeline.NewRegistry()
	reg.Register(&pipeline.Pipeline{
		Name: "par",
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				&mockStage{name: "p1", results: []*model.SearchResult{
					{Score: 0.9, Source: "sqlite"},
				}},
				&mockStage{name: "p2", results: []*model.SearchResult{
					{Score: 0.7, Source: "qdrant"},
				}},
			}},
		},
	})

	exec := pipeline.NewExecutor(reg)
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "par", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证并行 stage 都贡献了候选结果 / Verify parallel stages contributed candidates
	if len(result.Candidates) != 2 {
		t.Errorf("Candidates len = %d, want 2", len(result.Candidates))
	}

	// 验证有 parallel_group 汇总 trace / Verify parallel_group summary trace
	traceNames := traceNameSet(result.Traces)
	if !traceNames["parallel_group"] {
		t.Error("missing parallel_group summary trace")
	}
}

// TestExecutor_FallbackOnEmpty 空结果触发降级 / Empty results trigger fallback
func TestExecutor_FallbackOnEmpty(t *testing.T) {
	reg := pipeline.NewRegistry()
	// 主管线不返回结果 / Primary pipeline returns no results
	reg.Register(&pipeline.Pipeline{
		Name: "empty",
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{
				&mockStage{name: "noop", results: nil},
			}},
		},
		Fallback: "backup",
	})
	// 降级管线返回结果 / Fallback pipeline returns results
	reg.Register(&pipeline.Pipeline{
		Name: "backup",
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{
				&mockStage{name: "backup_stage", results: []*model.SearchResult{
					{Score: 0.5, Source: "sqlite"},
				}},
			}},
		},
	})

	exec := pipeline.NewExecutor(reg)
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "empty", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Candidates) != 1 {
		t.Errorf("Candidates len = %d, want 1 from fallback", len(result.Candidates))
	}

	// 验证降级管线的 trace 存在 / Verify fallback pipeline trace exists
	traceNames := traceNameSet(result.Traces)
	if !traceNames["backup_stage"] {
		t.Error("missing trace for backup_stage from fallback pipeline")
	}
}

// TestExecutor_FallbackDepthLimit 降级链深度限制 / Fallback chain depth limit
func TestExecutor_FallbackDepthLimit(t *testing.T) {
	reg := pipeline.NewRegistry()
	// 5 个空管线链式降级：p0→p1→p2→p3→p4 / 5 empty pipelines chaining fallbacks
	// maxFallbackDepth=3，允许 3 次降级跳转，p4 不应执行
	// maxFallbackDepth=3 allows 3 fallback hops, p4 should NOT execute
	names := []string{"p0", "p1", "p2", "p3", "p4"}
	fallbacks := []string{"p1", "p2", "p3", "p4", ""}
	for i, name := range names {
		reg.Register(&pipeline.Pipeline{
			Name: name,
			Stages: []pipeline.StageGroup{
				{Stages: []pipeline.Stage{
					&mockStage{name: "stage_" + name, results: nil},
				}},
			},
			Fallback: fallbacks[i],
		})
	}

	exec := pipeline.NewExecutor(reg)
	state := pipeline.NewState("test", nil)

	// 应该正常返回（不死循环）/ Should return normally (no infinite loop)
	result, err := exec.Execute(context.Background(), "p0", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	traceNames := traceNameSet(result.Traces)

	// p0→p1→p2→p3 共 3 次降级，均应执行 / 3 fallback hops, all should execute
	for _, name := range []string{"stage_p0", "stage_p1", "stage_p2", "stage_p3"} {
		if !traceNames[name] {
			t.Errorf("expected trace %q to exist", name)
		}
	}

	// p4 超过深度限制，不应执行 / p4 exceeds depth limit, should NOT execute
	if traceNames["stage_p4"] {
		t.Error("stage_p4 should not execute due to depth limit")
	}
}

// TestExecutor_PostStages 后处理 stage 执行 / Post-processing stages execution
func TestExecutor_PostStages(t *testing.T) {
	reg := pipeline.NewRegistry()
	reg.Register(&pipeline.Pipeline{
		Name: "main",
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{
				&mockStage{name: "fetch", results: []*model.SearchResult{
					{Score: 0.9, Source: "sqlite"},
					{Score: 0.8, Source: "qdrant"},
				}},
			}},
		},
	})

	postStage := &mockStage{name: "post_trim", results: []*model.SearchResult{
		{Score: 0.6, Source: "post"},
	}}

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStage))
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "main", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证后处理 stage 也贡献了结果 / Verify post-processing stage also contributed
	if len(result.Candidates) != 3 {
		t.Errorf("Candidates len = %d, want 3 (2 from main + 1 from post)", len(result.Candidates))
	}

	traceNames := traceNameSet(result.Traces)
	if !traceNames["post_trim"] {
		t.Error("missing trace for post_trim")
	}
}

// TestRegistry_UnknownPipeline 未知管线返回错误 / Unknown pipeline returns error
func TestRegistry_UnknownPipeline(t *testing.T) {
	reg := pipeline.NewRegistry()
	exec := pipeline.NewExecutor(reg)
	state := pipeline.NewState("test", nil)

	_, err := exec.Execute(context.Background(), "nonexistent", state)
	if err == nil {
		t.Fatal("expected error for unknown pipeline, got nil")
	}
}

// TestExecutor_ParallelPanicRecovery 并行 stage panic 恢复 / Parallel stage panic recovery
func TestExecutor_ParallelPanicRecovery(t *testing.T) {
	reg := pipeline.NewRegistry()
	reg.Register(&pipeline.Pipeline{
		Name: "panic_test",
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				&panicStage{name: "boom"},
				&mockStage{name: "safe", results: []*model.SearchResult{
					{Score: 0.5, Source: "sqlite"},
				}},
			}},
		},
	})

	exec := pipeline.NewExecutor(reg)
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "panic_test", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 安全的 stage 仍然执行 / Safe stage still executes
	if len(result.Candidates) < 1 {
		t.Errorf("Candidates len = %d, want >= 1 (safe stage should still contribute)", len(result.Candidates))
	}

	// panic stage 的 trace 应标记 Skipped / Panicked stage trace should be Skipped
	for _, tr := range result.Traces {
		if tr.Name == "boom" {
			if !tr.Skipped {
				t.Error("panicked stage should be marked Skipped")
			}
			return
		}
	}
	t.Error("missing trace for panicked stage 'boom'")
}

// TestExecutor_ParallelStageError 并行 stage 错误不阻塞其他 / Parallel stage error doesn't block others
func TestExecutor_ParallelStageError(t *testing.T) {
	reg := pipeline.NewRegistry()
	reg.Register(&pipeline.Pipeline{
		Name: "err_test",
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				&mockStage{name: "fail", err: errors.New("stage error")},
				&mockStage{name: "ok", results: []*model.SearchResult{
					{Score: 0.5, Source: "sqlite"},
				}},
			}},
		},
	})

	exec := pipeline.NewExecutor(reg)
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "err_test", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ok stage 仍然执行 / ok stage still executes
	if len(result.Candidates) < 1 {
		t.Error("ok stage should still contribute candidates")
	}

	// fail stage 的 trace 应标记 Skipped / Failed stage trace should be Skipped
	for _, tr := range result.Traces {
		if tr.Name == "fail" {
			if !tr.Skipped {
				t.Error("failed stage should be marked Skipped")
			}
			return
		}
	}
	t.Error("missing trace for failed stage")
}

// traceNameSet 辅助函数：从 traces 提取 name 集合 / Helper: extract name set from traces
func traceNameSet(traces []pipeline.StageTrace) map[string]bool {
	set := make(map[string]bool, len(traces))
	for _, tr := range traces {
		set[tr.Name] = true
	}
	return set
}
