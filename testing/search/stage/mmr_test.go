package stage_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestMMRStage_Name(t *testing.T) {
	s := stage.NewMMRStage(nil, 0, 0)
	if s.Name() != "mmr" {
		t.Errorf("Name() = %q, want %q", s.Name(), "mmr")
	}
}

func TestMMRStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewMMRStage(nil, 0, 0)
}

func TestMMRStage_Execute(t *testing.T) {
	tests := []struct {
		name       string
		searcher   stage.VectorSearcher
		candidates []*model.SearchResult
		wantCount  int
		wantSkip   bool
	}{
		{
			name:       "nil vecSearcher skips",
			searcher:   nil,
			candidates: makeCandidates(3),
			wantCount:  3,
			wantSkip:   true,
		},
		{
			name: "single candidate passthrough",
			searcher: &vectorSearcherMock{vectors: map[string][]float32{
				"a": {1.0, 0, 0},
			}},
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
			},
			wantCount: 1,
		},
		{
			name: "normal MMR selection",
			searcher: &vectorSearcherMock{vectors: map[string][]float32{
				"a": {1.0, 0, 0},
				"b": {0.9, 0.1, 0},
				"c": {0, 1.0, 0},
			}},
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "x"}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: "y"}, Score: 0.8},
				{Memory: &model.Memory{ID: "c", Content: "z"}, Score: 0.7},
			},
			wantCount: 3,
		},
		{
			name:     "empty vectors fallback",
			searcher: &vectorSearcherMock{vectors: map[string][]float32{}},
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "x"}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: "y"}, Score: 0.8},
			},
			wantCount: 2,
		},
		{
			name: "vector error fallback",
			searcher: &vectorSearcherMock{
				vecErr: context.DeadlineExceeded,
			},
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "x"}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: "y"}, Score: 0.8},
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewMMRStage(tt.searcher, 0.7, 10)
			state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
			state.Candidates = tt.candidates

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
					if tr.Name == "mmr" && tr.Skipped {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected skipped trace for mmr stage")
				}
			}
		})
	}
}

func TestMMRStage_Execute_DiversitySelection(t *testing.T) {
	// a and b are very similar, c is very different
	searcher := &vectorSearcherMock{vectors: map[string][]float32{
		"a": {1.0, 0, 0},
		"b": {0.99, 0.01, 0},
		"c": {0, 0, 1.0},
	}}

	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "x"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "y"}, Score: 0.85},
		{Memory: &model.Memory{ID: "c", Content: "z"}, Score: 0.8},
	}

	s := stage.NewMMRStage(searcher, 0.5, 2) // low lambda = high diversity
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}

	// With diversity preference, should pick a (top score) then c (diverse)
	if got.Candidates[0].Memory.ID != "a" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "a")
	}
	if got.Candidates[1].Memory.ID != "c" {
		t.Errorf("second candidate ID = %q, want %q (diverse pick)", got.Candidates[1].Memory.ID, "c")
	}
}

func TestMMRStage_Execute_LimitEnforced(t *testing.T) {
	searcher := &vectorSearcherMock{vectors: map[string][]float32{
		"a": {1.0, 0, 0},
		"b": {0, 1.0, 0},
		"c": {0, 0, 1.0},
	}}

	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "x"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "y"}, Score: 0.8},
		{Memory: &model.Memory{ID: "c", Content: "z"}, Score: 0.7},
	}

	s := stage.NewMMRStage(searcher, 0.7, 2)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Errorf("Candidates count = %d, want 2", len(got.Candidates))
	}
}

func TestMMRStage_Execute_NoNormalPathTrace(t *testing.T) {
	searcher := &vectorSearcherMock{vectors: map[string][]float32{
		"a": {1.0, 0},
		"b": {0, 1.0},
	}}
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "x"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "y"}, Score: 0.8},
	}

	s := stage.NewMMRStage(searcher, 0.7, 10)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	// Normal-path trace is now added by pipeline.executeWithTrace, not by the stage itself
	for _, tr := range got.Traces {
		if tr.Name == "mmr" && !tr.Skipped && tr.Note == "" {
			t.Error("stage should not emit its own normal-path trace (pipeline handles it)")
		}
	}
}

// makeCandidates 辅助函数，创建指定数量的候选 / Helper to create N candidates
func makeCandidates(n int) []*model.SearchResult {
	results := make([]*model.SearchResult, n)
	for i := range results {
		results[i] = &model.SearchResult{
			Memory: &model.Memory{ID: string(rune('a' + i)), Content: "x"},
			Score:  float64(n-i) / float64(n),
		}
	}
	return results
}
