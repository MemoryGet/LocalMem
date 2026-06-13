package stage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// FTS 阶梯扩词默认参数 / Default parameters for progressive tier expansion
const (
	defaultFTSRetryLimit         = 30
	defaultFTSRetryMaxGraphTerms = 5
	defaultFTSRetryMaxHyDETerms  = 15
	defaultFTSRetryHyDETimeout   = 10 * time.Second

	defaultFTSRetryMaxVectorResults = 5
	defaultFTSRetryMinVectorScore   = 0.4
)

// 意图字符串常量（与 search.QueryIntent 值一致，避免跨包循环依赖）
// Intent string constants matching search.QueryIntent values; avoids cross-package import cycle.
// state.Plan.Intent is stored as plain string in the pipeline layer — direct comparison is safe.
const (
	retryIntentSemantic   = "semantic"
	retryIntentTemporal   = "temporal"
	retryIntentKeyword    = "keyword"
	retryIntentRelational = "relational"
)

// FTSRetryTier 渐进式充足性阶梯（运行时类型）/ Progressive sufficiency tier (runtime)
type FTSRetryTier struct {
	MinCount  int
	MinScore  float64
	Expansion string // "synonyms" | "graph" | "hyde" | "none"
}

// defaultTiers 内置五阶梯默认值：synonyms→graph→vector→hyde→none
// Built-in 5-tier defaults. Vector expansion (tier 2) reuses existing vector candidates in state
// or falls back to a fresh embedding search, then extracts entity names for FTS retry.
var defaultTiers = []FTSRetryTier{
	{MinCount: 5, MinScore: 0.6, Expansion: "synonyms"},
	{MinCount: 3, MinScore: 0.4, Expansion: "graph"},
	{MinCount: 2, MinScore: 0.25, Expansion: "vector"},
	{MinCount: 2, MinScore: 0.15, Expansion: "hyde"},
	{MinCount: 1, MinScore: 0.0, Expansion: "none"},
}

// defaultIntentStartTier 内置意图→起始阶梯映射 / Built-in intent→start-tier mapping
var defaultIntentStartTier = map[string]int{
	retryIntentSemantic:   0,
	retryIntentTemporal:   4,
	retryIntentKeyword:    1,
	retryIntentRelational: 1,
	"general":             1,
}

// DefaultFTSRewriteConfig 返回内置默认配置（测试和 builtin 使用）/ Return built-in default config
func DefaultFTSRewriteConfig() config.FTSRewriteConfig {
	tiers := make([]config.FTSRetryTierConfig, len(defaultTiers))
	for i, t := range defaultTiers {
		tiers[i] = config.FTSRetryTierConfig{MinCount: t.MinCount, MinScore: t.MinScore, Expansion: t.Expansion}
	}
	intentMap := make(map[string]int, len(defaultIntentStartTier))
	for k, v := range defaultIntentStartTier {
		intentMap[k] = v
	}
	return config.FTSRewriteConfig{Tiers: tiers, IntentStartTier: intentMap}
}

// FTSRewriteRetryStage FTS 渐进式扩词重试阶段
// 按最多 5 个充足性阶梯逐步判定并累积扩词，semantic intent 从最严苛阶梯开始，temporal 直接跳到兜底。
// 每阶梯独立配置 {MinCount, MinScore, Expansion}；词项跨阶梯累积（不重置）。
// 所有阶梯耗尽时，trace 写入 low_confidence=true。
//
// FTS progressive rewrite-retry stage: up to 5 sufficiency tiers with configurable thresholds
// and expansion layers. Semantic intent starts at the strictest tier; temporal at accept-all.
// Expansion terms accumulate across tiers. Exhaustion emits low_confidence=true in trace.
type FTSRewriteRetryStage struct {
	fts             FTSSearcher
	expander        GraphTermExpander
	hydeGen         HyDEGenerator
	tiers           []FTSRetryTier
	intentStartTier map[string]int
	retryLimit      int
	maxGraphTerms   int
	maxHyDETerms    int
	hydeTimeout     time.Duration

	vectorSearcher   VectorSearcher     // optional: vector search for expansion when no cached candidates
	embedder         Embedder           // optional: embeds query for vector search fallback
	graphStore       GraphRetriever     // optional: fetches entity names from vector-matched memories
	maxVectorResults int
	minVectorScore   float64

	tok tokenizer.Tokenizer // optional: for bilingual content term tokenization (gse/jieba/simple)
}

