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

// seqLLMProvider 顺序调用 LLM 模拟 / Sequential mock LLM returning responses in order
type seqLLMProvider struct {
	responses []string
	errors    []error
	idx       int
}

func (m *seqLLMProvider) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.idx >= len(m.responses) {
		return &llm.ChatResponse{Content: ""}, nil
	}
	resp := m.responses[m.idx]
	var err error
	if m.errors != nil && m.idx < len(m.errors) {
		err = m.errors[m.idx]
	}
	m.idx++
	if err != nil {
		return nil, err
	}
	return &llm.ChatResponse{Content: resp}, nil
}

func TestPreprocessor_HyDE_DisabledByConfig(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	// Mock returns semantic JSON — intent would allow HyDE, but config disables it
	mockLLM := &mockLLMProvider{
		response: `{"rewritten_query": "authentication token refresh flow", "intent": "semantic", "keywords": ["auth", "token"]}`,
	}
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess: config.PreprocessConfig{
			Enabled:     true,
			UseLLM:      true,
			HyDEEnabled: false,
			LLMTimeout:  5 * time.Second,
		},
	}
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg)

	query := "how does the authentication system handle token refresh when the session expires"
	plan, err := pp.Process(context.Background(), query, "")
	require.NoError(t, err)
	assert.Equal(t, search.IntentSemantic, plan.Intent)
	assert.Empty(t, plan.HyDEDoc, "HyDE doc must be empty when hyde_enabled=false")
}

func TestPreprocessor_HyDE_SkippedForKeywordIntent(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	// Mock returns keyword intent — gate should block HyDE even when hyde_enabled=true
	mockLLM := &mockLLMProvider{
		response: `{"rewritten_query": "K8s deploy error", "intent": "keyword", "keywords": ["kubernetes", "deploy"]}`,
	}
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess: config.PreprocessConfig{
			Enabled:     true,
			UseLLM:      true,
			HyDEEnabled: true,
			LLMTimeout:  5 * time.Second,
		},
	}
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg)

	plan, err := pp.Process(context.Background(), "K8s error", "")
	require.NoError(t, err)
	assert.Equal(t, search.IntentKeyword, plan.Intent)
	assert.Empty(t, plan.HyDEDoc, "HyDE doc must be empty for keyword intent")
}

func TestPreprocessor_HyDE_RunsForSemanticIntent(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	// First call: LLM enhance → second call: HyDE generation
	mockLLM := &seqLLMProvider{
		responses: []string{
			`{"rewritten_query": "authentication token refresh flow", "intent": "semantic", "keywords": ["auth"]}`,
			"这是一段关于认证令牌刷新机制的假设性文档。",
		},
	}
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess: config.PreprocessConfig{
			Enabled:     true,
			UseLLM:      true,
			HyDEEnabled: true,
			LLMTimeout:  5 * time.Second,
		},
	}
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg)

	query := "how does the authentication system handle token refresh when the session expires"
	plan, err := pp.Process(context.Background(), query, "")
	require.NoError(t, err)
	assert.Equal(t, search.IntentSemantic, plan.Intent)
	assert.Equal(t, "这是一段关于认证令牌刷新机制的假设性文档。", plan.HyDEDoc)
}

func TestPreprocessor_HyDE_LLMFailureFallback(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	// First call (enhance): success — second call (HyDE): fails
	mockLLM := &seqLLMProvider{
		responses: []string{
			`{"rewritten_query": "authentication token refresh flow", "intent": "semantic", "keywords": ["auth"]}`,
			"",
		},
		errors: []error{
			nil,
			fmt.Errorf("LLM unavailable"),
		},
	}
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess: config.PreprocessConfig{
			Enabled:     true,
			UseLLM:      true,
			HyDEEnabled: true,
			LLMTimeout:  5 * time.Second,
		},
	}
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg)

	query := "how does the authentication system handle token refresh when the session expires"
	plan, err := pp.Process(context.Background(), query, "")
	require.NoError(t, err)
	assert.Equal(t, search.IntentSemantic, plan.Intent)
	assert.Empty(t, plan.HyDEDoc, "HyDE doc must be empty when HyDE LLM call fails")
}
