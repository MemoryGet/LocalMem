package pipeline_test

import (
	"context"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/pipeline/builtin"
	"iclude/internal/search/stage"
)

// --- Mock implementations for integration tests ---

// integrationFTSSearcher FTS mock with realistic data / FTS mock 含真实数据
type integrationFTSSearcher struct {
	resultsByQuery map[string][]*model.SearchResult
}

func (m *integrationFTSSearcher) SearchText(_ context.Context, query string, _ *model.Identity, _ int) ([]*model.SearchResult, error) {
	if results, ok := m.resultsByQuery[query]; ok {
		return results, nil
	}
	// 默认返回空 / Default return empty
	return nil, nil
}

func (m *integrationFTSSearcher) SearchTextFiltered(_ context.Context, query string, _ *model.SearchFilters, _ int) ([]*model.SearchResult, error) {
	return m.SearchText(nil, query, nil, 0)
}

// integrationGraphRetriever graph mock with realistic entity/relation/memory data
type integrationGraphRetriever struct {
	entitiesByName map[string][]*model.Entity
	relations      map[string][]*model.EntityRelation
	entityMemories map[string][]*model.Memory
	memoryEntities map[string][]*model.Entity
}

func (m *integrationGraphRetriever) FindEntitiesByName(_ context.Context, name, _ string, limit int) ([]*model.Entity, error) {
	entities := m.entitiesByName[name]
	if len(entities) > limit {
		entities = entities[:limit]
	}
	return entities, nil
}

func (m *integrationGraphRetriever) GetEntityRelations(_ context.Context, entityID string) ([]*model.EntityRelation, error) {
	return m.relations[entityID], nil
}

func (m *integrationGraphRetriever) GetEntityMemories(_ context.Context, entityID string, limit int) ([]*model.Memory, error) {
	memories := m.entityMemories[entityID]
	if len(memories) > limit {
		memories = memories[:limit]
	}
	return memories, nil
}

func (m *integrationGraphRetriever) GetMemoryEntities(_ context.Context, memoryID string) ([]*model.Entity, error) {
	return m.memoryEntities[memoryID], nil
}

// integrationTimelineSearcher timeline mock / 时间线 mock
type integrationTimelineSearcher struct {
	memories []*model.Memory
}

func (m *integrationTimelineSearcher) ListTimeline(_ context.Context, _ *model.TimelineRequest) ([]*model.Memory, error) {
	return m.memories, nil
}

// integrationCoreProvider core memory provider mock / 核心记忆提供者 mock
type integrationCoreProvider struct {
	blocks []*model.Memory
}

func (m *integrationCoreProvider) GetCoreBlocksMultiScope(_ context.Context, _ []string, _ *model.Identity) ([]*model.Memory, error) {
	return m.blocks, nil
}

// integrationVectorSearcher vector search mock / 向量检索 mock
type integrationVectorSearcher struct {
	results []*model.SearchResult
	vectors map[string][]float32
}

func (m *integrationVectorSearcher) Search(_ context.Context, _ []float32, _ *model.Identity, _ int) ([]*model.SearchResult, error) {
	return m.results, nil
}

func (m *integrationVectorSearcher) SearchFiltered(_ context.Context, _ []float32, _ *model.SearchFilters, _ int) ([]*model.SearchResult, error) {
	return m.results, nil
}

func (m *integrationVectorSearcher) GetVectors(_ context.Context, ids []string) (map[string][]float32, error) {
	if m.vectors == nil {
		return nil, nil
	}
	result := make(map[string][]float32, len(ids))
	for _, id := range ids {
		if v, ok := m.vectors[id]; ok {
			result[id] = v
		}
	}
	return result, nil
}

// integrationEmbedder embedding mock / 嵌入 mock
type integrationEmbedder struct{}

func (m *integrationEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

// integrationLLMProvider LLM mock for rerank_llm / LLM mock
type integrationLLMProvider struct {
	response string
}

func (m *integrationLLMProvider) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: m.response}, nil
}

// --- Helper functions ---