// NewFTSRewriteRetryStage 创建渐进式扩词重试阶段 / Create progressive FTS rewrite-retry stage
func NewFTSRewriteRetryStage(fts FTSSearcher, expander GraphTermExpander, hydeGen HyDEGenerator, cfg config.FTSRewriteConfig, vectorSearcher VectorSearcher, embedder Embedder, graphStore GraphRetriever) *FTSRewriteRetryStage {
	return &FTSRewriteRetryStage{
		fts:              fts,
		expander:         expander,
		hydeGen:          hydeGen,
		tiers:            parseTiers(cfg),
		intentStartTier:  parseIntentStartTier(cfg),
		retryLimit:       defaultFTSRetryLimit,
		maxGraphTerms:    defaultFTSRetryMaxGraphTerms,
		maxHyDETerms:     defaultFTSRetryMaxHyDETerms,
		hydeTimeout:      defaultFTSRetryHyDETimeout,
		vectorSearcher:   vectorSearcher,
		embedder:         embedder,
		graphStore:       graphStore,
		maxVectorResults: defaultFTSRetryMaxVectorResults,
		minVectorScore:   defaultFTSRetryMinVectorScore,
	}
}

// WithTokenizer 注入项目配置的分词器，用于内容词中文分词（gse/jieba/simple）
// Inject the project-configured tokenizer for bilingual content term extraction.
// When set, tokenizeContentTerms uses Tokenize() for proper CJK word segmentation
// instead of the fallback Unicode bigram approach.
func (s *FTSRewriteRetryStage) WithTokenizer(tok tokenizer.Tokenizer) *FTSRewriteRetryStage {
	s.tok = tok
	return s
}

// parseTiers cfg → runtime FTSRetryTier slice（最多 5 个；空则用默认）
func parseTiers(cfg config.FTSRewriteConfig) []FTSRetryTier {
	if len(cfg.Tiers) == 0 {
		return defaultTiers
	}
	n := len(cfg.Tiers)
	if n > 5 {
		n = 5
	}
	out := make([]FTSRetryTier, n)
	for i := 0; i < n; i++ {
		out[i] = FTSRetryTier{
			MinCount:  cfg.Tiers[i].MinCount,
			MinScore:  cfg.Tiers[i].MinScore,
			Expansion: cfg.Tiers[i].Expansion,
		}
	}
	return out
}

// parseIntentStartTier cfg → intent start-tier map（空则用默认）
func parseIntentStartTier(cfg config.FTSRewriteConfig) map[string]int {
	if len(cfg.IntentStartTier) == 0 {
		return defaultIntentStartTier
	}
	return cfg.IntentStartTier
}

// Name 返回阶段名称 / Return stage name
func (s *FTSRewriteRetryStage) Name() string { return "fts_rewrite_retry" }

