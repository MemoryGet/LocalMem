package search

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// QueryIntent 查询意图类型 / Query intent type
type QueryIntent string

const (
	IntentKeyword    QueryIntent = "keyword"    // 精确查找 / Exact lookup
	IntentSemantic   QueryIntent = "semantic"   // 模糊/探索性 / Fuzzy/exploratory
	IntentTemporal   QueryIntent = "temporal"   // 时间相关 / Time-related
	IntentRelational QueryIntent = "relational" // 关联查询 / Association query
	IntentGeneral    QueryIntent = "general"    // 默认 / Default
)

// ChannelWeights 通道权重 / Per-channel weights for RRF fusion
type ChannelWeights struct {
	FTS    float64
	Qdrant float64
	Graph  float64
}

// QueryPlan 预处理后的查询计划 / Pre-processed query plan
type QueryPlan struct {
	OriginalQuery string
	SemanticQuery string
	Keywords      []string
	Entities      []string // 匹配到的实体 ID / Matched entity IDs
	Intent        QueryIntent
	Weights       ChannelWeights
	Temporal      bool // 是否需要时间排序 / Whether temporal sorting is needed
}

// intentMultipliers 意图→权重系数映射 / Intent to weight multiplier mapping
// [fix] keyword 降低 Graph(0.5)，relational 降低 FTS(0.4)
var intentMultipliers = map[QueryIntent]ChannelWeights{
	IntentKeyword:    {FTS: 1.5, Qdrant: 0.6, Graph: 0.5},
	IntentSemantic:   {FTS: 0.6, Qdrant: 1.5, Graph: 0.8},
	IntentTemporal:   {FTS: 1.3, Qdrant: 0.8, Graph: 0.6},
	IntentRelational: {FTS: 0.4, Qdrant: 0.7, Graph: 1.8},
	IntentGeneral:    {FTS: 1.0, Qdrant: 1.0, Graph: 1.0},
}

// 时间关键词 / Temporal keywords
// [fix] 补充 last_quarter/past_few_days/前天/这几天/之前
var temporalPatterns = regexp.MustCompile(`(?i)\b(recent|latest|last\s+week|last\s+month|last\s+quarter|yesterday|today|this\s+week|this\s+month|past\s+few\s+days)\b|最近|上周|上月|前天|昨天|今天|本周|本月|这几天|之前`)

// 关联关键词 / Relational keywords
// [fix] 移除 "about"（误判率高），补充 depends_on/dependencies_of/之间/依赖
var relationalPatterns = regexp.MustCompile(`(?i)\b(related\s+to|associated\s+with|connected\s+to|regarding|depends\s+on|dependencies\s+of)\b|相关|关于|有关|关联|之间|依赖`)

// 探索性关键词 / Exploratory keywords
// [fix] 补充 when/where/which/谁/哪里
var exploratoryPatterns = regexp.MustCompile(`(?i)\b(how|why|what|when|where|which|explain|describe|summarize|overview)\b|怎么|为什么|什么|如何|谁|哪里|解释|描述|总结|概述|哪些`)

// Preprocessor 查询预处理器 / Query preprocessor
type Preprocessor struct {
	tokenizer  tokenizer.Tokenizer
	graphStore store.GraphStore      // 可为 nil / may be nil
	llm        llm.Provider          // 可为 nil / may be nil
	stopFilter *tokenizer.StopFilter // 停用词过滤器 / Stop word filter
	cfg        config.RetrievalConfig
}

// NewPreprocessor 创建预处理器 / Create a new preprocessor
// 自动从 cfg.Preprocess.StopwordFiles 加载停用词，加载失败时使用内置默认词表
func NewPreprocessor(tok tokenizer.Tokenizer, graphStore store.GraphStore, llm llm.Provider, cfg config.RetrievalConfig) *Preprocessor {
	sf := tokenizer.NewStopFilter(cfg.Preprocess.StopwordFiles...)
	return &Preprocessor{
		tokenizer:  tok,
		graphStore: graphStore,
		llm:        llm,
		stopFilter: sf,
		cfg:        cfg,
	}
}

