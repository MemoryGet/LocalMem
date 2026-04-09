package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// maxFallbackDepth 降级链最大深度 / Max fallback chain depth
const maxFallbackDepth = 3

// StageGroup 一组 stage，可并行或串行执行 / A group of stages, parallel or sequential
type StageGroup struct {
	Parallel bool    // true = 组内并行执行 / Parallel execution within group
	Stages   []Stage // 组内 stage 列表 / Stages in this group
}

// Pipeline 管线定义 / Pipeline definition
type Pipeline struct {
	Name     string       // 管线名称 / Pipeline name
	Stages   []StageGroup // 有序 stage 组 / Ordered stage groups
	Fallback string       // 降级管线名称，空=不降级 / Fallback pipeline name
}

// ExecutorOption 执行器选项 / Executor option
type ExecutorOption func(*Executor)

// WithPostStages 设置固定后处理 stage / Set fixed post-processing stages
func WithPostStages(stages ...Stage) ExecutorOption {
	return func(e *Executor) {
		e.postStages = stages
	}
}

// Executor 管线执行器 / Pipeline executor
type Executor struct {
	registry   *Registry
	postStages []Stage // 固定后处理 stage / Fixed post-processing stages
}

// NewExecutor 创建管线执行器 / Create a new pipeline executor
func NewExecutor(registry *Registry, opts ...ExecutorOption) *Executor {
	e := &Executor{
		registry: registry,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Execute 执行指定管线 / Execute a named pipeline
// 后处理 stage 仅在此处执行一次，不在降级递归中重复 / Post-stages run once here, not inside fallback recursion
func (e *Executor) Execute(ctx context.Context, name string, state *PipelineState) (*PipelineState, error) {
	state, err := e.executeWithDepth(ctx, name, state, 0)
	if err != nil {
		return state, err
	}

	// 后处理 stage（所有管线共享，仅执行一次）/ Post-processing stages (shared, run exactly once)
	for _, ps := range e.postStages {
		state, err = e.executeWithTrace(ctx, ps, state)
		if err != nil {
			return state, fmt.Errorf("post-stage %q: %w", ps.Name(), err)
		}
	}

	return state, nil
}

// executeWithDepth 带深度限制的管线执行 / Pipeline execution with depth limit
func (e *Executor) executeWithDepth(ctx context.Context, name string, state *PipelineState, depth int) (*PipelineState, error) {
	p := e.registry.Get(name)
	if p == nil {
		return nil, fmt.Errorf("pipeline %q not found in registry", name)
	}

	state.PipelineName = name

	// 执行 stage 组 / Execute stage groups
	var err error
	for _, group := range p.Stages {
		if group.Parallel {
			state, err = e.executeParallelGroup(ctx, group.Stages, state)
		} else {
			state, err = e.executeSequentialGroup(ctx, group.Stages, state)
		}
		if err != nil {
			return state, fmt.Errorf("pipeline %q stage group: %w", name, err)
		}
	}

	// 降级逻辑 / Fallback logic
	if len(state.Candidates) == 0 && p.Fallback != "" && depth < maxFallbackDepth {
		logger.Info("pipeline fallback triggered",
			zap.String("from", name),
			zap.String("to", p.Fallback),
			zap.Int("depth", depth+1),
		)
		fallbackState := state.Clone()
		fallbackState, err = e.executeWithDepth(ctx, p.Fallback, fallbackState, depth+1)
		if err != nil {
			return state, fmt.Errorf("fallback pipeline %q: %w", p.Fallback, err)
		}
		// 合并降级结果和 trace / Merge fallback results and traces
		state.Candidates = append(state.Candidates, fallbackState.Candidates...)
		state.Traces = append(state.Traces, fallbackState.Traces...)
	}

	return state, nil
}

// executeSequentialGroup 串行执行一组 stage / Execute stages sequentially
func (e *Executor) executeSequentialGroup(ctx context.Context, stages []Stage, state *PipelineState) (*PipelineState, error) {
	var err error
	for _, s := range stages {
		state, err = e.executeWithTrace(ctx, s, state)
		if err != nil {
			return state, err
		}
	}
	return state, nil
}

// executeParallelGroup 并行执行一组 stage / Execute stages in parallel
func (e *Executor) executeParallelGroup(ctx context.Context, stages []Stage, state *PipelineState) (*PipelineState, error) {
	groupStart := time.Now()
	totalInputCount := len(state.Candidates)

	type stageResult struct {
		candidates []*model.SearchResult
		trace      StageTrace
	}

	results := make([]stageResult, len(stages))
	var wg sync.WaitGroup

	for i, s := range stages {
		wg.Add(1)
		go func(idx int, stage Stage) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("parallel stage panicked",
						zap.String("stage", stage.Name()),
						zap.Any("panic", r),
					)
					results[idx] = stageResult{
						trace: StageTrace{
							Name:    stage.Name(),
							Skipped: true,
							Note:    fmt.Sprintf("panic: %v", r),
						},
					}
				}
			}()

			// 每个并行 stage 获得独立的 state 副本（空 Candidates）
			// Each parallel stage gets an independent state clone with empty Candidates
			cloned := state.Clone()
			start := time.Now()
			inputCount := len(cloned.Candidates)

			executed, err := stage.Execute(ctx, cloned)
			duration := time.Since(start)

			if err != nil {
				logger.Warn("parallel stage error",
					zap.String("stage", stage.Name()),
					zap.Error(err),
				)
				results[idx] = stageResult{
					trace: StageTrace{
						Name:    stage.Name(),
						Skipped: true,
						Note:    err.Error(),
					},
				}
				return
			}

			results[idx] = stageResult{
				candidates: executed.Candidates,
				trace: StageTrace{
					Name:        stage.Name(),
					Duration:    duration,
					InputCount:  inputCount,
					OutputCount: len(executed.Candidates),
				},
			}
		}(i, s)
	}

	wg.Wait()

	// 合并结果和 trace / Merge results and traces
	for _, r := range results {
		state.Candidates = append(state.Candidates, r.candidates...)
		state.AddTrace(r.trace)
	}

	// 添加并行组汇总 trace / Add parallel group summary trace
	state.AddTrace(StageTrace{
		Name:        "parallel_group",
		Duration:    time.Since(groupStart),
		InputCount:  totalInputCount,
		OutputCount: len(state.Candidates),
	})

	return state, nil
}

// executeWithTrace 带 trace 记录的 stage 执行 / Execute a stage with trace recording
func (e *Executor) executeWithTrace(ctx context.Context, s Stage, state *PipelineState) (*PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	result, err := s.Execute(ctx, state)
	duration := time.Since(start)

	trace := StageTrace{
		Name:        s.Name(),
		Duration:    duration,
		InputCount:  inputCount,
		OutputCount: len(result.Candidates),
	}

	if err != nil {
		trace.Note = err.Error()
	}

	result.AddTrace(trace)
	return result, err
}
