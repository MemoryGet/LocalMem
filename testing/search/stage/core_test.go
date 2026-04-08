package stage_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// coreProviderMock 核心记忆提供者 mock / Core provider mock
type coreProviderMock struct {
	memories []*model.Memory
	err      error
}

func (m *coreProviderMock) GetCoreBlocksMultiScope(_ context.Context, _ []string, _ *model.Identity) ([]*model.Memory, error) {
	return m.memories, m.err
}

func TestCoreStage_Name(t *testing.T) {
	s := stage.NewCoreStage(nil)
	if s.Name() != "core" {
		t.Errorf("Name() = %q, want %q", s.Name(), "core")
	}
}

func TestCoreStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewCoreStage(nil)
}

func TestCoreStage_Execute(t *testing.T) {
	coreMem := &model.Memory{ID: "core-1", Content: "core block"}
	existingMem := &model.Memory{ID: "existing", Content: "existing"}

	tests := []struct {
		name      string
		provider  stage.CoreProvider
		identity  *model.Identity
		existing  []*model.SearchResult
		filters   *model.SearchFilters
		wantCount int
		wantSkip  bool
	}{
		{
			name:      "nil provider skips",
			provider:  nil,
			identity:  &model.Identity{TeamID: "t", OwnerID: "alice"},
			wantCount: 0,
			wantSkip:  true,
		},
		{
			name:      "no scopes resolved skips",
			provider:  &coreProviderMock{memories: []*model.Memory{coreMem}},
			identity:  &model.Identity{TeamID: "t", OwnerID: ""},
			wantCount: 0,
		},
		{
			name:     "normal core injection",
			provider: &coreProviderMock{memories: []*model.Memory{coreMem}},
			identity: &model.Identity{TeamID: "t", OwnerID: "alice"},
			existing: []*model.SearchResult{
				{Memory: existingMem, Score: 0.8, Source: "fts"},
			},
			wantCount: 2,
		},
		{
			name:     "deduplicates existing IDs",
			provider: &coreProviderMock{memories: []*model.Memory{{ID: "existing", Content: "core version"}}},
			identity: &model.Identity{TeamID: "t", OwnerID: "alice"},
			existing: []*model.SearchResult{
				{Memory: existingMem, Score: 0.8, Source: "fts"},
			},
			wantCount: 1, // no new injection since ID already exists
		},
		{
			name:      "provider error graceful fallback",
			provider:  &coreProviderMock{err: errors.New("provider error")},
			identity:  &model.Identity{TeamID: "t", OwnerID: "alice"},
			wantCount: 0,
		},
		{
			name:      "empty core blocks",
			provider:  &coreProviderMock{memories: []*model.Memory{}},
			identity:  &model.Identity{TeamID: "t", OwnerID: "alice"},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewCoreStage(tt.provider)
			state := pipeline.NewState("test", tt.identity)
			state.Candidates = tt.existing
			if tt.filters != nil {
				state.Metadata["filters"] = tt.filters
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
					if tr.Name == "core" && tr.Skipped {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected skipped trace for core stage")
				}
			}
		})
	}
}

func TestCoreStage_Execute_CoreFirst(t *testing.T) {
	coreMem := &model.Memory{ID: "core-1", Content: "core block"}
	existing := []*model.SearchResult{
		{Memory: &model.Memory{ID: "existing"}, Score: 0.8, Source: "fts"},
	}

	s := stage.NewCoreStage(&coreProviderMock{memories: []*model.Memory{coreMem}})
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "alice"})
	state.Candidates = existing

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}

	// Core should be first
	if got.Candidates[0].Source != "core" {
		t.Errorf("first candidate source = %q, want %q", got.Candidates[0].Source, "core")
	}
	if got.Candidates[0].Score != 2.0 {
		t.Errorf("core score = %f, want 2.0", got.Candidates[0].Score)
	}
	if got.Candidates[0].Memory.ID != "core-1" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "core-1")
	}
}

func TestCoreStage_Execute_UsesFilterScope(t *testing.T) {
	coreMem := &model.Memory{ID: "core-1", Content: "core"}
	s := stage.NewCoreStage(&coreProviderMock{memories: []*model.Memory{coreMem}})
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "alice"})
	state.Metadata["filters"] = &model.SearchFilters{Scope: "project/x"}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 1 {
		t.Errorf("Candidates count = %d, want 1", len(got.Candidates))
	}
}

func TestCoreStage_Execute_TraceRecorded(t *testing.T) {
	s := stage.NewCoreStage(&coreProviderMock{memories: []*model.Memory{
		{ID: "core-1", Content: "core"},
	}})
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "alice"})

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	found := false
	for _, tr := range got.Traces {
		if tr.Name == "core" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected trace for core stage")
	}
}
