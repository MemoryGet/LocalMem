# 可组装检索管线 + 策略 Agent 设计

> Date: 2026-04-08
> Status: Draft
> Author: Tao + Claude

## 1. 背景与问题

### 现状

当前检索架构为四通道并行（FTS / Qdrant / Graph / Temporal）+ RRF 盲目融合。核心问题：

1. **FTS 无相关性过滤**：查询"点券在数据库的哪个字段"会匹配所有含"数据库"或"字段"的记忆，召回大量无关内容
2. **RRF 融合无阈值**：低相关性结果不经过滤直接返回
3. **单一管线无法适配所有场景**：精确实体查找、模糊探索、时间回溯、关系追踪需要不同检索策略
4. **Qdrant 有 0.3 阈值保护，FTS 完全裸奔**

### 目标

- 内置多条针对不同查询场景优化的管线
- 策略 Agent 自动选择最优管线
- 无 LLM 时规则分类器 fallback
- 用户可通过请求参数 override
- 每条管线内部可观测、可降级
- 向后兼容：零配置即可用

## 2. 架构总览

```
Query 进入
  │
  ▼
┌─────────────────────────────────────┐
│ Strategy Agent（一次 LLM 调用）       │
│  输出: pipeline 选择 + 预处理结果     │
│  (无 LLM → 规则分类器 fallback)      │
│  (请求指定 pipeline → 跳过 agent)    │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ Pipeline Executor                    │
│  执行选中管线的 stage 序列           │
│  结果为空 → 沿降级链尝试下一管线     │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│ 固定后处理尾巴（所有管线共享）        │
│  weight → mmr → core_inject → trim  │
└─────────────────────────────────────┘
               │
               ▼
           返回结果 + 置信度 + trace
```

## 3. Stage 抽象

### 3.1 接口定义

```go
// Stage 检索管线阶段接口 / Pipeline stage interface
type Stage interface {
    Name() string
    Execute(ctx context.Context, state *PipelineState) (*PipelineState, error)
}

// PipelineState 在 Stage 之间流转的状态 / State flowing between stages
type PipelineState struct {
    Query        string
    Identity     *model.Identity
    Plan         *QueryPlan              // 预处理结果（来自 Strategy Agent）
    Candidates   []*model.SearchResult   // 当前候选集
    Confidence   string                  // "high" | "low" | "none"
    Metadata     map[string]interface{}  // 自由扩展
    Traces       []StageTrace            // 各 stage 执行记录
    PipelineName string                  // 选中的管线名称
}

// StageTrace 单个 stage 的执行记录 / Execution trace for a single stage
type StageTrace struct {
    Name        string        // stage 名称 / Stage name
    Duration    time.Duration // 耗时 / Duration
    InputCount  int           // 输入候选数 / Input candidate count
    OutputCount int           // 输出候选数 / Output candidate count
    Skipped     bool          // 是否因依赖缺失跳过 / Skipped due to missing dependency
    Note        string        // 附加信息 / Additional info
}
```

### 3.2 可用 Stage 清单

| Stage 名称 | 类型 | 职责 | 延迟 | 外部依赖 |
|------------|------|------|------|---------|
| `graph` | 检索 | 实体匹配 → 图遍历 → 关联记忆 | ~10ms | GraphStore |
| `fts` | 检索 | BM25 + FTS5 全文检索 | ~5ms | MemoryStore |
| `vector` | 检索 | Qdrant 语义相似度检索 | ~20ms | VectorStore + Embedder |
| `temporal` | 检索 | 时间中心 + 距离衰减 | ~5ms | MemoryStore |
| `merge` | 合并 | RRF / GraphAware / Union 去重 | ~1ms | 无 |
| `score_filter` | 过滤 | score_ratio 砍尾 | ~0ms | 无 |
| `rerank_overlap` | 精排 | 轻量词重叠度重排 | ~1ms | 无 |
| `rerank_remote` | 精排 | 调用外部 rerank API | ~200ms | Remote API |
| `rerank_llm` | 精排 | LLM 相关性打分 + 过滤 | ~500ms | LLM Provider |
| `rerank_graph` | 精排 | 实体图距离打分 + 过滤 | ~10ms | GraphStore |

