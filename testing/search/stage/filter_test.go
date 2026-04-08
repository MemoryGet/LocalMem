package stage_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestFilterStage_Name(t *testing.T) {
	s := stage.NewFilterStage(0)
	if s.Name() != "filter" {
		t.Errorf("Name() = %q, want %q", s.Name(), "filter")
	}
}

func TestFilterStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewFilterStage(0)
}

func TestFilterStage_Execute(t *testing.T) {
	tests := []struct {
		name          string
		candidates    []*model.SearchResult
		minScoreRatio float64
		wantCount     int
	}{
		{
			name:       "empty candidates passthrough",
			candidates: nil,
			wantCount:  0,
		},
		{
			name: "all pass ratio filter",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a"}, Score: 1.0},
				{Memory: &model.Memory{ID: "b"}, Score: 0.8},
				{Memory: &model.Memory{ID: "c"}, Score: 0.5},
			},
			minScoreRatio: 0.3,
			wantCount:     3,
		},
		{
			name: "low score filtered",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a"}, Score: 1.0},
				{Memory: &model.Memory{ID: "b"}, Score: 0.5},
				{Memory: &model.Memory{ID: "c"}, Score: 0.2},
				{Memory: &model.Memory{ID: "d"}, Score: 0.1},
			},
			minScoreRatio: 0.3,
			wantCount:     2,
		},
		{
			name: "top score zero skips filter",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a"}, Score: 0},
				{Memory: &model.Memory{ID: "b"}, Score: 0},
			},
			minScoreRatio: 0.3,
			wantCount:     2,
		},
		{
			name: "single candidate passthrough",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a"}, Score: 0.1},
			},
			minScoreRatio: 0.5,
			wantCount:     1,
		},
		{
			name: "high ratio removes most",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a"}, Score: 1.0},
				{Memory: &model.Memory{ID: "b"}, Score: 0.5},
				{Memory: &model.Memory{ID: "c"}, Score: 0.3},
			},
			minScoreRatio: 0.8,
			wantCount:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewFilterStage(tt.minScoreRatio)
			state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
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

func TestFilterStage_Execute_PreservesOrder(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a"}, Score: 1.0},
		{Memory: &model.Memory{ID: "b"}, Score: 0.8},
		{Memory: &model.Memory{ID: "c"}, Score: 0.5},
	}
	s := stage.NewFilterStage(0.3)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	for i, c := range got.Candidates {
		if c.Memory.ID != candidates[i].Memory.ID {
			t.Errorf("candidate[%d].ID = %q, want %q", i, c.Memory.ID, candidates[i].Memory.ID)
		}
	}
}

func TestFilterStage_Execute_TraceRecorded(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a"}, Score: 1.0},
	}
	s := stage.NewFilterStage(0.3)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	found := false
	for _, tr := range got.Traces {
		if tr.Name == "filter" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected trace for filter stage")
	}
}
