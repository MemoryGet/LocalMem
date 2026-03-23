package report_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/search"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	suitePreprocess     = "查询预处理 (Query Preprocessor)"
	suitePreprocessIcon = "\U0001F9E0"
	suitePreprocessDesc = "规则式意图分类 + 可选 LLM 增强，为三通道检索提供差异化输入和动态权重"
)

func newPreprocessor(tok tokenizer.Tokenizer, llmProvider llm.Provider) *search.Preprocessor {
	cfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess:   config.PreprocessConfig{Enabled: true, UseLLM: llmProvider != nil, LLMTimeout: 5 * time.Second},
	}
	return search.NewPreprocessor(tok, nil, llmProvider, cfg)
}

func TestPreprocess_IntentClassification(t *testing.T) {
	tc := testreport.NewCase(t, suitePreprocess, suitePreprocessIcon, suitePreprocessDesc,
		"规则式意图分类")
	defer tc.Done()

	tok := tokenizer.NewSimpleTokenizer()
	pp := newPreprocessor(tok, nil)
	tc.Step("创建 Preprocessor (SimpleTokenizer, 无 LLM)")

	tests := []struct {
		query  string
		intent search.QueryIntent
		desc   string
	}{
		{"最近的会议记录", search.IntentTemporal, "时间关键词 → temporal"},
		{"recent meeting notes", search.IntentTemporal, "temporal keyword → temporal"},
		{"和Kubernetes相关的记忆", search.IntentRelational, "关联关键词 → relational"},
		{"K8s error", search.IntentKeyword, "短查询 → keyword"},
		{"什么是向量数据库", search.IntentSemantic, "探索性关键词 → semantic"},
		{"project status update meeting", search.IntentGeneral, "中等长度无特征词 → general"},
	}

	for _, tt := range tests {
		tc.Input("query", tt.query)
		plan, err := pp.Process(context.Background(), tt.query, "")
		require.NoError(t, err)
		assert.Equal(t, tt.intent, plan.Intent)
		tc.Step(tt.desc, fmt.Sprintf("intent=%s", plan.Intent))
	}

	tc.Output("覆盖意图类型", "temporal, relational, keyword, semantic, general")
}

func TestPreprocess_KeywordExtraction(t *testing.T) {
	tc := testreport.NewCase(t, suitePreprocess, suitePreprocessIcon, suitePreprocessDesc,
		"分词关键词提取")
	defer tc.Done()

	tok := tokenizer.NewSimpleTokenizer()
	pp := newPreprocessor(tok, nil)
	tc.Step("创建 Preprocessor (SimpleTokenizer)")

	tc.Input("query", "Kubernetes deployment error")
	plan, err := pp.Process(context.Background(), "Kubernetes deployment error", "")
	require.NoError(t, err)
	tc.Step("执行 Process()")

	assert.Contains(t, plan.Keywords, "Kubernetes")
	assert.Contains(t, plan.Keywords, "deployment")
	assert.Contains(t, plan.Keywords, "error")
	tc.Step("验证: Keywords 包含所有英文词")

	assert.Equal(t, "Kubernetes deployment error", plan.SemanticQuery)
	tc.Step("验证: SemanticQuery 保持原始 query (无 LLM)")

	tc.Output("Keywords", fmt.Sprintf("%v", plan.Keywords))
	tc.Output("SemanticQuery", plan.SemanticQuery)
}

func TestPreprocess_DynamicWeights(t *testing.T) {
	tc := testreport.NewCase(t, suitePreprocess, suitePreprocessIcon, suitePreprocessDesc,
		"意图驱动动态权重")
	defer tc.Done()

	tok := tokenizer.NewSimpleTokenizer()
	pp := newPreprocessor(tok, nil)
	tc.Step("创建 Preprocessor (base weights: FTS=1.0, Qdrant=1.0, Graph=0.8)")

	tests := []struct {
		query      string
		intent     string
		expectFTS  float64
		expectQdnt float64
	}{
		{"K8s error", "keyword", 1.5, 0.6},
		{"什么是向量数据库", "semantic", 0.6, 1.5},
		{"最近的会议记录", "temporal", 1.3, 0.8},
		{"和Kubernetes相关的记忆", "relational", 0.4, 0.7},
	}

	for _, tt := range tests {
		tc.Input("query", tt.query)
		plan, err := pp.Process(context.Background(), tt.query, "")
		require.NoError(t, err)
		assert.InDelta(t, tt.expectFTS, plan.Weights.FTS, 0.01)
		assert.InDelta(t, tt.expectQdnt, plan.Weights.Qdrant, 0.01)
		tc.Step(fmt.Sprintf("%s → FTS=%.1f Qdrant=%.1f Graph=%.1f",
			tt.intent, plan.Weights.FTS, plan.Weights.Qdrant, plan.Weights.Graph))
	}

	tc.Output("权重公式", "最终权重 = base_weight × intent_multiplier")
}

