package stage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestMergeStage_Name(t *testing.T) {
	s := stage.NewMergeStage("rrf", 0, 0, 0.1)
	if s.Name() != "merge" {
		t.Errorf("Name() = %q, want %q", s.Name(), "merge")
	}
}

func TestMergeStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewMergeStage("", 0, 0, 0.1)
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
			s := stage.NewMergeStage("rrf", 60, limit, 0.1)
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

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
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

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
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
			s := stage.NewMergeStage("graph_aware", 60, 100, 0.1)
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
	// Test memories have Strength=0 and no LastAccessedAt → CalculateEffectiveStrength returns 0.0,
	// then minEffectiveStrength floor (0.05) applies. No kind/scope set → kw=1.0, boost=1.0.
	// structuralWeight = 1.0 * 1.0 * 0.05 = 0.05
	//
	// With k=60, structural weight w=0.05:
	//   m1 cross-validated (trust 1.5), rank 0 in both sources:
	//     score = 1.5*w/61 + 1.5*w/61 = 1.5*0.05/61 + 1.5*0.05/61 ≈ 0.002459
	//   m2 fts-only (trust 0.8), rank 1 in fts (m1 is rank 0 but only in one source — actually m2
	//     has Score 0.95 so it's rank 0, m1 fts is rank 1):
	//     m2 score = 0.8*0.05/61 ≈ 0.000656; m1 fts (rank1) = 1.5*0.05/62
	//   Overall m1 = 1.5*0.05/61 + 1.5*0.05/62; m2 = 0.8*0.05/61
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "cross"}, Score: 0.9, Source: "graph"},
		{Memory: &model.Memory{ID: "m1", Content: "cross"}, Score: 0.9, Source: "fts"},
		{Memory: &model.Memory{ID: "m2", Content: "fts-only"}, Score: 0.95, Source: "fts"},
	}

	s := stage.NewMergeStage("graph_aware", 60, 100, 0.1)
	state := pipeline.NewState("q", nil)
	state.Candidates = candidates

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Candidates))
	}

	// m2 Score=0.95 is rank 0 in fts; m1 Score=0.9 is rank 1 in fts; m1 Score=0.9 is rank 0 in graph.
	// structural weight for all = 0.05 (minEffectiveStrength floor, no kind/scope set)
	// m1 = trust(1.5)*0.05/62 [fts rank1] + trust(1.5)*0.05/61 [graph rank0]
	// m2 = trust(0.8)*0.05/61 [fts rank0]
	const sw = 0.05 // structural weight floor
	expectedM1 := 1.5*sw/62 + 1.5*sw/61
	expectedM2 := 0.8 * sw / 61
	// plain RRF (no structural weight) m1 would be 1/62 + 1/61 ≈ 0.032610 — trust factor differentiates
	expectedM1PlainRRF := 1.0/62 + 1.0/61
	tolerance := 0.0005

	m1Score := result.Candidates[0].Score
	m2Score := result.Candidates[1].Score

	if result.Candidates[0].Memory.ID != "m1" {
		t.Fatalf("expected m1 first, got %s (m1=%.6f m2=%.6f)", result.Candidates[0].Memory.ID, m1Score, m2Score)
	}

	// m1 should have trust-weighted + structural-weight score, not plain RRF
	if m1Score < expectedM1-tolerance || m1Score > expectedM1+tolerance {
		t.Errorf("m1 score = %.6f, want ~%.6f (graph_aware+structural), plain RRF would give ~%.6f",
			m1Score, expectedM1, expectedM1PlainRRF)
	}

	// m2 should have 0.8 trust penalty × structural weight
	if m2Score < expectedM2-tolerance || m2Score > expectedM2+tolerance {
		t.Errorf("m2 score = %.6f, want ~%.6f (fts-only trust 0.8 × structural)", m2Score, expectedM2)
	}
}

func TestMergeStage_Execute_KeepsMostCompleteMemory(t *testing.T) {
	// 空 Content 的应被完整对象替代 / Empty Content should be replaced by complete object
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "x", Content: ""}, Score: 0.9, Source: "vector"},
		{Memory: &model.Memory{ID: "x", Content: "full content"}, Score: 0.8, Source: "fts"},
	}

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
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

