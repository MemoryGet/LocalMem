package stage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// timelineSearcherMock 时间线检索 mock / Timeline searcher mock
type timelineSearcherMock struct {
	memories []*model.Memory
	err      error
}

func (m *timelineSearcherMock) ListTimeline(_ context.Context, _ *model.TimelineRequest) ([]*model.Memory, error) {
	return m.memories, m.err
}

func TestTemporalStage_Name(t *testing.T) {
	s := stage.NewTemporalStage(nil, 0)
	if s.Name() != "temporal" {
		t.Errorf("Name() = %q, want %q", s.Name(), "temporal")
	}
}

func TestTemporalStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewTemporalStage(nil, 0)
}

func TestTemporalStage_Execute(t *testing.T) {
	now := time.Now()
	nearMem := &model.Memory{ID: "near", Content: "near", CreatedAt: now}
	farMem := &model.Memory{ID: "far", Content: "far", CreatedAt: now.Add(-30 * 24 * time.Hour)}
	center := now

	tests := []struct {
		name      string
		searcher  stage.TimelineSearcher
		plan      *pipeline.QueryPlan
		wantCount int
		wantSkip  bool
	}{
		{
			name:      "nil searcher skips",
			searcher:  nil,
			plan:      &pipeline.QueryPlan{Temporal: true, TemporalCenter: &center},
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "nil plan skips",
			searcher:  &timelineSearcherMock{memories: []*model.Memory{nearMem}},
			plan:      nil,
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "plan without temporal flag skips",
			searcher:  &timelineSearcherMock{memories: []*model.Memory{nearMem}},
			plan:      &pipeline.QueryPlan{Temporal: false, TemporalCenter: &center},
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "plan without center skips",
			searcher:  &timelineSearcherMock{memories: []*model.Memory{nearMem}},
			plan:      &pipeline.QueryPlan{Temporal: true, TemporalCenter: nil},
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "normal retrieval",
			searcher:  &timelineSearcherMock{memories: []*model.Memory{nearMem, farMem}},
			plan:      &pipeline.QueryPlan{Temporal: true, TemporalCenter: &center, TemporalRange: 7 * 24 * time.Hour},
			wantCount: 2,
		},
		{
			name:      "search error returns state without error",
			searcher:  &timelineSearcherMock{err: errors.New("db error")},
			plan:      &pipeline.QueryPlan{Temporal: true, TemporalCenter: &center},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewTemporalStage(tt.searcher, 20)
			identity := &model.Identity{TeamID: "team-1", OwnerID: "owner-1"}
			state := pipeline.NewState("test", identity)
			state.Plan = tt.plan

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
					if tr.Name == "temporal" && tr.Skipped {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected skipped trace for temporal stage")
				}
			}
		})
	}
}

func TestTemporalStage_Execute_DistanceDecayScoring(t *testing.T) {
	now := time.Now()
	center := now

	nearTime := now.Add(-1 * 24 * time.Hour)
	farTime := now.Add(-30 * 24 * time.Hour)
	nearMem := &model.Memory{ID: "near", Content: "near", HappenedAt: &nearTime}
	farMem := &model.Memory{ID: "far", Content: "far", HappenedAt: &farTime}

	mock := &timelineSearcherMock{memories: []*model.Memory{nearMem, farMem}}
	s := stage.NewTemporalStage(mock, 20)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{
		Temporal:       true,
		TemporalCenter: &center,
		TemporalRange:  7 * 24 * time.Hour,
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}

	// 近距离应得更高分 / Closer should score higher
	nearScore := got.Candidates[0].Score
	farScore := got.Candidates[1].Score
	if nearScore <= farScore {
		t.Errorf("near score (%f) should be > far score (%f)", nearScore, farScore)
	}

	// 结果应标记为 temporal / Results should be sourced as temporal
	for _, c := range got.Candidates {
		if c.Source != "temporal" {
			t.Errorf("Source = %q, want %q", c.Source, "temporal")
		}
	}
}

func TestTemporalStage_Execute_AppendsToCandidates(t *testing.T) {
	now := time.Now()
	center := now
	existing := []*model.SearchResult{
		{Memory: &model.Memory{ID: "existing"}, Score: 0.9, Source: "fts"},
	}

	mock := &timelineSearcherMock{memories: []*model.Memory{
		{ID: "temporal-1", Content: "t1", CreatedAt: now},
	}}
	s := stage.NewTemporalStage(mock, 20)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Temporal: true, TemporalCenter: &center}
	state.Candidates = existing

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}
}

func TestTemporalStage_Execute_LimitEnforced(t *testing.T) {
	now := time.Now()
	center := now
	memories := make([]*model.Memory, 50)
	for i := range memories {
		memories[i] = &model.Memory{ID: "m" + string(rune('A'+i)), Content: "x", CreatedAt: now}
	}

	mock := &timelineSearcherMock{memories: memories}
	s := stage.NewTemporalStage(mock, 5)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Temporal: true, TemporalCenter: &center}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) > 5 {
		t.Errorf("Candidates count = %d, want <= 5", len(got.Candidates))
	}
}
