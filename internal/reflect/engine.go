// Package reflect 反思推理引擎 / Reflect reasoning engine for multi-round memory retrieval and synthesis
package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"

	"go.uber.org/zap"
)

// LLM 输出解析方式常量 / LLM output parse method constants
const (
	ParseMethodJSON     = "json"
	ParseMethodExtract  = "extract"
	ParseMethodRetry    = "retry"
	ParseMethodFallback = "fallback"
)

// systemPrompt 反思引擎系统提示词 / Reflect engine system prompt
const systemPrompt = `You are a reflection engine that synthesizes information from memory retrieval results.

You MUST respond with valid JSON in the following format:
{
  "action": "need_more" or "conclusion",
  "reasoning": "your reasoning about the retrieved memories",
  "next_query": "follow-up search query (required when action is need_more)",
  "conclusion": "final synthesized answer (required when action is conclusion)"
}

Rules:
- If the retrieved memories provide enough information to answer the question, set action to "conclusion" and provide a comprehensive conclusion.
- If you need more information, set action to "need_more" and provide a next_query to search for additional memories.
- Always include reasoning to explain your thought process.
- Do NOT include any text outside the JSON object.`

// reflectLLMOutput LLM反思输出结构 / Internal struct for parsing LLM reflect output
type reflectLLMOutput struct {
	Action     string `json:"action"`
	NextQuery  string `json:"next_query"`
	Conclusion string `json:"conclusion"`
	Reasoning  string `json:"reasoning"`
}

// validate 校验LLM输出合法性 / Validate LLM output fields
func (o *reflectLLMOutput) validate() error {
	if o.Action != "need_more" && o.Action != "conclusion" {
		return fmt.Errorf("invalid action %q: must be need_more or conclusion", o.Action)
	}
	if o.Action == "need_more" && strings.TrimSpace(o.NextQuery) == "" {
		return fmt.Errorf("next_query is required when action is need_more")
	}
	if o.Action == "conclusion" && strings.TrimSpace(o.Conclusion) == "" {
		return fmt.Errorf("conclusion content is required when action is conclusion")
	}
	return nil
}

// ReflectEngine 反思推理引擎，多轮检索+LLM综合 / Reflect reasoning engine with multi-round retrieval and LLM synthesis
type ReflectEngine struct {
	retriever   *search.Retriever
	manager     *memory.Manager
	llmProvider llm.Provider
	cfg         config.ReflectConfig
}

// NewReflectEngine 创建反思引擎 / Create a new reflect engine
func NewReflectEngine(retriever *search.Retriever, manager *memory.Manager, llmProvider llm.Provider, cfg config.ReflectConfig) *ReflectEngine {
	return &ReflectEngine{
		retriever:   retriever,
		manager:     manager,
		llmProvider: llmProvider,
		cfg:         cfg,
	}
}

