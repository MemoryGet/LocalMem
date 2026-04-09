package stage_test

import (
	"context"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestWeightStage_Name(t *testing.T) {
	s := stage.NewWeightStage(0)
	if s.Name() != "weight" {
		t.Errorf("Name() = %q, want %q", s.Name(), "weight")
	}
}

func TestWeightStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewWeightStage(0)
}

func TestWeightStage_Execute(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name       string
		candidates []*model.SearchResult
		wantCount  int
	}{
		{
			name:       "empty candidates",
			candidates: nil,
			wantCount:  0,
		},
		{
			name: "basic weighting",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: "hello", Kind: "note", Strength: 1.0}, Score: 1.0},
				{Memory: &model.Memory{ID: "b", Content: "world", Kind: "skill", Strength: 1.0}, Score: 1.0},
			},
			wantCount: 2,
		},
		{
			name: "expired memory filtered",
			candidates: func() []*model.SearchResult {
				past := now.Add(-1 * time.Hour)
				return []*model.SearchResult{
					{Memory: &model.Memory{ID: "a", Content: "hello", Strength: 1.0}, Score: 1.0},
					{Memory: &model.Memory{ID: "b", Content: "expired", Strength: 1.0, ExpiresAt: &past}, Score: 0.8},
				}
			}(),
			wantCount: 1,
		},
		{
			name: "nil memory skipped",
			candidates: []*model.SearchResult{
				{Memory: nil, Score: 1.0},
				{Memory: &model.Memory{ID: "a", Content: "hello", Strength: 1.0}, Score: 0.8},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewWeightStage(0.1)
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

func TestWeightStage_Execute_SkillBoosted(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "note", Content: "n", Kind: "note", Strength: 1.0}, Score: 1.0},
		{Memory: &model.Memory{ID: "skill", Content: "s", Kind: "skill", Strength: 1.0}, Score: 1.0},
	}

	s := stage.NewWeightStage(0.1)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}

	// skill (1.5x) should score higher than note (1.0x) after weighting
	skillScore := 0.0
	noteScore := 0.0
	for _, c := range got.Candidates {
		if c.Memory.ID == "skill" {
			skillScore = c.Score
		}
		if c.Memory.ID == "note" {
			noteScore = c.Score
		}
	}
	if skillScore <= noteScore {
		t.Errorf("skill score (%f) should be > note score (%f)", skillScore, noteScore)
	}
}

func TestWeightStage_Execute_ScopePriority(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "session", Content: "s", Scope: "session/abc", Strength: 1.0}, Score: 1.0},
		{Memory: &model.Memory{ID: "agent", Content: "a", Scope: "agent/bot", Strength: 1.0}, Score: 1.0},
	}

	s := stage.NewWeightStage(0.1)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}

	// session (1.3x) should rank higher than agent (1.0x)
	if got.Candidates[0].Memory.ID != "session" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "session")
	}
}

func TestWeightStage_Execute_PermanentNoDecay(t *testing.T) {
	past := time.Now().UTC().Add(-720 * time.Hour)
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{
			ID:             "perm",
			Content:        "permanent",
			Strength:       1.0,
			DecayRate:      0.01,
			RetentionTier:  model.TierPermanent,
			LastAccessedAt: &past,
		}, Score: 1.0},
		{Memory: &model.Memory{
			ID:             "std",
			Content:        "standard",
			Strength:       1.0,
			DecayRate:      0.01,
			RetentionTier:  model.TierStandard,
			LastAccessedAt: &past,
		}, Score: 1.0},
	}

	s := stage.NewWeightStage(0.1)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}

	// permanent should retain full score while standard decays
	permScore := 0.0
	stdScore := 0.0
	for _, c := range got.Candidates {
		if c.Memory.ID == "perm" {
			permScore = c.Score
		}
		if c.Memory.ID == "std" {
			stdScore = c.Score
		}
	}
	if permScore <= stdScore {
		t.Errorf("permanent score (%f) should be > standard score (%f) after decay", permScore, stdScore)
	}
}

func TestWeightStage_Execute_SortedByScore(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "low", Content: "l", Kind: "note", Strength: 1.0}, Score: 0.3},
		{Memory: &model.Memory{ID: "high", Content: "h", Kind: "skill", Strength: 1.0}, Score: 0.9},
		{Memory: &model.Memory{ID: "mid", Content: "m", Kind: "fact", Strength: 1.0}, Score: 0.6},
	}

	s := stage.NewWeightStage(0.1)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	for i := 1; i < len(got.Candidates); i++ {
		if got.Candidates[i].Score > got.Candidates[i-1].Score {
			t.Errorf("results not sorted: candidates[%d].Score=%f > candidates[%d].Score=%f",
				i, got.Candidates[i].Score, i-1, got.Candidates[i-1].Score)
		}
	}
}

func TestWeightStage_Execute_ClassWeighting(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "episodic", Content: "e", MemoryClass: "episodic", Strength: 1.0}, Score: 1.0},
		{Memory: &model.Memory{ID: "procedural", Content: "p", MemoryClass: "procedural", Strength: 1.0}, Score: 1.0},
	}

	s := stage.NewWeightStage(0.1)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// procedural (1.5x) should score higher than episodic (1.0x)
	procScore := 0.0
	epiScore := 0.0
	for _, c := range got.Candidates {
		if c.Memory.ID == "procedural" {
			procScore = c.Score
		}
		if c.Memory.ID == "episodic" {
			epiScore = c.Score
		}
	}
	if procScore <= epiScore {
		t.Errorf("procedural score (%f) should be > episodic score (%f)", procScore, epiScore)
	}
}

func TestWeightStage_Execute_TraceRecorded(t *testing.T) {
	candidates := []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello", Strength: 1.0}, Score: 1.0},
	}
	s := stage.NewWeightStage(0.1)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	found := false
	for _, tr := range got.Traces {
		if tr.Name == "weight" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected trace for weight stage")
	}
}