### 3.3 固定后处理尾巴（所有管线共享，不可省略）

| Stage | 职责 |
|-------|------|
| `weight` | kind / memory_class / scope / strength 加权 |
| `mmr` | 多样性重排（需 VectorStore，否则跳过） |
| `core_inject` | 置顶 core 记忆 |
| `trim` | 按 token 预算裁剪 |

## 4. 内置管线

### 4.1 管线定义

#### `precision` — 实体精确查找

适用：查找特定实体/字段/配置的精确位置
示例："点券在数据库的哪个字段"、"getUserById 在哪个文件"

```
parallel:
  - graph(max_depth=2, limit=30)
  - fts(limit=30)
→ merge(strategy=graph_aware)
→ score_filter(min_score_ratio=0.3)
→ rerank_graph(min_graph_score=0.2)
→ [固定尾巴]
```

降级链：`precision → exploration`

#### `exploration` — 广泛探索 / 综述

适用：浏览近期活动、总结、进展回顾
示例："最近的项目进展"、"上周做了什么"

```
parallel:
  - fts(limit=30)
  - temporal(limit=30)
→ merge(strategy=rrf)
→ score_filter(min_score_ratio=0.2)
→ rerank_overlap(top_k=20)
→ [固定尾巴]
```

降级链：无（兜底管线）

#### `semantic` — 模糊/概念性查询

适用：模糊联想、概念探索、经验搜索
示例："和用户体验相关的决策"、"类似的 bug 处理经验"

```
parallel:
  - vector(min_score=0.3, limit=30)
  - fts(limit=20)
→ merge(strategy=rrf)
→ score_filter(min_score_ratio=0.3)
→ rerank_overlap(top_k=20)
→ [固定尾巴]
```

降级链：`semantic → exploration`（vector 不可用时）

#### `association` — 关系/依赖查询

适用：追踪关联、依赖关系、影响范围
示例："这个模块依赖哪些组件"、"谁引用了这个接口"

```
graph(max_depth=3, limit=50)
→ rerank_graph
→ score_filter(min_score_ratio=0.2)
→ [固定尾巴]
```

降级链：`association → precision`

#### `fast` — 高频快速检索

适用：MCP 高频调用、简单事实查找
示例："recall recent context"、"项目名称"

```
fts(limit=10)
→ score_filter(min_score_ratio=0.3)
→ [固定尾巴: trim(max_tokens=2000)]
```

降级链：无

#### `full` — 最高精度

适用：重要决策、反思、需要最高召回精度的场景
示例：reflect 工具调用、关键决策支持

```
parallel:
  - graph(max_depth=2, limit=30)
  - fts(limit=30)
  - vector(min_score=0.3, limit=30)
→ merge(strategy=graph_aware)
→ score_filter(min_score_ratio=0.3)
→ rerank_llm(top_k=20, min_relevance=0.3)
→ [固定尾巴]
```

降级链：`full → precision`（LLM 不可用时跳过 rerank_llm，降级到 precision 管线逻辑）

### 4.2 管线降级链总览

```
full ──(LLM不可用)──→ precision
precision ──(结果空)──→ exploration
association ──(结果空)──→ precision ──→ exploration
semantic ──(vector不可用)──→ exploration
exploration  (兜底，不再降级)
fast  (兜底，不再降级)
```

## 5. Strategy Agent

### 5.1 合并调用：策略选择 + 查询预处理

一次 LLM 调用同时完成管线选择和查询预处理（~100 token 输入 + ~80 token 输出）：

```
系统提示:
你是检索策略选择器。根据查询分析意图，选择最合适的检索管线，并提取检索关键信息。

管线选项:
- precision: 查找特定实体/字段/配置的精确位置
- exploration: 浏览近期活动、总结、进展
- semantic: 模糊概念、相关性、类似经验
- association: 依赖关系、连接、影响范围
- fast: 简单事实查找
- full: 需要最高精度的重要查询

用户查询: {query}

以 JSON 格式返回:
{
  "pipeline": "管线名称",
  "keywords": ["关键词列表"],
  "entities": ["识别到的实体"],
  "semantic_query": "语义改写后的查询（用于向量检索）",
  "intent": "keyword|semantic|temporal|relational|general"
}
```

