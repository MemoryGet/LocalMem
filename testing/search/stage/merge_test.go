package stage_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestMergeStage_Name(t *testing.T) {
	s := stage.NewMergeStage("rrf", 0, 0)
	if s.Name() != "merge" {
		t.Errorf("Name() = %q, want %q", s.Name(), "merge")
	}
}

func TestMergeStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewMergeStage("", 0, 0)
}

func TestMergeStage_Execute(t *testing.T) {
	tests := []struct {
		name       string
		candidates []*model.SearchResult
		limit      int
		wantCount  int
		wantSource string
	}{
		{
			name:       "empty candidates",
			candidates: nil,
			wantCount:  0,
		},
		{
			name: "single source passthrough with dedup",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a"}, Score: 0.9, Source: "fts"},
				{Memory: &model.Memory{ID: "a"}, Score: 0.8, Source: "fts"},
				{Memory: &model.Memory{ID: "b"}, Score: 0.7, Source: "fts"},
			},
			wantCount:  2,
			wantSource: "fts",
		},
		{
			name: "multi source RRF fusion",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9, Source: "fts"},
				{Memory: &model.Memory{ID: "b", Content: "world"}, Score: 0.8, Source: "fts"},
				{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.7, Source: "vector"},
				{Memory: &model.Memory{ID: "c", Content: "third"}, Score: 0.6, Source: "vector"},
			},
			wantCount:  3,
			wantSource: "hybrid",
		},
		{
			name: "limit enforced",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a"}, Score: 0.9, Source: "fts"},
				{Memory: &model.Memory{ID: "b"}, Score: 0.8, Source: "fts"},
				{Memory: &model.Memory{ID: "c"}, Score: 0.7, Source: "vector"},
			},
			limit:     1,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := tt.limit
			if limit == 0 {
				limit = 100
			}
			s := stage.NewMergeStage("rrf", 60, limit)
			state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
			state.Candidates = tt.candidates

			got, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("Execute() returned error: %v", err)
			}
			if len(got.Candidates) != tt.wantCount {
				t.Errorf("Candidates count = %d, want %d", len(got.Candidates), tt.wantCount)
			}

			if tt.wantSource != "" {
				for _, c := range got.Candidates {
					if c.Source != tt.wantSource {
						t.Errorf("Source = %q, want %q", c.Source, tt.wantSource)
					}
				}
			}
		})
	}
}

func TestMergeStage_Execute_RRFScoreAccumulates(t *testing.T) {
	// 一个 ID 出现在两个 source 中应累加 RRF 分数 / An ID appearing in two sources should accumulate RRF score
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "shared", Content: "x"}, Score: 0.9, Source: "fts"},
		{Memory: &model.Memory{ID: "only_fts", Content: "y"}, Score: 0.8, Source: "fts"},
		{Memory: &model.Memory{ID: "shared", Content: "x"}, Score: 0.7, Source: "vector"},
		{Memory: &model.Memory{ID: "only_vec", Content: "z"}, Score: 0.6, Source: "vector"},
	}

	s := stage.NewMergeStage("rrf", 60, 100)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 3 {
		t.Fatalf("Candidates count = %d, want 3", len(got.Candidates))
	}

	// shared should be first due to accumulated score
	if got.Candidates[0].Memory.ID != "shared" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "shared")
	}
}

func TestMergeStage_Execute_StableSortByID(t *testing.T) {
	// 同分时按 ID 字典序 / Same score breaks tie by ID ascending
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "b"}, Score: 0.9, Source: "fts"},
		{Memory: &model.Memory{ID: "a"}, Score: 0.9, Source: "vector"},
	}

	s := stage.NewMergeStage("rrf", 60, 100)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) < 2 {
		t.Fatalf("Candidates count = %d, want >= 2", len(got.Candidates))
	}

	// 同分时 a 应排在 b 前面 / With equal scores, "a" should come before "b"
	if got.Candidates[0].Memory.ID != "a" {
		t.Errorf("first candidate ID = %q, want %q (tie-break by ID)", got.Candidates[0].Memory.ID, "a")
	}
}

func TestMergeStage_Execute_KeepsMostCompleteMemory(t *testing.T) {
	// 空 Content 的应被完整对象替代 / Empty Content should be replaced by complete object
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "x", Content: ""}, Score: 0.9, Source: "vector"},
		{Memory: &model.Memory{ID: "x", Content: "full content"}, Score: 0.8, Source: "fts"},
	}

	s := stage.NewMergeStage("rrf", 60, 100)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("Candidates count = %d, want 1", len(got.Candidates))
	}
	if got.Candidates[0].Memory.Content != "full content" {
		t.Errorf("Memory.Content = %q, want %q", got.Candidates[0].Memory.Content, "full content")
	}
}