// Execute 渐进式充足性阶梯检测与扩词重试
// Progressive sufficiency cascade: check each tier, expand if insufficient, accumulate terms.
func (s *FTSRewriteRetryStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	if s.fts == nil {
		state.AddTrace(pipeline.StageTrace{Name: s.Name(), Skipped: true, Note: "fts searcher is nil"})
		return state, nil
	}

	// 意图 → 起始阶梯 / Intent → start tier
	intent := ""
	if state.Plan != nil {
		intent = state.Plan.Intent
	}
	startTier := s.resolveStartTier(intent)

	// 初始化累积扩词集。
	// 关键：当 startTier == 0（semantic）时，不预填 seenTerms，让 synonyms 阶梯能把 plan keywords
	// 作为"新词"加入；当 startTier > 0（跳过 synonyms）时，预填 plan keywords 作为基础查询。
	// Key: when startTier==0, do NOT pre-seed seenTerms — the synonyms tier adds plan keywords
	// as fresh expansion. When startTier>0, pre-seed plan keywords so later tiers still include them.
	expandedTerms := make([]string, 0)
	seenTerms := make(map[string]bool)
	if startTier > 0 && state.Plan != nil {
		for _, kw := range state.Plan.Keywords {
			lkw := strings.ToLower(strings.TrimSpace(kw))
			if lkw != "" && !seenTerms[lkw] {
				seenTerms[lkw] = true
				expandedTerms = append(expandedTerms, kw)
			}
		}
	}

	// 已有候选去重集 / Dedup set for existing candidates
	seenIDs := make(map[string]bool, len(state.Candidates))
	for _, c := range state.Candidates {
		if c != nil && c.Memory != nil {
			seenIDs[c.Memory.ID] = true
		}
	}

	for i := startTier; i < len(s.tiers); i++ {
		tier := s.tiers[i]

		// 充足性检查 / Sufficiency check
		ftsResults := collectFTSCandidates(state.Candidates)
		topScore := 0.0
		if len(ftsResults) > 0 {
			topScore = ftsResults[0].Score
		}

		if len(ftsResults) >= tier.MinCount && topScore >= tier.MinScore {
			state.AddTrace(pipeline.StageTrace{
				Name:        s.Name(),
				Duration:    time.Since(start),
				InputCount:  inputCount,
				OutputCount: len(state.Candidates),
				Note:        fmt.Sprintf("sufficient at tier %d: count=%d top_score=%.3f", i, len(ftsResults), topScore),
			})
			return state, nil
		}

		if tier.Expansion == "none" {
			state.AddTrace(pipeline.StageTrace{
				Name:        s.Name(),
				Duration:    time.Since(start),
				InputCount:  inputCount,
				OutputCount: len(state.Candidates),
				Note:        fmt.Sprintf("exhausted tiers; low_confidence=true; count=%d top_score=%.3f", len(ftsResults), topScore),
			})
			return state, nil
		}

		// 执行该阶梯的扩词，只取新词 / Expand for this tier, only new terms
		newTerms := s.expandTier(ctx, tier.Expansion, state, seenTerms)
		if len(newTerms) == 0 {
			state.AddTrace(pipeline.StageTrace{
				Name: s.Name(),
				Note: fmt.Sprintf("tier %d/%s: no new terms, advancing", i, tier.Expansion),
			})
			continue
		}

		for _, t := range newTerms {
			lkw := strings.ToLower(strings.TrimSpace(t))
			if lkw != "" && !seenTerms[lkw] {
				seenTerms[lkw] = true
				expandedTerms = append(expandedTerms, t)
			}
		}

		// 二次 FTS（用累积扩词集）/ Second FTS with accumulated terms
		expandedQuery := strings.Join(expandedTerms, " ")
		var retryResults []*model.SearchResult
		var err error
		if state.Filters != nil {
			retryResults, err = s.fts.SearchTextFiltered(ctx, expandedQuery, state.Filters, s.retryLimit)
		} else {
			retryResults, err = s.fts.SearchText(ctx, expandedQuery, state.Identity, s.retryLimit)
		}
		if err != nil {
			logger.Warn("fts_rewrite_retry: tier FTS search failed",
				zap.Int("tier", i), zap.String("query", expandedQuery), zap.Error(err))
			continue
		}

		// 去重追加新候选（不可变）/ Deduplicated immutable append
		var toAdd []*model.SearchResult
		for _, r := range retryResults {
			if r != nil && r.Memory != nil && !seenIDs[r.Memory.ID] {
				seenIDs[r.Memory.ID] = true
				toAdd = append(toAdd, r)
			}
		}
		if len(toAdd) > 0 {
			merged := make([]*model.SearchResult, 0, len(state.Candidates)+len(toAdd))
			merged = append(merged, state.Candidates...)
			merged = append(merged, toAdd...)
			state.Candidates = merged
		}

		state.AddTrace(pipeline.StageTrace{
			Name: s.Name(),
			Note: fmt.Sprintf("tier %d/%s: fts_count=%d top=%.3f new_terms=%d added=%d",
				i, tier.Expansion, len(ftsResults), topScore, len(newTerms), len(toAdd)),
		})
	}

	// 循环正常结束：仅当所有阶梯均已执行扩词且最后阶梯 Expansion != "none" 时走到此处。
	// 默认 4 阶梯配置中 tier 3 Expansion="none" 保证提前 return，此行在默认配置下不可达。
	return state, nil
}