### 5.2 规则分类器（无 LLM Fallback）

复用现有 `Preprocessor` 的意图识别 + 增强规则：

```go
func (s *RuleClassifier) Select(query string, plan *QueryPlan) string {
    // 1. 查询含已知实体名 → precision
    // 2. 查询 < 5 字 → fast
    // 3. 时间相关词（"最近"/"上周"/"昨天"）→ exploration
    // 4. 关系词（"依赖"/"关联"/"影响"）→ association
    // 5. 来自 reflect 工具 → full
    // 6. plan.Intent 映射:
    //    IntentKeyword    → precision
    //    IntentSemantic   → semantic
    //    IntentTemporal   → exploration
    //    IntentRelational → association
    //    IntentGeneral    → exploration
}
```

### 5.3 请求级 Override

```go
type RetrieveRequest struct {
    // ...现有字段
    Pipeline string `json:"pipeline,omitempty"` // 可选，强制指定管线名称
    Debug    bool   `json:"debug,omitempty"`    // 可选，返回 trace 信息
}
```

指定 `pipeline` 时跳过 Strategy Agent，直接执行指定管线。

## 6. 关键 Stage 实现细节

### 6.1 merge — GraphAware 策略

替代现有的盲目 RRF。核心区别：根据候选来源赋予不同信任度。

```
graph + fts 双命中  → score × 1.5（交叉验证加成）
graph 单命中        → score × 1.0（图关联可信）
fts 单命中          → score × 0.8（词法匹配，需降权）
vector 单命中       → score × 1.0（语义匹配可信）
```

仍基于 RRF 公式：`score(id) = Σ source_weight × 1/(k + rank + 1)`，但 `source_weight` 按来源组合动态调整。

### 6.2 rerank_graph — 图距离精排

对候选集中每条结果，计算其关联实体与查询实体的图距离：

| 关系 | 分数 |
|------|------|
| 直接实体重叠 | 1.0 |
| 1-hop 邻居 | 0.7 |
| 2-hop 邻居 | 0.4 |
| 无连接 | 0.0 |

多个查询实体命中时取最高分。最终分数与原始分数加权混合：
`final = (1-w) × base_norm + w × graph_distance_score`，`w` 默认 0.6。

过滤 `graph_distance_score < min_graph_score`（默认 0.2）的结果。

### 6.3 rerank_llm — LLM 精排 + 守门

对 top-K 候选调用 LLM 打相关性分：

```
系统: 对每条记忆评估与查询的相关性（0.0~1.0）。
用户:
  查询: "{query}"
  候选:
    [0] "{content_0}"
    [1] "{content_1}"
    ...
返回: [{"index":0,"score":0.95},{"index":1,"score":0.1},...]
```

- 过滤 `score < min_relevance`（默认 0.3）
- 混合分数：`final = (1-w) × base_norm + w × llm_score`，`w` 默认 0.7
- 置信度标记：top1 >= 0.6 → `"high"` | 0.3~0.6 → `"low"` | 全过滤 → `"none"`
- 熔断器：连续 3 次失败自动熔断 30s，fallback 到 `rerank_overlap`

### 6.4 score_filter — 相对比例过滤

```go
func (s *ScoreFilterStage) Execute(ctx context.Context, state *PipelineState) (*PipelineState, error) {
    if len(state.Candidates) == 0 {
        return state, nil
    }
    topScore := state.Candidates[0].Score
    threshold := topScore * s.minScoreRatio
    filtered := make([]*model.SearchResult, 0, len(state.Candidates))
    for _, c := range state.Candidates {
        if c.Score >= threshold {
            filtered = append(filtered, c)
        }
    }
    state.Candidates = filtered
    return state, nil
}
```

## 7. 管线降级机制

