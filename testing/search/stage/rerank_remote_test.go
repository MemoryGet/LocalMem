package stage_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func TestRemoteRerankStage_Name(t *testing.T) {
	s := stage.NewRemoteRerankStage(stage.RemoteRerankConfig{BaseURL: "http://localhost"})
	if s.Name() != "rerank_remote" {
		t.Errorf("Name() = %q, want %q", s.Name(), "rerank_remote")
	}
}

func TestRemoteRerankStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewRemoteRerankStage(stage.RemoteRerankConfig{BaseURL: "http://localhost"})
}

func TestRemoteRerankStage_Execute_EmptyBaseURL(t *testing.T) {
	s := stage.NewRemoteRerankStage(stage.RemoteRerankConfig{BaseURL: ""})
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "world"}, Score: 0.8},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	found := false
	for _, tr := range got.Traces {
		if tr.Name == "rerank_remote" && tr.Skipped {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected skipped trace for empty base_url")
	}
}

func TestRemoteRerankStage_Execute_InsufficientCandidates(t *testing.T) {
	s := stage.NewRemoteRerankStage(stage.RemoteRerankConfig{BaseURL: "http://localhost"})
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 1 {
		t.Errorf("Candidates count = %d, want 1", len(got.Candidates))
	}
}

func TestRemoteRerankStage_Execute_EmptyQuery(t *testing.T) {
	s := stage.NewRemoteRerankStage(stage.RemoteRerankConfig{BaseURL: "http://localhost"})
	state := pipeline.NewState("", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "world"}, Score: 0.8},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Errorf("Candidates count = %d, want 2", len(got.Candidates))
	}
}

func TestRemoteRerankStage_Execute_SuccessfulRerank(t *testing.T) {
	// 模拟远程 rerank API / Mock remote rerank API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"results": []map[string]interface{}{
				{"index": 1, "relevance_score": 0.95}, // b should come first
				{"index": 0, "relevance_score": 0.80}, // a second
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	s := stage.NewRemoteRerankStageWithClient(stage.RemoteRerankConfig{
		BaseURL:     server.URL,
		TopK:        10,
		ScoreWeight: 0.7,
	}, server.Client())

	state := pipeline.NewState("hello query", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello world"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "hello there"}, Score: 0.8},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}

	// b had higher relevance_score so should be reranked first
	if got.Candidates[0].Memory.ID != "b" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "b")
	}
}

func TestRemoteRerankStage_Execute_APIErrorFallback(t *testing.T) {
	// 模拟 API 错误 / Mock API error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	s := stage.NewRemoteRerankStageWithClient(stage.RemoteRerankConfig{
		BaseURL: server.URL,
	}, server.Client())

	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "world"}, Score: 0.8},
	}
	origCount := len(state.Candidates)

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() should not return error on API failure, got: %v", err)
	}
	if len(got.Candidates) != origCount {
		t.Errorf("Candidates count = %d, want %d (should fall back to original)", len(got.Candidates), origCount)
	}
}

func TestRemoteRerankStage_Execute_DataFormatResponse(t *testing.T) {
	// 模拟使用 data 格式的响应 / Mock response using data format
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"index": 1, "score": 0.95},
				{"index": 0, "score": 0.80},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	s := stage.NewRemoteRerankStageWithClient(stage.RemoteRerankConfig{
		BaseURL: server.URL,
	}, server.Client())

	state := pipeline.NewState("test query", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "first"}, Score: 0.9},
		{Memory: &model.Memory{ID: "b", Content: "second"}, Score: 0.8},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("Candidates count = %d, want 2", len(got.Candidates))
	}
	if got.Candidates[0].Memory.ID != "b" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "b")
	}
}