// Reflect 执行多轮反思推理 / Execute multi-round reflect reasoning
func (e *ReflectEngine) Reflect(ctx context.Context, req *model.ReflectRequest) (*model.ReflectResponse, error) {
	if req == nil || strings.TrimSpace(req.Question) == "" {
		return nil, model.ErrReflectInvalidRequest
	}

	// 默认值处理 / Apply defaults from config
	maxRounds := req.MaxRounds
	if maxRounds <= 0 {
		maxRounds = e.cfg.MaxRounds
	}

	tokenBudget := req.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = e.cfg.TokenBudget
	}

	autoSave := e.cfg.AutoSave
	if req.AutoSave != nil {
		autoSave = *req.AutoSave
	}

	// 总超时 = 轮次数 × 单轮超时 / Total timeout = maxRounds × roundTimeout
	totalTimeout := time.Duration(maxRounds) * e.cfg.RoundTimeout
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	resp := &model.ReflectResponse{
		Trace:   make([]model.ReflectRound, 0, maxRounds),
		Sources: make([]string, 0),
	}

	seenQueries := make(map[string]bool)
	var totalTokens int
	var lastConclusion string
	currentQuery := req.Question

	for round := 1; round <= maxRounds; round++ {
		// 检查上下文取消 / Check context cancellation
		if err := ctx.Err(); err != nil {
			resp.Metadata.Timeout = true
			if lastConclusion != "" {
				resp.Result = lastConclusion
				break
			}
			return nil, fmt.Errorf("round %d: %w", round, model.ErrReflectTimeout)
		}

		// 查询去重 / Deduplicate queries
		if seenQueries[currentQuery] && round > 1 {
			resp.Metadata.QueryDeduped = true
			logger.Info("reflect query deduped, ending loop",
				zap.Int("round", round),
				zap.String("query", currentQuery),
			)
			break
		}
		seenQueries[currentQuery] = true

		// 检索记忆 / Retrieve memories
		retrieveReq := &model.RetrieveRequest{
			Query:  currentQuery,
			TeamID: req.TeamID,
			Limit:  10,
		}
		if req.Scope != "" {
			retrieveReq.Filters = &model.SearchFilters{Scope: req.Scope}
		}

		results, err := e.retriever.Retrieve(ctx, retrieveReq)
		if err != nil {
			logger.Warn("reflect retrieval failed",
				zap.Int("round", round),
				zap.Error(err),
			)
			if round == 1 {
				return nil, fmt.Errorf("retrieval failed: %w", model.ErrReflectNoMemories)
			}
			break
		}

		if len(results) == 0 && round == 1 {
			return nil, model.ErrReflectNoMemories
		}

		// 收集来源 / Collect source memory IDs
		retrievedIDs := make([]string, 0, len(results))
		for _, r := range results {
			if r.Memory != nil {
				retrievedIDs = append(retrievedIDs, r.Memory.ID)
				resp.Sources = append(resp.Sources, r.Memory.ID)
			}
		}

		// 构建 LLM 请求 / Build LLM request
		memoriesText := formatMemoriesForLLM(results)
		userContent := fmt.Sprintf("Question: %s\n\nRound: %d/%d\n\nRetrieved memories:\n%s",
			req.Question, round, maxRounds, memoriesText)

		messages := []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		}

		temp := 0.1
		chatReq := &llm.ChatRequest{
			Messages:       messages,
			ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
			Temperature:    &temp,
		}

		// 单次 LLM 调用独立超时（防止单个请求 hang 住整轮）/ Per-call timeout prevents single LLM hang
		llmCtx, llmCancel := context.WithTimeout(ctx, e.cfg.RoundTimeout)
		chatResp, err := e.llmProvider.Chat(llmCtx, chatReq)
		llmCancel()
		if err != nil {
			logger.Warn("reflect LLM call failed",
				zap.Int("round", round),
				zap.Error(err),
			)
			if round == 1 && lastConclusion == "" {
				return nil, fmt.Errorf("llm call failed in round 1: %w", model.ErrReflectLLMFailed)
			}
			break
		}

		// Token 预算管理 / Token budget tracking
		totalTokens += chatResp.TotalTokens
		if totalTokens > tokenBudget {
			logger.Info("reflect token budget exceeded",
				zap.Int("total_tokens", totalTokens),
				zap.Int("budget", tokenBudget),
			)
			roundTrace := model.ReflectRound{
				Round:        round,
				Query:        currentQuery,
				RetrievedIDs: retrievedIDs,
				Reasoning:    "token budget exceeded",
				NeedMore:     false,
				TokensUsed:   chatResp.TotalTokens,
			}
			resp.Trace = append(resp.Trace, roundTrace)
			if lastConclusion != "" {
				resp.Result = lastConclusion
			} else {
				resp.Result = chatResp.Content
			}
			resp.Metadata.Timeout = false
			break
		}

		// 解析 LLM 输出 / Parse LLM output
		output, parseMethod := e.parseOutput(ctx, chatResp.Content, messages)

		if parseMethod != ParseMethodJSON {
			resp.Metadata.ParseFallbacks++
		}

		roundTrace := model.ReflectRound{
			Round:        round,
			Query:        currentQuery,
			RetrievedIDs: retrievedIDs,
			Reasoning:    output.Reasoning,
			NeedMore:     output.Action == "need_more",
			ParseMethod:  parseMethod,
			TokensUsed:   chatResp.TotalTokens,
		}
		resp.Trace = append(resp.Trace, roundTrace)

		logger.Info("reflect round completed",
			zap.Int("round", round),
			zap.String("action", output.Action),
			zap.String("parse_method", parseMethod),
			zap.Int("tokens_used", chatResp.TotalTokens),
		)

		if output.Action == "conclusion" {
			lastConclusion = output.Conclusion
			resp.Result = output.Conclusion
			break
		}

		// 继续下一轮 / Proceed to next round
		currentQuery = output.NextQuery
		lastConclusion = output.Conclusion // 可能为空 / may be empty
	}

	// 如果循环结束但没有结论 / If loop ended without conclusion
	if resp.Result == "" && lastConclusion != "" {
		resp.Result = lastConclusion
	}

	resp.Metadata.RoundsUsed = len(resp.Trace)
	resp.Metadata.TotalTokens = totalTokens

	// 去重来源 / Deduplicate sources
	resp.Sources = dedup(resp.Sources)

	// 自动保存 / Auto-save conclusion as new memory
	if autoSave && resp.Result != "" && e.manager != nil {
		createReq := &model.CreateMemoryRequest{
			Content:    resp.Result,
			Kind:       "mental_model",
			SourceType: "reflect",
			Scope:      req.Scope,
			TeamID:     req.TeamID,
			Metadata: map[string]any{
				"question":     req.Question,
				"rounds_used":  resp.Metadata.RoundsUsed,
				"total_tokens": resp.Metadata.TotalTokens,
			},
		}
		newMem, err := e.manager.Create(ctx, createReq)
		if err != nil {
			logger.Warn("reflect auto-save failed",
				zap.Error(err),
			)
		} else if newMem != nil {
			resp.NewMemoryID = newMem.ID
			logger.Info("reflect conclusion auto-saved",
				zap.String("memory_id", newMem.ID),
			)
		}
	}

	return resp, nil
}