```go
func (e *Executor) Execute(ctx context.Context, pipelineName string, state *PipelineState) (*PipelineState, error) {
    pipeline := e.registry.Get(pipelineName)
    if pipeline == nil {
        return nil, fmt.Errorf("unknown pipeline: %s", pipelineName)
    }

    state.PipelineName = pipelineName

    // 执行管线的可变部分
    result, err := e.executeStages(ctx, pipeline.Stages, state)
    if err != nil {
        return nil, err
    }

    // 结果为空 + 有降级链 → 尝试下一管线
    if len(result.Candidates) == 0 && pipeline.Fallback != "" {
        result.Traces = append(result.Traces, StageTrace{
            Name: "fallback",
            Note: fmt.Sprintf("empty results, falling back to %s", pipeline.Fallback),
        })
        return e.Execute(ctx, pipeline.Fallback, state) // 递归，state 保留原始查询
    }

    // 执行固定后处理尾巴
    result, err = e.executePostProcess(ctx, result)
    if err != nil {
        return nil, err
    }

    return result, nil
}
```

降级链最大深度限制为 3，防止循环。

## 8. 可观测性

### 8.1 Trace 输出

每个 stage 执行时自动记录 trace：

```go
func executeWithTrace(ctx context.Context, stage Stage, state *PipelineState) (*PipelineState, error) {
    inputCount := len(state.Candidates)
    start := time.Now()

    result, err := stage.Execute(ctx, state)

    trace := StageTrace{
        Name:        stage.Name(),
        Duration:    time.Since(start),
        InputCount:  inputCount,
        OutputCount: len(result.Candidates),
    }
    result.Traces = append(result.Traces, trace)
    return result, err
}
```

### 8.2 API 响应

请求 `debug: true` 时返回完整 trace：

```json
{
  "results": [...],
  "confidence": "high",
  "debug": {
    "pipeline": "precision",
    "strategy_reason": "query contains known entity '点券'",
    "fallback_used": false,
    "stages": [
      {"name": "graph", "ms": 8, "in": 0, "out": 12, "skipped": false},
      {"name": "fts", "ms": 5, "in": 0, "out": 23, "skipped": false},
      {"name": "merge", "ms": 1, "in": 35, "out": 28},
      {"name": "score_filter", "ms": 0, "in": 28, "out": 15},
      {"name": "rerank_graph", "ms": 3, "in": 15, "out": 8},
      {"name": "weight", "ms": 0, "in": 8, "out": 8},
      {"name": "core_inject", "ms": 2, "in": 8, "out": 10},
      {"name": "trim", "ms": 0, "in": 10, "out": 7}
    ],
    "total_ms": 19
  }
}
```

默认不返回 debug 信息，生产环境通过日志记录（Debug 级别）。

## 9. Stage 内部降级

每个 stage 依赖缺失时自动跳过，不阻断管线：

| Stage | 依赖 | 缺失行为 |
|-------|------|---------|
| `graph` | GraphStore | 跳过，trace.Skipped=true |
| `vector` | VectorStore + Embedder | 跳过 |
| `rerank_llm` | LLM Provider | fallback 到 rerank_overlap |
| `rerank_graph` | GraphStore | 跳过 |
| `mmr` | VectorStore | 跳过 |
| `temporal` | MemoryStore + Plan.Temporal | 跳过（无时间信息时） |

## 10. 配置

### 10.1 最小配置（零配置即可用）

不配置 `pipeline` 相关字段 → 使用默认行为：
- 有 LLM → Strategy Agent 自动选管线
- 无 LLM → 规则分类器选管线
- 所有内置管线使用默认参数

### 10.2 完整配置

```yaml
retrieval:
  # 策略 Agent 配置
  strategy:
    use_llm: true                    # 启用 LLM 策略选择（默认 true，无 LLM 自动降级为规则）
    fallback_pipeline: "exploration" # 规则分类也失败时的兜底管线

  # 覆盖内置管线的 stage 参数
  pipelines:
    precision:
      graph_depth: 2
      graph_limit: 30
      fts_limit: 30
      score_ratio: 0.3
      graph_rerank_min_score: 0.2
    exploration:
      fts_limit: 30
      temporal_limit: 30
      score_ratio: 0.2
      rerank_top_k: 20
    semantic:
      vector_min_score: 0.3
      vector_limit: 30
      fts_limit: 20
      score_ratio: 0.3
    association:
      graph_depth: 3
      graph_limit: 50
      score_ratio: 0.2
    fast:
      fts_limit: 10
      trim_max_tokens: 2000
    full:
      graph_depth: 2
      rerank_top_k: 20
      rerank_min_relevance: 0.3
      trim_max_tokens: 4000

  # 现有配置保持兼容（映射到对应 stage 参数）
  fts_weight: 1.0
  qdrant_weight: 1.0
  graph_weight: 0.8
  graph_enabled: true
  graph_depth: 2
  access_alpha: 0.1
  rerank:
    enabled: true
    provider: "overlap"
    top_k: 20
    score_weight: 0.7
  mmr:
    enabled: false
    lambda: 0.7
  preprocess:
    enabled: true
    use_llm: false
```

