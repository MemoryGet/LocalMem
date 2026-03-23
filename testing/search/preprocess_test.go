package search_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/search"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreprocessor_ClassifyIntent(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	baseCfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess:   config.PreprocessConfig{Enabled: true},
	}
	pp := search.NewPreprocessor(tok, nil, nil, baseCfg)

	tests := []struct {
		name   string
		query  string
		intent search.QueryIntent
	}{
		{"temporal_chinese", "最近的会议记录", search.IntentTemporal},
		{"temporal_english", "recent meeting notes", search.IntentTemporal},
		{"relational_chinese", "和Kubernetes相关的记忆", search.IntentRelational},
		{"relational_english", "related to deployment pipeline", search.IntentRelational},
		{"keyword_short", "K8s error", search.IntentKeyword},
		{"semantic_long", "how does the authentication system handle token refresh when the session expires and the user needs to re-login automatically", search.IntentSemantic},
		{"semantic_exploratory", "什么是向量数据库", search.IntentSemantic},
		{"general_midlength", "project status update meeting", search.IntentGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := pp.Process(context.Background(), tt.query, "")
			require.NoError(t, err)
			assert.Equal(t, tt.intent, plan.Intent)
		})
	}
}

func TestPreprocessor_Keywords(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess:   config.PreprocessConfig{Enabled: true},
	}
	pp := search.NewPreprocessor(tok, nil, nil, cfg)

	plan, err := pp.Process(context.Background(), "Kubernetes deployment error", "")
	require.NoError(t, err)
	assert.Contains(t, plan.Keywords, "Kubernetes")
	assert.Contains(t, plan.Keywords, "deployment")
	assert.Contains(t, plan.Keywords, "error")
	assert.NotEmpty(t, plan.SemanticQuery)
}

func TestPreprocessor_Weights(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess:   config.PreprocessConfig{Enabled: true},
	}
	pp := search.NewPreprocessor(tok, nil, nil, cfg)

	tests := []struct {
		name         string
		query        string
		expectFTS    float64
		expectQdrant float64
	}{
		{"keyword_boosts_fts", "K8s error", 1.5, 0.6},
		{"semantic_boosts_qdrant", "how does the authentication system handle token refresh when the session expires and the user needs to re-login automatically", 0.6, 1.5},
		{"general_unchanged", "project status update meeting", 1.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := pp.Process(context.Background(), tt.query, "")
			require.NoError(t, err)
			assert.InDelta(t, tt.expectFTS, plan.Weights.FTS, 0.01)
			assert.InDelta(t, tt.expectQdrant, plan.Weights.Qdrant, 0.01)
		})
	}
}

func TestPreprocessor_EmptyQuery(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess:   config.PreprocessConfig{Enabled: true},
	}
	pp := search.NewPreprocessor(tok, nil, nil, cfg)

	plan, err := pp.Process(context.Background(), "", "")
	require.NoError(t, err)
	assert.Equal(t, search.IntentGeneral, plan.Intent)
	assert.Empty(t, plan.Keywords)
}

// mockLLMProvider 测试用 LLM 模拟 / Mock LLM provider for testing
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

func TestPreprocessor_LLMEnhance(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	mockLLM := &mockLLMProvider{
		response: `{"rewritten_query": "Kubernetes pod deployment failure troubleshooting", "intent": "semantic", "keywords": ["pod", "failure"]}`,
	}
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess: config.PreprocessConfig{
			Enabled:    true,
			UseLLM:     true,
			LLMTimeout: 5 * time.Second,
		},
	}
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg)

	plan, err := pp.Process(context.Background(), "K8s deploy broken", "")
	require.NoError(t, err)
	assert.Equal(t, "Kubernetes pod deployment failure troubleshooting", plan.SemanticQuery)
	assert.Equal(t, search.IntentSemantic, plan.Intent)
	assert.Contains(t, plan.Keywords, "pod")
	assert.Contains(t, plan.Keywords, "failure")
}

func TestPreprocessor_LLMFallback(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	mockLLM := &mockLLMProvider{
		err: fmt.Errorf("connection refused"),
	}
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess: config.PreprocessConfig{
			Enabled:    true,
			UseLLM:     true,
			LLMTimeout: 5 * time.Second,
		},
	}
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg)

	plan, err := pp.Process(context.Background(), "K8s deploy broken", "")
	require.NoError(t, err)
	// LLM 失败，应回退到规则式结果 / LLM fails, should fall back to rule-based result
	assert.Equal(t, "K8s deploy broken", plan.SemanticQuery)
	assert.Equal(t, search.IntentKeyword, plan.Intent)
}

func TestPreprocessor_LLMBadJSON(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	mockLLM := &mockLLMProvider{
		response: `not valid json at all`,
	}
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess: config.PreprocessConfig{
			Enabled:    true,
			UseLLM:     true,
			LLMTimeout: 5 * time.Second,
		},
	}
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg)

	plan, err := pp.Process(context.Background(), "K8s deploy broken", "")
	require.NoError(t, err)
	// 解析失败，应保留规则式结果 / Parse fails, should keep rule-based result
	assert.Equal(t, "K8s deploy broken", plan.SemanticQuery)
}
