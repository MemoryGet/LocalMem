// Package stage 检索管线阶段实现 / Pipeline stage implementations
package stage

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// defaultLLMRerankTopK LLM 精排默认 top-K / Default top-K for LLM reranking
const defaultLLMRerankTopK = 20

// defaultLLMScoreWeight LLM 分数默认权重 / Default LLM score weight
const defaultLLMScoreWeight = 0.7

// defaultLLMMinRelevance LLM 最低相关性阈值 / Default minimum relevance threshold
const defaultLLMMinRelevance = 0.3

// defaultLLMRerankTimeout LLM 精排默认超时 / Default timeout for LLM reranking
const defaultLLMRerankTimeout = 5 * time.Second

// llmRerankTemperature LLM 精排低温度以保证一致性 / Low temperature for consistent scoring
var llmRerankTemperature = 0.1

// confidenceHighThreshold 高置信度阈值 / High confidence threshold
const confidenceHighThreshold = 0.6

// confidenceLowThreshold 低置信度阈值 / Low confidence threshold
const confidenceLowThreshold = 0.3

// confidenceSkipGapRatio 跳过 LLM rerank 的分数差阈值 / Score gap ratio threshold to skip LLM rerank
// top1 与 top2 分差超过此比例时认为排序已确定，无需 LLM / When gap between top1 and top2 exceeds this ratio, ranking is confident
const confidenceSkipGapRatio = 0.2

// scoreRegex 正则回退解析 LLM 分数响应 / Regex fallback for parsing LLM score response
var scoreRegex = regexp.MustCompile(`"index"\s*:\s*(\d+)\s*,\s*"score"\s*:\s*([\d.]+)`)

// RerankLLMStage LLM 精排阶段，对候选记忆进行相关性评分和置信度标记
// LLM reranking stage with relevance scoring and confidence marking
type RerankLLMStage struct {
	llm          llm.Provider
	topK         int
	scoreWeight  float64
	minRelevance float64
	timeout      time.Duration
	breaker      *stageCircuitBreaker
}

// NewRerankLLMStage 创建 LLM 精排阶段 / Create a new LLM reranker stage
// Defaults: topK=20, scoreWeight=0.7, minRelevance=0.3, timeout=5s
func NewRerankLLMStage(provider llm.Provider, topK int, scoreWeight, minRelevance float64, timeout time.Duration) *RerankLLMStage {
	if topK <= 0 {
		topK = defaultLLMRerankTopK
	}
	if scoreWeight <= 0 {
		scoreWeight = defaultLLMScoreWeight
	}
	if scoreWeight > 1 {
		scoreWeight = 1
	}
	if minRelevance <= 0 {
		minRelevance = defaultLLMMinRelevance
	}
	if timeout <= 0 {
		timeout = defaultLLMRerankTimeout
	}
	return &RerankLLMStage{
		llm:          provider,
		topK:         topK,
		scoreWeight:  scoreWeight,
		minRelevance: minRelevance,
		timeout:      timeout,
		breaker:      newStageCircuitBreaker(3, 30*time.Second),
	}
}

// Name 返回阶段名称 / Return stage name
func (s *RerankLLMStage) Name() string {
	return "rerank_llm"
}