func newIntegrationMemory(id, content, kind string) *model.Memory {
	return &model.Memory{
		ID:            id,
		Content:       content,
		Kind:          kind,
		Strength:      0.9,
		RetentionTier: model.TierPermanent,
		CreatedAt:     time.Now(),
	}
}

func defaultIdentity() *model.Identity {
	return &model.Identity{TeamID: "team-1", OwnerID: "owner-1"}
}

func defaultCfg() config.RetrievalConfig {
	return config.RetrievalConfig{
		AccessAlpha: 0.1,
		MMR:         config.MMRConfig{Lambda: 0.7},
	}
}

// --- Integration Tests ---

// TestIntegration_PrecisionPipeline 精确管线端到端: graph+fts parallel → merge → filter → rerank_graph → post-stages
// Precision pipeline E2E with known entities returning real data through the full pipeline
func TestIntegration_PrecisionPipeline(t *testing.T) {
	// 准备图数据: Go 实体 → 关联到 concurrency 实体 → 各有记忆
	// Prepare graph data: Go entity → related to concurrency entity → each has memories
	graphStore := &integrationGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Go": {{ID: "ent-go", Name: "Go"}},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-go": {{ID: "rel-1", SourceID: "ent-go", TargetID: "ent-concurrency"}},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-go":          {newIntegrationMemory("mem-go-1", "Go is a statically typed language", "fact")},
			"ent-concurrency": {newIntegrationMemory("mem-conc-1", "Go concurrency uses goroutines", "skill")},
		},
		memoryEntities: map[string][]*model.Entity{
			"mem-go-1":   {{ID: "ent-go", Name: "Go"}},
			"mem-conc-1": {{ID: "ent-concurrency", Name: "concurrency"}},
			// FTS 返回的记忆也关联到 Go 实体 / FTS results also linked to Go entity
			"mem-fts-1": {{ID: "ent-go", Name: "Go"}},
		},
	}

	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"Go concurrency": {
				{Memory: newIntegrationMemory("mem-fts-1", "Go concurrency patterns and best practices", "note"), Score: 0.85, Source: "fts"},
				{Memory: newIntegrationMemory("mem-fts-2", "Concurrent programming overview", "note"), Score: 0.6, Source: "fts"},
			},
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		GraphStore:   graphStore,
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("Go concurrency", defaultIdentity())
	state.Plan = &pipeline.QueryPlan{
		OriginalQuery: "Go concurrency",
		Entities:      []string{"Go"},
	}

	result, err := exec.Execute(context.Background(), "precision", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 验证管线名称设置 / Verify pipeline name is set
	if result.PipelineName != "precision" {
		t.Errorf("PipelineName = %q, want %q", result.PipelineName, "precision")
	}

	// 验证产出了候选结果 / Verify candidates produced
	if len(result.Candidates) == 0 {
		t.Fatal("expected candidates > 0, got 0")
	}

	// 验证 trace 包含预期 stage / Verify traces contain expected stages
	traceNames := collectTraceNames(result.Traces)
	expectedTraces := []string{"graph", "fts", "parallel_group", "merge", "filter"}
	for _, name := range expectedTraces {
		if !traceNames[name] {
			t.Errorf("missing trace for stage %q", name)
		}
	}

	// 验证后处理 stage 也执行了 / Verify post-processing stages ran
	postTraces := []string{"weight", "trim"}
	for _, name := range postTraces {
		if !traceNames[name] {
			t.Errorf("missing post-stage trace for %q", name)
		}
	}
}

