package stage_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/llm"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// mockLLMProvider LLM mock / LLM mock for testing
type mockLLMProvider struct {
	response string
	err      error
}

func (m *mockLLMProvider) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}

func TestRerankLLMStage_Name(t *testing.T) {
	s := stage.NewRerankLLMStage(nil, 0, 0, 0, 0)
	if s.Name() != "rerank_llm" {
		t.Errorf("Name() = %q, want %q", s.Name(), "rerank_llm")
	}
}

func TestRerankLLMStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewRerankLLMStage(nil, 0, 0, 0, 0)
}

func TestRerankLLMStage_Execute(t *testing.T) {
	tests := []struct {
		name           string
		llmProvider    llm.Provider
		llmResponse    string
		llmErr         error
		candidates     []*model.SearchResult
		wantTop        string
		wantConfidence pipeline.Confidence
		wantCount      int
		wantSkipped    bool
	}{
		{
			name:        "successful rerank with filtering",
			llmResponse: `[{"index":0,"score":0.95},{"index":1,"score":0.1},{"index":2,"score":0.5}]`,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "relevant"}, Score: 0.5},
				{Memory: &model.Memory{ID: "m2", Content: "noise"}, Score: 0.9},
				{Memory: &model.Memory{ID: "m3", Content: "somewhat"}, Score: 0.3},
			},
			wantTop:        "m1",
			wantConfidence: pipeline.ConfidenceHigh,
			wantCount:      2,
		},
		{
			name:        "low confidence",
			llmResponse: `[{"index":0,"score":0.4}]`,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
			},
			wantTop:        "m1",
			wantConfidence: pipeline.ConfidenceLow,
			wantCount:      1,
		},
		{
			name:        "all filtered produces confidence none",
			llmResponse: `[{"index":0,"score":0.1},{"index":1,"score":0.05}]`,
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.50},
				{Memory: &model.Memory{ID: "m2", Content: "b"}, Score: 0.45}, // 分差 < 20% → 不触发条件跳过 / Gap < 20% → won't skip
			},
			wantConfidence: pipeline.ConfidenceNone,
			wantCount:      0,
		},
		{
			name:   "LLM error returns original candidates",
			llmErr: errors.New("LLM unavailable"),
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
			},
			wantTop:     "m1",
			wantCount:   1,
			wantSkipped: true,
		},
		{
			name: "nil LLM provider skips",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
			},
			wantCount:   1,
			wantSkipped: true,
		},
		{
			name:        "empty candidates skips",
			llmResponse: `[]`,
			candidates:  nil,
			wantCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var provider llm.Provider
			if tt.llmProvider != nil {
				provider = tt.llmProvider
			} else if tt.llmResponse != "" || tt.llmErr != nil {
				provider = &mockLLMProvider{response: tt.llmResponse, err: tt.llmErr}
			}
			// nil provider 时 provider 保持 nil / nil provider stays nil

			s := stage.NewRerankLLMStage(provider, 20, 0.7, 0.3, 0)
			state := pipeline.NewState("test query", &model.Identity{TeamID: "t", OwnerID: "o"})

			// 复制候选列表避免修改原始数据 / Copy candidates to avoid mutating original
			if tt.candidates != nil {
				state.Candidates = make([]*model.SearchResult, len(tt.candidates))
				copy(state.Candidates, tt.candidates)
			}

			got, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("Execute() returned error: %v", err)
			}

			if len(got.Candidates) != tt.wantCount {
				t.Errorf("Candidates count = %d, want %d", len(got.Candidates), tt.wantCount)
			}

			if tt.wantTop != "" && len(got.Candidates) > 0 {
				if got.Candidates[0].Memory.ID != tt.wantTop {
					t.Errorf("top candidate ID = %q, want %q", got.Candidates[0].Memory.ID, tt.wantTop)
				}
			}

			if tt.wantConfidence != "" {
				if got.Confidence != tt.wantConfidence {
					t.Errorf("Confidence = %q, want %q", got.Confidence, tt.wantConfidence)
				}
			}

			if tt.wantSkipped {
				found := false
				for _, tr := range got.Traces {
					if tr.Name == "rerank_llm" && tr.Skipped {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected skipped trace for rerank_llm stage, but not found")
				}
			}
		})
	}
}

// TestRerankLLMStage_Execute_RegexFallback LLM 返回非标准 JSON 时正则回退 / Regex fallback for non-standard JSON
func TestRerankLLMStage_Execute_RegexFallback(t *testing.T) {
	// 非标准 JSON，但正则可解析 / Non-standard JSON but parseable by regex
	provider := &mockLLMProvider{
		response: `Here are the scores: {"index": 0, "score": 0.9}, {"index": 1, "score": 0.6}`,
	}
	s := stage.NewRerankLLMStage(provider, 20, 0.7, 0.3, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "first"}, Score: 0.5},
		{Memory: &model.Memory{ID: "m2", Content: "second"}, Score: 0.8},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 正则应能解析出分数 / Regex should parse scores
	if len(got.Candidates) != 2 {
		t.Errorf("Candidates count = %d, want 2", len(got.Candidates))
	}
	if got.Confidence != pipeline.ConfidenceHigh {
		t.Errorf("Confidence = %q, want %q", got.Confidence, pipeline.ConfidenceHigh)
	}
}