// Process 执行查询预处理 / Execute query preprocessing
func (p *Preprocessor) Process(ctx context.Context, query string, scope string) (*QueryPlan, error) {
	plan := &QueryPlan{
		OriginalQuery: query,
		SemanticQuery: query,
		Intent:        IntentGeneral,
	}

	if query == "" {
		plan.Weights = p.computeWeights(IntentGeneral)
		return plan, nil
	}

	// 步骤 1: 分词提取关键词（含停用词过滤）/ Step 1: Tokenize, extract keywords, filter stop words
	keywords := p.extractKeywords(ctx, query)
	plan.Keywords = keywords

	// 步骤 2: 实体快速匹配 / Step 2: Fast entity matching
	if p.graphStore != nil {
		plan.Entities = p.matchEntities(ctx, keywords, scope)
	}

	// 步骤 3: 规则意图分类 / Step 3: Rule-based intent classification
	plan.Intent = p.classifyIntent(query)

	// 步骤 4: 计算动态权重 / Step 4: Compute dynamic weights
	plan.Weights = p.computeWeights(plan.Intent)

	// 步骤 4.5: temporal 标记 / Mark temporal for retriever to inject time sorting
	if plan.Intent == IntentTemporal {
		plan.Temporal = true
	}

	// 步骤 5: LLM 增强（可选）/ Step 5: Optional LLM enhancement
	if p.cfg.Preprocess.UseLLM && p.llm != nil {
		p.llmEnhance(ctx, plan)
	}

	return plan, nil
}

// extractKeywords 分词提取关键词（过滤停用词）/ Tokenize query into keywords with stop word filtering
func (p *Preprocessor) extractKeywords(ctx context.Context, query string) []string {
	tokenized, err := p.tokenizer.Tokenize(ctx, query)
	if err != nil || tokenized == "" {
		return nil
	}
	tokens := strings.Fields(tokenized)
	var keywords []string
	for _, tok := range tokens {
		if p.stopFilter.IsStopWord(tok) {
			continue
		}
		if len([]rune(tok)) >= 1 {
			keywords = append(keywords, tok)
		}
	}
	return keywords
}

// matchEntities 实体快速匹配 / Match keywords against graph entities
// [fix] 去掉 100 硬限，短关键词(<3 rune)要求精确匹配
// 注意：GraphStore.ListEntities 接口仅支持 limit，不支持 offset，
// 因此无法真正分页遍历全部实体；当实体总量超过 batchSize 时仅能读取前 N 条。
// Note: GraphStore.ListEntities only accepts limit (no offset), so full pagination
// is not possible; only the first batchSize entities are matched when total > batchSize.
func (p *Preprocessor) matchEntities(ctx context.Context, keywords []string, scope string) []string {
	if len(keywords) == 0 {
		return nil
	}

	// 一次性拉取实体（接口不支持 offset，无法分页）/ Fetch entities in one call (no offset support in interface)
	batchSize := 500
	allEntities, err := p.graphStore.ListEntities(ctx, scope, "", batchSize)
	if err != nil {
		return nil
	}

	var matched []string
	for _, ent := range allEntities {
		entNameLower := strings.ToLower(ent.Name)
		for _, kw := range keywords {
			kwRunes := len([]rune(kw))
			kwLower := strings.ToLower(kw)
			// 短关键词(<3 rune)要求精确匹配 / Short keywords require exact match
			if kwRunes < 3 {
				if strings.EqualFold(kw, ent.Name) {
					matched = append(matched, ent.ID)
					break
				}
			} else {
				if strings.EqualFold(kw, ent.Name) || strings.Contains(entNameLower, kwLower) {
					matched = append(matched, ent.ID)
					break
				}
			}
		}
	}
	return matched
}

