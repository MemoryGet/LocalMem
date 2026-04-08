package strategy

import (
	"context"
	"encoding/json"
	"time"

	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// defaultTimeout Agent 默认超时 / Default agent timeout
const defaultTimeout = 5 * time.Second

// llmTemperature LLM 推理温度 / LLM inference temperature
var llmTemperature = func() *float64 { v := 0.1; return &v }()

// validPipelineSet 有效管线集合 / Valid pipeline names
var validPipelineSet = map[string]bool{
	"precision":   true,
	"exploration": true,
	"semantic":    true,
	"association": true,
	"fast":        true,
	"full":        true,
}

// systemPrompt 策略选择器系统提示 / Strategy selector system prompt
const systemPrompt = `你是检索策略选择器。根据查询分析意图，选择最合适的检索管线，并提取检索关键信息。

管线选项:
- precision: 查找特定实体/字段/配置的精确位置
- exploration: 浏览近期活动、总结、进展
- semantic: 模糊概念、相关性、类似经验
- association: 依赖关系、连接、影响范围
- fast: 简单事实查找
- full: 需要最高精度的重要查询

以JSON格式返回:
{"pipeline":"管线名称","keywords":["关键词"],"entities":["实体"],"semantic_query":"语义改写","intent":"keyword|semantic|temporal|relational|general"}`

// llmResponse LLM 返回的 JSON 结构 / JSON structure returned by LLM
type llmResponse struct {
	Pipeline      string   `json:"pipeline"`
	Keywords      []string `json:"keywords"`
	Entities      []string `json:"entities"`
	SemanticQuery string   `json:"semantic_query"`
	Intent        string   `json:"intent"`
}

// Agent LLM 策略选择器 / LLM-powered strategy selector
// 一次调用同时完成管线选择 + 查询预处理 / Single call combines pipeline selection + preprocessing
type Agent struct {
	llm            llm.Provider
	ruleClassifier *RuleClassifier
	timeout        time.Duration
}

// NewAgent 创建策略 Agent / Create strategy agent
func NewAgent(llmProvider llm.Provider, ruleClassifier *RuleClassifier, timeout time.Duration) *Agent {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Agent{
		llm:            llmProvider,
		ruleClassifier: ruleClassifier,
		timeout:        timeout,
	}
}

// Select 选择管线并返回预处理结果 / Select pipeline and return preprocessing results
// 有 LLM → LLM 调用；无 LLM 或 LLM 失败 → 规则分类器 fallback
func (a *Agent) Select(ctx context.Context, query string) (string, *pipeline.QueryPlan, error) {
	// 无 LLM → 规则分类器 / No LLM → rule classifier
	if a.llm == nil {
		return a.fallbackToRules(query), nil, nil
	}

	// LLM 调用 / Call LLM
	resp, err := a.callLLM(ctx, query)
	if err != nil {
		logger.Warn("strategy agent LLM call failed, falling back to rules",
			zap.String("query", query), zap.Error(err))
		return a.fallbackToRules(query), nil, nil
	}

	// 解析 JSON / Parse JSON response
	var result llmResponse
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		logger.Warn("strategy agent failed to parse LLM response, falling back to rules",
			zap.String("query", query), zap.String("response", resp), zap.Error(err))
		return a.fallbackToRules(query), nil, nil
	}

	// 验证管线名称 / Validate pipeline name
	if !validPipelineSet[result.Pipeline] {
		logger.Warn("strategy agent LLM returned invalid pipeline, falling back to rules",
			zap.String("query", query), zap.String("pipeline", result.Pipeline))
		return a.fallbackToRules(query), nil, nil
	}

	// 构建 QueryPlan / Build QueryPlan from response
	plan := &pipeline.QueryPlan{
		OriginalQuery: query,
		SemanticQuery: result.SemanticQuery,
		Keywords:      result.Keywords,
		Entities:      result.Entities,
		Intent:        result.Intent,
	}

	return result.Pipeline, plan, nil
}

// callLLM 调用 LLM 并返回内容 / Call LLM with timeout and return content
func (a *Agent) callLLM(ctx context.Context, query string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: query},
		},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    llmTemperature,
	}

	resp, err := a.llm.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// fallbackToRules 使用规则分类器选择管线 / Use rule classifier as fallback
func (a *Agent) fallbackToRules(query string) string {
	return a.ruleClassifier.Select(query, "")
}
