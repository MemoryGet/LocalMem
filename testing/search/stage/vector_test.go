package stage_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// vectorSearcherMock 向量检索 mock / Vector searcher mock
type vectorSearcherMock struct {
	results  []*model.SearchResult
	err      error
	vectors  map[string][]float32
	vecErr   error
	usedFilt bool
}

func (m *vectorSearcherMock) Search(_ context.Context, _ []float32, _ *model.Identity, _ int) ([]*model.SearchResult, error) {
	return m.results, m.err
}
func (m *vectorSearcherMock) SearchFiltered(_ context.Context, _ []float32, _ *model.SearchFilters, _ int) ([]*model.SearchResult, error) {
	m.usedFilt = true
	return m.results, m.err
}
func (m *vectorSearcherMock) GetVectors(_ context.Context, ids []string) (map[string][]float32, error) {
	return m.vectors, m.vecErr
}

// embedderMock 向量化 mock / Embedder mock
type embedderMock struct {
	embedding []float32
	err       error
}

func (m *embedderMock) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.embedding, m.err
}

func TestVectorStage_Name(t *testing.T) {
	s := stage.NewVectorStage(nil, nil, 0, 0)
	if s.Name() != "vector" {
		t.Errorf("Name() = %q, want %q", s.Name(), "vector")
	}
}

func TestVectorStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewVectorStage(nil, nil, 0, 0)
}

func TestVectorStage_Execute(t *testing.T) {
	highScoreResult := []*model.SearchResult{
		{Memory: &model.Memory{ID: "v-1", Content: "vec result"}, Score: 0.9, Source: "vector"},
	}
	lowScoreResult := []*model.SearchResult{
		{Memory: &model.Memory{ID: "v-2", Content: "low"}, Score: 0.1, Source: "vector"},
	}
	mixedResults := []*model.SearchResult{
		{Memory: &model.Memory{ID: "v-1"}, Score: 0.9, Source: "vector"},
		{Memory: &model.Memory{ID: "v-2"}, Score: 0.1, Source: "vector"},
	}

	tests := []struct {
		name      string
		searcher  stage.VectorSearcher
		embedder  stage.Embedder
		query     string
		embedding []float32
		wantCount int
		wantSkip  bool
	}{
		{
			name:      "nil searcher skips",
			searcher:  nil,
			embedder:  nil,
			query:     "test",
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "nil embedder and no embedding skips",
			searcher:  &vectorSearcherMock{results: highScoreResult},
			embedder:  nil,
			query:     "test",
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "pre-set embedding used",
			searcher:  &vectorSearcherMock{results: highScoreResult},
			embedder:  nil,
			query:     "test",
			embedding: []float32{0.1, 0.2, 0.3},
			wantCount: 1,
		},
		{
			name:      "embedder generates embedding",
			searcher:  &vectorSearcherMock{results: highScoreResult},
			embedder:  &embedderMock{embedding: []float32{0.1, 0.2}},
			query:     "test",
			wantCount: 1,
		},
		{
			name:      "low score filtered out",
			searcher:  &vectorSearcherMock{results: lowScoreResult},
			embedder:  &embedderMock{embedding: []float32{0.1}},
			query:     "test",
			wantCount: 0,
		},
		{
			name:      "mixed scores partially filtered",
			searcher:  &vectorSearcherMock{results: mixedResults},
			embedder:  &embedderMock{embedding: []float32{0.1}},
			query:     "test",
			wantCount: 1,
		},
		{
			name:      "search error returns state without error",
			searcher:  &vectorSearcherMock{err: errors.New("search failed")},
			embedder:  &embedderMock{embedding: []float32{0.1}},
			query:     "test",
			wantCount: 0,
		},
		{
			name:      "embedder error skips",
			searcher:  &vectorSearcherMock{results: highScoreResult},
			embedder:  &embedderMock{err: errors.New("embed failed")},
			query:     "test",
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "empty query and no embedding skips",
			searcher:  &vectorSearcherMock{results: highScoreResult},
			embedder:  &embedderMock{embedding: []float32{0.1}},
			query:     "",
			wantCount: 0,
			wantSkip:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewVectorStage(tt.searcher, tt.embedder, 30, 0.3)
			identity := &model.Identity{TeamID: "team-1", OwnerID: "owner-1"}
			state := pipeline.NewState(tt.query, identity)
			if tt.embedding != nil {
				state.Embedding = tt.embedding
			}

			got, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("Execute() returned error: %v", err)
			}
			if len(got.Candidates) != tt.wantCount {
				t.Errorf("Candidates count = %d, want %d", len(got.Candidates), tt.wantCount)
			}
			if tt.wantSkip {
				found := false
				for _, tr := range got.Traces {
					if tr.Name == "vector" && tr.Skipped {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected skipped trace for vector stage")
				}
			}
		})
	}
}

func TestVectorStage_Execute_UsesSemanticQuery(t *testing.T) {
	mock := &vectorSearcherMock{results: []*model.SearchResult{
		{Memory: &model.Memory{ID: "v-1"}, Score: 0.9, Source: "vector"},
	}}
	emb := &embedderMock{embedding: []float32{0.1, 0.2}}
	s := stage.NewVectorStage(mock, emb, 30, 0.3)
	state := pipeline.NewState("original", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{SemanticQuery: "semantic version"}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 1 {
		t.Errorf("Candidates count = %d, want 1", len(got.Candidates))
	}
}

func TestVectorStage_Execute_UsesFilters(t *testing.T) {
	mock := &vectorSearcherMock{results: []*model.SearchResult{
		{Memory: &model.Memory{ID: "v-1"}, Score: 0.9, Source: "vector"},
	}}
	s := stage.NewVectorStage(mock, nil, 30, 0.3)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Embedding = []float32{0.1, 0.2}
	state.Filters = &model.SearchFilters{Scope: "project/x"}

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !mock.usedFilt {
		t.Error("expected SearchFiltered to be called")
	}
}

func TestVectorStage_Execute_AppendsToCandidates(t *testing.T) {
	existing := []*model.SearchResult{
		{Memory: &model.Memory{ID: "existing"}, Score: 0.9, Source: "fts"},
	}
	mock := &vectorSearcherMock{results: []*model.SearchResult{
		{Memory: &model.Memory{ID: "new"}, Score: 0.8, Source: "vector"},
	}}
	s := stage.NewVectorStage(mock, nil, 30, 0.3)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = existing
	state.Embedding = []float32{0.1}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}
	if got.Candidates[0].Memory.ID != "existing" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "existing")
	}
}