// preprocessMockLLM 测试用 LLM 模拟 / Mock LLM for preprocess tests
type preprocessMockLLM struct {
	response string
	err      error
}

func (m *preprocessMockLLM) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}

func TestPreprocess_LLMEnhance(t *testing.T) {
	tc := testreport.NewCase(t, suitePreprocess, suitePreprocessIcon, suitePreprocessDesc,
		"LLM 增强模式")
	defer tc.Done()

	tok := tokenizer.NewSimpleTokenizer()
	mockLLM := &preprocessMockLLM{
		response: `{"rewritten_query": "Kubernetes pod deployment failure troubleshooting", "intent": "semantic", "keywords": ["pod", "failure"]}`,
	}
	pp := newPreprocessor(tok, mockLLM)
	tc.Step("创建 Preprocessor (MockLLM, use_llm=true)")

	tc.Input("query", "K8s deploy broken")
	tc.Input("LLM response", mockLLM.response)

	plan, err := pp.Process(context.Background(), "K8s deploy broken", "")
	require.NoError(t, err)
	tc.Step("执行 Process()")

	assert.Equal(t, "Kubernetes pod deployment failure troubleshooting", plan.SemanticQuery)
	tc.Step("验证: SemanticQuery 被 LLM 改写")

	assert.Equal(t, search.IntentSemantic, plan.Intent)
	tc.Step("验证: Intent 被 LLM 覆盖为 semantic")

	assert.Contains(t, plan.Keywords, "pod")
	assert.Contains(t, plan.Keywords, "failure")
	tc.Step("验证: LLM 补充关键词已合并到 Keywords")

	tc.Output("SemanticQuery", plan.SemanticQuery)
	tc.Output("Intent", string(plan.Intent))
	tc.Output("Keywords", fmt.Sprintf("%v", plan.Keywords))
}

func TestPreprocess_LLMFallback(t *testing.T) {
	tc := testreport.NewCase(t, suitePreprocess, suitePreprocessIcon, suitePreprocessDesc,
		"LLM 失败静默降级")
	defer tc.Done()

	tok := tokenizer.NewSimpleTokenizer()
	mockLLM := &preprocessMockLLM{err: fmt.Errorf("connection refused")}
	pp := newPreprocessor(tok, mockLLM)
	tc.Step("创建 Preprocessor (MockLLM 返回错误)")

	tc.Input("query", "K8s deploy broken")
	tc.Input("LLM error", "connection refused")

	plan, err := pp.Process(context.Background(), "K8s deploy broken", "")
	require.NoError(t, err)
	tc.Step("执行 Process() — LLM 调用失败")

	assert.Equal(t, "K8s deploy broken", plan.SemanticQuery)
	tc.Step("验证: SemanticQuery 保持原始 query (未被覆盖)")

	assert.Equal(t, search.IntentKeyword, plan.Intent)
	tc.Step("验证: Intent 使用规则式结果 (keyword)")

	tc.Output("降级行为", "LLM 失败 → 静默 warn 日志 → 保留规则式结果")
	tc.Output("SemanticQuery", plan.SemanticQuery)
	tc.Output("Intent", string(plan.Intent))
}

func TestPreprocess_EmptyQuery(t *testing.T) {
	tc := testreport.NewCase(t, suitePreprocess, suitePreprocessIcon, suitePreprocessDesc,
		"空查询处理")
	defer tc.Done()

	tok := tokenizer.NewSimpleTokenizer()
	pp := newPreprocessor(tok, nil)
	tc.Step("创建 Preprocessor")

	tc.Input("query", "(空字符串)")

	plan, err := pp.Process(context.Background(), "", "")
	require.NoError(t, err)
	tc.Step("执行 Process(\"\")")

	assert.Equal(t, search.IntentGeneral, plan.Intent)
	assert.Empty(t, plan.Keywords)
	tc.Step("验证: Intent=general, Keywords=空")

	tc.Output("Intent", string(plan.Intent))
	tc.Output("Weights", fmt.Sprintf("FTS=%.1f Qdrant=%.1f Graph=%.1f",
		plan.Weights.FTS, plan.Weights.Qdrant, plan.Weights.Graph))
}
