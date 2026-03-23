# Query Preprocessor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a query preprocessor to the search pipeline that transforms raw queries into structured QueryPlans with per-channel optimized inputs and dynamic RRF weights based on intent classification.

**Architecture:** A `Preprocessor` struct in `internal/search/` sits before the existing `Retriever`. It runs rule-based keyword extraction + intent classification (always), with optional LLM query rewriting (config toggle). The preprocessor produces a `QueryPlan` consumed by each retrieval channel. When disabled, retrieval behavior is unchanged.

**Tech Stack:** Go, existing `pkg/tokenizer`, existing `store.GraphStore`, existing `llm.Provider`

**Spec:** `docs/superpowers/specs/2026-03-20-query-preprocessor-design.md`

---

### Task 1: Expose Tokenizer from Stores

**Files:**
- Modify: `internal/store/factory.go:16-24` (Stores struct) and `:42` (tok assignment)

- [ ] **Step 1: Add Tokenizer field to Stores struct**

In `internal/store/factory.go`, add the field to `Stores`:

```go
type Stores struct {
	MemoryStore   MemoryStore
	VectorStore   VectorStore        // 可为 nil / may be nil
	Embedder      Embedder           // 可为 nil / may be nil
	ContextStore  ContextStore       // 可为 nil / may be nil
	TagStore      TagStore           // 可为 nil / may be nil
	GraphStore    GraphStore         // 可为 nil / may be nil
	DocumentStore DocumentStore      // 可为 nil / may be nil
	Tokenizer     tokenizer.Tokenizer // 可为 nil / may be nil (only when SQLite disabled)
}
```

Import `"iclude/pkg/tokenizer"` is already present.

- [ ] **Step 2: Assign Tokenizer in InitStores**

In `InitStores`, after line 42 (`tok := newTokenizer(...)`), add:

```go
stores.Tokenizer = tok
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: SUCCESS, no compilation errors

- [ ] **Step 4: Commit**

```bash
git add internal/store/factory.go
git commit -m "refactor: expose Tokenizer on store.Stores for reuse by preprocessor"
```

---

### Task 2: Add PreprocessConfig to config

**Files:**
- Modify: `internal/config/config.go:129-137` (RetrievalConfig) and `:181-188` (defaults)

- [ ] **Step 1: Add PreprocessConfig struct and embed in RetrievalConfig**

In `internal/config/config.go`, add the struct after `RetrievalConfig`:

```go
// PreprocessConfig 查询预处理配置 / Query preprocessing configuration
type PreprocessConfig struct {
	Enabled    bool          `mapstructure:"enabled"`
	UseLLM     bool          `mapstructure:"use_llm"`
	LLMTimeout time.Duration `mapstructure:"llm_timeout"`
}
```

Add field to `RetrievalConfig`:

```go
type RetrievalConfig struct {
	GraphEnabled     bool             `mapstructure:"graph_enabled"`
	GraphDepth       int              `mapstructure:"graph_depth"`
	GraphWeight      float64          `mapstructure:"graph_weight"`
	FTSWeight        float64          `mapstructure:"fts_weight"`
	QdrantWeight     float64          `mapstructure:"qdrant_weight"`
	GraphFTSTop      int              `mapstructure:"graph_fts_top"`
	GraphEntityLimit int              `mapstructure:"graph_entity_limit"`
	Preprocess       PreprocessConfig `mapstructure:"preprocess"`
}
```

- [ ] **Step 2: Add viper defaults in LoadConfig**

After the existing retrieval defaults block (after line 188), add:

```go
// Preprocess 默认值 / Preprocess defaults
viper.SetDefault("retrieval.preprocess.enabled", true)
viper.SetDefault("retrieval.preprocess.use_llm", false)
viper.SetDefault("retrieval.preprocess.llm_timeout", "5s")
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add PreprocessConfig for query preprocessing"
```

---

### Task 3: Implement rule-based Preprocessor with tests (TDD)

**Files:**
- Create: `internal/search/preprocess.go`
- Create: `testing/search/preprocess_test.go`

- [ ] **Step 1: Write failing tests for intent classification**

Create `testing/search/preprocess_test.go`:

```go
package search_test