// TestRerankLLMStage_Execute_RemainingAppended top-K 之外的候选追加到末尾 / Non-top-K candidates appended
func TestRerankLLMStage_Execute_RemainingAppended(t *testing.T) {
	provider := &mockLLMProvider{
		response: `[{"index":0,"score":0.9}]`,
	}
	// topK=1, 只对第一个候选做 LLM 评估 / topK=1, only evaluate first candidate
	s := stage.NewRerankLLMStage(provider, 1, 0.7, 0.3, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "top"}, Score: 0.5},
		{Memory: &model.Memory{ID: "m2", Content: "remaining"}, Score: 0.3},
		{Memory: &model.Memory{ID: "m3", Content: "also remaining"}, Score: 0.2},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// m1 经 LLM 评分保留, m2+m3 追加 / m1 kept by LLM, m2+m3 appended
	if len(got.Candidates) != 3 {
		t.Fatalf("Candidates count = %d, want 3", len(got.Candidates))
	}
	if got.Candidates[0].Memory.ID != "m1" {
		t.Errorf("first candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "m1")
	}
	// 确认 remaining 候选在末尾 / Confirm remaining candidates are at the end
	remainIDs := map[string]bool{"m2": false, "m3": false}
	for _, c := range got.Candidates[1:] {
		remainIDs[c.Memory.ID] = true
	}
	for id, found := range remainIDs {
		if !found {
			t.Errorf("remaining candidate %q not found in output", id)
		}
	}
}

// TestRerankLLMStage_Execute_CircuitBreakerTrips 连续失败触发熔断 / Consecutive failures trip circuit breaker
func TestRerankLLMStage_Execute_CircuitBreakerTrips(t *testing.T) {
	failProvider := &mockLLMProvider{err: errors.New("fail")}
	s := stage.NewRerankLLMStage(failProvider, 20, 0.7, 0.3, 0)

	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
	}

	// 连续失败 3 次触发熔断（threshold=3）/ Fail 3 times to trip breaker
	for i := 0; i < 3; i++ {
		s.Execute(context.Background(), state)
	}

	// 第 4 次应被熔断跳过 / 4th call should be skipped by breaker
	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	found := false
	for _, tr := range got.Traces {
		if tr.Name == "rerank_llm" && tr.Note == "circuit breaker open" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected circuit breaker open trace, but not found")
	}
}

// TestRerankLLMStage_Execute_Immutability 验证不修改输入候选 / Verify input candidates not mutated
func TestRerankLLMStage_Execute_Immutability(t *testing.T) {
	provider := &mockLLMProvider{
		response: `[{"index":0,"score":0.9},{"index":1,"score":0.8}]`,
	}
	s := stage.NewRerankLLMStage(provider, 20, 0.7, 0.3, 0)

	original := []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "first"}, Score: 0.5},
		{Memory: &model.Memory{ID: "m2", Content: "second"}, Score: 0.3},
	}
	origScores := []float64{original[0].Score, original[1].Score}

	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = make([]*model.SearchResult, len(original))
	copy(state.Candidates, original)

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 原始对象的 Score 不应被修改 / Original objects' Score should not be modified
	for i, orig := range original {
		if orig.Score != origScores[i] {
			t.Errorf("original[%d].Score was mutated: got %f, want %f", i, orig.Score, origScores[i])
		}
	}
}

// TestRerankLLMStage_Execute_ScoreBlending 验证分数混合逻辑 / Verify score blending logic
func TestRerankLLMStage_Execute_ScoreBlending(t *testing.T) {
	// baseScore=1.0, maxBaseScore=1.0, baseNorm=1.0
	// llmScore=0.8, scoreWeight=0.7
	// final = (1-0.7)*1.0 + 0.7*0.8 = 0.3 + 0.56 = 0.86
	provider := &mockLLMProvider{
		response: `[{"index":0,"score":0.8}]`,
	}
	s := stage.NewRerankLLMStage(provider, 20, 0.7, 0.3, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "only"}, Score: 1.0},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if len(got.Candidates) != 1 {
		t.Fatalf("Candidates count = %d, want 1", len(got.Candidates))
	}

	expectedScore := 0.86
	tolerance := 0.01
	actualScore := got.Candidates[0].Score
	if actualScore < expectedScore-tolerance || actualScore > expectedScore+tolerance {
		t.Errorf("blended score = %f, want ~%f", actualScore, expectedScore)
	}
}

// TestRerankLLMStage_Execute_UnparseableResponse 完全无法解析的响应 / Completely unparseable response
func TestRerankLLMStage_Execute_UnparseableResponse(t *testing.T) {
	provider := &mockLLMProvider{
		response: `I cannot evaluate these memories.`,
	}
	s := stage.NewRerankLLMStage(provider, 20, 0.7, 0.3, 0)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "a"}, Score: 0.5},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// 无法解析应返回原始候选 / Unparseable should return original candidates
	if len(got.Candidates) != 1 {
		t.Errorf("Candidates count = %d, want 1", len(got.Candidates))
	}
	if got.Candidates[0].Memory.ID != "m1" {
		t.Errorf("candidate ID = %q, want %q", got.Candidates[0].Memory.ID, "m1")
	}
}
