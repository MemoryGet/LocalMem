# OMEGA 检索技巧移植 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 移植 OMEGA 的 5 个检索技巧到 LocalMem，将 500 组测试命中率从 84.2% 提升至 90%+

**Architecture:** 改动集中在 `internal/search/` 和 `internal/store/` 两个包。(1) FTS5 混合打分 (2) 时间独立 RRF 通道 (3) 类型权重后处理 (4) 自适应重试 (5) HyDE 查询扩展。每项改动独立可测，按依赖顺序实施。

**Tech Stack:** Go, SQLite FTS5, LLM (OpenAI-compatible)

---

## File Map

| 文件 | 职责 | 改动类型 |
|------|------|---------|
| `internal/store/sqlite.go` | FTS5 查询净化 + 二元组 | Modify |
| `internal/store/sqlite_memory_lifecycle.go` | SearchText/SearchTextFiltered 混合打分 | Modify |
| `internal/search/retriever.go` | 时间 RRF 通道 + 类型权重 + 自适应重试 | Modify |
| `internal/search/preprocess.go` | HyDE 查询扩展 + QueryPlan 新字段 | Modify |
| `internal/config/config.go` | 新配置项 | Modify |

---

### Task 1: FTS5 二元组增强 + 70/30 混合打分

**Goal:** FTS5 查询追加相邻词对短语，打分改为 70% BM25 + 30% 词覆盖率。

**Files:**
- Modify: `internal/store/sqlite.go:193-221` (sanitizeFTS5Query)
- Modify: `internal/store/sqlite_memory_lifecycle.go:312-454` (SearchText + SearchTextFiltered)

- [ ] **Step 1: 修改 sanitizeFTS5Query 添加二元组增强**

在 `internal/store/sqlite.go` 中，将 `sanitizeFTS5Query` 替换为：

```go
func sanitizeFTS5Query(query string) string {
	if query == "" {
		return query
	}
	// 移除 FTS5 特殊字符和操作符 / Remove FTS5 special chars
	replacer := strings.NewReplacer(
		`"`, ``, `*`, ``, `(`, ``, `)`, ``, `^`, ``, `-`, ` `, `+`, ` `,
	)
	cleaned := replacer.Replace(query)

	// 过滤 FTS5 保留字 / Filter reserved words
	words := strings.Fields(cleaned)
	var filtered []string
	for _, w := range words {
		if w == "" {
			continue
		}
		upper := strings.ToUpper(w)
		if upper == "AND" || upper == "OR" || upper == "NOT" || upper == "NEAR" {
			filtered = append(filtered, `"`+w+`"`)
		} else {
			filtered = append(filtered, w)
		}
	}
	if len(filtered) == 0 {
		return cleaned
	}

	// 单词用 OR 连接 / Join words with OR
	parts := make([]string, len(filtered))
	copy(parts, filtered)

	// 二元组增强：3+ 词时追加相邻词对短语，提升精确度 / Bigram boost: append adjacent word pairs for 3+ word queries
	if len(filtered) >= 3 {
		for i := 0; i < len(filtered)-1; i++ {
			parts = append(parts, `"`+filtered[i]+" "+filtered[i+1]+`"`)
		}
	}

	return strings.Join(parts, " OR ")
}
```

- [ ] **Step 2: 修改 SearchText 添加词覆盖率混合打分**

在 `internal/store/sqlite_memory_lifecycle.go` 的 `SearchText` 方法中，将结果构建逻辑从纯 BM25 改为 70/30 混合。

在 `SearchText` 方法中，找到结果构建部分（约 351-361 行），替换为：

```go
	queryWords := extractQueryWords(query)
	var results []*model.SearchResult
	for rows.Next() {
		mem, rank, err := s.scanMemoryWithRank(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}
		bm25Score := -rank
		coverageScore := wordCoverage(mem.Content+" "+mem.Abstract, queryWords)
		hybridScore := 0.7*bm25Score + 0.3*coverageScore*bm25Score
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  hybridScore,
			Source: "sqlite",
		})
	}