import (
	"context"
	"testing"

	"iclude/internal/config"
	"iclude/internal/search"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreprocessor_ClassifyIntent(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	cfg := config.PreprocessConfig{Enabled: true}
	baseCfg := config.RetrievalConfig{
		FTSWeight:    1.0,
		QdrantWeight: 1.0,
		GraphWeight:  0.8,
		Preprocess:   cfg,
	}
	pp := search.NewPreprocessor(tok, nil, baseCfg)

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
		{"general_midlength", "weekly project status update meeting notes for the team", search.IntentGeneral},
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
	pp := search.NewPreprocessor(tok, nil, cfg)

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
	pp := search.NewPreprocessor(tok, nil, cfg)

	tests := []struct {
		name        string
		query       string
		expectFTS   float64
		expectQdrant float64
	}{
		{"keyword_boosts_fts", "K8s error", 1.5, 0.6},
		{"semantic_boosts_qdrant", "how does the authentication system handle token refresh when the session expires and the user needs to re-login automatically", 0.6, 1.5},
		{"general_unchanged", "weekly project status update meeting notes for the team", 1.0, 1.0},
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
	pp := search.NewPreprocessor(tok, nil, cfg)

	plan, err := pp.Process(context.Background(), "", "")
	require.NoError(t, err)
	assert.Equal(t, search.IntentGeneral, plan.Intent)
	assert.Empty(t, plan.Keywords)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./testing/search/ -run TestPreprocessor -v`
Expected: FAIL — `search.NewPreprocessor` undefined

- [ ] **Step 3: Implement Preprocessor**

Create `internal/search/preprocess.go`:

```go
// Package search 检索业务逻辑 / Retrieval business logic
package search

import (
	"context"
	"regexp"
	"strings"

	"iclude/internal/config"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
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
}

// intentMultipliers 意图→权重系数映射 / Intent to weight multiplier mapping
var intentMultipliers = map[QueryIntent]ChannelWeights{
	IntentKeyword:    {FTS: 1.5, Qdrant: 0.6, Graph: 1.2},
	IntentSemantic:   {FTS: 0.6, Qdrant: 1.5, Graph: 0.8},
	IntentTemporal:   {FTS: 1.3, Qdrant: 0.8, Graph: 0.6},
	IntentRelational: {FTS: 0.8, Qdrant: 0.7, Graph: 1.8},
	IntentGeneral:    {FTS: 1.0, Qdrant: 1.0, Graph: 1.0},
}

// 时间关键词 / Temporal keywords
var temporalPatterns = regexp.MustCompile(`(?i)\b(recent|latest|last\s+week|last\s+month|yesterday|today|this\s+week|this\s+month)\b|最近|上周|上月|昨天|今天|本周|本月`)

// 关联关键词 / Relational keywords
var relationalPatterns = regexp.MustCompile(`(?i)\b(related\s+to|associated\s+with|connected\s+to|about|regarding)\b|相关|关于|有关|关联`)

// 探索性关键词 / Exploratory keywords
var exploratoryPatterns = regexp.MustCompile(`(?i)\b(how|why|what|explain|describe|summarize|overview)\b|怎么|为什么|什么|解释|描述|总结|概述|哪些`)

// Preprocessor 查询预处理器 / Query preprocessor
type Preprocessor struct {
	tokenizer  tokenizer.Tokenizer
	graphStore store.GraphStore // 可为 nil / may be nil
	cfg        config.RetrievalConfig
}

// NewPreprocessor 创建预处理器 / Create a new preprocessor
// llm 参数在 Task 4 中添加 / llm parameter added in Task 4
func NewPreprocessor(tok tokenizer.Tokenizer, graphStore store.GraphStore, cfg config.RetrievalConfig) *Preprocessor {
	return &Preprocessor{
		tokenizer:  tok,
		graphStore: graphStore,
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

	// 步骤 1: 分词提取关键词 / Step 1: Tokenize and extract keywords
	keywords := p.extractKeywords(ctx, query)
	plan.Keywords = keywords

	// 步骤 2: 实体快速匹配 / Step 2: Fast entity matching
	if p.graphStore != nil {
		plan.Entities = p.matchEntities(ctx, keywords, scope)
	}

	// 步骤 3: 规则意图分类 / Step 3: Rule-based intent classification
	plan.Intent = p.classifyIntent(query, keywords)

	// 步骤 4: 计算动态权重 / Step 4: Compute dynamic weights
	plan.Weights = p.computeWeights(plan.Intent)

	return plan, nil
}

// extractKeywords 分词提取关键词 / Tokenize query into keywords
func (p *Preprocessor) extractKeywords(ctx context.Context, query string) []string {
	tokenized, err := p.tokenizer.Tokenize(ctx, query)
	if err != nil || tokenized == "" {
		return nil
	}
	tokens := strings.Fields(tokenized)
	// 过滤过短的 token / Filter very short tokens
	var keywords []string
	for _, tok := range tokens {
		if len([]rune(tok)) >= 1 {
			keywords = append(keywords, tok)
		}
	}
	return keywords
}

// matchEntities 实体快速匹配 / Match keywords against graph entities
func (p *Preprocessor) matchEntities(ctx context.Context, keywords []string, scope string) []string {
	if len(keywords) == 0 {
		return nil
	}

	entities, err := p.graphStore.ListEntities(ctx, scope, "", 100)
	if err != nil {
		return nil
	}

	var matched []string
	for _, ent := range entities {
		entNameLower := strings.ToLower(ent.Name)
		for _, kw := range keywords {
			if strings.EqualFold(kw, ent.Name) || strings.Contains(entNameLower, strings.ToLower(kw)) {
				matched = append(matched, ent.ID)
				break
			}
		}
	}
	return matched
}

// classifyIntent 规则意图分类 / Rule-based intent classification
func (p *Preprocessor) classifyIntent(query string, keywords []string) QueryIntent {
	// 按优先级匹配 / Match by priority
	if temporalPatterns.MatchString(query) {
		return IntentTemporal
	}
	if relationalPatterns.MatchString(query) {
		return IntentRelational
	}

	tokenCount := len(keywords)

	// 短查询 → keyword / Short query → keyword
	if tokenCount > 0 && tokenCount <= 5 && !exploratoryPatterns.MatchString(query) {
		return IntentKeyword
	}

	// 长查询或探索性 → semantic / Long query or exploratory → semantic
	if tokenCount > 15 || exploratoryPatterns.MatchString(query) {
		return IntentSemantic
	}

	return IntentGeneral
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./testing/search/ -run TestPreprocessor -v`
Expected: PASS — all 4 test functions pass

- [ ] **Step 5: Commit**

```bash
git add internal/search/preprocess.go testing/search/preprocess_test.go
git commit -m "feat: add rule-based query preprocessor with intent classification"
```

---

### Task 4: Add LLM enhancement with fallback tests (TDD)

**Files:**
- Modify: `internal/search/preprocess.go` (add `llm` field, update constructor, add `llmEnhance` method, add LLM call to `Process()`)
- Modify: `testing/search/preprocess_test.go` (add LLM tests)

**Note:** This task adds the `llm.Provider` dependency to `Preprocessor`. The struct, constructor, and `Process()` all change. Task 3's tests continue to pass since they pass `nil` for llm.

- [ ] **Step 1: Update Preprocessor struct and constructor to accept llm.Provider**

In `internal/search/preprocess.go`:

Add `"iclude/internal/llm"` to imports.

Update struct:
```go
type Preprocessor struct {
	tokenizer  tokenizer.Tokenizer
	graphStore store.GraphStore // 可为 nil / may be nil
	llm        llm.Provider    // 可为 nil / may be nil
	cfg        config.RetrievalConfig
}
```

Update constructor:
```go
func NewPreprocessor(tok tokenizer.Tokenizer, graphStore store.GraphStore, llm llm.Provider, cfg config.RetrievalConfig) *Preprocessor {
	return &Preprocessor{
		tokenizer:  tok,
		graphStore: graphStore,
		llm:        llm,
		cfg:        cfg,
	}
}
```

Add the LLM call to `Process()`, after `computeWeights`:
```go
	plan.Weights = p.computeWeights(plan.Intent)

	// LLM 增强（可选）/ Optional LLM enhancement
	if p.cfg.Preprocess.UseLLM && p.llm != nil {
		p.llmEnhance(ctx, plan)
	}

	return plan, nil
```

Update Task 3's test constructors to pass the new `nil` llm argument — change all `search.NewPreprocessor(tok, nil, cfg)` to `search.NewPreprocessor(tok, nil, nil, cfg)` in `testing/search/preprocess_test.go`.

- [ ] **Step 2: Write failing tests for LLM enhancement and fallback**

Append to `testing/search/preprocess_test.go` (add `"fmt"`, `"time"`, `"iclude/internal/llm"` to imports):

```go
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
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg) // nil graphStore, mock llm

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
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg) // nil graphStore, mock llm

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
	pp := search.NewPreprocessor(tok, nil, mockLLM, cfg) // nil graphStore, mock llm

	plan, err := pp.Process(context.Background(), "K8s deploy broken", "")
	require.NoError(t, err)
	// 解析失败，应保留规则式结果 / Parse fails, should keep rule-based result
	assert.Equal(t, "K8s deploy broken", plan.SemanticQuery)
}
```

Add imports at the top of the file: `"fmt"`, `"time"`, `"iclude/internal/llm"`.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./testing/search/ -run TestPreprocessor_LLM -v`
Expected: FAIL — `llmEnhance` method not defined yet

- [ ] **Step 4: Implement llmEnhance method**

Add to `internal/search/preprocess.go`:

```go
import (
	"encoding/json"
	// ... existing imports
)

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
```

Add `"time"`, `"encoding/json"`, `"iclude/internal/logger"`, and `"go.uber.org/zap"` to the import block in `preprocess.go`.

- [ ] **Step 5: Run all preprocessor tests**

Run: `go test ./testing/search/ -run TestPreprocessor -v`
Expected: PASS — all 7 test functions pass (4 from Task 3 + 3 new)

- [ ] **Step 6: Commit**

```bash
git add internal/search/preprocess.go testing/search/preprocess_test.go
git commit -m "feat: add LLM-enhanced query preprocessing with fallback"
```

---

### Task 5: Integrate Preprocessor into Retriever

**Files:**
- Modify: `internal/search/retriever.go:21-40` (struct + constructor) and `:44-159` (Retrieve method)

- [ ] **Step 1: Add preprocessor field to Retriever and update constructor**

In `internal/search/retriever.go`, add field to struct:

```go
type Retriever struct {
	memStore     store.MemoryStore
	vecStore     store.VectorStore
	embedder     store.Embedder
	graphStore   store.GraphStore
	llm          llm.Provider
	cfg          config.RetrievalConfig
	preprocessor *Preprocessor // 可为 nil / may be nil
}
```

Update constructor to accept preprocessor:

```go
func NewRetriever(memStore store.MemoryStore, vecStore store.VectorStore, embedder store.Embedder, graphStore store.GraphStore, llm llm.Provider, cfg config.RetrievalConfig, preprocessor *Preprocessor) *Retriever {
	return &Retriever{
		memStore:     memStore,
		vecStore:     vecStore,
		embedder:     embedder,
		graphStore:   graphStore,
		llm:          llm,
		cfg:          cfg,
		preprocessor: preprocessor,
	}
}
```

- [ ] **Step 2: Update all NewRetriever call sites**

In `cmd/server/main.go` (line 119), pass `nil` temporarily (Task 6 will replace with real preprocessor):

```go
ret := search.NewRetriever(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.GraphStore, llmProvider, cfg.Retrieval, nil)
```

Update all 6 test call sites to pass `nil` as the 7th argument:

- `testing/reflect/engine_test.go:96`:
  `search.NewRetriever(stores.MemoryStore, nil, nil, nil, nil, config.RetrievalConfig{}, nil)`
- `testing/api/reflect_test.go:52`:
  `search.NewRetriever(s, nil, nil, nil, nil, config.RetrievalConfig{}, nil)`
- `testing/search/graph_retrieval_test.go:75`:
  `search.NewRetriever(stores.MemoryStore, nil, nil, stores.GraphStore, mockLLM, cfg, nil)`
- `testing/search/graph_retrieval_test.go:228`:
  `search.NewRetriever(stores.MemoryStore, nil, nil, nil, nil, cfg, nil)`
- `testing/api/integration_test.go:44`:
  `search.NewRetriever(s, nil, nil, nil, nil, config.RetrievalConfig{}, nil)`
- `testing/api/handler_test.go:41`:
  `search.NewRetriever(s, nil, nil, nil, nil, config.RetrievalConfig{}, nil)`

- [ ] **Step 3: Integrate preprocessing into Retrieve method**

At the beginning of `Retrieve()`, after the limit validation (line ~56), add:

```go
	// 预处理 / Preprocessing
	var plan *QueryPlan
	if r.preprocessor != nil && req.Query != "" {
		scope := ""
		if req.Filters != nil {
			scope = req.Filters.Scope
		}
		var err error
		plan, err = r.preprocessor.Process(ctx, req.Query, scope)
		if err != nil {
			logger.Warn("preprocess failed, using original query", zap.Error(err))
		}
	}

	// 确定各通道输入 / Determine per-channel inputs
	ftsQuery := req.Query
	semanticQuery := req.Query
	if plan != nil {
		if len(plan.Keywords) > 0 {
			ftsQuery = strings.Join(plan.Keywords, " ")
		}
		if plan.SemanticQuery != "" {
			semanticQuery = plan.SemanticQuery
		}
	}
```

Then modify the three channel sections to use differentiated inputs:

**FTS channel** — replace `req.Query` with `ftsQuery` in the SQLite section:

```go
	if hasSQLite && ftsQuery != "" {
		var textResults []*model.SearchResult
		var err error
		if filters != nil {
			textResults, err = r.memStore.SearchTextFiltered(ctx, ftsQuery, filters, limit)
		} else {
			textResults, err = r.memStore.SearchText(ctx, ftsQuery, req.TeamID, limit)
		}
		// ... rest unchanged
		if err != nil {
			logger.Warn("text search failed", zap.Error(err))
		} else if len(textResults) > 0 {
			ftsWeight := r.cfg.FTSWeight
			if plan != nil {
				ftsWeight = plan.Weights.FTS
			}
			if ftsWeight == 0 {
				ftsWeight = 1.0
			}
			rrfInputs = append(rrfInputs, RRFInput{Results: textResults, Weight: ftsWeight})
		}
	}
```

**Qdrant channel** — Move the embedding resolution (currently at lines 62-74 before the channels) into the Qdrant channel block so it uses `semanticQuery`:

```go
	// Qdrant 向量检索（embedding 延迟到此处解析，使用预处理后的 semanticQuery）
	if hasVector {
		embedding, err := r.resolveEmbedding(ctx, req.Embedding, semanticQuery)
		if err != nil {
			logger.Warn("failed to resolve embedding for search, falling back to text-only", zap.Error(err))
			hasVector = false
		}
		if len(embedding) == 0 {
			hasVector = false
		}
```

Remove the old embedding resolution block at lines 62-74 (which used `req.Query`). The rest of the Qdrant section stays the same, plus use plan weights:

```go
		} else if len(vecResults) > 0 {
			qdrantWeight := r.cfg.QdrantWeight
			if plan != nil {
				qdrantWeight = plan.Weights.Qdrant
			}
			if qdrantWeight == 0 {
				qdrantWeight = 1.0
			}
			rrfInputs = append(rrfInputs, RRFInput{Results: vecResults, Weight: qdrantWeight})
		}
```

**Graph channel** — use pre-matched entities when available:

```go
	if graphEnabled && r.graphStore != nil && req.Query != "" {
		scope := ""
		if filters != nil {
			scope = filters.Scope
		}
		var graphResults []*model.SearchResult
		if plan != nil && len(plan.Entities) > 0 {
			// 预处理已匹配实体，跳过 FTS5 反查 / Preprocessor matched entities, skip FTS5 reverse lookup
			graphResults = r.graphRetrieveByEntities(ctx, plan.Entities, scope, limit)
		} else {
			graphResults = r.graphRetrieve(ctx, req.Query, req.TeamID, scope, limit)
		}
		if len(graphResults) > 0 {
			graphWeight := r.cfg.GraphWeight
			if plan != nil {
				graphWeight = plan.Weights.Graph
			}
			if graphWeight == 0 {
				graphWeight = 0.8
			}
			rrfInputs = append(rrfInputs, RRFInput{Results: graphResults, Weight: graphWeight})
		}
	}
```

- [ ] **Step 4: Refactor graph traversal to support pre-matched entities**

Extract the shared traversal logic (phases 2-4) from `graphRetrieve` into a private helper, then call it from both `graphRetrieve` and the new preprocessor path.

Add to `internal/search/retriever.go`:

```go
// graphTraverseAndCollect 从已知实体 ID 遍历图谱并收集关联记忆 / Traverse graph from entity IDs and collect associated memories
func (r *Retriever) graphTraverseAndCollect(ctx context.Context, seedEntityIDs map[string]bool, limit int) []*model.SearchResult {
	depth := r.cfg.GraphDepth
	if depth <= 0 {
		depth = 1
	}

	visited := make(map[string]int) // entityID → depth level
	currentEntities := make([]string, 0, len(seedEntityIDs))
	for id := range seedEntityIDs {
		visited[id] = 0
		currentEntities = append(currentEntities, id)
	}

	for d := 1; d <= depth; d++ {
		var nextEntities []string
		for _, entityID := range currentEntities {
			relations, err := r.graphStore.GetEntityRelations(ctx, entityID)
			if err != nil {
				logger.Warn("graph: GetEntityRelations failed", zap.String("entity_id", entityID), zap.Error(err))
				continue
			}
			for _, rel := range relations {
				for _, targetID := range []string{rel.SourceID, rel.TargetID} {
					if targetID == entityID {
						continue
					}
					if _, seen := visited[targetID]; !seen {
						visited[targetID] = d
						nextEntities = append(nextEntities, targetID)
					}
				}
			}
		}
		currentEntities = nextEntities
		if len(currentEntities) == 0 {
			break
		}
	}

	entityLimit := r.cfg.GraphEntityLimit
	if entityLimit <= 0 {
		entityLimit = 10
	}

	memoryMap := make(map[string]*model.Memory)
	memoryDepth := make(map[string]int)
	for entityID, d := range visited {
		memories, err := r.graphStore.GetEntityMemories(ctx, entityID, entityLimit)
		if err != nil {
			logger.Warn("graph: GetEntityMemories failed", zap.String("entity_id", entityID), zap.Error(err))
			continue
		}
		for _, mem := range memories {
			if _, exists := memoryMap[mem.ID]; !exists {
				memoryMap[mem.ID] = mem
				memoryDepth[mem.ID] = d
			} else if d < memoryDepth[mem.ID] {
				memoryDepth[mem.ID] = d
			}
		}
	}

	type depthMem struct {
		mem   *model.Memory
		depth int
	}
	var sorted []depthMem
	for id, mem := range memoryMap {
		sorted = append(sorted, depthMem{mem: mem, depth: memoryDepth[id]})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].depth < sorted[i].depth {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	results := make([]*model.SearchResult, 0, len(sorted))
	for _, dm := range sorted {
		results = append(results, &model.SearchResult{
			Memory: dm.mem,
			Score:  0,
			Source: "graph",
		})
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
```

Then refactor `graphRetrieve` to use it — replace phases 2-4 (lines 204-301) with:

```go
	// 原 graphRetrieve 中，在阶段 1/1.5 收集完 entityIDs 后：
	return r.graphTraverseAndCollect(ctx, entityIDs, limit)
```

And add a new thin wrapper for the preprocessor path:

```go
// graphRetrieveByEntities 从预匹配的实体 ID 开始图谱检索 / Graph retrieval from pre-matched entity IDs
func (r *Retriever) graphRetrieveByEntities(ctx context.Context, entityIDs []string, scope string, limit int) []*model.SearchResult {
	if len(entityIDs) == 0 {
		return nil
	}
	seedIDs := make(map[string]bool, len(entityIDs))
	for _, id := range entityIDs {
		seedIDs[id] = true
	}
	return r.graphTraverseAndCollect(ctx, seedIDs, limit)
}
```

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 6: Run all existing tests to check for regressions**

Run: `go test ./testing/... -v -count=1`
Expected: PASS — no regressions (existing tests pass `nil` for preprocessor)

- [ ] **Step 7: Commit**

```bash
git add internal/search/retriever.go cmd/server/main.go testing/reflect/engine_test.go testing/api/reflect_test.go testing/search/graph_retrieval_test.go testing/api/integration_test.go testing/api/handler_test.go
git commit -m "feat: integrate query preprocessor into retriever pipeline"
```

---

### Task 6: Wire Preprocessor in main.go

**Files:**
- Modify: `cmd/server/main.go:110-119`

- [ ] **Step 1: Add Preprocessor construction and update Retriever call**

In `cmd/server/main.go`, after the Extractor initialization block and before the Retriever construction, add:

```go
	// 初始化查询预处理器 / Initialize query preprocessor
	var preprocessor *search.Preprocessor
	if cfg.Retrieval.Preprocess.Enabled {
		preprocessor = search.NewPreprocessor(stores.Tokenizer, stores.GraphStore, llmProvider, cfg.Retrieval)
		logger.Info("query preprocessor initialized",
			zap.Bool("use_llm", cfg.Retrieval.Preprocess.UseLLM),
		)
	}
```

Replace the `nil` placeholder from Task 5 with the real preprocessor:

```go
	ret := search.NewRetriever(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.GraphStore, llmProvider, cfg.Retrieval, preprocessor)
```

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/server/`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire query preprocessor into server startup"
```

---

### Task 7: End-to-end verification

- [ ] **Step 1: Run full test suite**

Run: `go test ./testing/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 2: Run vet and fmt**

Run: `go vet ./... && go fmt ./...`
Expected: No issues

- [ ] **Step 3: Generate test report**

Run: `go test ./testing/report/ -v -count=1`
Expected: HTML report generated at `testing/report/report.html`

- [ ] **Step 4: Final commit if any formatting changes**

```bash
git add -A
git commit -m "chore: fmt and vet cleanup"
```
