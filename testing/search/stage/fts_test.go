package stage_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// ftsSearcherSpy 带捕获能力的 FTS 检索 mock / FTS searcher mock with capture capabilities
type ftsSearcherSpy struct {
	results       []*model.SearchResult
	err           error
	capturedQuery string
	usedFiltered  bool
}

func (m *ftsSearcherSpy) SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
	m.capturedQuery = query
	return m.results, m.err
}

func (m *ftsSearcherSpy) SearchTextFiltered(ctx context.Context, query string, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	m.capturedQuery = query
	m.usedFiltered = true
	return m.results, m.err
}

func TestFTSStage_Name(t *testing.T) {
	s := stage.NewFTSStage(nil, 0)
	if s.Name() != "fts" {
		t.Errorf("Name() = %q, want %q", s.Name(), "fts")
	}
}

func TestFTSStage_Execute(t *testing.T) {
	oneResult := []*model.SearchResult{
		{Memory: &model.Memory{ID: "mem-1", Content: "hello"}, Score: 0.8, Source: "fts"},
	}

	tests := []struct {
		name           string
		searcher       stage.FTSSearcher
		query          string
		keywords       []string
		filters        *model.SearchFilters
		wantCount      int
		wantSource     string
		wantQuery      string
		wantFiltered   bool
		wantSkipTrace  bool
	}{
		{
			name:       "basic search",
			searcher:   &ftsSearcherSpy{results: oneResult},
			query:      "test",
			wantCount:  1,
			wantSource: "fts",
		},
		{
			name:          "nil searcher skips",
			searcher:      nil,
			query:         "test",
			wantCount:     0,
			wantSkipTrace: true,
		},
		{
			name:          "empty query skips",
			searcher:      &ftsSearcherSpy{results: oneResult},
			query:         "",
			wantCount:     0,
			wantSkipTrace: true,
		},
		{
			name:      "uses keywords from plan",
			searcher:  &ftsSearcherSpy{results: oneResult},
			query:     "original",
			keywords:  []string{"key1", "key2"},
			wantCount: 1,
			wantQuery: "key1 key2",
		},
		{
			name:      "search error returns state without error",
			searcher:  &ftsSearcherSpy{err: errors.New("db error")},
			query:     "test",
			wantCount: 0,
		},
		{
			name:         "uses SearchTextFiltered when filters present",
			searcher:     &ftsSearcherSpy{results: oneResult},
			query:        "test",
			filters:      &model.SearchFilters{Scope: "project/x"},
			wantCount:    1,
			wantFiltered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewFTSStage(tt.searcher, 30)
			identity := &model.Identity{TeamID: "team-1", OwnerID: "owner-1"}
			state := pipeline.NewState(tt.query, identity)

			// 设置 Plan 中的关键词 / Set keywords in plan
			if len(tt.keywords) > 0 {
				state.Plan = &pipeline.QueryPlan{Keywords: tt.keywords}
			}

			// 设置 Metadata 中的过滤器 / Set filters in metadata
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

			// 验证结果源标识 / Verify result source
			if tt.wantSource != "" && tt.wantCount > 0 {
				for _, c := range got.Candidates {
					if c.Source != tt.wantSource {
						t.Errorf("Source = %q, want %q", c.Source, tt.wantSource)
					}
				}
			}

			// 验证捕获的查询 / Verify captured query
			if tt.wantQuery != "" {
				mock, ok := tt.searcher.(*ftsSearcherSpy)
				if !ok {
					t.Fatal("searcher is not *ftsSearcherSpy")
				}
				if mock.capturedQuery != tt.wantQuery {
					t.Errorf("capturedQuery = %q, want %q", mock.capturedQuery, tt.wantQuery)
				}
			}

			// 验证使用了 filtered 方法 / Verify filtered method was used
			if tt.wantFiltered {
				mock, ok := tt.searcher.(*ftsSearcherSpy)
				if !ok {
					t.Fatal("searcher is not *ftsSearcherSpy")
				}
				if !mock.usedFiltered {
					t.Error("expected SearchTextFiltered to be called, but it was not")
				}
			}

			// 验证跳过时的 trace 标记 / Verify skip trace
			if tt.wantSkipTrace {
				found := false
				for _, tr := range got.Traces {
					if tr.Name == "fts" && tr.Skipped {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected skipped trace for fts stage, but not found")
				}
			}
		})
	}
}

