package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"iclude/internal/llm"
	"iclude/internal/search/strategy"
)

// mockLLM 模拟 LLM 提供者 / Mock LLM provider for testing
type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}

func TestAgent_Select(t *testing.T) {
	tests := []struct {
		name         string
		llmResponse  string
		llmErr       error
		llmNil       bool // use nil LLM provider
		query        string
		wantPipeline string
		wantKeywords []string
		wantPlanNil  bool
	}{
		{
			name:         "LLM selects precision",
			llmResponse:  `{"pipeline":"precision","keywords":["点券","字段"],"entities":["点券"],"semantic_query":"点券存储字段","intent":"keyword"}`,
			query:        "点券在数据库的哪个字段",
			wantPipeline: "precision",
			wantKeywords: []string{"点券", "字段"},
		},
		{
			name:         "LLM selects exploration",
			llmResponse:  `{"pipeline":"exploration","keywords":["进展"],"entities":[],"semantic_query":"项目进展","intent":"temporal"}`,
			query:        "最近的项目进展",
			wantPipeline: "exploration",
		},
		{
			name:         "LLM returns invalid pipeline fallback to rules",
			llmResponse:  `{"pipeline":"invalid_name","keywords":[],"entities":[],"intent":"general"}`,
			query:        "一段普通的查询文本超过五个字",
			wantPipeline: "exploration", // rule classifier fallback
			wantPlanNil:  true,
		},
		{
			name:         "LLM error fallback to rules",
			llmErr:       errors.New("timeout"),
			query:        "最近做了什么",
			wantPipeline: "exploration", // temporal pattern → exploration
			wantPlanNil:  true,
		},
		{
			name:         "nil LLM rules only",
			llmNil:       true,
			query:        "abc",
			wantPipeline: "fast", // short query
			wantPlanNil:  true,
		},
		{
			name:         "malformed JSON fallback",
			llmResponse:  `not json at all`,
			query:        "这个模块依赖什么",
			wantPipeline: "association", // relational pattern
			wantPlanNil:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var provider llm.Provider
			if !tt.llmNil {
				provider = &mockLLM{response: tt.llmResponse, err: tt.llmErr}
			}
			rc := strategy.NewRuleClassifier("exploration")
			agent := strategy.NewAgent(provider, rc, 5*time.Second)

			name, plan, err := agent.Select(context.Background(), tt.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantPipeline {
				t.Errorf("pipeline = %q, want %q", name, tt.wantPipeline)
			}
			if tt.wantPlanNil && plan != nil {
				t.Errorf("expected nil plan, got %+v", plan)
			}
			if !tt.wantPlanNil && plan == nil {
				t.Error("expected non-nil plan")
			}
			if plan != nil && tt.wantKeywords != nil {
				if len(plan.Keywords) != len(tt.wantKeywords) {
					t.Errorf("keywords = %v, want %v", plan.Keywords, tt.wantKeywords)
				}
			}
		})
	}
}

func TestAgent_Select_ContextCancelled(t *testing.T) {
	provider := &mockLLM{response: `{"pipeline":"precision","keywords":[],"entities":[],"intent":"keyword"}`}
	rc := strategy.NewRuleClassifier("exploration")
	agent := strategy.NewAgent(provider, rc, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Agent.Select は LLM 失敗時にルールにフォールバック / Should fallback to rules on cancelled context
	name, plan, err := agent.Select(ctx, "测试查询超过五个字")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// LLM may still succeed with mock (no real network), but context cancellation
	// is handled by the timeout wrapper; with a mock it may or may not trigger.
	// Just verify we get a valid result.
	if name == "" {
		t.Error("expected non-empty pipeline name")
	}
	_ = plan // plan may or may not be nil depending on mock behavior
}

func TestAgent_Select_AllValidPipelines(t *testing.T) {
	validPipelines := []string{"precision", "exploration", "semantic", "association", "fast", "full"}

	for _, p := range validPipelines {
		t.Run(p, func(t *testing.T) {
			resp := `{"pipeline":"` + p + `","keywords":["test"],"entities":[],"semantic_query":"test","intent":"general"}`
			provider := &mockLLM{response: resp}
			rc := strategy.NewRuleClassifier("exploration")
			agent := strategy.NewAgent(provider, rc, 5*time.Second)

			name, plan, err := agent.Select(context.Background(), "测试查询超过五个字")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != p {
				t.Errorf("pipeline = %q, want %q", name, p)
			}
			if plan == nil {
				t.Error("expected non-nil plan for valid pipeline")
			}
		})
	}
}

func TestAgent_Select_PlanFields(t *testing.T) {
	resp := `{"pipeline":"semantic","keywords":["记忆","向量"],"entities":["Qdrant"],"semantic_query":"向量记忆检索系统","intent":"semantic"}`
	provider := &mockLLM{response: resp}
	rc := strategy.NewRuleClassifier("exploration")
	agent := strategy.NewAgent(provider, rc, 5*time.Second)

	name, plan, err := agent.Select(context.Background(), "向量记忆检索")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "semantic" {
		t.Errorf("pipeline = %q, want %q", name, "semantic")
	}
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if plan.OriginalQuery != "向量记忆检索" {
		t.Errorf("OriginalQuery = %q, want %q", plan.OriginalQuery, "向量记忆检索")
	}
	if plan.SemanticQuery != "向量记忆检索系统" {
		t.Errorf("SemanticQuery = %q, want %q", plan.SemanticQuery, "向量记忆检索系统")
	}
	if len(plan.Keywords) != 2 {
		t.Errorf("Keywords len = %d, want 2", len(plan.Keywords))
	}
	if len(plan.Entities) != 1 || plan.Entities[0] != "Qdrant" {
		t.Errorf("Entities = %v, want [Qdrant]", plan.Entities)
	}
	if plan.Intent != "semantic" {
		t.Errorf("Intent = %q, want %q", plan.Intent, "semantic")
	}
}