// resolveStartTier 根据意图返回起始阶梯下标 / Resolve start tier index from intent
func (s *FTSRewriteRetryStage) resolveStartTier(intent string) int {
	if intent == "" {
		intent = "general"
	}
	if idx, ok := s.intentStartTier[intent]; ok {
		if idx >= 0 && idx < len(s.tiers) {
			return idx
		}
	}
	if 1 < len(s.tiers) {
		return 1
	}
	return 0
}

// expandTier 按阶梯扩词类型调用对应私有方法 / Call the right expansion method for a tier
func (s *FTSRewriteRetryStage) expandTier(ctx context.Context, expansion string, state *pipeline.PipelineState, seen map[string]bool) []string {
	switch expansion {
	case "synonyms":
		return s.expandSynonyms(state, seen)
	case "graph":
		return s.expandGraph(ctx, state, seen)
	case "hyde":
		return s.expandHyDE(ctx, state, seen)
	case "vector":
		return s.expandVector(ctx, state, seen)
	default:
		return nil
	}
}

// expandSynonyms 取 plan.Keywords 中 seen 中不存在的词 / Return plan keywords not already in seen
func (s *FTSRewriteRetryStage) expandSynonyms(state *pipeline.PipelineState, seen map[string]bool) []string {
	if state.Plan == nil {
		return nil
	}
	var out []string
	for _, kw := range state.Plan.Keywords {
		lkw := strings.ToLower(strings.TrimSpace(kw))
		if lkw != "" && !seen[lkw] {
			out = append(out, kw)
		}
	}
	return out
}

// expandGraph 从图谱 1-hop 邻居实体名中取新词 / Return graph 1-hop entity names not already in seen
func (s *FTSRewriteRetryStage) expandGraph(ctx context.Context, state *pipeline.PipelineState, seen map[string]bool) []string {
	if s.expander == nil || state.Plan == nil || len(state.Plan.Entities) == 0 {
		return nil
	}
	names, err := s.expander.GetEntityNeighborNames(ctx, state.Plan.Entities, s.maxGraphTerms)
	if err != nil {
		logger.Debug("fts_rewrite_retry: graph expansion failed", zap.Error(err))
		return nil
	}
	var out []string
	for _, n := range names {
		ln := strings.ToLower(strings.TrimSpace(n))
		if ln != "" && !seen[ln] {
			out = append(out, n)
		}
	}
	return out
}

// expandHyDE 按需调用 HyDEGenerator，从假设文档提取新关键词
// On-demand HyDE: generate hypothetical document, extract keywords not already in seen.
func (s *FTSRewriteRetryStage) expandHyDE(ctx context.Context, state *pipeline.PipelineState, seen map[string]bool) []string {
	// 优先使用 plan 预生成的 HyDEDoc（无需 LLM）；为空且配置了 hydeGen 时才按需生成。
	// Prefer the plan's pre-generated HyDEDoc (no LLM); only call hydeGen on-demand when it is empty.
	hydeDoc := ""
	if state.Plan != nil {
		hydeDoc = state.Plan.HyDEDoc
	}
	if hydeDoc == "" {
		if s.hydeGen == nil || state.Query == "" {
			return nil
		}
		hydeCtx, cancel := context.WithTimeout(ctx, s.hydeTimeout)
		defer cancel()
		doc, err := s.hydeGen.GenerateHyDE(hydeCtx, state.Query)
		if err != nil {
			logger.Debug("fts_rewrite_retry: HyDE generation failed", zap.Error(err))
			return nil
		}
		hydeDoc = doc
	}
	if hydeDoc == "" {
		return nil
	}
	candidates := extractHyDEKeywords(hydeDoc, s.maxHyDETerms)
	var out []string
	for _, kw := range candidates {
		lkw := strings.ToLower(strings.TrimSpace(kw))
		if lkw != "" && !seen[lkw] {
			out = append(out, kw)
		}
	}
	return out
}