```

- [ ] **Step 3: 修改 SearchTextFiltered 同理添加词覆盖率混合打分**

在 `SearchTextFiltered` 方法中（约 438-448 行），同样替换结果构建：

```go
	queryWords := extractQueryWords(query)
	var results []*model.SearchResult
	for rows.Next() {
		mem, rank, err := s.scanMemoryWithRank(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan filtered search result: %w", err)
		}
		bm25Score := -rank
		coverageScore := wordCoverage(mem.Content+" "+mem.Abstract, queryWords)
		hybridScore := 0.7*bm25Score + 0.3*coverageScore*bm25Score
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  hybridScore,
			Source: "sqlite",
		})
	}
```

- [ ] **Step 4: 添加 extractQueryWords 和 wordCoverage 辅助函数**

在 `internal/store/sqlite.go` 中（`sanitizeFTS5Query` 之后）添加：

```go
// extractQueryWords 提取查询中的有效词（去重、小写化）/ Extract unique lowercased words from query
func extractQueryWords(query string) []string {
	seen := make(map[string]bool)
	var words []string
	for _, w := range strings.Fields(query) {
		lower := strings.ToLower(w)
		if lower != "" && !seen[lower] {
			seen[lower] = true
			words = append(words, lower)
		}
	}
	return words
}

// wordCoverage 计算文档对查询词的覆盖率 / Calculate query word coverage ratio in document
// 返回 0.0~1.0，表示查询词中有多少比例出现在文档中
func wordCoverage(doc string, queryWords []string) float64 {
	if len(queryWords) == 0 {
		return 0
	}
	docLower := strings.ToLower(doc)
	matched := 0
	for _, w := range queryWords {
		if strings.Contains(docLower, w) {
			matched++
		}
	}
	return float64(matched) / float64(len(queryWords))
}
```

- [ ] **Step 5: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`

- [ ] **Step 6: Commit**

```bash
git add internal/store/sqlite.go internal/store/sqlite_memory_lifecycle.go
git commit -m "feat: FTS5 bigram boost + 70/30 BM25-coverage hybrid scoring (OMEGA port)"
```

---

### Task 2: 时间独立 RRF 通道

**Goal:** 将时间从"事后过滤"改为"独立 RRF 通道"，权重 1.2，用渐变衰减代替硬过滤。

**Files:**
- Modify: `internal/search/retriever.go:66-258` (Retrieve method)
- Modify: `internal/search/preprocess.go:39-47` (QueryPlan struct)

- [ ] **Step 1: 在 QueryPlan 中添加时间范围字段**

在 `internal/search/preprocess.go` 的 `QueryPlan` struct 中添加：

```go
type QueryPlan struct {
	OriginalQuery string
	SemanticQuery string
	Keywords      []string
	Entities      []string
	Intent        QueryIntent
	Weights       ChannelWeights
	Temporal      bool
	TemporalCenter *time.Time // 时间查询的中心点 / Center point for temporal queries
	TemporalRange  time.Duration // 时间查询的范围 / Duration range for temporal queries
}
```

需要在文件顶部添加 `"time"` import（如果尚未引入）。

- [ ] **Step 2: 在 Preprocessor.Process 中解析时间范围**

在 `preprocess.go` 的 `Process` 方法中，步骤 4.5（temporal 标记）之后，添加时间范围解析：

```go
	// 步骤 4.5: temporal 标记 + 时间范围 / Mark temporal and set time range
	if plan.Intent == IntentTemporal {
		plan.Temporal = true
		now := time.Now().UTC()
		plan.TemporalCenter = &now
		plan.TemporalRange = 7 * 24 * time.Hour // 默认 7 天范围 / Default 7-day range
	}
```

- [ ] **Step 3: 在 Retriever.Retrieve 中添加时间 RRF 通道**

在 `internal/search/retriever.go` 的 `Retrieve` 方法中，删除旧的 temporal 过滤注入（约 107-116 行）：

```go
		// [删除] 旧代码: temporal 意图注入时间过滤
		// if plan.Temporal && filters == nil { ... }
```

