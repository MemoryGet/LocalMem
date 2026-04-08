# Pipeline Retrieval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the monolithic 4-channel parallel retriever with a strategy-agent-driven, multi-pipeline architecture where each pipeline is composed of reusable stages.

**Architecture:** Stage interface + Pipeline executor with parallel groups, fallback chains, and trace. Strategy Agent (LLM + rule fallback) selects from 6 built-in pipelines. Fixed post-processing tail shared by all pipelines.

**Tech Stack:** Go 1.25+, SQLite FTS5, Qdrant, existing `llm.Provider`, existing `store.*` interfaces

**Spec:** `docs/superpowers/specs/2026-04-08-pipeline-retrieval-design.md`

---

## File Structure

```
internal/search/
  pipeline/
    stage.go             — Stage interface + PipelineState + StageTrace (NEW)
    pipeline.go          — Pipeline struct + Executor + parallel/trace/fallback (NEW)
    registry.go          — Pipeline registry (name → Pipeline) (NEW)
    builtin.go           — 6 built-in pipeline definitions + post-processing tail (NEW)
  strategy/
    agent.go             — LLM strategy selector + query preprocessing (NEW)
    rules.go             — Rule-based classifier (no-LLM fallback) (NEW)
  stage/
    graph.go             — Graph retrieval stage (EXTRACT from retriever.go + retriever_graph.go)
    fts.go               — FTS5 retrieval stage (EXTRACT from retriever.go)
    vector.go            — Qdrant vector stage (EXTRACT from retriever.go)
    temporal.go          — Temporal retrieval stage (EXTRACT from retriever_util.go)
    merge.go             — Merge stage: RRF + GraphAware (EXTRACT from rrf.go + NEW)
    filter.go            — Score ratio filter stage (NEW)
    rerank_overlap.go    — Overlap reranker stage (EXTRACT from reranker.go)
    rerank_remote.go     — Remote reranker stage (EXTRACT from reranker_remote.go)
    rerank_llm.go        — LLM reranker stage (NEW)
    rerank_graph.go      — Graph distance reranker stage (NEW)
    weight.go            — Kind/class/scope/strength weight stage (EXTRACT from retriever_weights.go + scoring)
    mmr.go               — MMR diversity stage (EXTRACT from mmr.go)
    core.go              — Core memory injection stage (EXTRACT from retriever_util.go)
    trim.go              — Token budget trim stage (EXTRACT from retriever_util.go)
  retriever.go           — REFACTOR: delegate to strategy → pipeline
  circuit_breaker.go     — KEEP (shared by rerank_remote + rerank_llm)
  experience_recall.go   — KEEP (calls Retriever.Retrieve, no change needed)
  reranker_common.go     — KEEP (shared helpers: normalizeRerankText, splitRerankTerms, isHanOnly)

internal/config/config.go — ADD: StrategyConfig, PipelineOverrides, MinRelevance

testing/search/
  stage/                 — Unit tests per stage (NEW dir)
  pipeline/              — Pipeline integration tests (NEW dir)
  strategy/              — Strategy agent tests (NEW dir)
```

---

## Phase 1: Foundation (Stage Interface + Pipeline Engine)

### Task 1: Stage Interface + PipelineState

**Files:**
- Create: `internal/search/pipeline/stage.go`
- Test: `testing/search/pipeline/stage_test.go`

- [ ] **Step 1: Write the test for PipelineState initialization**

```go
// testing/search/pipeline/stage_test.go
package pipeline_test

import (
	"testing"

	"iclude/internal/search/pipeline"
)

func TestPipelineState_NewState(t *testing.T) {
	state := pipeline.NewState("test query", nil)
	if state.Query != "test query" {
		t.Errorf("expected query 'test query', got %q", state.Query)
	}
	if state.Confidence != "" {
		t.Errorf("expected empty confidence, got %q", state.Confidence)
	}
	if len(state.Candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(state.Candidates))
	}
	if len(state.Traces) != 0 {
		t.Errorf("expected 0 traces, got %d", len(state.Traces))
	}
}

func TestPipelineState_AddTrace(t *testing.T) {
	state := pipeline.NewState("q", nil)
	state.AddTrace(pipeline.StageTrace{Name: "fts", InputCount: 0, OutputCount: 5})
	if len(state.Traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(state.Traces))
	}
	if state.Traces[0].Name != "fts" {
		t.Errorf("expected trace name 'fts', got %q", state.Traces[0].Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/LocalMem && go test ./testing/search/pipeline/ -run TestPipelineState -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement Stage interface + PipelineState**

```go
// internal/search/pipeline/stage.go
package pipeline

import (
	"context"
	"time"

	"iclude/internal/model"
)

// NOTE: PipelineState.Plan 复用现有 search.QueryPlan（定义在 internal/search/preprocess.go）
// 不新增类型，避免重复定义

// Stage 检索管线阶段接口 / Pipeline stage interface
type Stage interface {
	Name() string
	Execute(ctx context.Context, state *PipelineState) (*PipelineState, error)
}

// QueryPlan 从 search 包引入的类型别名，避免循环依赖时可改为接口
// 目前直接引用 search.QueryPlan
type QueryPlan = search.QueryPlan

// PipelineState 在 Stage 之间流转的状态 / State flowing between stages
type PipelineState struct {
	Query        string
	Identity     *model.Identity
	Plan         *QueryPlan
	Candidates   []*model.SearchResult
	Confidence   string // "high" | "low" | "none" | ""
	Metadata     map[string]interface{}
	Traces       []StageTrace
	PipelineName string
}

// StageTrace 单个 stage 的执行记录 / Execution trace for a single stage
type StageTrace struct {
	Name        string        `json:"name"`
	Duration    time.Duration `json:"duration"`
	InputCount  int           `json:"in"`
	OutputCount int           `json:"out"`
	Skipped     bool          `json:"skipped,omitempty"`
	Note        string        `json:"note,omitempty"`
}

