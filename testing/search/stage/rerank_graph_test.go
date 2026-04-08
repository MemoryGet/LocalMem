package stage_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// mockGraphRetrieverForRerank 图距离精排用 mock / Mock graph retriever for reranking
type mockGraphRetrieverForRerank struct {
	memEntities map[string][]*model.Entity         // memID → entities associated with the memory
	relations   map[string][]*model.EntityRelation // entityID → relations (1-hop neighbors)
}

func (m *mockGraphRetrieverForRerank) FindEntitiesByName(_ context.Context, _ string, _ string, _ int) ([]*model.Entity, error) {
	return nil, nil
}

func (m *mockGraphRetrieverForRerank) GetEntityRelations(_ context.Context, entityID string) ([]*model.EntityRelation, error) {
	return m.relations[entityID], nil
}

func (m *mockGraphRetrieverForRerank) GetEntityMemories(_ context.Context, _ string, _ int) ([]*model.Memory, error) {
	return nil, nil
}

func (m *mockGraphRetrieverForRerank) GetMemoryEntities(_ context.Context, memoryID string) ([]*model.Entity, error) {
	return m.memEntities[memoryID], nil
}

func TestRerankGraphStage_Name(t *testing.T) {
	s := stage.NewRerankGraphStage(nil, 0, 0)
	if s.Name() != "rerank_graph" {
		t.Errorf("Name() = %q, want %q", s.Name(), "rerank_graph")
	}
}

func TestRerankGraphStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewRerankGraphStage(nil, 0, 0)
}

func TestRerankGraphStage_Execute(t *testing.T) {
	tests := []struct {
		name          string
		graphStore    stage.GraphRetriever
		queryEntities []string
		candidates    []*model.SearchResult
		memEntities   map[string][]*model.Entity
		relations     map[string][]*model.EntityRelation
		wantTop       string
		wantCount     int
		wantSkipped   bool
	}{
		{
			name:          "direct entity match ranks highest",
			queryEntities: []string{"e_券"},
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.9},
			},
			memEntities: map[string][]*model.Entity{
				"m1": {{ID: "e_券", Name: "券"}},
				"m2": {{ID: "e_db", Name: "db"}},
			},
			relations:   map[string][]*model.EntityRelation{},
			wantTop:     "m1",
			wantCount:   1, // m2 filtered (graphScore=0.0 < 0.2)
			wantSkipped: false,
		},
		{
			name:          "1-hop neighbor ranks above unconnected",
			queryEntities: []string{"e_券"},
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.8},
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.8},
			},
			memEntities: map[string][]*model.Entity{
				"m1": {{ID: "e_table", Name: "table"}},
				"m2": {{ID: "e_redis", Name: "redis"}},
			},
			relations: map[string][]*model.EntityRelation{
				"e_券": {{ID: "rel-1", SourceID: "e_券", TargetID: "e_table"}},
			},
			wantTop:     "m1",
			wantCount:   1, // m2 filtered
			wantSkipped: false,
		},
		{
			name:        "nil graph store skips",
			graphStore:  nil,
			wantSkipped: true,
			wantCount:   2,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1"}, Score: 0.5},
				{Memory: &model.Memory{ID: "m2"}, Score: 0.3},
			},
		},
		{
			name:          "no query entities skips",
			queryEntities: nil,
			wantSkipped:   true,
			wantCount:     1,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1"}, Score: 0.5},
			},
			memEntities: map[string][]*model.Entity{},
			relations:   map[string][]*model.EntityRelation{},
		},
		{
			name:          "empty candidates returns empty",
			queryEntities: []string{"e1"},
			candidates:    nil,
			wantCount:     0,
			memEntities:   map[string][]*model.Entity{},
			relations:     map[string][]*model.EntityRelation{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gs stage.GraphRetriever
			if tt.name == "nil graph store skips" {
				gs = nil
			} else if tt.graphStore != nil {
				gs = tt.graphStore
			} else {
				gs = &mockGraphRetrieverForRerank{
					memEntities: tt.memEntities,
					relations:   tt.relations,
				}
			}

			s := stage.NewRerankGraphStage(gs, 0, 0)
			state := pipeline.NewState("test query", &model.Identity{TeamID: "t", OwnerID: "o"})
			if tt.queryEntities != nil || tt.name == "empty candidates returns empty" {
				state.Plan = &pipeline.QueryPlan{
					Entities: tt.queryEntities,
				}
			}
			state.Candidates = tt.candidates

			got, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("Execute() returned error: %v", err)
			}

			if len(got.Candidates) != tt.wantCount {
				t.Errorf("Candidates count = %d, want %d", len(got.Candidates), tt.wantCount)
			}

			if tt.wantSkipped {
				found := false
				for _, tr := range got.Traces {
					if tr.Name == "rerank_graph" && tr.Skipped {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected skipped trace for rerank_graph stage")
				}
			}

			if tt.wantTop != "" && len(got.Candidates) > 0 {
				if got.Candidates[0].Memory.ID != tt.wantTop {
					t.Errorf("top candidate ID = %q, want %q", got.Candidates[0].Memory.ID, tt.wantTop)
				}
			}
		})
	}
}