在 Graph 通道之后、`if len(rrfInputs) == 0` 之前（约 206 行），添加时间通道：

```go
	// 时间通道（独立参与 RRF 融合）/ Temporal channel as independent RRF input
	if plan != nil && plan.Temporal && plan.TemporalCenter != nil && hasSQLite {
		temporalResults := r.temporalRetrieve(ctx, req, plan, limit)
		if len(temporalResults) > 0 {
			rrfInputs = append(rrfInputs, RRFInput{Results: temporalResults, Weight: 1.2})
		}
	}
```

- [ ] **Step 4: 实现 temporalRetrieve 方法**

在 `retriever.go` 中，`backfillMemories` 方法之前，添加：

```go
// temporalRetrieve 时间通道检索 / Temporal channel retrieval
// 查询时间范围内的记忆，用距离衰减打分 / Query memories in time range with distance-based scoring
func (r *Retriever) temporalRetrieve(ctx context.Context, req *model.RetrieveRequest, plan *QueryPlan, limit int) []*model.SearchResult {
	center := *plan.TemporalCenter
	rangeD := plan.TemporalRange
	if rangeD <= 0 {
		rangeD = 7 * 24 * time.Hour
	}

	// 查询范围扩大 3 倍以捕获边缘记忆 / Expand window 3x to catch edge memories
	expandedRange := rangeD * 3
	after := center.Add(-expandedRange)
	before := center.Add(expandedRange)

	filters := &model.SearchFilters{
		HappenedAfter:  &after,
		HappenedBefore: &before,
		TeamID:         req.TeamID,
		OwnerID:        req.OwnerID,
	}

	memories, err := r.memStore.ListByFilters(ctx, filters, limit*2)
	if err != nil {
		logger.Warn("temporal retrieve failed", zap.Error(err))
		return nil
	}

	rangeDays := rangeD.Hours() / 24
	if rangeDays < 1 {
		rangeDays = 1
	}

	var results []*model.SearchResult
	for _, mem := range memories {
		ts := mem.HappenedAt
		if ts.IsZero() {
			ts = mem.CreatedAt
		}
		daysAway := center.Sub(ts).Hours() / 24
		if daysAway < 0 {
			daysAway = -daysAway
		}

		var score float64
		if daysAway <= rangeDays {
			score = 1.0
		} else {
			score = 1.0 / (1.0 + daysAway/rangeDays)
		}

		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  score,
			Source: "temporal",
		})
	}

	// 按分数排序 / Sort by score desc
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
```

- [ ] **Step 5: 检查 ListByFilters 是否存在**

需要确认 `MemoryStore` 接口是否有 `ListByFilters` 方法。如果没有，用现有的 `List` 方法加时间参数代替，或直接用 `SearchTextFiltered` 空查询。查看 store 接口确定方案后实现。

- [ ] **Step 6: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`

- [ ] **Step 7: Commit**

```bash
git add internal/search/retriever.go internal/search/preprocess.go
git commit -m "feat: temporal as independent RRF channel with distance-decay scoring (OMEGA port)"
```

---

### Task 3: Kind 类型权重后处理

**Goal:** RRF 融合后按记忆 kind 乘以权重系数（skill/决策类 1.5x），简单有效。

**Files:**
- Modify: `internal/search/retriever.go` (Retrieve method, 在强度加权之前)

- [ ] **Step 1: 添加 kindWeight 映射和 applyKindWeights 函数**

在 `internal/search/retriever.go` 中（`EstimateTokens` 之前）添加：

```go
// kindWeights 记忆类型权重 / Memory kind weights
// skill（经验教训）和决策类权重更高 / Higher weights for skill (lessons) and decisions
var kindWeights = map[string]float64{
	"skill":   1.5,  // 经验教训 / Lessons learned
	"profile": 1.2,  // 用户画像 / User profile
	"fact":    1.0,  // 事实 / Facts
	"note":    1.0,  // 笔记 / Notes
}