// NewState 创建初始状态 / Create initial pipeline state
func NewState(query string, identity *model.Identity) *PipelineState {
	return &PipelineState{
		Query:    query,
		Identity: identity,
		Metadata: make(map[string]interface{}),
	}
}

// AddTrace 追加 stage 执行记录 / Append stage trace
func (s *PipelineState) AddTrace(t StageTrace) {
	s.Traces = append(s.Traces, t)
}

// Clone 浅拷贝状态（用于降级链，保留原始查询）/ Shallow-clone state for fallback chain
func (s *PipelineState) Clone() *PipelineState {
	c := *s
	c.Candidates = nil
	c.Traces = append([]StageTrace(nil), s.Traces...)
	c.Metadata = make(map[string]interface{})
	for k, v := range s.Metadata {
		c.Metadata[k] = v
	}
	return c
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/LocalMem && go test ./testing/search/pipeline/ -run TestPipelineState -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/search/pipeline/stage.go testing/search/pipeline/stage_test.go
git commit -m "feat(search): add Stage interface and PipelineState"
```

---

### Task 2: Pipeline Executor + Registry

**Files:**
- Create: `internal/search/pipeline/pipeline.go`
- Create: `internal/search/pipeline/registry.go`
- Test: `testing/search/pipeline/executor_test.go`

- [ ] **Step 1: Write the test for sequential pipeline execution**

```go
// testing/search/pipeline/executor_test.go
package pipeline_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// mockStage 测试用 stage / Test stage
type mockStage struct {
	name    string
	results []*model.SearchResult
	err     error
}

func (m *mockStage) Name() string { return m.name }
func (m *mockStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	if m.err != nil {
		return state, m.err
	}
	state.Candidates = append(state.Candidates, m.results...)
	return state, nil
}

func TestExecutor_Sequential(t *testing.T) {
	mem1 := &model.Memory{ID: "m1", Content: "hello"}
	mem2 := &model.Memory{ID: "m2", Content: "world"}

	p := &pipeline.Pipeline{
		Name: "test",
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{&mockStage{name: "s1", results: []*model.SearchResult{{Memory: mem1, Score: 1.0}}}}},
			{Stages: []pipeline.Stage{&mockStage{name: "s2", results: []*model.SearchResult{{Memory: mem2, Score: 0.5}}}}},
		},
	}

	reg := pipeline.NewRegistry()
	reg.Register(p)

	exec := pipeline.NewExecutor(reg, nil) // nil post-process stages
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "test", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Candidates))
	}
	if len(result.Traces) != 2 {
		t.Fatalf("expected 2 traces, got %d", len(result.Traces))
	}
	if result.PipelineName != "test" {
		t.Errorf("expected pipeline name 'test', got %q", result.PipelineName)
	}
}

func TestExecutor_Parallel(t *testing.T) {
	mem1 := &model.Memory{ID: "m1", Content: "a"}
	mem2 := &model.Memory{ID: "m2", Content: "b"}

	p := &pipeline.Pipeline{
		Name: "par",
		Stages: []pipeline.StageGroup{
			{
				Parallel: true,
				Stages: []pipeline.Stage{
					&mockStage{name: "p1", results: []*model.SearchResult{{Memory: mem1, Score: 1.0}}},
					&mockStage{name: "p2", results: []*model.SearchResult{{Memory: mem2, Score: 0.8}}},
				},
			},
		},
	}

	reg := pipeline.NewRegistry()
	reg.Register(p)

	exec := pipeline.NewExecutor(reg, nil)
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "par", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Candidates))
	}
}

func TestExecutor_FallbackOnEmpty(t *testing.T) {
	mem1 := &model.Memory{ID: "m1", Content: "fallback"}

	emptyPipeline := &pipeline.Pipeline{
		Name:     "empty",
		Fallback: "backup",
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{&mockStage{name: "noop"}}},
		},
	}
	backupPipeline := &pipeline.Pipeline{
		Name: "backup",
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{&mockStage{name: "s1", results: []*model.SearchResult{{Memory: mem1, Score: 1.0}}}}},
		},
	}

	reg := pipeline.NewRegistry()
	reg.Register(emptyPipeline)
	reg.Register(backupPipeline)

	exec := pipeline.NewExecutor(reg, nil)
	state := pipeline.NewState("test", nil)

	result, err := exec.Execute(context.Background(), "empty", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate from fallback, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Memory.ID != "m1" {
		t.Errorf("expected memory m1, got %s", result.Candidates[0].Memory.ID)
	}
}