// Execute 执行 LLM 精排 / Execute LLM reranking
func (s *RerankLLMStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// nil LLM provider → 跳过 / nil LLM provider → skip
	if s.llm == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "LLM provider not available",
		})
		return state, nil
	}

	// 空候选列表 → 直接返回 / Empty candidates → return directly
	if len(state.Candidates) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  0,
			OutputCount: 0,
			Note:        "no candidates",
		})
		return state, nil
	}

	// 置信度检查：top1 明显领先时跳过 LLM（节省成本）/ Confidence check: skip LLM when top1 clearly leads
	if len(state.Candidates) >= 2 && !s.forceRerank(state) {
		top1 := state.Candidates[0].Score
		top2 := state.Candidates[1].Score
		if top1 > 0 && (top1-top2)/top1 > confidenceSkipGapRatio {
			state.Confidence = pipeline.ConfidenceHigh
			state.AddTrace(pipeline.StageTrace{
				Name:        s.Name(),
				Duration:    time.Since(start),
				InputCount:  inputCount,
				OutputCount: inputCount,
				Skipped:     true,
				Note:        fmt.Sprintf("skipped: top1 (%.4f) clearly leads top2 (%.4f), gap %.0f%%", top1, top2, (top1-top2)/top1*100),
			})
			return state, nil
		}
	}

	// 熔断器检查 / Circuit breaker check
	if !s.breaker.allow() {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Skipped:     true,
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "circuit breaker open",
		})
		return state, nil
	}

	// 取 top-K 候选 / Take top-K candidates
	topK := s.topK
	if topK > len(state.Candidates) {
		topK = len(state.Candidates)
	}
	subset := state.Candidates[:topK]
	remaining := state.Candidates[topK:]

	// 调用 LLM 评分 / Call LLM for scoring
	scores, err := s.callLLM(ctx, state.Query, subset)
	if err != nil {
		s.breaker.recordFailure()
		logger.Warn("rerank_llm: LLM call failed, using original order", zap.Error(err))
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Skipped:     true,
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "LLM error: " + err.Error(),
		})
		return state, nil
	}

	// 解析失败（空分数）→ 返回原始 / Parse failure (empty scores) → return original
	if len(scores) == 0 {
		s.breaker.recordFailure()
		logger.Warn("rerank_llm: failed to parse LLM response")
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Skipped:     true,
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "parse failure: no scores extracted",
		})
		return state, nil
	}

	s.breaker.recordSuccess()

	// 计算最大基础分数用于归一化 / Compute max base score for normalization
	maxBaseScore := 0.0
	for _, res := range subset {
		if res != nil && res.Score > maxBaseScore {
			maxBaseScore = res.Score
		}
	}
	if maxBaseScore <= 0 {
		maxBaseScore = 1
	}

	// 构建评分结果 / Build scored results
	type scoredResult struct {
		result   *model.SearchResult
		llmScore float64
		blended  float64
	}

	var kept []scoredResult
	var topLLMScore float64
	hasTopScore := false

	for _, sc := range scores {
		if sc.Index < 0 || sc.Index >= len(subset) {
			continue
		}
		if sc.Score < s.minRelevance {
			continue
		}

		res := subset[sc.Index]
		baseNorm := res.Score / maxBaseScore
		blended := (1-s.scoreWeight)*baseNorm + s.scoreWeight*sc.Score

		kept = append(kept, scoredResult{
			result:   res,
			llmScore: sc.Score,
			blended:  blended,
		})

		if !hasTopScore || sc.Score > topLLMScore {
			topLLMScore = sc.Score
			hasTopScore = true
		}
	}

	// 设置置信度 / Set confidence
	if len(kept) == 0 {
		state.Confidence = pipeline.ConfidenceNone
	} else if topLLMScore >= confidenceHighThreshold {
		state.Confidence = pipeline.ConfidenceHigh
	} else if topLLMScore >= confidenceLowThreshold {
		state.Confidence = pipeline.ConfidenceLow
	} else {
		state.Confidence = pipeline.ConfidenceNone
	}

	// 按混合分数排序 / Sort by blended score
	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].blended > kept[j].blended
	})

	// 构建输出：创建副本避免修改输入 / Build output: create copies to avoid mutating input
	out := make([]*model.SearchResult, 0, len(kept)+len(remaining))
	for _, item := range kept {
		resCopy := *item.result
		resCopy.Score = item.blended
		out = append(out, &resCopy)
	}

	// 追加未参与 LLM 评估的候选 / Append non-top-K candidates
	out = append(out, remaining...)

	state.Candidates = out

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(out),
	})

	return state, nil
}

// forceRerank 检查是否强制 LLM rerank（full 管线或显式请求）/ Check if LLM rerank is forced
func (s *RerankLLMStage) forceRerank(state *pipeline.PipelineState) bool {
	if v, ok := state.Metadata[pipeline.MetaForceRerank]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// llmScoreItem LLM 返回的单个评分项 / Single score item from LLM response
type llmScoreItem struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

// callLLM 构建提示词并调用 LLM / Build prompt and call LLM
func (s *RerankLLMStage) callLLM(ctx context.Context, query string, candidates []*model.SearchResult) ([]llmScoreItem, error) {
	// 构建候选列表文本 / Build candidate list text
	var sb strings.Builder
	for i, c := range candidates {
		content := ""
		if c != nil && c.Memory != nil {
			content = strings.TrimSpace(c.Memory.Content)
		}
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i, content))
	}

	systemPrompt := "你是相关性评估器。对每条候选记忆评估与查询的相关性。\n" +
		"返回JSON数组: [{\"index\":0,\"score\":0.95},{\"index\":1,\"score\":0.1}]\n" +
		"score范围0.0~1.0，1.0表示完全相关。"

	userPrompt := fmt.Sprintf("查询: \"%s\"\n\n候选记忆:\n%s", query, sb.String())

	// 使用超时 context / Use timeout context
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	temperature := llmRerankTemperature
	resp, err := s.llm.Chat(callCtx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM chat failed: %w", err)
	}

	return parseLLMScores(resp.Content), nil
}

// parseLLMScores 解析 LLM 返回的分数，先尝试 JSON，再正则回退
// Parse LLM score response: try JSON first, then regex fallback
func parseLLMScores(content string) []llmScoreItem {
	content = strings.TrimSpace(content)

	// 尝试 JSON 直接解析 / Try direct JSON unmarshal
	var items []llmScoreItem
	if err := json.Unmarshal([]byte(content), &items); err == nil && len(items) > 0 {
		return items
	}

	// 正则回退 / Regex fallback
	matches := scoreRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	results := make([]llmScoreItem, 0, len(matches))
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		score, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		results = append(results, llmScoreItem{Index: idx, Score: score})
	}
	return results
}