// subKindWeights 子类型权重加成 / Sub-kind weight boost
var subKindWeights = map[string]float64{
	"pattern": 1.3, // 设计决策 / Design decisions
	"case":    1.3, // 案例经验 / Case experience
}

// applyKindWeights 按记忆类型加权 / Weight results by memory kind
func applyKindWeights(results []*model.SearchResult) []*model.SearchResult {
	for _, r := range results {
		if r.Memory == nil {
			continue
		}
		w := 1.0
		if kw, ok := kindWeights[r.Memory.Kind]; ok {
			w = kw
		}
		if sw, ok := subKindWeights[r.Memory.SubKind]; ok {
			w *= sw
		}
		r.Score *= w
	}
	return results
}
```

- [ ] **Step 2: 在 Retrieve 方法中插入类型权重**

在 `retriever.go` 的 `Retrieve` 方法中，`backfillMemories` 之后、`ApplyStrengthWeighting` 之前（约 230 行），添加：

```go
	// 类型权重（skill/决策类提权）/ Kind-based weighting (boost skill/decision types)
	results = applyKindWeights(results)
```

- [ ] **Step 3: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`

- [ ] **Step 4: Commit**

```bash
git add internal/search/retriever.go
git commit -m "feat: kind-based weight boost for skill/decision memories (OMEGA port)"
```

---

### Task 4: 自适应重试

**Goal:** 当首次检索结果置信度低（top score < 阈值）时，放宽条件重试。

**Files:**
- Modify: `internal/search/retriever.go` (Retrieve method, 末尾)

- [ ] **Step 1: 添加自适应重试逻辑**

在 `retriever.go` 的 `Retrieve` 方法中，MMR 重排之后、异步访问追踪之前（约 247 行），添加：

```go
	// 自适应重试：置信度低时放宽条件重查 / Adaptive retry: relax constraints when confidence is low
	if len(results) > 0 && results[0].Score < 0.3 && req.Query != "" && filters != nil {
		logger.Debug("adaptive retry: low confidence, retrying without time filter",
			zap.Float64("top_score", results[0].Score),
		)
		retryReq := *req
		retryFilters := *filters
		retryFilters.HappenedAfter = nil
		retryFilters.HappenedBefore = nil
		retryFilters.MinStrength = filters.MinStrength * 0.6
		retryReq.Filters = &retryFilters
		retryResults, err := r.Retrieve(ctx, &retryReq)
		if err == nil && len(retryResults) > 0 && retryResults[0].Score > results[0].Score {
			results = retryResults
		}
	}
```

注意：需要防止无限递归。在 `RetrieveRequest` 中加一个标记。

- [ ] **Step 2: 在 RetrieveRequest 中添加 noRetry 标记防递归**

在 `internal/model/memory.go` 的 `RetrieveRequest` struct 中添加：

```go
	NoRetry bool `json:"-"` // 内部标记，防止自适应重试递归 / Internal flag to prevent adaptive retry recursion
```

然后在 Step 1 的重试逻辑前加条件：

```go
	if len(results) > 0 && results[0].Score < 0.3 && req.Query != "" && filters != nil && !req.NoRetry {
```

并在 `retryReq` 设置中加：

```go
		retryReq.NoRetry = true
```

- [ ] **Step 3: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`

- [ ] **Step 4: Commit**

```bash
git add internal/search/retriever.go internal/model/memory.go
git commit -m "feat: adaptive retry with relaxed constraints on low confidence (OMEGA port)"
```

---

### Task 5: HyDE 查询扩展

**Goal:** 让 LLM 生成一段"假设性回答文档"，用这段文档作为额外的 FTS 查询参与 RRF 融合，显著提升隐式/间接查询命中率。

**Files:**
- Modify: `internal/search/preprocess.go` (QueryPlan struct + llmEnhance method)
- Modify: `internal/search/retriever.go` (Retrieve method, 添加 HyDE 通道)

- [ ] **Step 1: 在 QueryPlan 中添加 HyDE 字段**

在 `internal/search/preprocess.go` 的 `QueryPlan` struct 中添加：

```go
	HyDEDoc string // LLM 生成的假设性回答文档 / Hypothetical Document for HyDE retrieval