func TestRegistry_UnknownPipeline(t *testing.T) {
	reg := pipeline.NewRegistry()
	exec := pipeline.NewExecutor(reg, nil)
	state := pipeline.NewState("test", nil)

	_, err := exec.Execute(context.Background(), "nonexistent", state)
	if err == nil {
		t.Fatal("expected error for unknown pipeline")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/LocalMem && go test ./testing/search/pipeline/ -run TestExecutor -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement Pipeline + Executor + Registry**

```go
// internal/search/pipeline/pipeline.go
package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"iclude/internal/logger"

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

// Executor 管线执行器 / Pipeline executor
type Executor struct {
	registry   *Registry
	postStages []Stage // 固定后处理 stage / Fixed post-processing stages
}

// NewExecutor 创建执行器 / Create executor
func NewExecutor(registry *Registry, postStages []Stage) *Executor {
	return &Executor{registry: registry, postStages: postStages}
}

// Execute 执行指定管线 / Execute a named pipeline
func (e *Executor) Execute(ctx context.Context, name string, state *PipelineState) (*PipelineState, error) {
	return e.executeWithDepth(ctx, name, state, 0)
}

func (e *Executor) executeWithDepth(ctx context.Context, name string, state *PipelineState, depth int) (*PipelineState, error) {
	if depth >= maxFallbackDepth {
		return state, nil
	}

	p := e.registry.Get(name)
	if p == nil {
		return nil, fmt.Errorf("unknown pipeline: %s", name)
	}

	state.PipelineName = name

	// 执行管线的可变部分 / Execute pipeline stages
	var err error
	for _, group := range p.Stages {
		state, err = e.executeGroup(ctx, group, state)
		if err != nil {
			return nil, fmt.Errorf("pipeline %s: %w", name, err)
		}
	}

	// 结果为空 + 有降级链 → 尝试下一管线 / Empty results + fallback → try next pipeline
	if len(state.Candidates) == 0 && p.Fallback != "" {
		state.AddTrace(StageTrace{
			Name: "fallback",
			Note: fmt.Sprintf("empty results, falling back to %s", p.Fallback),
		})
		fallbackState := state.Clone()
		return e.executeWithDepth(ctx, p.Fallback, fallbackState, depth+1)
	}

	// 执行固定后处理 / Execute post-processing stages
	for _, stage := range e.postStages {
		state, err = executeWithTrace(ctx, stage, state)
		if err != nil {
			return nil, fmt.Errorf("post-process %s: %w", stage.Name(), err)
		}
	}

	return state, nil
}

// executeGroup 执行一组 stage（并行或串行）/ Execute a stage group
func (e *Executor) executeGroup(ctx context.Context, group StageGroup, state *PipelineState) (*PipelineState, error) {
	if !group.Parallel || len(group.Stages) <= 1 {
		// 串行执行 / Sequential
		var err error
		for _, stage := range group.Stages {
			state, err = executeWithTrace(ctx, stage, state)
			if err != nil {
				return nil, err
			}
		}
		return state, nil
	}

	// 并行执行 / Parallel execution
	var mu sync.Mutex
	var wg sync.WaitGroup
	inputCount := len(state.Candidates)

	for _, s := range group.Stages {
		wg.Add(1)
		go func(stage Stage) {
			defer wg.Done()
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("pipeline stage panic recovered",
						zap.String("stage", stage.Name()),
						zap.Any("panic", rv),
					)
				}
			}()

			// 每个并行 stage 用独立状态副本执行 / Each parallel stage gets its own state copy
			localState := state.Clone()
			localState.Candidates = nil

			start := time.Now()
			result, err := stage.Execute(ctx, localState)

			trace := StageTrace{
				Name:        stage.Name(),
				Duration:    time.Since(start),
				InputCount:  0, // 并行 stage 从空开始 / Parallel stages start empty
				OutputCount: 0,
			}

			if err != nil {
				trace.Note = fmt.Sprintf("error: %v", err)
				trace.Skipped = true
			} else if result != nil {
				trace.OutputCount = len(result.Candidates)
			}

			mu.Lock()
			state.Traces = append(state.Traces, trace)
			if result != nil && len(result.Candidates) > 0 {
				state.Candidates = append(state.Candidates, result.Candidates...)
			}
			mu.Unlock()
		}(s)
	}

	wg.Wait()

	// 并行组整体 trace / Overall parallel group trace
	state.AddTrace(StageTrace{
		Name:        "parallel_group",
		InputCount:  inputCount,
		OutputCount: len(state.Candidates),
		Note:        fmt.Sprintf("%d stages", len(group.Stages)),
	})

	return state, nil
}

// executeWithTrace 带 trace 的 stage 执行 / Execute stage with tracing
func executeWithTrace(ctx context.Context, stage Stage, state *PipelineState) (*PipelineState, error) {
	inputCount := len(state.Candidates)
	start := time.Now()

	result, err := stage.Execute(ctx, state)
	if err != nil {
		return result, err
	}

	trace := StageTrace{
		Name:        stage.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(result.Candidates),
	}
	result.Traces = append(result.Traces, trace)
	return result, nil
}
```

```go
// internal/search/pipeline/registry.go
package pipeline

import "sync"

// Registry 管线注册表 / Pipeline registry
type Registry struct {
	mu        sync.RWMutex
	pipelines map[string]*Pipeline
}

// NewRegistry 创建注册表 / Create registry
func NewRegistry() *Registry {
	return &Registry{pipelines: make(map[string]*Pipeline)}
}

// Register 注册管线 / Register a pipeline
func (r *Registry) Register(p *Pipeline) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pipelines[p.Name] = p
}

// Get 获取管线 / Get pipeline by name
func (r *Registry) Get(name string) *Pipeline {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pipelines[name]
}

// Names 列出所有管线名称 / List all pipeline names
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.pipelines))
	for name := range r.pipelines {
		names = append(names, name)
	}
	return names
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/LocalMem && go test ./testing/search/pipeline/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/search/pipeline/ testing/search/pipeline/
git commit -m "feat(search): add Pipeline executor with parallel groups, fallback chains, and tracing"
```

---

## Phase 2: Core Stages (Extract from existing code)

### Task 3: FTS Stage

**Files:**
- Create: `internal/search/stage/fts.go`
- Test: `testing/search/stage/fts_test.go`

- [ ] **Step 1: Write test for FTS stage**

```go
// testing/search/stage/fts_test.go
package stage_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

type mockMemorySearch struct {
	results []*model.SearchResult
	err     error
}

func (m *mockMemorySearch) SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
	return m.results, m.err
}
func (m *mockMemorySearch) SearchTextFiltered(ctx context.Context, query string, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	return m.results, m.err
}

func TestFTSStage_Execute(t *testing.T) {
	mem := &model.Memory{ID: "m1", Content: "test"}
	mock := &mockMemorySearch{results: []*model.SearchResult{{Memory: mem, Score: 1.0, Source: "fts"}}}

	s := stage.NewFTSStage(mock, 30)
	state := pipeline.NewState("test query", &model.Identity{TeamID: "t1", OwnerID: "o1"})

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Source != "fts" {
		t.Errorf("expected source 'fts', got %q", result.Candidates[0].Source)
	}
}

func TestFTSStage_NilStore(t *testing.T) {
	s := stage.NewFTSStage(nil, 30)
	state := pipeline.NewState("query", nil)

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("expected 0 candidates for nil store, got %d", len(result.Candidates))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/LocalMem && go test ./testing/search/stage/ -run TestFTSStage -v`
Expected: FAIL

- [ ] **Step 3: Implement FTS stage**

Extract FTS search logic from `retriever.go:133-181` into a new Stage. The stage needs a narrow interface — only the search methods, not the full `MemoryStore`.

```go
// internal/search/stage/fts.go
package stage

import (
	"context"
	"strings"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// FTSSearcher FTS 检索所需的最小接口 / Minimal interface for FTS search
type FTSSearcher interface {
	SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error)
	SearchTextFiltered(ctx context.Context, query string, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error)
}

// FTSStage FTS5 全文检索阶段 / FTS5 full-text search stage
type FTSStage struct {
	searcher FTSSearcher
	limit    int
}

// NewFTSStage 创建 FTS stage / Create FTS stage
func NewFTSStage(searcher FTSSearcher, limit int) *FTSStage {
	if limit <= 0 {
		limit = 30
	}
	return &FTSStage{searcher: searcher, limit: limit}
}

func (s *FTSStage) Name() string { return "fts" }

func (s *FTSStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	if s.searcher == nil {
		return state, nil
	}

	query := state.Query
	if state.Plan != nil && len(state.Plan.Keywords) > 0 {
		query = strings.Join(state.Plan.Keywords, " ")
	}
	if query == "" {
		return state, nil
	}

	var results []*model.SearchResult
	var err error

	filters := s.resolveFilters(state)
	if filters != nil {
		results, err = s.searcher.SearchTextFiltered(ctx, query, filters, s.limit)
	} else {
		results, err = s.searcher.SearchText(ctx, query, state.Identity, s.limit)
	}

	if err != nil {
		logger.Warn("fts stage: search failed", zap.Error(err))
		return state, nil // 不阻断管线 / Don't block pipeline
	}

	state.Candidates = append(state.Candidates, results...)
	return state, nil
}

// resolveFilters 从 state.Metadata 中提取过滤条件 / Extract filters from state metadata
func (s *FTSStage) resolveFilters(state *pipeline.PipelineState) *model.SearchFilters {
	if f, ok := state.Metadata["filters"].(*model.SearchFilters); ok {
		return f
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/LocalMem && go test ./testing/search/stage/ -run TestFTSStage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/search/stage/fts.go testing/search/stage/fts_test.go
git commit -m "feat(search): add FTS stage extracted from retriever"
```

---

### Task 4: Graph Stage

**Files:**
- Create: `internal/search/stage/graph.go`
- Test: `testing/search/stage/graph_test.go`

- [ ] **Step 1: Write test**

```go
// testing/search/stage/graph_test.go
package stage_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

type mockGraphStore struct {
	entities  []*model.Entity
	memories  []*model.Memory
	relations []*model.EntityRelation
}

func (m *mockGraphStore) FindEntitiesByName(ctx context.Context, name, scope string, limit int) ([]*model.Entity, error) {
	return m.entities, nil
}
func (m *mockGraphStore) GetEntityRelations(ctx context.Context, entityID string) ([]*model.EntityRelation, error) {
	return m.relations, nil
}
func (m *mockGraphStore) GetEntityMemories(ctx context.Context, entityID string, limit int) ([]*model.Memory, error) {
	return m.memories, nil
}
func (m *mockGraphStore) GetMemoryEntities(ctx context.Context, memoryID string) ([]*model.Entity, error) {
	return m.entities, nil
}

func TestGraphStage_Execute(t *testing.T) {
	ent := &model.Entity{ID: "e1", Name: "点券"}
	mem := &model.Memory{ID: "m1", Content: "点券 balance"}
	mock := &mockGraphStore{
		entities: []*model.Entity{ent},
		memories: []*model.Memory{mem},
	}

	s := stage.NewGraphStage(mock, nil, 2, 30, 5, 10)
	state := pipeline.NewState("点券", nil)
	state.Plan = &search.QueryPlan{Entities: []string{"e1"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) == 0 {
		t.Fatal("expected candidates from graph stage")
	}
	if result.Candidates[0].Source != "graph" {
		t.Errorf("expected source 'graph', got %q", result.Candidates[0].Source)
	}
}

func TestGraphStage_NilStore(t *testing.T) {
	s := stage.NewGraphStage(nil, nil, 2, 30, 5, 10)
	state := pipeline.NewState("query", nil)

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(result.Candidates))
	}
}
```

- [ ] **Step 2: Run test → FAIL**

Run: `cd /root/LocalMem && go test ./testing/search/stage/ -run TestGraphStage -v`

- [ ] **Step 3: Implement Graph stage**

Extract graph traversal logic from `retriever_graph.go` into a Stage. The stage encapsulates entity lookup + BFS traversal + memory collection + depth-decay scoring. Uses a narrow `GraphRetriever` interface instead of the full `GraphStore`.

```go
// internal/search/stage/graph.go
package stage

import (
	"context"
	"sort"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// maxVisitedEntities 图谱遍历最大实体数 / Max visited entities during traversal
const maxVisitedEntities = 50

// GraphRetriever 图检索所需的最小接口 / Minimal interface for graph retrieval
type GraphRetriever interface {
	FindEntitiesByName(ctx context.Context, name, scope string, limit int) ([]*model.Entity, error)
	GetEntityRelations(ctx context.Context, entityID string) ([]*model.EntityRelation, error)
	GetEntityMemories(ctx context.Context, entityID string, limit int) ([]*model.Memory, error)
	GetMemoryEntities(ctx context.Context, memoryID string) ([]*model.Entity, error)
}

// GraphStage 图关联检索阶段 / Graph association retrieval stage
type GraphStage struct {
	graphStore  GraphRetriever
	ftsSearcher FTSSearcher // FTS 反查实体用 / For reverse entity lookup
	maxDepth    int
	limit       int
	ftsTop      int
	entityLimit int
}

// NewGraphStage 创建 Graph stage / Create Graph stage
func NewGraphStage(graphStore GraphRetriever, ftsSearcher FTSSearcher, maxDepth, limit, ftsTop, entityLimit int) *GraphStage {
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if limit <= 0 {
		limit = 30
	}
	if ftsTop <= 0 {
		ftsTop = 5
	}
	if entityLimit <= 0 {
		entityLimit = 10
	}
	return &GraphStage{
		graphStore:  graphStore,
		ftsSearcher: ftsSearcher,
		maxDepth:    maxDepth,
		limit:       limit,
		ftsTop:      ftsTop,
		entityLimit: entityLimit,
	}
}

func (s *GraphStage) Name() string { return "graph" }

func (s *GraphStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	if s.graphStore == nil {
		return state, nil
	}

	// 优先使用预处理提取的实体 ID / Prefer pre-extracted entity IDs from plan
	var seedEntityIDs map[string]bool
	if state.Plan != nil && len(state.Plan.Entities) > 0 {
		seedEntityIDs = make(map[string]bool, len(state.Plan.Entities))
		for _, id := range state.Plan.Entities {
			seedEntityIDs[id] = true
		}
	}

	// 若无预提取实体，用 FTS 反查 / Fallback: reverse lookup via FTS
	if len(seedEntityIDs) == 0 && s.ftsSearcher != nil && state.Query != "" {
		seedEntityIDs = s.ftsReverseLookup(ctx, state)
	}

	if len(seedEntityIDs) == 0 {
		return state, nil
	}

	results := s.traverseAndCollect(ctx, seedEntityIDs)
	state.Candidates = append(state.Candidates, results...)
	return state, nil
}

func (s *GraphStage) ftsReverseLookup(ctx context.Context, state *pipeline.PipelineState) map[string]bool {
	ftsResults, err := s.ftsSearcher.SearchText(ctx, state.Query, state.Identity, s.ftsTop)
	if err != nil {
		logger.Warn("graph stage: FTS reverse lookup failed", zap.Error(err))
		return nil
	}

	entityIDs := make(map[string]bool)
	for _, result := range ftsResults {
		entities, err := s.graphStore.GetMemoryEntities(ctx, result.Memory.ID)
		if err != nil {
			continue
		}
		for _, ent := range entities {
			entityIDs[ent.ID] = true
		}
	}
	return entityIDs
}

func (s *GraphStage) traverseAndCollect(ctx context.Context, seedIDs map[string]bool) []*model.SearchResult {
	visited := make(map[string]int) // entityID → depth
	current := make([]string, 0, len(seedIDs))
	for id := range seedIDs {
		visited[id] = 0
		current = append(current, id)
	}

	for d := 1; d <= s.maxDepth; d++ {
		var next []string
		for _, entityID := range current {
			if len(visited) >= maxVisitedEntities {
				break
			}
			relations, err := s.graphStore.GetEntityRelations(ctx, entityID)
			if err != nil {
				continue
			}
			for _, rel := range relations {
				for _, targetID := range []string{rel.SourceID, rel.TargetID} {
					if targetID == entityID {
						continue
					}
					if _, seen := visited[targetID]; !seen {
						visited[targetID] = d
						next = append(next, targetID)
					}
				}
			}
		}
		current = next
		if len(current) == 0 || len(visited) >= maxVisitedEntities {
			break
		}
	}

	memoryMap := make(map[string]*model.Memory)
	memoryDepth := make(map[string]int)
	for entityID, d := range visited {
		memories, err := s.graphStore.GetEntityMemories(ctx, entityID, s.entityLimit)
		if err != nil {
			continue
		}
		for _, mem := range memories {
			if _, exists := memoryMap[mem.ID]; !exists {
				memoryMap[mem.ID] = mem
				memoryDepth[mem.ID] = d
			} else if d < memoryDepth[mem.ID] {
				memoryDepth[mem.ID] = d
			}
		}
	}

	type depthMem struct {
		mem   *model.Memory
		depth int
	}
	var sorted []depthMem
	for id, mem := range memoryMap {
		sorted = append(sorted, depthMem{mem: mem, depth: memoryDepth[id]})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].depth < sorted[j].depth
	})

	results := make([]*model.SearchResult, 0, len(sorted))
	for _, dm := range sorted {
		depthScore := 1.0 / float64(dm.depth+1)
		results = append(results, &model.SearchResult{
			Memory: dm.mem,
			Score:  depthScore,
			Source: "graph",
		})
	}
	if len(results) > s.limit {
		results = results[:s.limit]
	}
	return results
}
```

- [ ] **Step 4: Run test → PASS**

Run: `cd /root/LocalMem && go test ./testing/search/stage/ -run TestGraphStage -v`

- [ ] **Step 5: Commit**

```bash
git add internal/search/stage/graph.go testing/search/stage/graph_test.go
git commit -m "feat(search): add Graph stage extracted from retriever_graph"
```

---

### Task 5: Vector, Temporal, Merge (RRF), Score Filter, Weight, MMR, Core, Trim stages

Each of these stages follows the exact same pattern as Tasks 3-4: extract existing logic into a `Stage.Execute()` method, add narrow interface, write table-driven tests. Since this is mechanical extraction, they are grouped here.

**Files to create (one per stage):**

| File | Source | Key logic |
|------|--------|-----------|
| `stage/vector.go` | `retriever.go:186-234` | Embed query → search → filter `score >= minScore` |
| `stage/temporal.go` | `retriever_util.go:172-236` | Timeline query → distance-decay scoring |
| `stage/merge.go` | `rrf.go:72-113` | `MergeWeightedRRF` — reuse as-is, wrap in Stage |
| `stage/filter.go` | NEW | `score_ratio` filter: keep candidates `>= top1 × ratio` |
| `stage/rerank_overlap.go` | `reranker.go:50-133` | `OverlapReranker.Rerank` — wrap in Stage |
| `stage/rerank_remote.go` | `reranker_remote.go:83-131` | `RemoteReranker.Rerank` — wrap in Stage |
| `stage/weight.go` | `retriever_weights.go` + `pkg/scoring/strength.go` | `ApplyKindAndClassWeights` + `ApplyScopePriority` + `ApplyStrengthWeighting` |
| `stage/mmr.go` | `mmr.go` | `MMRRerank` — wrap in Stage |
| `stage/core.go` | `retriever_util.go:76-140` | `injectCoreMemories` — wrap in Stage |
| `stage/trim.go` | `retriever_util.go:26-46` | `TrimByTokenBudget` — wrap in Stage |

**For each stage file, the implementation pattern is identical:**

```go
type XxxStage struct {
    // narrow interface dependencies
}

func (s *XxxStage) Name() string { return "xxx" }

func (s *XxxStage) Execute(ctx context.Context, state *PipelineState) (*PipelineState, error) {
    // nil-check dependencies → skip if missing
    // call existing function on state.Candidates
    // return modified state
}
```

**Test files:** `testing/search/stage/{vector,temporal,merge,filter,rerank_overlap,rerank_remote,weight,mmr,core,trim}_test.go`

- [ ] **Step 1: Create all stage files** (one file per stage, extracting from existing code)
- [ ] **Step 2: Create all test files** (table-driven tests per stage)
- [ ] **Step 3: Run all tests**

Run: `cd /root/LocalMem && go test ./testing/search/stage/ -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/search/stage/ testing/search/stage/
git commit -m "feat(search): add remaining stages extracted from retriever"
```

---

## Phase 3: New Capabilities

### Task 6: GraphAware Merge Strategy

**Files:**
- Modify: `internal/search/stage/merge.go` (add `graph_aware` strategy)
- Test: `testing/search/stage/merge_test.go` (add cases)

- [ ] **Step 1: Write test for GraphAware merge**

```go
// Add to testing/search/stage/merge_test.go
func TestMergeStage_GraphAware(t *testing.T) {
	// graph+fts 双命中 → 加成
	mem1 := &model.Memory{ID: "m1", Content: "a"}
	// fts 单命中 → 降权
	mem2 := &model.Memory{ID: "m2", Content: "b"}
	// graph 单命中 → 正常
	mem3 := &model.Memory{ID: "m3", Content: "c"}

	state := pipeline.NewState("q", nil)
	// 模拟多通道结果已合并到 Candidates，用 Source 标记来源
	state.Candidates = []*model.SearchResult{
		{Memory: mem1, Score: 0.9, Source: "graph"},
		{Memory: mem1, Score: 0.8, Source: "fts"},  // m1 双命中
		{Memory: mem2, Score: 0.7, Source: "fts"},   // m2 仅 fts
		{Memory: mem3, Score: 0.6, Source: "graph"}, // m3 仅 graph
	}

	s := stage.NewMergeStage("graph_aware", 60, 100)
	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// m1 should be top (cross-validated boost)
	if result.Candidates[0].Memory.ID != "m1" {
		t.Errorf("expected m1 as top result, got %s", result.Candidates[0].Memory.ID)
	}
	// m3 (graph-only) should rank above m2 (fts-only)
	m3Rank, m2Rank := -1, -1
	for i, c := range result.Candidates {
		if c.Memory.ID == "m3" {
			m3Rank = i
		}
		if c.Memory.ID == "m2" {
			m2Rank = i
		}
	}
	if m3Rank >= m2Rank {
		t.Errorf("expected graph-only (m3 rank=%d) above fts-only (m2 rank=%d)", m3Rank, m2Rank)
	}
}
```

- [ ] **Step 2: Run test → FAIL**
- [ ] **Step 3: Implement GraphAware merge**

In `stage/merge.go`, add a `mergeGraphAware()` function that groups candidates by Memory ID, detects source combinations, and applies source-based weight multipliers:

- `graph + fts` → `× 1.5`
- `graph only` → `× 1.0`
- `vector only` → `× 1.0`
- `fts only` → `× 0.8`

Then apply RRF formula with adjusted weights.

- [ ] **Step 4: Run test → PASS**
- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): add GraphAware merge strategy"
```

---

### Task 7: Graph Distance Reranker

**Files:**
- Create: `internal/search/stage/rerank_graph.go`
- Test: `testing/search/stage/rerank_graph_test.go`

- [ ] **Step 1: Write test**

```go
func TestRerankGraphStage_Execute(t *testing.T) {
	tests := []struct {
		name           string
		queryEntities  []string
		memEntities    map[string][]string // memID → entity IDs
		relations      map[string][]string // entityID → neighbor IDs
		expectedTop    string
		expectedFilter int // how many should be filtered out
	}{
		{
			name:          "direct entity match ranks highest",
			queryEntities: []string{"e_券"},
			memEntities:   map[string][]string{"m1": {"e_券"}, "m2": {"e_db"}},
			expectedTop:   "m1",
		},
		{
			name:          "1-hop neighbor ranks above unconnected",
			queryEntities: []string{"e_券"},
			memEntities:   map[string][]string{"m1": {"e_table"}, "m2": {"e_redis"}},
			relations:     map[string][]string{"e_券": {"e_table"}},
			expectedTop:   "m1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ... setup mock graph store + execute stage + verify ordering
		})
	}
}
```

- [ ] **Step 2: Run test → FAIL**
- [ ] **Step 3: Implement graph distance reranker**

For each candidate: look up its entities via `GetMemoryEntities`, compute min graph distance to any query entity (0-hop=1.0, 1-hop=0.7, 2-hop=0.4, none=0.0). Mix with base score: `final = (1-w)*base_norm + w*graph_score`, filter `graph_score < min_graph_score`.

- [ ] **Step 4: Run test → PASS**
- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): add graph distance reranker stage"
```

