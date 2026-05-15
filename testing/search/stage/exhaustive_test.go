package stage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

var errTimeline = errors.New("timeline error")

// timePtr returns a pointer to the given time value
func timePtr(t time.Time) *time.Time {
	return &t
}

func TestExhaustiveStage_NilGraphStore(t *testing.T) {
	// nil graphStore → skip, no error, Name()=="exhaustive"
	s := stage.NewExhaustiveStage(nil, nil, 0)
	if s.Name() != "exhaustive" {
		t.Errorf("Name() = %q, want %q", s.Name(), "exhaustive")
	}

	state := newState("total spend")
	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("Candidates = %d, want 0", len(result.Candidates))
	}
	// should have a skipped trace
	if len(result.Traces) == 0 {
		t.Fatal("expected at least one trace")
	}
	if !result.Traces[len(result.Traces)-1].Skipped {
		t.Error("expected trace to be marked as skipped")
	}
}

func TestExhaustiveStage_NoEntitiesInPlan_NilTimeline(t *testing.T) {
	// no plan.Entities + nil timeline → empty candidates (no fallback)
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
	}
	s := stage.NewExhaustiveStage(graph, nil, 0)
	state := newState("total spend")
	state.Plan = &pipeline.QueryPlan{Entities: []string{}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("Candidates = %d, want 0 (nil timeline, no fallback)", len(result.Candidates))
	}
}

func TestExhaustiveStage_NoEntitiesFallbackToTimeline(t *testing.T) {
	// no plan.Entities + timeline available → falls back to full timeline scan
	now := time.Now()
	mem1 := &model.Memory{ID: "mem-1", Content: "Did task A", CreatedAt: now.Add(-2 * time.Hour)}
	mem2 := &model.Memory{ID: "mem-2", Content: "Did task B", CreatedAt: now.Add(-1 * time.Hour)}

	timeline := &timelineSearcherMock{memories: []*model.Memory{mem1, mem2}}
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
	}

	s := stage.NewExhaustiveStage(graph, timeline, 0)
	state := newState("之前我都做了哪些事情")
	state.Plan = &pipeline.QueryPlan{Entities: []string{}}
	state.Identity = &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %d, want 2 (from timeline fallback)", len(result.Candidates))
	}
	// ContextType must still be aggregation
	if result.ContextType != model.RetrievalContextAggregation {
		t.Errorf("ContextType = %q, want %q", result.ContextType, model.RetrievalContextAggregation)
	}
	// source should be "exhaustive" for fallback results too
	for _, c := range result.Candidates {
		if c.Source != "exhaustive" {
			t.Errorf("Candidate source = %q, want %q", c.Source, "exhaustive")
		}
	}
}

func TestExhaustiveStage_NoEntitiesFallbackToTimeline_Error(t *testing.T) {
	// timeline returns error → graceful, return empty (no panic)
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
	}
	timeline := &timelineSearcherMock{err: errTimeline}
	s := stage.NewExhaustiveStage(graph, timeline, 0)
	state := newState("之前我都做了哪些事情")
	state.Plan = &pipeline.QueryPlan{Entities: []string{}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() should not propagate timeline error: %v", err)
	}
	// ContextType still set even on error
	if result.ContextType != model.RetrievalContextAggregation {
		t.Errorf("ContextType = %q, want %q", result.ContextType, model.RetrievalContextAggregation)
	}
}