// TestFTSStage_Execute_AppendsToCandidates 验证追加而非替换 / Verify append, not replace
func TestFTSStage_Execute_AppendsToCandidates(t *testing.T) {
	existing := []*model.SearchResult{
		{Memory: &model.Memory{ID: "existing"}, Score: 0.9, Source: "qdrant"},
	}
	newResults := []*model.SearchResult{
		{Memory: &model.Memory{ID: "new"}, Score: 0.7, Source: "fts"},
	}

	s := stage.NewFTSStage(&ftsSearcherSpy{results: newResults}, 30)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = existing

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}
	if got.Candidates[0].Memory.ID != "existing" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "existing")
	}
	if got.Candidates[1].Memory.ID != "new" {
		t.Errorf("second candidate ID = %q, want %q", got.Candidates[1].Memory.ID, "new")
	}
}

// TestFTSStage_Execute_DefaultLimit 验证默认 limit / Verify default limit
func TestFTSStage_Execute_DefaultLimit(t *testing.T) {
	s := stage.NewFTSStage(&ftsSearcherSpy{results: []*model.SearchResult{}}, 0)
	// 只验证构造无 panic、Name 正确 / Just verify no panic and correct name
	if s.Name() != "fts" {
		t.Errorf("Name() = %q, want %q", s.Name(), "fts")
	}
}

// TestFTSStage_Execute_KeywordsJoinedWithSpace 验证关键词拼接 / Verify keywords joined with space
func TestFTSStage_Execute_KeywordsJoinedWithSpace(t *testing.T) {
	mock := &ftsSearcherSpy{results: []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1"}, Score: 0.5, Source: "fts"},
	}}
	s := stage.NewFTSStage(mock, 30)
	state := pipeline.NewState("fallback", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Keywords: []string{"alpha", "beta", "gamma"}}

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	want := "alpha beta gamma"
	if mock.capturedQuery != want {
		t.Errorf("capturedQuery = %q, want %q", mock.capturedQuery, want)
	}
}

// TestFTSStage_Implements_Stage 验证接口实现 / Verify Stage interface compliance
func TestFTSStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewFTSStage(nil, 0)
}

// TestFTSStage_Execute_EmptyKeywordsUsesOriginalQuery 空关键词回退到原始查询 / Empty keywords falls back to original query
func TestFTSStage_Execute_EmptyKeywordsUsesOriginalQuery(t *testing.T) {
	mock := &ftsSearcherSpy{results: []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1"}, Score: 0.5, Source: "fts"},
	}}
	s := stage.NewFTSStage(mock, 30)
	state := pipeline.NewState("original query", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Plan = &pipeline.QueryPlan{Keywords: []string{}}

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if mock.capturedQuery != "original query" {
		t.Errorf("capturedQuery = %q, want %q", mock.capturedQuery, "original query")
	}
}

// TestFTSStage_Execute_TraceRecorded 验证非跳过时记录 trace / Verify trace recorded on non-skip execution
func TestFTSStage_Execute_TraceRecorded(t *testing.T) {
	mock := &ftsSearcherSpy{results: []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1"}, Score: 0.5, Source: "fts"},
	}}
	s := stage.NewFTSStage(mock, 30)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	found := false
	for _, tr := range got.Traces {
		if tr.Name == "fts" && !tr.Skipped {
			found = true
			if tr.OutputCount != 1 {
				t.Errorf("trace OutputCount = %d, want 1", tr.OutputCount)
			}
			break
		}
	}
	if !found {
		t.Error("expected non-skipped trace for fts stage")
	}
}

// TestFTSStage_Execute_WhitespaceOnlyQuerySkips 纯空白查询应跳过 / Whitespace-only query should skip
func TestFTSStage_Execute_WhitespaceOnlyQuerySkips(t *testing.T) {
	mock := &ftsSearcherSpy{results: []*model.SearchResult{}}
	s := stage.NewFTSStage(mock, 30)
	state := pipeline.NewState("   ", &model.Identity{TeamID: "t", OwnerID: "o"})

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if len(got.Candidates) != 0 {
		t.Errorf("Candidates count = %d, want 0 for whitespace-only query", len(got.Candidates))
	}

	// 验证 searcher 未被调用 / Verify searcher was not called
	if mock.capturedQuery != "" {
		t.Errorf("searcher should not be called for whitespace-only query, but capturedQuery = %q", mock.capturedQuery)
	}
}

// TestFTSStage_Execute_FilteredSearchError 带过滤的搜索错误也应优雅处理 / Filtered search error should be handled gracefully
func TestFTSStage_Execute_FilteredSearchError(t *testing.T) {
	mock := &ftsSearcherSpy{err: errors.New("filtered db error")}
	s := stage.NewFTSStage(mock, 30)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Metadata["filters"] = &model.SearchFilters{Scope: "proj/x"}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() should not return error, got: %v", err)
	}

	if len(got.Candidates) != 0 {
		t.Errorf("Candidates count = %d, want 0 on error", len(got.Candidates))
	}

	if !mock.usedFiltered {
		t.Error("expected SearchTextFiltered to be called")
	}
}