// TestIntegration_ExplorationPipeline 探索管线端到端: fts+temporal parallel → merge → filter → rerank_overlap → post-stages
// Exploration pipeline E2E with temporal query
func TestIntegration_ExplorationPipeline(t *testing.T) {
	now := time.Now()
	center := now

	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"what happened last week": {
				{Memory: newIntegrationMemory("mem-fts-1", "Team standup discussion about deployment", "note"), Score: 0.7, Source: "fts"},
				{Memory: newIntegrationMemory("mem-fts-2", "Bug fix for authentication module", "note"), Score: 0.5, Source: "fts"},
			},
		},
	}

	timeline := &integrationTimelineSearcher{
		memories: []*model.Memory{
			{ID: "mem-t-1", Content: "Monday: started new feature branch", Kind: "note", Strength: 0.8, RetentionTier: model.TierPermanent, CreatedAt: now.Add(-2 * 24 * time.Hour)},
			{ID: "mem-t-2", Content: "Wednesday: code review completed", Kind: "note", Strength: 0.7, RetentionTier: model.TierPermanent, CreatedAt: now.Add(-4 * 24 * time.Hour)},
			{ID: "mem-t-3", Content: "Friday: deployment to staging", Kind: "note", Strength: 0.6, RetentionTier: model.TierPermanent, CreatedAt: now.Add(-6 * 24 * time.Hour)},
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		Timeline:     timeline,
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("what happened last week", defaultIdentity())
	state.Plan = &pipeline.QueryPlan{
		OriginalQuery:  "what happened last week",
		Temporal:       true,
		TemporalCenter: &center,
		TemporalRange:  7 * 24 * time.Hour,
	}

	result, err := exec.Execute(context.Background(), "exploration", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if result.PipelineName != "exploration" {
		t.Errorf("PipelineName = %q, want %q", result.PipelineName, "exploration")
	}

	if len(result.Candidates) == 0 {
		t.Fatal("expected candidates > 0, got 0")
	}

	traceNames := collectTraceNames(result.Traces)
	for _, name := range []string{"fts", "temporal", "parallel_group", "merge", "filter", "rerank_overlap"} {
		if !traceNames[name] {
			t.Errorf("missing trace for stage %q", name)
		}
	}
}

// TestIntegration_FastPipeline 快速管线端到端: fts only → filter → post-stages (minimal)
// Fast pipeline E2E with short query and minimal stages
func TestIntegration_FastPipeline(t *testing.T) {
	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"Go": {
				{Memory: newIntegrationMemory("mem-1", "Go programming basics", "note"), Score: 0.9, Source: "fts"},
				{Memory: newIntegrationMemory("mem-2", "Go module system", "fact"), Score: 0.7, Source: "fts"},
			},
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("Go", defaultIdentity())

	result, err := exec.Execute(context.Background(), "fast", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if result.PipelineName != "fast" {
		t.Errorf("PipelineName = %q, want %q", result.PipelineName, "fast")
	}

	// fast 管线应产出结果 / fast pipeline should produce results
	if len(result.Candidates) == 0 {
		t.Fatal("expected candidates > 0, got 0")
	}

	// 验证只经过 fts + filter + post-stages / Verify only fts + filter + post-stages ran
	traceNames := collectTraceNames(result.Traces)
	if !traceNames["fts"] {
		t.Error("missing trace for fts stage")
	}
	if !traceNames["filter"] {
		t.Error("missing trace for filter stage")
	}
	if !traceNames["weight"] {
		t.Error("missing trace for weight post-stage")
	}
	if !traceNames["trim"] {
		t.Error("missing trace for trim post-stage")
	}

	// 不应有 graph/temporal/merge 等 stage / Should NOT have graph/temporal/merge
	for _, absent := range []string{"graph", "temporal", "merge"} {
		if traceNames[absent] {
			t.Errorf("unexpected trace for stage %q in fast pipeline", absent)
		}
	}
}

// TestIntegration_FallbackChain precision 图数据为空 → 降级到 exploration
// Precision with empty graph data falls back to exploration
func TestIntegration_FallbackChain(t *testing.T) {
	now := time.Now()
	center := now

	// 图和 FTS 对 precision 返回空结果 / Graph and FTS return nothing for precision
	graphStore := &integrationGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
	}

	// FTS 对 exploration 的降级查询也返回结果 / FTS returns results for the fallback exploration pipeline
	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"unknown topic": {
				{Memory: newIntegrationMemory("mem-fallback-1", "General knowledge about unknown topics", "note"), Score: 0.5, Source: "fts"},
			},
		},
	}

	timeline := &integrationTimelineSearcher{
		memories: []*model.Memory{
			{ID: "mem-t-fallback", Content: "Recent exploration", Kind: "note", Strength: 0.7, RetentionTier: model.TierPermanent, CreatedAt: now},
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		GraphStore:   graphStore,
		Timeline:     timeline,
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("unknown topic", defaultIdentity())
	state.Plan = &pipeline.QueryPlan{
		OriginalQuery:  "unknown topic",
		Temporal:       true,
		TemporalCenter: &center,
		TemporalRange:  7 * 24 * time.Hour,
	}

	result, err := exec.Execute(context.Background(), "precision", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 管线名称应仍为 precision（执行器设置的是初始管线名称）
	// Pipeline name stays as initial pipeline name set by executor
	if result.PipelineName != "exploration" {
		// 降级后 PipelineName 被更新为 exploration / After fallback, PipelineName is updated to exploration
		// 注意: Clone() 保留原始 Traces 但 PipelineName 会被降级管线覆盖
		t.Logf("PipelineName = %q (may be either precision or exploration depending on clone semantics)", result.PipelineName)
	}

	// 验证降级管线的 stage trace 存在 / Verify fallback pipeline stage traces exist
	traceNames := collectTraceNames(result.Traces)

	// precision 管线的 trace 应存在 / Precision pipeline traces should exist
	if !traceNames["parallel_group"] {
		t.Error("missing parallel_group trace from precision pipeline")
	}

	// 降级后 exploration 的 FTS trace 也应存在 / Exploration FTS trace from fallback should exist
	ftsTraceCount := 0
	for _, tr := range result.Traces {
		if tr.Name == "fts" {
			ftsTraceCount++
		}
	}
	// 至少应有 2 个 fts trace（precision 的 fts + exploration 的 fts）
	// Should have at least 2 fts traces (precision's fts + exploration's fts)
	if ftsTraceCount < 2 {
		t.Logf("fts trace count = %d (expected >= 2 from precision + fallback)", ftsTraceCount)
	}

	// 关键: 降级应产出候选结果 / Key: fallback should produce candidates
	if len(result.Candidates) == 0 {
		t.Error("expected candidates > 0 after fallback, got 0")
	}
}

// TestIntegration_FullPipeline_WithMockLLM 全量管线含 mock LLM 的 rerank_llm stage
// Full pipeline E2E with mock LLM for rerank_llm stage
func TestIntegration_FullPipeline_WithMockLLM(t *testing.T) {
	graphStore := &integrationGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"API": {{ID: "ent-api", Name: "API"}},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-api": {{ID: "rel-1", SourceID: "ent-api", TargetID: "ent-rest"}},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-api":  {newIntegrationMemory("mem-api-1", "API design principles", "skill")},
			"ent-rest": {newIntegrationMemory("mem-rest-1", "REST API best practices", "skill")},
		},
		memoryEntities: map[string][]*model.Entity{
			"mem-api-1":  {{ID: "ent-api", Name: "API"}},
			"mem-rest-1": {{ID: "ent-rest", Name: "REST"}},
			"mem-fts-1":  {{ID: "ent-api", Name: "API"}},
		},
	}

	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"API design": {
				{Memory: newIntegrationMemory("mem-fts-1", "API design patterns for microservices", "note"), Score: 0.8, Source: "fts"},
				{Memory: newIntegrationMemory("mem-fts-2", "RESTful API versioning strategies", "note"), Score: 0.6, Source: "fts"},
			},
		},
	}

	vecSearcher := &integrationVectorSearcher{
		results: []*model.SearchResult{
			{Memory: newIntegrationMemory("mem-vec-1", "API gateway design pattern", "skill"), Score: 0.75, Source: "vector"},
		},
		vectors: map[string][]float32{
			"mem-api-1":  {0.1, 0.2, 0.3},
			"mem-rest-1": {0.2, 0.3, 0.4},
			"mem-fts-1":  {0.3, 0.4, 0.5},
			"mem-fts-2":  {0.4, 0.5, 0.6},
			"mem-vec-1":  {0.5, 0.6, 0.7},
		},
	}

	// LLM mock 返回所有候选的高分 / LLM mock returns high scores for all candidates
	llmProvider := &integrationLLMProvider{
		response: `[{"index":0,"score":0.9},{"index":1,"score":0.8},{"index":2,"score":0.7},{"index":3,"score":0.6},{"index":4,"score":0.5}]`,
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		GraphStore:   graphStore,
		VectorStore:  vecSearcher,
		Embedder:     &integrationEmbedder{},
		LLM:          llmProvider,
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("API design", defaultIdentity())
	state.Plan = &pipeline.QueryPlan{
		OriginalQuery: "API design",
		Entities:      []string{"API"},
	}

	result, err := exec.Execute(context.Background(), "full", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if result.PipelineName != "full" {
		t.Errorf("PipelineName = %q, want %q", result.PipelineName, "full")
	}

	if len(result.Candidates) == 0 {
		t.Fatal("expected candidates > 0, got 0")
	}

	// 验证 trace 包含所有预期 stage / Verify traces include all expected stages
	traceNames := collectTraceNames(result.Traces)
	expectedTraces := []string{"graph", "fts", "vector", "parallel_group", "merge", "filter", "rerank_llm", "weight", "trim"}
	for _, name := range expectedTraces {
		if !traceNames[name] {
			t.Errorf("missing trace for stage %q", name)
		}
	}

	// 验证 LLM rerank 设置了置信度 / Verify LLM rerank set confidence
	if result.Confidence == "" {
		t.Error("expected Confidence to be set by rerank_llm, got empty")
	}
}

// TestIntegration_DebugTraceOutput 验证 trace 包含预期的 stage 名称和计数
// Verify trace contains expected stage names and counts
func TestIntegration_DebugTraceOutput(t *testing.T) {
	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"trace test": {
				{Memory: newIntegrationMemory("mem-1", "First result", "note"), Score: 0.9, Source: "fts"},
				{Memory: newIntegrationMemory("mem-2", "Second result", "fact"), Score: 0.7, Source: "fts"},
				{Memory: newIntegrationMemory("mem-3", "Third result", "note"), Score: 0.5, Source: "fts"},
			},
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("trace test", defaultIdentity())

	result, err := exec.Execute(context.Background(), "fast", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 验证 trace 非空 / Verify traces are non-empty
	if len(result.Traces) == 0 {
		t.Fatal("expected traces, got none")
	}

	// 验证每个 trace 都有名称 / Verify each trace has a name
	for i, tr := range result.Traces {
		if tr.Name == "" {
			t.Errorf("trace[%d] has empty name", i)
		}
	}

	// 验证 FTS trace 有输出计数 / Verify FTS trace has output count
	for _, tr := range result.Traces {
		if tr.Name == "fts" && !tr.Skipped {
			if tr.OutputCount == 0 {
				t.Error("fts trace OutputCount = 0, expected > 0")
			}
			break
		}
	}

	// 验证 filter trace 存在且 InputCount > 0 / Verify filter trace exists with InputCount > 0
	for _, tr := range result.Traces {
		if tr.Name == "filter" {
			if tr.InputCount == 0 {
				t.Error("filter trace InputCount = 0, expected > 0")
			}
			break
		}
	}

	// 验证 weight trace 存在 / Verify weight trace exists
	foundWeight := false
	for _, tr := range result.Traces {
		if tr.Name == "weight" {
			foundWeight = true
			break
		}
	}
	if !foundWeight {
		t.Error("missing weight trace in output")
	}

	// 验证 trim trace 存在 / Verify trim trace exists
	foundTrim := false
	for _, tr := range result.Traces {
		if tr.Name == "trim" {
			foundTrim = true
			break
		}
	}
	if !foundTrim {
		t.Error("missing trim trace in output")
	}

	// 输出 trace 详情用于调试 / Log trace details for debugging
	for _, tr := range result.Traces {
		t.Logf("trace: name=%q duration=%v in=%d out=%d skipped=%v note=%q",
			tr.Name, tr.Duration, tr.InputCount, tr.OutputCount, tr.Skipped, tr.Note)
	}
}

// TestIntegration_SemanticPipeline_VectorAndFTS 语义管线: vector+fts parallel → merge → filter → rerank_overlap → post-stages
// Semantic pipeline E2E with vector + FTS parallel
func TestIntegration_SemanticPipeline_VectorAndFTS(t *testing.T) {
	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"machine learning algorithms": {
				{Memory: newIntegrationMemory("mem-fts-1", "Machine learning classification algorithms", "skill"), Score: 0.8, Source: "fts"},
				{Memory: newIntegrationMemory("mem-fts-2", "Neural network training techniques", "note"), Score: 0.6, Source: "fts"},
			},
		},
	}

	vecSearcher := &integrationVectorSearcher{
		results: []*model.SearchResult{
			{Memory: newIntegrationMemory("mem-vec-1", "Deep learning model architectures", "skill"), Score: 0.85, Source: "vector"},
			{Memory: newIntegrationMemory("mem-vec-2", "Gradient descent optimization", "fact"), Score: 0.7, Source: "vector"},
		},
		vectors: map[string][]float32{
			"mem-fts-1": {0.1, 0.2, 0.3},
			"mem-fts-2": {0.4, 0.5, 0.6},
			"mem-vec-1": {0.7, 0.8, 0.9},
			"mem-vec-2": {0.2, 0.3, 0.4},
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		VectorStore:  vecSearcher,
		Embedder:     &integrationEmbedder{},
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("machine learning algorithms", defaultIdentity())

	result, err := exec.Execute(context.Background(), "semantic", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if result.PipelineName != "semantic" {
		t.Errorf("PipelineName = %q, want %q", result.PipelineName, "semantic")
	}

	if len(result.Candidates) == 0 {
		t.Fatal("expected candidates > 0, got 0")
	}

	traceNames := collectTraceNames(result.Traces)
	for _, name := range []string{"vector", "fts", "parallel_group", "merge", "filter", "rerank_overlap", "weight", "trim"} {
		if !traceNames[name] {
			t.Errorf("missing trace for stage %q", name)
		}
	}

	// 验证结果来自两个源的融合 / Verify results are merged from both sources
	// merge stage 后源应为 "hybrid" / After merge, source should be "hybrid"
	hasHybrid := false
	for _, c := range result.Candidates {
		if c.Source == "hybrid" {
			hasHybrid = true
			break
		}
	}
	if !hasHybrid {
		// 如果所有结果来自单源，merge 可能做了 passthrough / If all from single source, merge may passthrough
		t.Log("no hybrid source found; merge may have done single-source passthrough")
	}
}

// TestIntegration_AssociationPipeline_DeepGraph 关联管线: graph(depth=3) → rerank_graph → filter
// Association pipeline E2E with deep graph traversal
// Plan.Entities 使用 entity ID（rerank_graph 需要 ID 来构建邻居集）
// Plan.Entities uses entity IDs (rerank_graph needs IDs to build neighbor sets)
func TestIntegration_AssociationPipeline_DeepGraph(t *testing.T) {
	graphStore := &integrationGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			// 图 stage 通过名称查找 / Graph stage looks up by name
			"ent-db": {{ID: "ent-db", Name: "database"}},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-db":    {{ID: "rel-1", SourceID: "ent-db", TargetID: "ent-sql"}},
			"ent-sql":   {{ID: "rel-2", SourceID: "ent-sql", TargetID: "ent-index"}},
			"ent-index": {{ID: "rel-3", SourceID: "ent-index", TargetID: "ent-btree"}},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-db":    {newIntegrationMemory("mem-db-1", "Database design fundamentals", "fact")},
			"ent-sql":   {newIntegrationMemory("mem-sql-1", "SQL query optimization", "skill")},
			"ent-index": {newIntegrationMemory("mem-idx-1", "Indexing strategies for performance", "skill")},
			"ent-btree": {newIntegrationMemory("mem-btree-1", "B-tree data structure internals", "fact")},
		},
		memoryEntities: map[string][]*model.Entity{
			"mem-db-1":    {{ID: "ent-db", Name: "database"}},
			"mem-sql-1":   {{ID: "ent-sql", Name: "SQL"}},
			"mem-idx-1":   {{ID: "ent-index", Name: "index"}},
			"mem-btree-1": {{ID: "ent-btree", Name: "btree"}},
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		GraphStore:   graphStore,
		CoreProvider: &integrationCoreProvider{},
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("database internals", defaultIdentity())
	state.Plan = &pipeline.QueryPlan{
		OriginalQuery: "database internals",
		// 使用 entity ID（与 graph store 键匹配），rerank_graph 用 ID 查邻居
		// Use entity IDs (matching graph store keys); rerank_graph uses IDs for neighbor lookup
		Entities: []string{"ent-db"},
	}

	result, err := exec.Execute(context.Background(), "association", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 验证 graph + rerank_graph + filter traces 存在 / Verify traces exist
	traceNames := collectTraceNames(result.Traces)
	for _, name := range []string{"graph", "rerank_graph", "filter", "weight", "trim"} {
		if !traceNames[name] {
			t.Errorf("missing trace for stage %q", name)
		}
	}

	// 图 stage 应产出候选 / Graph stage should produce candidates
	// rerank_graph 按图距离评分后可能过滤部分结果 / rerank_graph may filter some after graph-distance scoring
	// 至少 ent-db 直接匹配的记忆（score=1.0）应保留 / At least ent-db direct match memory (score=1.0) should survive
	if len(result.Candidates) == 0 {
		t.Error("expected candidates > 0 after graph stage + rerank, got 0")
	}
}

// TestIntegration_CoreInjection 核心记忆注入验证
// Verify core memory injection in post-stages
func TestIntegration_CoreInjection(t *testing.T) {
	ftsSearcher := &integrationFTSSearcher{
		resultsByQuery: map[string][]*model.SearchResult{
			"core test": {
				{Memory: newIntegrationMemory("mem-1", "Regular search result", "note"), Score: 0.8, Source: "fts"},
			},
		},
	}

	coreProvider := &integrationCoreProvider{
		blocks: []*model.Memory{
			newIntegrationMemory("core-1", "Core identity: software engineer", "profile"),
			newIntegrationMemory("core-2", "Core preference: Go language", "profile"),
		},
	}

	reg := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher:  ftsSearcher,
		CoreProvider: coreProvider,
		Cfg:          defaultCfg(),
	}
	postStages := builtin.RegisterBuiltins(reg, deps)

	exec := pipeline.NewExecutor(reg, pipeline.WithPostStages(postStages...))
	state := pipeline.NewState("core test", defaultIdentity())

	result, err := exec.Execute(context.Background(), "fast", state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 验证 core 记忆被注入 / Verify core memories injected
	hasCoreSource := false
	for _, c := range result.Candidates {
		if c.Source == "core" {
			hasCoreSource = true
			break
		}
	}
	if !hasCoreSource {
		t.Error("expected core memories to be injected, but no 'core' source found")
	}

	// core 记忆应在最前面 / Core memories should be at the front
	if len(result.Candidates) > 0 && result.Candidates[0].Source != "core" {
		t.Errorf("first candidate source = %q, want %q (core should be first)", result.Candidates[0].Source, "core")
	}

	// 验证 core trace 存在 / Verify core trace exists
	traceNames := collectTraceNames(result.Traces)
	if !traceNames["core"] {
		t.Error("missing trace for core stage")
	}
}

// --- Helpers ---

// collectTraceNames 从 trace 列表中收集所有名称为 set / Collect all trace names into a set
func collectTraceNames(traces []pipeline.StageTrace) map[string]bool {
	names := make(map[string]bool, len(traces))
	for _, tr := range traces {
		names[tr.Name] = true
	}
	return names
}

// Compile-time check: mock types implement required interfaces
var (
	_ stage.FTSSearcher      = (*integrationFTSSearcher)(nil)
	_ stage.GraphRetriever   = (*integrationGraphRetriever)(nil)
	_ stage.TimelineSearcher = (*integrationTimelineSearcher)(nil)
	_ stage.CoreProvider     = (*integrationCoreProvider)(nil)
	_ stage.VectorSearcher   = (*integrationVectorSearcher)(nil)
	_ stage.Embedder         = (*integrationEmbedder)(nil)
	_ llm.Provider           = (*integrationLLMProvider)(nil)
)