// classifyIntent 规则意图分类 / Rule-based intent classification
// [fix] 语言感知阈值：CJK 主导的 query 用更小的阈值
func (p *Preprocessor) classifyIntent(query string) QueryIntent {
	// 按优先级匹配 / Match by priority
	if temporalPatterns.MatchString(query) {
		return IntentTemporal
	}
	if relationalPatterns.MatchString(query) {
		return IntentRelational
	}

	// 语言感知长度阈值 / Language-aware length thresholds
	runes := []rune(query)
	runeCount := len(runes)
	cjkRatio := cjkRatio(runes)

	var shortMax, longMin int
	if cjkRatio > 0.5 {
		// CJK 主导：8 字以内短查询，25 字以上长查询
		shortMax = 8
		longMin = 25
	} else {
		// 英文主导：20 runes 以内短查询，50 runes 以上长查询
		shortMax = 20
		longMin = 50
	}

	// 短查询 → keyword
	if runeCount > 0 && runeCount <= shortMax && !exploratoryPatterns.MatchString(query) {
		return IntentKeyword
	}

	// 长查询或探索性 → semantic
	if runeCount > longMin || exploratoryPatterns.MatchString(query) {
		return IntentSemantic
	}

	return IntentGeneral
}

// cjkRatio 计算 CJK 字符占比 / Calculate CJK character ratio
func cjkRatio(runes []rune) float64 {
	if len(runes) == 0 {
		return 0
	}
	cjk := 0
	for _, r := range runes {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) ||
			unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			cjk++
		}
	}
	return float64(cjk) / float64(len(runes))
}

// llmEnhanceResponse LLM 增强响应 / LLM enhancement response
type llmEnhanceResponse struct {
	RewrittenQuery string   `json:"rewritten_query"`
	Intent         string   `json:"intent"`
	Keywords       []string `json:"keywords"`
}

// llmEnhance LLM 增强预处理 / LLM-enhanced preprocessing (overwrites rule-based fields on success)
func (p *Preprocessor) llmEnhance(ctx context.Context, plan *QueryPlan) {
	timeout := p.cfg.Preprocess.LLMTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	temp := 0.1
	resp, err := p.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{
				Role: "system",
				Content: `You are a query preprocessor. Given a search query, output JSON:
{"rewritten_query": "semantically expanded query for vector search", "intent": "keyword|semantic|temporal|relational|general", "keywords": ["optional", "extra", "keywords"]}
Respond ONLY with valid JSON.`,
			},
			{Role: "user", Content: plan.OriginalQuery},
		},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	})
	if err != nil {
		logger.Warn("preprocess: LLM enhancement failed, using rule-based result", zap.Error(err))
		return
	}

	var result llmEnhanceResponse
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		logger.Warn("preprocess: LLM response parse failed, using rule-based result",
			zap.String("raw", resp.Content), zap.Error(err))
		return
	}

	// 覆盖规则式结果 / Override rule-based results
	if result.RewrittenQuery != "" {
		plan.SemanticQuery = result.RewrittenQuery
	}
	if intent := QueryIntent(result.Intent); isValidIntent(intent) {
		plan.Intent = intent
		plan.Weights = p.computeWeights(intent)
	}
	if len(result.Keywords) > 0 {
		// 合并去重 / Merge and deduplicate
		existing := make(map[string]bool)
		for _, kw := range plan.Keywords {
			existing[strings.ToLower(kw)] = true
		}
		for _, kw := range result.Keywords {
			if !existing[strings.ToLower(kw)] {
				plan.Keywords = append(plan.Keywords, kw)
				existing[strings.ToLower(kw)] = true
			}
		}
	}
}

// isValidIntent 校验意图标签是否合法 / Check if intent label is valid
func isValidIntent(intent QueryIntent) bool {
	switch intent {
	case IntentKeyword, IntentSemantic, IntentTemporal, IntentRelational, IntentGeneral:
		return true
	default:
		return false
	}
}

// computeWeights 计算通道权重 / Compute channel weights from intent
func (p *Preprocessor) computeWeights(intent QueryIntent) ChannelWeights {
	m, ok := intentMultipliers[intent]
	if !ok {
		m = intentMultipliers[IntentGeneral]
	}

	ftsBase := p.cfg.FTSWeight
	if ftsBase == 0 {
		ftsBase = 1.0
	}
	qdrantBase := p.cfg.QdrantWeight
	if qdrantBase == 0 {
		qdrantBase = 1.0
	}
	graphBase := p.cfg.GraphWeight
	if graphBase == 0 {
		graphBase = 0.8
	}

	return ChannelWeights{
		FTS:    ftsBase * m.FTS,
		Qdrant: qdrantBase * m.Qdrant,
		Graph:  graphBase * m.Graph,
	}
}