```

- [ ] **Step 2: 在 llmEnhance 中生成 HyDE 文档**

在 `preprocess.go` 的 `llmEnhance` 方法末尾（合并关键词之后），添加 HyDE 生成：

```go
	// HyDE: 生成假设性回答文档 / Generate Hypothetical Document Embedding
	hydeCtx, hydeCancel := context.WithTimeout(context.Background(), timeout)
	defer hydeCancel()

	hydeResp, err := p.llm.Chat(hydeCtx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "你是一个记忆系统。根据用户的问题，写出一段可能存在于记忆库中的文档片段（50-100字）。直接输出内容，不加前缀。用中文回答。"},
			{Role: "user", Content: plan.OriginalQuery},
		},
		Temperature: &temp,
	})
	if err != nil {
		logger.Debug("preprocess: HyDE generation failed", zap.Error(err))
	} else if hydeResp.Content != "" {
		plan.HyDEDoc = strings.TrimSpace(hydeResp.Content)
	}
```

- [ ] **Step 3: 在 Retriever 中添加 HyDE FTS 通道**

在 `retriever.go` 的 `Retrieve` 方法中，SQLite 全文检索通道之后（约 142 行），添加 HyDE 通道：

```go
	// HyDE 通道：用 LLM 假设性回答做 FTS 检索，0.8 折扣权重 / HyDE channel: FTS with hypothetical doc, 0.8 discount
	if hasSQLite && plan != nil && plan.HyDEDoc != "" {
		var hydeResults []*model.SearchResult
		var err error
		if filters != nil {
			hydeResults, err = r.memStore.SearchTextFiltered(ctx, plan.HyDEDoc, filters, limit)
		} else {
			hydeResults, err = r.memStore.SearchText(ctx, plan.HyDEDoc, r.resolveIdentity(req), limit)
		}
		if err != nil {
			logger.Debug("HyDE search failed", zap.Error(err))
		} else if len(hydeResults) > 0 {
			hydeWeight := 0.8
			if plan != nil {
				hydeWeight *= plan.Weights.FTS
			}
			if hydeWeight == 0 {
				hydeWeight = 0.8
			}
			rrfInputs = append(rrfInputs, RRFInput{Results: hydeResults, Weight: hydeWeight})
		}
	}
```

- [ ] **Step 4: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`

- [ ] **Step 5: Commit**

```bash
git add internal/search/preprocess.go internal/search/retriever.go
git commit -m "feat: HyDE query expansion for implicit/indirect query boost (OMEGA port)"
```

---

### Task 6: 重新运行 500 组测试

**Files:**
- Existing: `tools/retrieval_test_500.py`

- [ ] **Step 1: 重建并启动**

```bash
pkill -f "./server" 2>/dev/null; sleep 2
rm -f data/iclude.db
go build -o server ./cmd/server/
# 临时关 auth
sed -i 's/enabled: true/enabled: false/' config.yaml
sed -i 's/auth_enabled: true/auth_enabled: false/' config.yaml
./server > /dev/null 2>&1 &
sleep 5
```

- [ ] **Step 2: 运行测试**

```bash
python3 tools/retrieval_test_500.py
```

目标:
- 总命中率: 84.2% → **90%+**
- 跨语言: 77.5% → **80%+**
- 隐含/间接: 47.5% → **65%+**（HyDE 最大价值）
- 上下文推理: 72.0% → **80%+**
- 困难级: 63.2% → **72%+**

- [ ] **Step 3: 恢复 auth 配置**

```bash
sed -i 's/enabled: false/enabled: true/' config.yaml
sed -i 's/auth_enabled: false/auth_enabled: true/' config.yaml
```

- [ ] **Step 4: Commit 报告**

```bash
git add tools/retrieval_report.html tools/retrieval_results.json
git commit -m "test: 500-query benchmark after OMEGA retrieval port — target 90%+"
```