// parseOutput 解析LLM输出，三级fallback / Parse LLM output with 3-level fallback
func (e *ReflectEngine) parseOutput(ctx context.Context, raw string, prevMessages []llm.ChatMessage) (*reflectLLMOutput, string) {
	// L1: 直接 JSON 解析 / Direct JSON unmarshal
	var output reflectLLMOutput
	if err := json.Unmarshal([]byte(raw), &output); err == nil {
		if err := output.validate(); err == nil {
			return &output, ParseMethodJSON
		}
	}

	// L2: 正则提取 JSON 对象（允许一层嵌套）/ Regex extract JSON object (allows one level of nesting)
	re := regexp.MustCompile(`\{(?:[^{}]|\{[^{}]*\})*"action"(?:[^{}]|\{[^{}]*\})*\}`)
	if match := re.FindString(raw); match != "" {
		var extracted reflectLLMOutput
		if err := json.Unmarshal([]byte(match), &extracted); err == nil {
			if err := extracted.validate(); err == nil {
				return &extracted, ParseMethodExtract
			}
		}
	}

	// L3: 重试 LLM / Retry with correction message (copy slice to avoid mutation)
	retryMessages := make([]llm.ChatMessage, len(prevMessages), len(prevMessages)+2)
	copy(retryMessages, prevMessages)
	retryMessages = append(retryMessages,
		llm.ChatMessage{Role: "assistant", Content: raw},
		llm.ChatMessage{Role: "user", Content: "Your previous response was not valid JSON. Please respond with ONLY a valid JSON object containing action, reasoning, conclusion, and next_query fields."},
	)

	temp := 0.1
	retryReq := &llm.ChatRequest{
		Messages:       retryMessages,
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	retryResp, err := e.llmProvider.Chat(ctx, retryReq)
	if err == nil {
		var retryOutput reflectLLMOutput
		if err := json.Unmarshal([]byte(retryResp.Content), &retryOutput); err == nil {
			if err := retryOutput.validate(); err == nil {
				return &retryOutput, ParseMethodRetry
			}
		}
	}

	// L4: 降级为原始结论 / Fallback: treat raw content as conclusion
	logger.Warn("reflect parse fallback to raw conclusion",
		zap.String("raw_content", raw),
	)
	return &reflectLLMOutput{
		Action:     "conclusion",
		Conclusion: raw,
		Reasoning:  "parse fallback: could not parse LLM output as structured JSON",
	}, ParseMethodFallback
}

// formatMemoriesForLLM 格式化检索结果为LLM可读文本 / Format search results as numbered text for LLM consumption
func formatMemoriesForLLM(results []*model.SearchResult) string {
	if len(results) == 0 {
		return "(no memories retrieved)"
	}
	var sb strings.Builder
	for i, r := range results {
		if r.Memory == nil {
			continue
		}
		// 优先使用用户标注的事件时间，否则使用记忆创建时间 / Prefer user-annotated event time, fall back to creation time
		effectiveTime := r.Memory.CreatedAt
		if r.Memory.HappenedAt != nil && !r.Memory.HappenedAt.IsZero() {
			effectiveTime = *r.Memory.HappenedAt
		}
		timeStr := effectiveTime.Format("2006-01-02 15:04")
		fmt.Fprintf(&sb, "[%d] (score=%.3f, source=%s, time=%s) %s\n",
			i+1, r.Score, r.Source, timeStr, r.Memory.Content)
	}
	return sb.String()
}

// dedup 字符串切片去重 / Deduplicate a string slice preserving order
func dedup(items []string) []string {
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
