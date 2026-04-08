package stage_test

import (
	"context"
	"fmt"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// --- Mock implementations ---

// mockGraphRetriever 图检索 mock / Mock graph retriever
type mockGraphRetriever struct {
	entitiesByName map[string][]*model.Entity         // name → entities
	relations      map[string][]*model.EntityRelation  // entityID → relations
	entityMemories map[string][]*model.Memory          // entityID → memories
	memoryEntities map[string][]*model.Entity          // memoryID → entities
}

func (m *mockGraphRetriever) FindEntitiesByName(_ context.Context, name, _ string, limit int) ([]*model.Entity, error) {
	entities := m.entitiesByName[name]
	if len(entities) > limit {
		entities = entities[:limit]
	}
	return entities, nil
}

func (m *mockGraphRetriever) GetEntityRelations(_ context.Context, entityID string) ([]*model.EntityRelation, error) {
	return m.relations[entityID], nil
}

func (m *mockGraphRetriever) GetEntityMemories(_ context.Context, entityID string, limit int) ([]*model.Memory, error) {
	memories := m.entityMemories[entityID]
	if len(memories) > limit {
		memories = memories[:limit]
	}
	return memories, nil
}

func (m *mockGraphRetriever) GetMemoryEntities(_ context.Context, memoryID string) ([]*model.Entity, error) {
	return m.memoryEntities[memoryID], nil
}

// ftsSearcherSpy 已在 fts_test.go 中定义 / Defined in fts_test.go

// --- Helper functions ---

func newEntity(id, name string) *model.Entity {
	return &model.Entity{ID: id, Name: name}
}

func newMemory(id, content string) *model.Memory {
	return &model.Memory{ID: id, Content: content}
}

func newRelation(id, sourceID, targetID string) *model.EntityRelation {
	return &model.EntityRelation{ID: id, SourceID: sourceID, TargetID: targetID}
}

func newState(query string) *pipeline.PipelineState {
	return pipeline.NewState(query, &model.Identity{TeamID: "team1", OwnerID: "owner1"})
}

// --- Tests ---

func TestGraphStage_Name(t *testing.T) {
	s := stage.NewGraphStage(nil, nil)
	if s.Name() != "graph" {
		t.Errorf("Name() = %q, want %q", s.Name(), "graph")
	}
}

func TestGraphStage_NilStore(t *testing.T) {
	// graphStore nil → 0 candidates, no error
	s := stage.NewGraphStage(nil, nil)
	state := newState("test query")

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("Candidates = %d, want 0", len(result.Candidates))
	}
	// 应有 skipped trace / Should have skipped trace
	if len(result.Traces) == 0 {
		t.Fatal("expected at least one trace")
	}
	if !result.Traces[len(result.Traces)-1].Skipped {
		t.Error("expected trace to be marked as skipped")
	}
}

func TestGraphStage_WithPreExtractedEntities(t *testing.T) {
	// Plan has entity names → FindEntitiesByName → traverse → collect memories
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Go": {newEntity("ent-go", "Go")},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-go": {newRelation("rel-1", "ent-go", "ent-concurrency")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-go":          {newMemory("mem-1", "Go is a language")},
			"ent-concurrency": {newMemory("mem-2", "Concurrency patterns")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil)
	state := newState("Go concurrency")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"Go"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) < 1 {
		t.Fatalf("Candidates = %d, want >= 1", len(result.Candidates))
	}
	// 所有结果应标记为 graph source / All results should have graph source
	for _, c := range result.Candidates {
		if c.Source != "graph" {
			t.Errorf("Candidate source = %q, want %q", c.Source, "graph")
		}
	}
}

func TestGraphStage_WithFTSReverseLookup(t *testing.T) {
	// No plan entities, FTS finds memories → GetMemoryEntities → traverse → candidates
	ftsResults := []*model.SearchResult{
		{Memory: newMemory("fts-mem-1", "Found via FTS"), Score: 0.9},
	}
	fts := &ftsSearcherSpy{results: ftsResults}

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		relations: map[string][]*model.EntityRelation{
			"ent-fts": {newRelation("rel-fts", "ent-fts", "ent-related")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-fts":     {newMemory("mem-fts-1", "FTS entity memory")},
			"ent-related": {newMemory("mem-related", "Related memory")},
		},
		memoryEntities: map[string][]*model.Entity{
			"fts-mem-1": {newEntity("ent-fts", "FTS entity")},
		},
	}

	s := stage.NewGraphStage(graph, fts)
	state := newState("some query")

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) < 1 {
		t.Fatalf("Candidates = %d, want >= 1", len(result.Candidates))
	}
	for _, c := range result.Candidates {
		if c.Source != "graph" {
			t.Errorf("Candidate source = %q, want %q", c.Source, "graph")
		}
	}
}

func TestGraphStage_NoEntitiesFound(t *testing.T) {
	// Neither plan nor FTS finds entities → 0 candidates
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
	}

	// FTS returns results but no entities linked to them
	fts := &ftsSearcherSpy{
		results: []*model.SearchResult{
			{Memory: newMemory("orphan", "No entities"), Score: 0.5},
		},
	}

	s := stage.NewGraphStage(graph, fts)
	state := newState("orphan query")

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("Candidates = %d, want 0", len(result.Candidates))
	}
}