---

### Task 8: LLM Reranker

**Files:**
- Create: `internal/search/stage/rerank_llm.go`
- Test: `testing/search/stage/rerank_llm_test.go`

- [ ] **Step 1: Write test**

Test with a mock `llm.Provider` that returns fixed relevance scores. Verify: (a) results are reranked by LLM score, (b) results below `min_relevance` are filtered, (c) confidence is set correctly, (d) LLM failure falls back to no-op.

- [ ] **Step 2: Run test → FAIL**
- [ ] **Step 3: Implement LLM reranker**

Construct prompt with query + top-K candidate contents. Parse JSON response `[{"index":N,"score":F},...]` with 3-level fallback (JSON → regex → skip). Apply score mixing + filtering + confidence marking. Use `circuitBreaker` for resilience.

- [ ] **Step 4: Run test → PASS**
- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): add LLM reranker stage with confidence marking"
```

---

## Phase 4: Strategy Agent

### Task 9: Rule-Based Classifier

**Files:**
- Create: `internal/search/strategy/rules.go`
- Test: `testing/search/strategy/rules_test.go`

- [ ] **Step 1: Write table-driven test**

```go
func TestRuleClassifier_Select(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		intent   search.QueryIntent
		expected string
	}{
		{"short query", "项目名", "", "fast"},
		{"temporal query", "最近的进展", "", "exploration"},
		{"relational query", "这个模块依赖什么", "", "association"},
		{"entity match", "点券", search.IntentKeyword, "precision"},
		{"semantic query", "类似的经验", search.IntentSemantic, "semantic"},
		{"general query", "如何优化性能", search.IntentGeneral, "exploration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := strategy.NewRuleClassifier()
			result := c.Select(tt.query, tt.intent)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
```

- [ ] **Step 2: Run → FAIL**
- [ ] **Step 3: Implement rule classifier** with pattern matching (temporal/relational/exploratory patterns from existing `preprocess.go`) + length heuristics + intent mapping
- [ ] **Step 4: Run → PASS**
- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): add rule-based pipeline classifier"
```

---

### Task 10: LLM Strategy Agent

**Files:**
- Create: `internal/search/strategy/agent.go`
- Test: `testing/search/strategy/agent_test.go`

- [ ] **Step 1: Write test** with mock LLM that returns `{"pipeline":"precision","keywords":["点券"],"entities":["点券"]}`
- [ ] **Step 2: Run → FAIL**
- [ ] **Step 3: Implement** — single LLM call combining pipeline selection + preprocessing (keywords, entities, semantic_query, intent). JSON response parsing with fallback to rule classifier on error.
- [ ] **Step 4: Run → PASS**
- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): add LLM strategy agent with combined preprocessing"
```

---

## Phase 5: Built-in Pipelines + Wiring

### Task 11: Built-in Pipeline Definitions

**Files:**
- Create: `internal/search/pipeline/builtin.go`
- Test: `testing/search/pipeline/builtin_test.go`

- [ ] **Step 1: Write test** verifying all 6 pipelines are registered and have correct fallback chains
- [ ] **Step 2: Run → FAIL**
- [ ] **Step 3: Implement `RegisterBuiltins()`** that constructs and registers all 6 pipelines (precision, exploration, semantic, association, fast, full) + post-processing tail (weight → mmr → core → trim). Takes dependencies (stores, LLM, config) as params, wires stages with nil-check skipping.
- [ ] **Step 4: Run → PASS**
- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): register 6 built-in pipelines with fallback chains"
```

---

### Task 12: Refactor retriever.go to Use Pipeline

**Files:**
- Modify: `internal/search/retriever.go`
- Test: Run existing tests to verify no regression

- [ ] **Step 1: Run existing tests as baseline**

Run: `cd /root/LocalMem && go test ./testing/search/ -v -count=1`
Record pass/fail count.

- [ ] **Step 2: Add pipeline fields to Retriever**

Add `executor *pipeline.Executor`, `strategy *strategy.Agent`, `ruleClassifier *strategy.RuleClassifier` fields. Add `InitPipeline()` method that calls `RegisterBuiltins()` + creates executor.

- [ ] **Step 3: Refactor `Retrieve()` method**

Replace the monolithic 4-channel logic with:

```go
func (r *Retriever) Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
    // 1. Build initial state
    state := pipeline.NewState(req.Query, r.resolveIdentity(req))
    state.Metadata["filters"] = req.Filters
    state.Metadata["request"] = req

    // 2. Select pipeline
    pipelineName := req.Pipeline // per-request override
    if pipelineName == "" {
        pipelineName = r.selectPipeline(ctx, req)
    }

    // 3. Execute pipeline
    result, err := r.executor.Execute(ctx, pipelineName, state)
    if err != nil {
        return nil, err
    }

    // 4. Async access tracking
    if r.tracker != nil {
        for _, res := range result.Candidates {
            if res.Memory != nil {
                r.tracker.Track(res.Memory.ID)
            }
        }
    }

    return result.Candidates, nil
}
```

- [ ] **Step 4: Run existing tests — verify no regression**

Run: `cd /root/LocalMem && go test ./testing/search/ -v -count=1`
Expected: Same pass count as baseline. Any failures indicate regression — fix before proceeding.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(search): delegate Retrieve() to pipeline executor"
```

---

### Task 13: Config Changes

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add new config structs**

```go
// StrategyConfig 策略 Agent 配置 / Strategy agent configuration
type StrategyConfig struct {
    UseLLM           bool   `mapstructure:"use_llm"`
    FallbackPipeline string `mapstructure:"fallback_pipeline"`
}

// PipelineOverrides 管线参数覆盖 / Pipeline parameter overrides
type PipelineOverrides struct {
    GraphDepth         int     `mapstructure:"graph_depth"`
    GraphLimit         int     `mapstructure:"graph_limit"`
    FTSLimit           int     `mapstructure:"fts_limit"`
    VectorMinScore     float64 `mapstructure:"vector_min_score"`
    VectorLimit        int     `mapstructure:"vector_limit"`
    ScoreRatio         float64 `mapstructure:"score_ratio"`
    RerankTopK         int     `mapstructure:"rerank_top_k"`
    RerankMinRelevance float64 `mapstructure:"rerank_min_relevance"`
    GraphRerankMinScore float64 `mapstructure:"graph_rerank_min_score"`
    TemporalLimit      int     `mapstructure:"temporal_limit"`
    TrimMaxTokens      int     `mapstructure:"trim_max_tokens"`
}
```

Add to `RetrievalConfig`:
```go
Strategy  StrategyConfig                `mapstructure:"strategy"`
Pipelines map[string]PipelineOverrides  `mapstructure:"pipelines"`
```

- [ ] **Step 2: Add defaults in `LoadConfig()`**

```go
viper.SetDefault("retrieval.strategy.use_llm", true)
viper.SetDefault("retrieval.strategy.fallback_pipeline", "exploration")
```

- [ ] **Step 3: Run existing config tests**

Run: `cd /root/LocalMem && go test ./testing/... -run Config -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(config): add strategy and pipeline override configuration"
```

---

### Task 14: API + MCP Changes

**Files:**
- Modify: `internal/model/dto.go` (or wherever `RetrieveRequest` is defined)
- Modify: `internal/api/search_handler.go`
- Modify: `internal/mcp/tools/recall.go`

- [ ] **Step 1: Add `Pipeline` and `Debug` fields to `RetrieveRequest`**

```go
Pipeline string `json:"pipeline,omitempty"` // 可选管线 override / Optional pipeline override
Debug    bool   `json:"debug,omitempty"`    // 返回 trace / Return debug trace
```

- [ ] **Step 2: Update search handler to pass pipeline + return debug info**

In `SearchHandler.Retrieve()`, pass `req.Pipeline` through. If `req.Debug`, include trace in response.

- [ ] **Step 3: Update MCP recall tool** to accept optional `pipeline` parameter
- [ ] **Step 4: Run full test suite**

Run: `cd /root/LocalMem && go test ./testing/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(api): add pipeline and debug params to retrieve endpoints"
```

---

## Phase 6: Integration Verification

### Task 15: Pipeline Integration Tests

**Files:**
- Create: `testing/search/pipeline/integration_test.go`

- [ ] **Step 1: Write integration tests**

Test each built-in pipeline end-to-end with a real SQLite store (test DB):
- `precision`: query with known entity → returns entity-related memories
- `exploration`: temporal query → returns time-sorted results
- `fast`: simple query → returns quickly with low limit
- Fallback: `precision` with no graph data → falls back to `exploration`

- [ ] **Step 2: Run integration tests**

Run: `cd /root/LocalMem && go test ./testing/search/pipeline/ -run TestIntegration -v -count=1`

- [ ] **Step 3: Run full test suite to verify no regressions**

Run: `cd /root/LocalMem && go test ./testing/... -v -count=1`

- [ ] **Step 4: Commit**

```bash
git commit -m "test(search): add pipeline integration tests"
```

---

### Task 16: Cleanup Old Code

**Files:**
- Modify: `internal/search/retriever.go` — remove inlined channel logic (now in stages)
- Keep: `rrf.go`, `reranker.go`, `reranker_remote.go`, `retriever_weights.go`, `retriever_util.go`, `mmr.go` — keep originals until all callers are migrated, then delete in a follow-up

- [ ] **Step 1: Verify no direct callers of old functions remain** (except stages)

Run: `cd /root/LocalMem && grep -rn "MergeWeightedRRF\|MergeRRF\|ApplyKindAndClassWeights\|ApplyScopePriority\|TrimByTokenBudget\|MMRRerank" internal/search/ --include="*.go" | grep -v stage/ | grep -v _test.go`

- [ ] **Step 2: If safe, mark old files with deprecation comments** (actual deletion in follow-up PR)
- [ ] **Step 3: Run full tests**
- [ ] **Step 4: Final commit**

```bash
git commit -m "refactor(search): mark deprecated retriever functions for removal"
```

---

## Summary

| Phase | Tasks | Key Deliverable |
|-------|-------|----------------|
| 1 | 1-2 | Stage interface + Pipeline engine (parallel, fallback, trace) |
| 2 | 3-5 | All 14 stages extracted from existing code |
| 3 | 6-8 | GraphAware merge + Graph reranker + LLM reranker |
| 4 | 9-10 | Strategy Agent (rules + LLM) |
| 5 | 11-14 | 6 built-in pipelines + retriever refactor + config + API |
| 6 | 15-16 | Integration tests + cleanup |