func TestRerankGraphStage_2HopNeighbor(t *testing.T) {
	// 2-hop 邻居得分 0.4 / 2-hop neighbor scores 0.4
	mock := &mockGraphRetrieverForRerank{
		memEntities: map[string][]*model.Entity{
			"m1": {{ID: "e_target", Name: "target"}},   // 2-hop from query entity
			"m2": {{ID: "e_unrelated", Name: "unrel"}}, // no connection
		},
		relations: map[string][]*model.EntityRelation{
			"e_query": {{ID: "r1", SourceID: "e_query", TargetID: "e_mid"}},
			"e_mid":   {{ID: "r2", SourceID: "e_mid", TargetID: "e_target"}},
		},
	}

	s := stage.NewRerankGraphStage(mock, 0, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Entities: []string{"e_query"}}
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
		{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.5},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// m1 有 2-hop 连接 (graphScore=0.4 >= 0.2)，m2 无连接 (graphScore=0.0 < 0.2 被过滤)
	// m1 has 2-hop connection (graphScore=0.4 >= 0.2), m2 has no connection (filtered)
	if len(got.Candidates) != 1 {
		t.Fatalf("Candidates count = %d, want 1", len(got.Candidates))
	}
	if got.Candidates[0].Memory.ID != "m1" {
		t.Errorf("top candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "m1")
	}
}

func TestRerankGraphStage_MaxGraphScoreAcrossEntities(t *testing.T) {
	// 候选有多个实体时取最大图距离分 / Take max graph score across candidate's entities
	mock := &mockGraphRetrieverForRerank{
		memEntities: map[string][]*model.Entity{
			"m1": {
				{ID: "e_far", Name: "far"},   // no connection → score 0.0
				{ID: "e_near", Name: "near"}, // direct match → score 1.0
			},
		},
		relations: map[string][]*model.EntityRelation{},
	}

	s := stage.NewRerankGraphStage(mock, 0, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Entities: []string{"e_near"}}
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if len(got.Candidates) != 1 {
		t.Fatalf("Candidates count = %d, want 1", len(got.Candidates))
	}
	// 直接匹配 graphScore=1.0, blended = (1-0.6)*1.0 + 0.6*1.0 = 1.0
	// Direct match graphScore=1.0, blended = (1-0.6)*norm + 0.6*1.0
	if got.Candidates[0].Score < 0.9 {
		t.Errorf("blended score = %f, want >= 0.9 (direct entity match)", got.Candidates[0].Score)
	}
}

func TestRerankGraphStage_ImmutableInput(t *testing.T) {
	// 验证原始候选分数不被修改 / Verify original candidate scores are not mutated
	mock := &mockGraphRetrieverForRerank{
		memEntities: map[string][]*model.Entity{
			"m1": {{ID: "e_q", Name: "q"}},
			"m2": {{ID: "e_other", Name: "other"}},
		},
		relations: map[string][]*model.EntityRelation{},
	}

	origCandidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.8},
		{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.6},
	}
	origScore0 := origCandidates[0].Score
	origScore1 := origCandidates[1].Score

	s := stage.NewRerankGraphStage(mock, 0, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Entities: []string{"e_q"}}
	state.Candidates = origCandidates

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if origCandidates[0].Score != origScore0 {
		t.Errorf("original candidates[0].Score changed from %f to %f", origScore0, origCandidates[0].Score)
	}
	if origCandidates[1].Score != origScore1 {
		t.Errorf("original candidates[1].Score changed from %f to %f", origScore1, origCandidates[1].Score)
	}
}

func TestRerankGraphStage_TraceRecorded(t *testing.T) {
	mock := &mockGraphRetrieverForRerank{
		memEntities: map[string][]*model.Entity{
			"m1": {{ID: "e1", Name: "e1"}},
		},
		relations: map[string][]*model.EntityRelation{},
	}

	s := stage.NewRerankGraphStage(mock, 0, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Entities: []string{"e1"}}
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.8},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	found := false
	for _, tr := range got.Traces {
		if tr.Name == "rerank_graph" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected trace for rerank_graph stage")
	}
}