func TestGraphStage_DepthDecayScoring(t *testing.T) {
	// Verify depth 0 → score 1.0, depth 1 → score 0.5
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"root": {newEntity("ent-root", "root")},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-root": {newRelation("rel-1", "ent-root", "ent-child")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-root":  {newMemory("mem-depth0", "Root memory")},
			"ent-child": {newMemory("mem-depth1", "Child memory")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithMaxDepth(2))
	state := newState("root query")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"root"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	// 收集 score 按 memory ID / Collect scores by memory ID
	scores := make(map[string]float64)
	for _, c := range result.Candidates {
		scores[c.Memory.ID] = c.Score
	}

	// depth 0 → 1/(0+1) = 1.0
	if s0, ok := scores["mem-depth0"]; !ok {
		t.Error("missing mem-depth0")
	} else if s0 != 1.0 {
		t.Errorf("mem-depth0 score = %f, want 1.0", s0)
	}

	// depth 1 → 1/(1+1) = 0.5
	if s1, ok := scores["mem-depth1"]; !ok {
		t.Error("missing mem-depth1")
	} else if s1 != 0.5 {
		t.Errorf("mem-depth1 score = %f, want 0.5", s1)
	}
}

func TestGraphStage_FanOutCap(t *testing.T) {
	// Many relations → capped at maxVisitedEntities (50)
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"hub": {newEntity("ent-hub", "hub")},
		},
		relations:      map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
	}

	// ent-hub 有 60 个直接关系目标 / ent-hub has 60 direct relation targets
	hubRelations := make([]*model.EntityRelation, 60)
	for i := 0; i < 60; i++ {
		targetID := fmt.Sprintf("ent-fan-%d", i)
		hubRelations[i] = newRelation(fmt.Sprintf("rel-%d", i), "ent-hub", targetID)
		graph.entityMemories[targetID] = []*model.Memory{
			newMemory(fmt.Sprintf("mem-fan-%d", i), fmt.Sprintf("Fan-out memory %d", i)),
		}
	}
	graph.relations["ent-hub"] = hubRelations
	graph.entityMemories["ent-hub"] = []*model.Memory{
		newMemory("mem-hub", "Hub memory"),
	}

	s := stage.NewGraphStage(graph, nil, stage.WithMaxDepth(1))
	state := newState("fan-out query")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"hub"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	// 总共不能超过 maxVisitedEntities(50) 个实体产出的记忆
	// Total should not exceed memories from maxVisitedEntities(50) entities
	// hub(1) + up to 49 fan-out = 50 entities max
	if len(result.Candidates) > 50 {
		t.Errorf("Candidates = %d, want <= 50 (fan-out cap)", len(result.Candidates))
	}
	// 但应该至少有 hub 自身的记忆 / At least hub's own memory
	if len(result.Candidates) < 1 {
		t.Error("Candidates = 0, expected at least hub memory")
	}
}

func TestGraphStage_LimitResults(t *testing.T) {
	// 结果超过 limit 时截断 / Truncate when results exceed limit
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"seed": {newEntity("ent-seed", "seed")},
		},
		relations: map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{
			"ent-seed": make([]*model.Memory, 20),
		},
		memoryEntities: map[string][]*model.Entity{},
	}
	for i := 0; i < 20; i++ {
		graph.entityMemories["ent-seed"][i] = newMemory(fmt.Sprintf("mem-%d", i), fmt.Sprintf("Memory %d", i))
	}

	s := stage.NewGraphStage(graph, nil, stage.WithLimit(5))
	state := newState("limit test")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"seed"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) > 5 {
		t.Errorf("Candidates = %d, want <= 5", len(result.Candidates))
	}
}

func TestGraphStage_DeduplicatesMemories(t *testing.T) {
	// 同一 memory 被多个实体引用时应去重 / Same memory referenced by multiple entities should be deduplicated
	sharedMem := newMemory("mem-shared", "Shared memory")
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"alpha": {newEntity("ent-a", "alpha")},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-a": {newRelation("rel-1", "ent-a", "ent-b")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-a": {sharedMem},
			"ent-b": {sharedMem},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithMaxDepth(1))
	state := newState("dedup test")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"alpha"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	// 同一 memory 只应出现一次 / Same memory should appear only once
	seen := make(map[string]bool)
	for _, c := range result.Candidates {
		if seen[c.Memory.ID] {
			t.Errorf("duplicate memory ID %q in candidates", c.Memory.ID)
		}
		seen[c.Memory.ID] = true
	}
}

func TestGraphStage_AppendsToCandidates(t *testing.T) {
	// 已有 candidates 时 graph 结果应追加，而非覆盖 / Graph results append, not overwrite
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"entity": {newEntity("ent-1", "entity")},
		},
		relations: map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{
			"ent-1": {newMemory("graph-mem", "From graph")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil)
	state := newState("append test")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"entity"}}
	// 预先添加一个 candidate / Pre-add one candidate
	state.Candidates = []*model.SearchResult{
		{Memory: newMemory("existing", "Existing"), Score: 0.8, Source: "fts"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) < 2 {
		t.Errorf("Candidates = %d, want >= 2 (1 existing + graph results)", len(result.Candidates))
	}
	// 第一个应是原有的 / First should be the existing one
	if result.Candidates[0].Source != "fts" {
		t.Errorf("First candidate source = %q, want %q", result.Candidates[0].Source, "fts")
	}
}
