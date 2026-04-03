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
	"iclude/internal/store"

	"go.uber.org/zap"
)

// LLM 输出解析方式常量 / LLM output parse method constants
const (
	ParseMethodJSON     = "json"
	ParseMethodExtract  = "extract"
	ParseMethodRetry    = "retry"
	ParseMethodFallback = "fallback"
)

// baseSystemPrompt 反思引擎基础系统提示词 / Reflect engine base system prompt
const baseSystemPrompt = `You are a reflection engine that synthesizes information from memory retrieval results.

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
	retriever    *search.Retriever
	manager      *memory.Manager
	contextStore store.ContextStore // 可为 nil / May be nil
	llmProvider  llm.Provider
	cfg          config.ReflectConfig
}

// NewReflectEngine 创建反思引擎 / Create a new reflect engine
func NewReflectEngine(retriever *search.Retriever, manager *memory.Manager, contextStore store.ContextStore, llmProvider llm.Provider, cfg config.ReflectConfig) *ReflectEngine {
	return &ReflectEngine{
		retriever:    retriever,
		manager:      manager,
		contextStore: contextStore,
		llmProvider:  llmProvider,
		cfg:          cfg,
	}
}

// BuildSystemPrompt 构建系统提示词，可选注入 Context 行为约束 / Build system prompt with optional behavioral constraints
func (e *ReflectEngine) BuildSystemPrompt(ctx context.Context, contextID string) string {
	// 无 ContextID 或无 ContextStore 时返回基础提示词 / Return base prompt when no ContextID or ContextStore
	if contextID == "" || e.contextStore == nil {
		return baseSystemPrompt
	}

	ctxObj, err := e.contextStore.Get(ctx, contextID)
	if err != nil {
		logger.Debug("reflect: failed to load context, using base prompt",
			zap.String("context_id", contextID),
			zap.Error(err),
		)
		return baseSystemPrompt
	}

	var constraints []string
	if ctxObj.Mission != "" {
		constraints = append(constraints, fmt.Sprintf("Mission: %s", ctxObj.Mission))
	}
	if ctxObj.Directives != "" {
		constraints = append(constraints, fmt.Sprintf("Directives:\n%s", ctxObj.Directives))
	}
	if ctxObj.Disposition != "" {
		constraints = append(constraints, fmt.Sprintf("Disposition: %s", ctxObj.Disposition))
	}

	if len(constraints) == 0 {
		return baseSystemPrompt
	}

	return baseSystemPrompt + "\n\nContext behavioral constraints:\n" + strings.Join(constraints, "\n")
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
	var totalTokens int    // LLM token 消耗 / LLM token consumption
	var evidenceTokens int // 检索证据 token 消耗 / Retrieval evidence token consumption
	var lastConclusion string
	currentQuery := req.Question

	// B3#8: 累积前几轮的 reasoning + 证据摘要 / Accumulate prior rounds' reasoning and evidence summaries
	var priorRounds []priorRoundSummary

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

		// B2#5: 动态 Top-K / Adaptive Top-K based on round, budget, and prior evidence quality
		limit := adaptiveTopK(round, maxRounds, totalTokens, tokenBudget, priorRounds)

		// 检索记忆 / Retrieve memories
		retrieveReq := &model.RetrieveRequest{
			Query:   currentQuery,
			TeamID:  req.TeamID,
			OwnerID: req.OwnerID,
			Limit:   limit,
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

		// B3: 过滤极低分证据，减少 token 浪费 / Filter very low-score evidence to save token budget
		if len(results) > 1 {
			results = filterLowQualityEvidence(results, 0.05)
		}

		// B3: 精确追踪证据 token + 最高分 / Track evidence tokens and top score precisely
		roundEvidenceTokens := 0
		roundTopScore := 0.0
		for _, r := range results {
			if r.Score > roundTopScore {
				roundTopScore = r.Score
			}
			if r.Memory != nil {
				roundEvidenceTokens += search.EstimateTokens(r.Memory.Content)
			}
		}
		evidenceTokens += roundEvidenceTokens

		// 收集来源 / Collect source memory IDs
		retrievedIDs := make([]string, 0, len(results))
		for _, r := range results {
			if r.Memory != nil {
				retrievedIDs = append(retrievedIDs, r.Memory.ID)
				resp.Sources = append(resp.Sources, r.Memory.ID)
			}
		}

		// 构建 LLM 请求（含历史轮次摘要）/ Build LLM request with prior round summaries
		memoriesText := formatMemoriesForLLM(results)
		var userContent string
		if len(priorRounds) > 0 {
			historyText := formatPriorRounds(priorRounds)
			userContent = fmt.Sprintf("Question: %s\n\nRound: %d/%d\n\n--- Prior reasoning ---\n%s\n--- Current retrieval (query: %s) ---\n%s",
				req.Question, round, maxRounds, historyText, currentQuery, memoriesText)
		} else {
			userContent = fmt.Sprintf("Question: %s\n\nRound: %d/%d\n\nRetrieved memories:\n%s",
				req.Question, round, maxRounds, memoriesText)
		}

		sysPrompt := e.BuildSystemPrompt(ctx, req.ContextID)
		messages := []llm.ChatMessage{
			{Role: "system", Content: sysPrompt},
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

		// B3: Token 预算管理（LLM + 证据 token 综合计算）/ Token budget tracking (LLM + evidence tokens combined)
		totalTokens += chatResp.TotalTokens
		effectiveBudget := totalTokens + evidenceTokens
		if effectiveBudget > tokenBudget {
			logger.Info("reflect token budget exceeded",
				zap.Int("llm_tokens", totalTokens),
				zap.Int("evidence_tokens", evidenceTokens),
				zap.Int("effective_total", effectiveBudget),
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

			// B3#8: 累积本轮摘要（含证据质量指标）/ Accumulate this round's summary with evidence quality metrics
		priorRounds = append(priorRounds, priorRoundSummary{
			Round:          round,
			Query:          currentQuery,
			Reasoning:      output.Reasoning,
			Evidence:       summarizeEvidence(results, 3), // 保留 top-3 证据摘要 / Keep top-3 evidence summaries
			TopScore:       roundTopScore,
			EvidenceTokens: roundEvidenceTokens,
		})

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
	resp.Metadata.EvidenceTokens = evidenceTokens

	// 去重来源 / Deduplicate sources
	resp.Sources = dedup(resp.Sources)

	// Collect evidence IDs across all rounds for derived_from / 收集所有轮次的证据 ID
	var evidenceIDs []string
	seen := make(map[string]bool)
	for _, round := range resp.Trace {
		for _, id := range round.RetrievedIDs {
			if id != "" && !seen[id] {
				seen[id] = true
				evidenceIDs = append(evidenceIDs, id)
			}
		}
	}

	// 自动保存 / Auto-save conclusion as new memory
	if autoSave && resp.Result != "" && e.manager != nil {
		createReq := &model.CreateMemoryRequest{
			Content:     resp.Result,
			Kind:        "mental_model",
			MemoryClass: "procedural",
			DerivedFrom: evidenceIDs,
			SourceType:  "reflect",
			Scope:       req.Scope,
			TeamID:      req.TeamID,
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

// adaptiveTopK 根据轮次、预算和前轮证据质量动态调整检索数量 / Dynamically adjust retrieval limit
// 综合考虑：轮次阶段、token 预算消耗比、前轮证据质量
func adaptiveTopK(round, maxRounds, usedTokens, tokenBudget int, priorRounds []priorRoundSummary) int {
	// 基础值：第 1 轮宽搜，后续精确 / Base: wide search in round 1, narrow later
	base := 15
	if round > 1 {
		base = 8
	}

	// 预算因子：剩余预算不足 30% 时缩减 / Budget factor: reduce when < 30% budget remains
	if tokenBudget > 0 && usedTokens > 0 {
		remaining := float64(tokenBudget-usedTokens) / float64(tokenBudget)
		if remaining < 0.3 {
			base = base * 2 / 3 // 缩减 1/3
		}
		if remaining < 0.1 {
			base = base / 2 // 缩减一半
		}
	}

	// 证据质量因子：前轮高质量时收窄（已有足够好的线索），低质量时加宽（需要更多候选）
	// Evidence quality factor: narrow if prior rounds had strong evidence, widen if weak
	if round > 1 && len(priorRounds) > 0 {
		lastEvidence := priorRounds[len(priorRounds)-1].TopScore
		if lastEvidence >= 0.8 {
			base = base * 3 / 4 // 高质量证据，收窄 25% / Strong evidence, narrow 25%
		} else if lastEvidence < 0.3 {
			base = base * 5 / 4 // 低质量证据，加宽 25% / Weak evidence, widen 25%
		}
	}

	// 最终轮收窄：最后一轮只取最相关的 / Last round: minimal retrieval
	if round == maxRounds {
		base = 5
	}

	// 下限 3，上限 20 / Clamp [3, 20]
	if base < 3 {
		base = 3
	}
	if base > 20 {
		base = 20
	}
	return base
}

// priorRoundSummary 前轮摘要 / Summary of a prior reflect round
type priorRoundSummary struct {
	Round         int
	Query         string
	Reasoning     string
	Evidence      string
	TopScore      float64 // 本轮最高检索分数，用于证据质量评估 / Top retrieval score for evidence quality assessment
	EvidenceTokens int    // 本轮证据消耗 token 数 / Token count consumed by evidence this round
}

// formatPriorRounds 格式化历史轮次 / Format prior rounds for LLM context
func formatPriorRounds(rounds []priorRoundSummary) string {
	var sb strings.Builder
	for _, r := range rounds {
		fmt.Fprintf(&sb, "Round %d (query: %s):\n  Reasoning: %s\n  Key evidence: %s\n\n",
			r.Round, r.Query, r.Reasoning, r.Evidence)
	}
	return sb.String()
}

// summarizeEvidence 提取 top-N 检索结果的简短摘要 / Extract brief summaries from top-N results
func summarizeEvidence(results []*model.SearchResult, topN int) string {
	if len(results) == 0 {
		return "(none)"
	}
	n := topN
	if n > len(results) {
		n = len(results)
	}
	var parts []string
	for i := 0; i < n; i++ {
		if results[i].Memory == nil {
			continue
		}
		content := results[i].Memory.Content
		runes := []rune(content)
		if len(runes) > 80 {
			content = string(runes[:80]) + "..."
		}
		parts = append(parts, fmt.Sprintf("[%.2f] %s", results[i].Score, content))
	}
	return strings.Join(parts, " | ")
}

// filterLowQualityEvidence 过滤极低分证据，保留至少 1 条 / Filter very low-score evidence, keep at least 1 result
func filterLowQualityEvidence(results []*model.SearchResult, minScore float64) []*model.SearchResult {
	if len(results) <= 1 {
		return results
	}
	filtered := make([]*model.SearchResult, 0, len(results))
	for _, r := range results {
		if r.Score >= minScore {
			filtered = append(filtered, r)
		}
	}
	// 至少保留 1 条 / Always keep at least 1 result
	if len(filtered) == 0 {
		return results[:1]
	}
	return filtered
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