func TestExhaustiveStage_ReturnsAllMemoriesForEntity(t *testing.T) {
	// 3 memories out of order → sorted oldest-first, Source=="exhaustive", state.ContextType=="aggregation"
	now := time.Now()
	oldest := now.Add(-72 * time.Hour)
	middle := now.Add(-24 * time.Hour)
	newest := now

	mem1 := &model.Memory{ID: "mem-newest", Content: "newest", CreatedAt: newest}
	mem2 := &model.Memory{ID: "mem-oldest", Content: "oldest", CreatedAt: oldest}
	mem3 := &model.Memory{ID: "mem-middle", Content: "middle", CreatedAt: middle}

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Alice": {newEntity("ent-alice", "Alice")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-alice": {mem1, mem2, mem3},
		},
		memoryEntities: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
	}

	s := stage.NewExhaustiveStage(graph, nil, 0)
	state := newState("Alice total spend")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Alice"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	if len(result.Candidates) != 3 {
		t.Fatalf("Candidates = %d, want 3", len(result.Candidates))
	}

	// sorted oldest-first
	ids := []string{result.Candidates[0].Memory.ID, result.Candidates[1].Memory.ID, result.Candidates[2].Memory.ID}
	wantOrder := []string{"mem-oldest", "mem-middle", "mem-newest"}
	for i, want := range wantOrder {
		if ids[i] != want {
			t.Errorf("Candidates[%d].ID = %q, want %q", i, ids[i], want)
		}
	}

	// all sources should be "exhaustive"
	for _, c := range result.Candidates {
		if c.Source != "exhaustive" {
			t.Errorf("Candidate source = %q, want %q", c.Source, "exhaustive")
		}
	}

	// ContextType should be set to aggregation
	if result.ContextType != model.RetrievalContextAggregation {
		t.Errorf("ContextType = %q, want %q", result.ContextType, model.RetrievalContextAggregation)
	}
}

func TestExhaustiveStage_HappenedAtPreferredForSort(t *testing.T) {
	// When HappenedAt is set, it should be preferred over CreatedAt for sort order
	now := time.Now()
	happenedLongAgo := now.Add(-7 * 24 * time.Hour)
	happenedRecently := now.Add(-1 * time.Hour)

	memA := &model.Memory{
		ID:         "mem-a",
		Content:    "happened long ago",
		CreatedAt:  now,
		HappenedAt: timePtr(happenedLongAgo),
	}
	memB := &model.Memory{
		ID:         "mem-b",
		Content:    "happened recently",
		CreatedAt:  now.Add(-10 * 24 * time.Hour),
		HappenedAt: timePtr(happenedRecently),
	}

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Bob": {newEntity("ent-bob", "Bob")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-bob": {memB, memA},
		},
		memoryEntities: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
	}

	s := stage.NewExhaustiveStage(graph, nil, 0)
	state := newState("Bob spending")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Bob"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("Candidates = %d, want 2", len(result.Candidates))
	}

	if result.Candidates[0].Memory.ID != "mem-a" {
		t.Errorf("Candidates[0].ID = %q, want %q", result.Candidates[0].Memory.ID, "mem-a")
	}
	if result.Candidates[1].Memory.ID != "mem-b" {
		t.Errorf("Candidates[1].ID = %q, want %q", result.Candidates[1].Memory.ID, "mem-b")
	}
}

func TestExhaustiveStage_DeduplicatesAcrossEntities(t *testing.T) {
	// shared memory across 2 entities → appears once
	sharedMem := newMemory("mem-shared", "Shared memory")

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Alice": {newEntity("ent-alice", "Alice")},
			"Bob":   {newEntity("ent-bob", "Bob")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-alice": {sharedMem},
			"ent-bob":   {sharedMem},
		},
		memoryEntities: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
	}

	s := stage.NewExhaustiveStage(graph, nil, 0)
	state := newState("Alice Bob total")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Alice", "Bob"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	seen := make(map[string]bool)
	for _, c := range result.Candidates {
		if seen[c.Memory.ID] {
			t.Errorf("duplicate memory ID %q in candidates", c.Memory.ID)
		}
		seen[c.Memory.ID] = true
	}
	if len(result.Candidates) != 1 {
		t.Errorf("Candidates = %d, want 1 (deduped)", len(result.Candidates))
	}
}

func TestExhaustiveStage_RespectsMaxResults(t *testing.T) {
	// 10 memories, max=5 → ≤5 results
	memories := make([]*model.Memory, 10)
	now := time.Now()
	for i := 0; i < 10; i++ {
		memories[i] = &model.Memory{
			ID:        "mem-" + string(rune('0'+i)),
			Content:   "Memory " + string(rune('0'+i)),
			CreatedAt: now.Add(-time.Duration(i) * time.Hour),
		}
	}

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"entity": {newEntity("ent-1", "entity")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-1": memories,
		},
		memoryEntities: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
	}

	s := stage.NewExhaustiveStage(graph, nil, 5)
	state := newState("entity total")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"entity"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	if len(result.Candidates) > 5 {
		t.Errorf("Candidates = %d, want <= 5", len(result.Candidates))
	}
	if len(result.Candidates) == 0 {
		t.Error("Candidates = 0, expected some results")
	}
}