// expandVector 向量引导内容词 + 实体名扩词 / Vector-guided content-term and entity-name expansion
// 策略（双轨）：
//  1. 优先复用 state.Candidates 中已有的向量候选（零成本）
//  2. 无缓存时用 embedder + vectorSearcher 发起新搜索（fallback）
//  3. 按 minVectorScore 过滤低质候选
//  4a. 从 Memory.Content/Abstract/Summary 提取内容关键词（直接词汇桥接，优先放前）
//  4b. 批量调用 graphStore.GetMemoriesEntities 提取实体名（补充）
//  5. 过滤 seen + 查询词，去重后返回（内容词在前，实体名在后）
func (s *FTSRewriteRetryStage) expandVector(ctx context.Context, state *pipeline.PipelineState, seen map[string]bool) []string {
	// 1. 优先用 state 中已有的向量候选 / Prefer cached vector candidates from state
	var vecCandidates []*model.SearchResult
	for _, c := range state.Candidates {
		if c.Source == SourceVector && c.Score >= s.minVectorScore && c.Memory != nil {
			vecCandidates = append(vecCandidates, c)
		}
	}

	// 2. 无缓存时发起新向量搜索 / Fresh vector search when no cached candidates
	if len(vecCandidates) == 0 {
		if s.vectorSearcher == nil || s.embedder == nil {
			return nil
		}
		emb, err := s.embedder.Embed(ctx, state.Query)
		if err != nil || len(emb) == 0 {
			return nil
		}
		results, err := s.vectorSearcher.Search(ctx, emb, state.Identity, s.maxVectorResults)
		if err != nil {
			return nil
		}
		for _, r := range results {
			if r.Score >= s.minVectorScore && r.Memory != nil {
				vecCandidates = append(vecCandidates, r)
			}
		}
	}

	if len(vecCandidates) == 0 {
		return nil
	}

	// 构建查询词集（原始 query 的分词，过滤时避免重复加入）
	// Build query term set so we don't re-add words already in the query.
	queryTerms := s.tokenizeContentTerms(ctx, state.Query)
	querySet := make(map[string]bool, len(queryTerms))
	for _, t := range queryTerms {
		querySet[t] = true
	}

	added := make(map[string]bool)

	// 4a. 内容词提取：从 Memory 文本字段提取高频关键词 / Extract content terms from memory text fields
	// 按词频统计，选取出现次数最多的词，直接桥接词汇不匹配（如 "trans woman" 对应 "identity"）
	termFreq := make(map[string]int)
	for _, c := range vecCandidates {
		texts := []string{c.Memory.Content, c.Memory.Summary}
		for _, text := range texts {
			for _, tok := range s.tokenizeContentTerms(ctx, text) {
				if !seen[tok] && !querySet[tok] && len([]rune(tok)) >= 2 {
					termFreq[tok]++
				}
			}
		}
	}
	// 按频次降序取前 maxVectorContentTerms 个 / Take top terms by frequency
	type termCount struct {
		term  string
		count int
	}
	topTerms := make([]termCount, 0, len(termFreq))
	for t, c := range termFreq {
		topTerms = append(topTerms, termCount{t, c})
	}
	sort.Slice(topTerms, func(i, j int) bool {
		if topTerms[i].count != topTerms[j].count {
			return topTerms[i].count > topTerms[j].count
		}
		return topTerms[i].term < topTerms[j].term
	})
	var out []string
	for _, tc := range topTerms {
		if added[tc.term] {
			continue
		}
		added[tc.term] = true
		out = append(out, tc.term)
		if len(out) >= maxVectorContentTerms {
			break
		}
	}

	// 4b. 实体名补充（graphStore 可选）/ Supplement with entity names (optional graphStore)
	if s.graphStore != nil {
		memIDs := make([]string, 0, len(vecCandidates))
		for _, c := range vecCandidates {
			memIDs = append(memIDs, c.Memory.ID)
		}
		if entitiesMap, err := s.graphStore.GetMemoriesEntities(ctx, memIDs); err == nil {
			for _, ents := range entitiesMap {
				for _, e := range ents {
					if e == nil || e.Name == "" {
						continue
					}
					lname := strings.ToLower(strings.TrimSpace(e.Name))
					if lname == "" || seen[lname] || querySet[lname] || added[lname] {
						continue
					}
					added[lname] = true
					out = append(out, e.Name)
				}
			}
		}
	}

	return out
}

// maxVectorContentTerms 内容词提取上限 / Max content terms extracted from vector candidates
const maxVectorContentTerms = 8

