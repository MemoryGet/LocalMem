package stage_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestOverlapRerankStage_Name(t *testing.T) {
	s := stage.NewOverlapRerankStage(0, 0)
	if s.Name() != "rerank_overlap" {
		t.Errorf("Name() = %q, want %q", s.Name(), "rerank_overlap")
	}
}

func TestOverlapRerankStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewOverlapRerankStage(0, 0)
}

func TestOverlapRerankStage_Execute(t *testing.T) {
	tests := []struct {
		name       string
		candidates []*model.SearchResult
		query      string
		wantCount  int
	}{
		{
			name:       "empty candidates passthrough",
			candidates: nil,
			query:      "test",
			wantCount:  0,
		},
		{
			name: "single candidate passthrough",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
			},
			query:     "hello",
			wantCount: 1,
		},
		{
			name: "empty query passthrough",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: "world"}, Score: 0.8},
			},
			query:     "",
			wantCount: 2,
		},
		{
			name: "normal reranking",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "python programming tutorial"}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: "go language basics"}, Score: 0.8},
				{Memory: &model.Memory{ID: "c", Content: "python data science"}, Score: 0.7},
			},
			query:     "python",
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewOverlapRerankStage(20, 0.7)
			state := pipeline.NewState(tt.query, &model.Identity{TeamID: "t", OwnerID: "o"})
			state.Candidates = tt.candidates

			got, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("Execute() returned error: %v", err)
			}
			if len(got.Candidates) != tt.wantCount {
				t.Errorf("Candidates count = %d, want %d", len(got.Candidates), tt.wantCount)
			}
		})
	}
}

func TestOverlapRerankStage_Execute_BoostsPhraseMatch(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "go language basics"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "python programming is great for data science"}, Score: 0.85},
		{Memory: &model.Memory{ID: "c", Content: "python programming tutorial"}, Score: 0.8},
	}

	s := stage.NewOverlapRerankStage(20, 0.7)
	state := pipeline.NewState("python programming", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 3 {
		t.Fatalf("Candidates count = %d, want 3", len(got.Candidates))
	}

	// "python programming" 完全包含的候选应提权 / Candidates containing full phrase should be boosted
	// Both b and c contain "python programming", but a (go language) should be last
	lastID := got.Candidates[2].Memory.ID
	if lastID != "a" {
		t.Errorf("last candidate ID = %q, want %q (non-matching should be last)", lastID, "a")
	}
}

func TestOverlapRerankStage_Execute_ImmutableInput(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello world"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "hello there"}, Score: 0.8},
	}
	origScoreA := candidates[0].Score
	origScoreB := candidates[1].Score

	s := stage.NewOverlapRerankStage(20, 0.7)
	state := pipeline.NewState("hello", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 原始对象的分数不应被修改 / Original object scores should not be mutated
	if candidates[0].Score != origScoreA {
		t.Errorf("original candidates[0].Score changed from %f to %f", origScoreA, candidates[0].Score)
	}
	if candidates[1].Score != origScoreB {
		t.Errorf("original candidates[1].Score changed from %f to %f", origScoreB, candidates[1].Score)
	}
}

func TestOverlapRerankStage_Execute_TraceRecorded(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "world"}, Score: 0.8},
	}
	s := stage.NewOverlapRerankStage(20, 0.7)
	state := pipeline.NewState("hello", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	found := false
	for _, tr := range got.Traces {
		if tr.Name == "rerank_overlap" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected trace for rerank_overlap stage")
	}
}
