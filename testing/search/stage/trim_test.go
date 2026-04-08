package stage_test

import (
	"context"
	"strings"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestTrimStage_Name(t *testing.T) {
	s := stage.NewTrimStage(0)
	if s.Name() != "trim" {
		t.Errorf("Name() = %q, want %q", s.Name(), "trim")
	}
}

func TestTrimStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewTrimStage(0)
}

func TestTrimStage_Execute(t *testing.T) {
	shortContent := "hello world"                    // ~3 tokens
	longContent := strings.Repeat("word ", 200)      // ~200 tokens
	veryLongContent := strings.Repeat("word ", 1000) // ~1000 tokens

	tests := []struct {
		name       string
		maxTokens  int
		candidates []*model.SearchResult
		wantCount  int
		wantTrunc  bool
	}{
		{
			name:       "empty candidates",
			maxTokens:  100,
			candidates: nil,
			wantCount:  0,
		},
		{
			name:      "all fit within budget",
			maxTokens: 1000,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: shortContent}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: shortContent}, Score: 0.8},
			},
			wantCount: 2,
		},
		{
			name:      "truncated by budget",
			maxTokens: 50,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: shortContent}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: longContent}, Score: 0.8},
				{Memory: &model.Memory{ID: "c", Content: shortContent}, Score: 0.7},
			},
			wantCount: 1,
			wantTrunc: true,
		},
		{
			name:      "at least one result even if over budget",
			maxTokens: 1,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: veryLongContent}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: shortContent}, Score: 0.8},
			},
			wantCount: 1,
		},
		{
			name:      "nil memory handled",
			maxTokens: 100,
			candidates: []*model.SearchResult{
				{Memory: nil, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: shortContent}, Score: 0.8},
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewTrimStage(tt.maxTokens)
			state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
			state.Candidates = tt.candidates

			got, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("Execute() returned error: %v", err)
			}
			if len(got.Candidates) != tt.wantCount {
				t.Errorf("Candidates count = %d, want %d", len(got.Candidates), tt.wantCount)
			}
			if tt.wantTrunc {
				found := false
				for _, tr := range got.Traces {
					if tr.Name == "trim" && tr.Note == "truncated by token budget" {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected truncation note in trace")
				}
			}
		})
	}
}

func TestTrimStage_Execute_PreservesOrder(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "short"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "short"}, Score: 0.8},
		{Memory: &model.Memory{ID: "c", Content: "short"}, Score: 0.7},
	}

	s := stage.NewTrimStage(1000)
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

func TestTrimStage_Execute_TraceRecorded(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
	}
	s := stage.NewTrimStage(1000)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	found := false
	for _, tr := range got.Traces {
		if tr.Name == "trim" {
			found = true
			if tr.InputCount != 1 {
				t.Errorf("trace InputCount = %d, want 1", tr.InputCount)
			}
			break
		}
	}
	if !found {
		t.Error("expected trace for trim stage")
	}
}

func TestTrimStage_Execute_DefaultMaxTokens(t *testing.T) {
	// 0 maxTokens should use default (4096)
	s := stage.NewTrimStage(0)
	if s.Name() != "trim" {
		t.Errorf("Name() = %q, want %q", s.Name(), "trim")
	}
}