// contentStopFilter 中英双语内容词停用词过滤器（包级单例）
// Bilingual (EN+ZH) stop word filter for content term extraction (package-level singleton).
// Relies on pkg/tokenizer defaults (EN + ZH) — no hardcoded Chinese word list here.
// English-only extras are added for contracted forms that a tokenizer splits oddly.
var contentStopFilter = func() *tokenizer.StopFilter {
	sf := tokenizer.NewStopFilter()
	// 仅补充英文缩写残片（分词器可能把 "I’m" 切成 "i" + "m"，两者均应过滤）
	// Only supplement English contraction fragments (tokenizer may split "I’m" → "i"+"m").
	sf.AddWords(
		"i’m", "i’ve", "i’d", "i’ll", "it’s", "that’s", "there’s", "they’re", "we’re",
		"who", "what", "when", "where", "how", "why",
		"like", "think", "know", "want", "just", "also", "really", "actually",
		"still", "even", "now", "am", "im", "ive", "ill",
		"his", "her", "their",
	)
	// 中文停用词交由 pkg/tokenizer.defaultStopWordsZH 覆盖，不在此处硬编码
	// Chinese stop words are handled by pkg/tokenizer.defaultStopWordsZH — not hardcoded here.
	return sf
}()

// tokenizeContentTerms 从文本提取内容词（中英双语方法，可访问注入的分词器）
// Bilingual content term extraction — method form to access the injected tokenizer.
//
// 两条路径：
//  1. 有注入分词器（gse/jieba）→ 委托 Tokenize()，中文正确切词（跨性别整词）
//  2. 无注入分词器（fallback）→ CJK unigram + 英文按空白切词
// 两条路径共用 contentStopFilter 过滤停用词（EN+ZH 双语）。
func (s *FTSRewriteRetryStage) tokenizeContentTerms(ctx context.Context, text string) []string {
	if text == "" {
		return nil
	}

	var rawTokens []string

	if s.tok != nil {
		// 路径 1：委托给项目配置的分词器 / Path 1: delegate to project tokenizer
		if tokenized, err := s.tok.Tokenize(ctx, text); err == nil && tokenized != "" {
			for _, t := range strings.Fields(tokenized) {
				rawTokens = append(rawTokens, t)
			}
		}
		// 分词失败 fallback 到路径 2
	}

	if len(rawTokens) == 0 {
		// 路径 2：Unicode fallback — CJK unigram + bigram + 英文按词切分
		// Path 2: unigrams for single chars, bigrams for adjacent CJK pairs (better precision),
		// word-split for English. Used when no tokenizer is injected.
		var enWord []rune
		var cjkRun []rune
		flushEN := func() {
			if len(enWord) == 0 {
				return
			}
			rawTokens = append(rawTokens, string(enWord))
			enWord = enWord[:0]
		}
		flushCJK := func() {
			if len(cjkRun) == 0 {
				return
			}
			for _, r := range cjkRun {
				rawTokens = append(rawTokens, string(r)) // unigram
			}
			for i := 0; i+1 < len(cjkRun); i++ {
				rawTokens = append(rawTokens, string(cjkRun[i:i+2])) // bigram
			}
			cjkRun = cjkRun[:0]
		}
		for _, r := range text {
			switch {
			case tokenizer.IsCJK(r):
				flushEN()
				cjkRun = append(cjkRun, r)
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				flushCJK()
				enWord = append(enWord, r)
			case r == 0x27 || r == 0x2018 || r == 0x2019: // apostrophes
				flushEN()
			default:
				flushEN()
				flushCJK()
			}
		}
		flushEN()
		flushCJK()
	}

	// 统一过滤停用词 + 最短 2 字符 / Unified stop word filter + min 2 chars
	var out []string
	for _, t := range rawTokens {
		tok := strings.ToLower(strings.TrimSpace(t))
		if len([]rune(tok)) < 2 || contentStopFilter.IsStopWord(tok) {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// collectFTSCandidates 提取 FTS 候选并按分数降序排列 / Extract and sort FTS candidates by score desc
func collectFTSCandidates(candidates []*model.SearchResult) []*model.SearchResult {
	var out []*model.SearchResult
	for _, c := range candidates {
		if c != nil && c.Source == SourceFTS {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// extractHyDEKeywords 从 HyDE 假设文档提取关键词 / Extract keywords from HyDE document
func extractHyDEKeywords(doc string, limit int) []string {
	fields := strings.FieldsFunc(doc, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, limit)
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if len([]rune(f)) < 2 {
			continue
		}
		lower := strings.ToLower(f)
		if !seen[lower] {
			seen[lower] = true
			out = append(out, f)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}
