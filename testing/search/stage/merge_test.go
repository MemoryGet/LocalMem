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

func TestMergeStage_GraphAware(t *testing.T) {
	tests := []struct {
		name       string
		candidates []*model.SearchResult
		wantTop    string
		wantCount  int
	}{
		{
			name: "cross-validated graph+fts beats single source",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.9, Source: "graph"},
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.8, Source: "fts"},
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.95, Source: "fts"},
				{Memory: &model.Memory{ID: "m3", Content: "c"}, Score: 0.85, Source: "graph"},
			},
			wantTop:   "m1",
			wantCount: 3,
		},
		{
			name: "graph-only ranks above fts-only due to trust weighting",
			candidates: []*model.SearchResult{
				// m1 is graph-only (trust 1.0), m2 is fts-only (trust 0.8)
				// Both rank 0 in their source => base RRF equal, but trust tips m1 ahead
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.8, Source: "graph"},
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.8, Source: "fts"},
			},
			wantTop:   "m1",
			wantCount: 2,
		},
		{
			name: "fts-only trust penalty flips ranking vs plain rrf",
			candidates: []*model.SearchResult{
				// fts source has m2 rank0, m1 rank1 => RRF: m2 > m1
				// graph source has m1 rank0 => m1 appears in graph+fts → trust 1.5
				// m2 only in fts → trust 0.8
				// graph_aware: m1 = 1.5*(1/62) + 1.5*(1/62) vs m2 = 0.8*(1/61) — m1 wins
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.95, Source: "fts"},
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.80, Source: "fts"},
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.90, Source: "graph"},
			},
			wantTop:   "m1",
			wantCount: 2,
		},
		{
			name: "vector treated same as graph trust",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.8, Source: "vector"},
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.8, Source: "fts"},
			},
			wantTop:   "m1",
			wantCount: 2,
		},
		{
			name:       "empty candidates",
			candidates: nil,
			wantCount:  0,
		},
		{
			name: "single source passthrough",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.9, Source: "graph"},
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.5, Source: "graph"},
			},
			wantTop:   "m1",
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewMergeStage("graph_aware", 60, 100)
			state := pipeline.NewState("q", nil)
			state.Candidates = tt.candidates

			result, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Candidates) != tt.wantCount {
				t.Fatalf("expected %d candidates, got %d", tt.wantCount, len(result.Candidates))
			}
			if tt.wantTop != "" && len(result.Candidates) > 0 {
				if result.Candidates[0].Memory.ID != tt.wantTop {
					t.Errorf("expected top %s, got %s", tt.wantTop, result.Candidates[0].Memory.ID)
					for _, c := range result.Candidates {
						t.Logf("  %s: %.6f (source: %s)", c.Memory.ID, c.Score, c.Source)
					}
				}
			}
		})
	}
}

func TestMergeStage_GraphAware_ScoreValues(t *testing.T) {
	// 验证 graph_aware 实际应用信任因子而非普通 RRF / Verify graph_aware applies trust factors, not plain RRF
	// With k=60: rank 0 base RRF = 1/(60+0+1) = 1/61 ≈ 0.016393
	// graph_aware for cross-validated (trust 1.5): 1.5 * 1/61 ≈ 0.024590 per source
	// graph_aware for fts-only (trust 0.8): 0.8 * 1/61 ≈ 0.013115
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "cross"}, Score: 0.9, Source: "graph"},
		{Memory: &model.Memory{ID: "m1", Content: "cross"}, Score: 0.9, Source: "fts"},
		{Memory: &model.Memory{ID: "m2", Content: "fts-only"}, Score: 0.95, Source: "fts"},
	}

	s := stage.NewMergeStage("graph_aware", 60, 100)
	state := pipeline.NewState("q", nil)
	state.Candidates = candidates

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Candidates))
	}

	// m1: cross-validated → trust 1.5, appears in 2 sources (rank 0 each)
	// m1 score = 1.5 * 1/61 + 1.5 * 1/61 ≈ 0.049180
	// m2: fts-only → trust 0.8, rank 1 in fts
	// m2 score = 0.8 * 1/62 ≈ 0.012903
	m1Score := result.Candidates[0].Score
	m2Score := result.Candidates[1].Score

	if result.Candidates[0].Memory.ID != "m1" {
		t.Fatalf("expected m1 first, got %s", result.Candidates[0].Memory.ID)
	}

	// m1 should have trust-weighted score, not plain RRF
	// Plain RRF m1 = 1/61 + 1/61 ≈ 0.032787
	// graph_aware m1 = 1.5/61 + 1.5/61 ≈ 0.049180
	expectedM1GraphAware := 1.5/61 + 1.5/61
	expectedM1PlainRRF := 1.0/61 + 1.0/61
	tolerance := 0.001

	if m1Score < expectedM1GraphAware-tolerance || m1Score > expectedM1GraphAware+tolerance {
		t.Errorf("m1 score = %f, want ~%f (graph_aware), plain RRF would give ~%f",
			m1Score, expectedM1GraphAware, expectedM1PlainRRF)
	}

	// m2 should have 0.8 trust penalty
	expectedM2 := 0.8 / 62
	if m2Score < expectedM2-tolerance || m2Score > expectedM2+tolerance {
		t.Errorf("m2 score = %f, want ~%f (fts-only trust 0.8)", m2Score, expectedM2)
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