## 11. 代码结构

```
internal/search/
  pipeline/
    pipeline.go          — Pipeline 定义 + Executor（编排 stage + 降级链 + trace）
    state.go             — PipelineState + StageTrace
    registry.go          — 管线注册表（name → Pipeline 映射）
    builtin.go           — 6 条内置管线定义 + 固定后处理尾巴
  strategy/
    agent.go             — StrategyAgent（LLM 选择 + 预处理合并调用）
    rules.go             — RuleClassifier（无 LLM fallback）
  stage/
    stage.go             — Stage 接口
    graph.go             — 图关联检索
    fts.go               — FTS5 全文检索
    vector.go            — Qdrant 向量检索
    temporal.go          — 时间检索
    merge.go             — RRF / GraphAware / Union
    filter.go            — score_ratio 过滤
    rerank_overlap.go    — 词覆盖度精排
    rerank_remote.go     — 远程 API 精排
    rerank_llm.go        — LLM 精排 + 守门
    rerank_graph.go      — 图距离精排
    weight.go            — kind/class/scope/strength 加权
    mmr.go               — 多样性重排
    core.go              — Core 记忆注入
    trim.go              — Token 裁剪
  retriever.go           — Retrieve() 改为: strategy.Select() → pipeline.Execute()
```

### 现有代码迁移

| 现有文件 | 迁移目标 |
|---------|---------|
| `retriever.go` 四通道并行逻辑 | 拆分到 `stage/graph.go`, `stage/fts.go`, `stage/vector.go`, `stage/temporal.go` |
| `rrf.go` | 移入 `stage/merge.go`，新增 GraphAware 策略 |
| `reranker.go` + `reranker_common.go` | 移入 `stage/rerank_overlap.go` |
| `reranker_remote.go` + `circuit_breaker.go` | 移入 `stage/rerank_remote.go`，熔断器提取到 `pipeline/` 共享 |
| `retriever_weights.go` | 移入 `stage/weight.go` |
| `retriever_util.go` (TrimByTokenBudget, backfill) | `trim.go` + `pipeline/pipeline.go` |
| `mmr.go` | 移入 `stage/mmr.go` |
| `preprocess.go` | 移入 `strategy/agent.go`（LLM 模式合并）+ `strategy/rules.go` |

## 12. 向后兼容

| 场景 | 行为 |
|------|------|
| 不配置 `retrieval.pipeline` 相关字段 | 使用默认管线（strategy agent + 内置管线） |
| 现有 `retrieval.fts_weight` 等旧配置 | 自动映射到对应 stage 参数 |
| API 请求 `rerank_enabled`, `mmr_enabled` | 作为 per-request override 生效 |
| MCP 工具 `iclude_recall` / `iclude_scan` | 增加可选 `pipeline` 参数，不传则走 strategy |

## 13. 非目标 / 不做的事

- **不暴露自定义管线 DSL**：用户通过 `pipeline` 参数选内置管线 + 参数覆盖，不做自由 stage 组装
- **不做跨管线结果合并**：一次查询只走一条管线（+ 降级链）
- **不做管线热加载**：管线在启动时注册，运行时不变
- **不做 A/B 测试框架**：可通过 debug trace 手动对比

## 14. 测试策略

| 层级 | 测试内容 | 位置 |
|------|---------|------|
| 单元 | 每个 Stage 独立测试 | `testing/search/stage/` |
| 集成 | 每条内置管线端到端 | `testing/search/pipeline/` |
| 集成 | Strategy Agent 选择正确性 | `testing/search/strategy/` |
| 集成 | 降级链行为 | `testing/search/pipeline/` |
| Dashboard | 管线精度对比报告 | `testing/report/pipeline_test.go` |