func TestMergeStage_StructuralWeight_ExpiredFiltered(t *testing.T) {
	now := time.Now()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	expired := &model.Memory{ID: "expired", Kind: "note", ExpiresAt: &past, Strength: 1.0}
	live := &model.Memory{ID: "live", Kind: "note", ExpiresAt: &future, Strength: 1.0}

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
	state := &pipeline.PipelineState{
		Candidates: []*model.SearchResult{
			{Memory: expired, Score: 0.9},
			{Memory: live, Score: 0.8},
		},
	}
	out, err := s.Execute(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, out.Candidates, 1)
	assert.Equal(t, "live", out.Candidates[0].Memory.ID)
}

func TestMergeStage_StructuralWeight_SkillRanksAboveNote(t *testing.T) {
	skillMem := &model.Memory{ID: "skill", Kind: "skill", Strength: 0.8, MemoryClass: "procedural"}
	noteMem := &model.Memory{ID: "note", Kind: "note", Strength: 0.8, MemoryClass: "episodic"}

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
	state := &pipeline.PipelineState{
		Candidates: []*model.SearchResult{
			{Memory: noteMem, Score: 1.0, Source: "fts"},
			{Memory: skillMem, Score: 1.0, Source: "fts"},
		},
	}
	out, err := s.Execute(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, out.Candidates, 2)
	assert.Equal(t, "skill", out.Candidates[0].Memory.ID, "skill (1.5x kind weight) should rank above note (1.0x)")
}

func TestMergeStage_StructuralWeight_SessionScopeBoost(t *testing.T) {
	sessionMem := &model.Memory{ID: "session", Kind: "note", Scope: "session/abc", Strength: 0.8}
	agentMem := &model.Memory{ID: "agent", Kind: "note", Scope: "agent/xyz", Strength: 0.8}

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
	state := &pipeline.PipelineState{
		Candidates: []*model.SearchResult{
			{Memory: agentMem, Score: 1.0, Source: "fts"},
			{Memory: sessionMem, Score: 1.0, Source: "fts"},
		},
	}
	out, err := s.Execute(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, out.Candidates, 2)
	assert.Equal(t, "session", out.Candidates[0].Memory.ID, "session/ scope (1.3x) should rank above agent/ (1.0x)")
}

func TestMergeStage_StructuralWeight_ProceduralClass(t *testing.T) {
	proceduralMem := &model.Memory{ID: "proc", Kind: "note", MemoryClass: "procedural", Strength: 0.8}
	episodicMem := &model.Memory{ID: "epis", Kind: "note", MemoryClass: "episodic", Strength: 0.8}

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
	state := &pipeline.PipelineState{
		Candidates: []*model.SearchResult{
			{Memory: episodicMem, Score: 1.0, Source: "fts"},
			{Memory: proceduralMem, Score: 1.0, Source: "fts"},
		},
	}
	out, err := s.Execute(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, out.Candidates, 2)
	assert.Equal(t, "proc", out.Candidates[0].Memory.ID, "procedural class (1.5x) should rank above episodic (1.0x)")
}

func TestMergeStage_StructuralWeight_PermanentNoDecay(t *testing.T) {
	oldTime := time.Now().Add(-720 * time.Hour)
	permanentMem := &model.Memory{
		ID: "perm", Kind: "note", Strength: 0.8,
		RetentionTier: model.TierPermanent, LastAccessedAt: &oldTime,
	}
	standardMem := &model.Memory{
		ID: "std", Kind: "note", Strength: 0.8,
		RetentionTier: model.TierStandard, DecayRate: 0.01, LastAccessedAt: &oldTime,
	}

	s := stage.NewMergeStage("rrf", 60, 100, 0.1)
	state := &pipeline.PipelineState{
		Candidates: []*model.SearchResult{
			{Memory: standardMem, Score: 1.0, Source: "fts"},
			{Memory: permanentMem, Score: 1.0, Source: "fts"},
		},
	}
	out, err := s.Execute(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, out.Candidates, 2)
	assert.Equal(t, "perm", out.Candidates[0].Memory.ID, "permanent tier should outrank standard after 720h decay")
}
