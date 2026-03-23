# 图谱参与检索 — 三路 RRF 设计规格 / Graph-Enhanced Retrieval — Three-Way RRF Design Spec

**日期 / Date:** 2026-03-19
**状态 / Status:** Approved
**阶段 / Phase:** Phase 2 Task 3
**预估工期 / Estimated effort:** 2 weeks
**依赖 / Dependency:** Task 1 Reflect Engine（`internal/llm/`）, Task 2 Extractor（GraphStore 数据填充）

## 概述 / Overview

将现有 Retriever 从双路 RRF（FTS5 + Qdrant）升级为三路加权 RRF（FTS5 + Qdrant + Graph），新增图谱关联检索通道。通过反向利用现有 FTS5 + memory_entities + entity_relations 表链路识别实体，零新表、零迁移。

Upgrades the existing Retriever from two-way RRF (FTS5 + Qdrant) to three-way weighted RRF (FTS5 + Qdrant + Graph), adding a graph-based retrieval channel. Identifies entities by reverse-leveraging existing FTS5 + memory_entities + entity_relations table chain — zero new tables, zero migrations.

## 设计决策 / Design Decisions

| 决策项 / Decision | 选择 / Choice | 原因 / Rationale |
|-------------------|---------------|-------------------|
| 实体识别方式 / Entity identification | FTS5 反查 + LLM fallback / FTS5 reverse lookup + LLM fallback | 零 LLM 成本覆盖大部分查询；LLM 仅在 FTS5 无命中时 fallback / Zero LLM cost for most queries; LLM only when FTS5 returns nothing |
| 图谱遍历深度 / Graph depth | 可配置，默认 1 层 / Configurable, default 1 | 深层关联相关性低且结果指数膨胀，默认 1 层够用，可调 / Deep associations have low relevance and exploding results |
| RRF 融合方式 / RRF fusion | 可配置权重，Graph 默认 0.8 / Configurable weights, Graph default 0.8 | Graph 关联记忆相关性弱于直接命中，低权重更合理，可调优 / Graph associations are weaker than direct hits |
| 新增表 / New tables | 无 / None | 复用现有 memory_entities + entity_relations 链路，避免迁移 / Reuse existing table chain, avoid migration |

## 1. Graph 检索通道 / Graph Retrieval Channel（`internal/search/retriever.go` 修改 / modification）

### 1.1 Retriever 结构体增强 / Retriever Struct Enhancement

```go
type Retriever struct {
    memStore   store.MemoryStore
    vecStore   store.VectorStore    // 可为 nil / may be nil
    embedder   store.Embedder       // 可为 nil / may be nil
    graphStore store.GraphStore      // 新增，可为 nil / new, may be nil
    llm        llm.Provider         // 新增，可为 nil（LLM fallback 用）/ new, may be nil (for LLM fallback)
    cfg        config.RetrievalConfig // 新增 / new
}

// NewRetriever 构造函数增加参数 / Constructor adds parameters
func NewRetriever(
    memStore store.MemoryStore,
    vecStore store.VectorStore,
    embedder store.Embedder,
    graphStore store.GraphStore,    // 新增，可传 nil / new, nil-able
    llm llm.Provider,              // 新增，可传 nil / new, nil-able
    cfg config.RetrievalConfig,    // 新增 / new
) *Retriever
```

### 1.2 Graph 检索核心流程 / Graph Retrieval Core Flow

```
输入 / Input: query string, scope string, limit int, filters

阶段 1：从 FTS5 命中记忆反向找实体（零 LLM 成本）
Phase 1: Reverse entity lookup from FTS5 hits (zero LLM cost)
  MemoryStore.SearchText(ctx, query, teamID, cfg.GraphFTSTop) → top-N SearchResults
  对每个 SearchResult / for each SearchResult:
    GraphStore.GetMemoryEntities(ctx, memoryID) → 实体列表 []*Entity
  收集所有实体 IDs（去重）/ collect all entity IDs (deduplicated)

阶段 1.5（fallback）：FTS5 无命中 且 LLM Provider 可用时
Phase 1.5 (fallback): When FTS5 returns nothing AND LLM Provider available
  LLM 从查询文本中抽取实体名 / LLM extracts entity names from query
  → GraphStore.ListEntities(scope, entityType) 中精确匹配（EqualFold）
  → 实体 IDs

如果实体 IDs 为空 → Graph 路返回空结果集
If entity IDs empty → Graph channel returns empty result set

阶段 2：图谱遍历（可配置深度，默认 1 层）
Phase 2: Graph traversal (configurable depth, default 1)
  初始化 / initialize:
    visited = set{初始实体 IDs} // 防环 / cycle protection
    currentEntities = 初始实体 IDs

  循环 depth 次 / loop depth times:
    对每个 entity in currentEntities:
      GraphStore.GetEntityRelations(entityID) → 关联实体 IDs
      过滤已访问实体 / filter visited entities
      加入 visited + 收集新实体 / add to visited + collect new entities
    currentEntities = 新实体 / new entities

  allEntities = visited 中所有实体 / all entities in visited

阶段 3：获取关联记忆
Phase 3: Fetch associated memories
  对每个实体 / for each entity in allEntities:
    GraphStore.GetEntityMemories(entityID, cfg.GraphEntityLimit)
  合并 + 去重 / merge + deduplicate
  去除已在 FTS5/Qdrant 结果中的记忆（由 RRF 阶段自然处理，相同 ID 分数叠加）
  / Duplicates with FTS5/Qdrant naturally handled by RRF (same ID scores accumulate)

阶段 4：Memory → SearchResult 转换
Phase 4: Memory → SearchResult conversion
  GetEntityMemories 返回 []*model.Memory，需要转为 []*model.SearchResult
  / GetEntityMemories returns []*model.Memory, needs conversion to []*model.SearchResult
  对排序后的 memories 按 rank 赋分 / assign rank-based score to sorted memories:
    SearchResult{Memory: mem, Score: 0, Source: "graph"}
  Score 由 RRF 阶段根据 rank 计算，此处初始为 0
  / Score calculated by RRF based on rank, initially 0 here

返回 / Return: 结果集 C（带 Source="graph" 标记）/ result set C (marked Source="graph")
```

### 1.3 graphRetrieve 方法签名 / graphRetrieve Method Signature

```go
// graphRetrieve 图谱关联检索 / Graph-based association retrieval
// 返回按关联度排序的记忆列表，Source 标记为 "graph"
// Returns memories ranked by association, Source marked as "graph"
func (r *Retriever) graphRetrieve(ctx context.Context, query string, teamID string, scope string, limit int) []*model.SearchResult
```

> **错误处理 / Error handling:** graphRetrieve 内部所有 GraphStore 调用的错误均静默处理（`logger.Warn`），返回空结果。Graph 通道是最佳努力增强，不应阻塞检索。
> All GraphStore call errors inside graphRetrieve are silently handled (`logger.Warn`), returning empty results. The Graph channel is a best-effort enhancement that should never block retrieval.

### 1.5 LLM Fallback 实体抽取 / LLM Fallback Entity Extraction

```
仅当以下条件全部满足时触发 / Only triggered when ALL conditions met:
  - 阶段 1 FTS5 返回零命中 / Phase 1 FTS5 returns zero hits
  - llm Provider != nil
  - graphStore != nil

流程 / Flow:
  system prompt: "从用户查询中提取可能的实体名称，输出严格 JSON"
  / "Extract possible entity names from user query, output strict JSON"
  user prompt: query text
  response_format: { type: "json_object" }
  temperature: 0.1

  LLM 输出 JSON 结构 / LLM output JSON structure:
    {"entities": ["Alice", "Go"]}

  解析 / Parsing:
    json.Unmarshal → 成功则使用 / success → use
    失败 → 静默返回空（查询实体抽取足够简单，不需要三级 fallback）
    / failure → silently return empty (query entity extraction is simple enough, no 3-level fallback needed)

  → 在 GraphStore.ListEntities(scope, "", limit=100) 中按 name EqualFold 匹配
  / Match by name EqualFold in GraphStore.ListEntities(scope, "", limit=100)
  → 实体 IDs

  超时 / timeout: 10s（比 Reflect/Extractor 短，避免拖慢检索）
  / 10s (shorter than Reflect/Extractor to avoid slowing retrieval)
  失败 / on failure: 静默返回空，日志 Warn / silently return empty, log Warn
```

### 1.6 Graph 检索结果排序 / Graph Result Ranking

```
Graph 路内部排序依据 / Graph channel internal ranking:
  1. 直接关联实体的记忆 > 间接关联（depth > 1）的记忆
     / Direct association memories > indirect (depth > 1) memories
  2. 同层内按 entity 的关联度（与多少初始实体有关系）排序
     / Within same depth, rank by entity association count
  3. 最终按此排序赋 rank，用于 RRF 计算
     / Final ranking assigned for RRF calculation
```

## 2. 三路加权 RRF 融合 / Three-Way Weighted RRF Fusion（`internal/search/rrf.go` 修改 / modification）

### 2.1 加权 RRF 公式 / Weighted RRF Formula

```
现有 RRF / Existing RRF:
  score(id) = Σ 1/(k + rank + 1)

增强为加权 RRF / Enhanced to Weighted RRF:
  score(id) = Σ weight[source] × 1/(k + rank + 1)

默认权重 / Default weights:
  FTS5:   1.0
  Qdrant: 1.0
  Graph:  0.8
```

### 2.2 实现 / Implementation

```go
// RRFInput 加权RRF输入 / Weighted RRF input
type RRFInput struct {
    Results []*model.SearchResult
    Weight  float64 // 该路的权重系数 / weight coefficient for this channel
}

// MergeWeightedRRF 加权RRF融合 / Weighted RRF fusion
// 保留现有 MergeRRF/MergeRRFWithK 向后兼容
// Preserves existing MergeRRF/MergeRRFWithK for backward compatibility
func MergeWeightedRRF(inputs []RRFInput, k int, limit int) []*model.SearchResult
```

> **向后兼容 / Backward compatibility:** 现有 `MergeRRF` 和 `MergeRRFWithK` 函数保留不变。`MergeWeightedRRF` 是新函数。当所有权重均为 1.0 且只有两路输入时，结果与 `MergeRRFWithK` 完全一致。
> Existing `MergeRRF` and `MergeRRFWithK` preserved unchanged. `MergeWeightedRRF` is a new function. With all weights=1.0 and two inputs, results are identical to `MergeRRFWithK`.

### 2.3 Retrieve 方法模式变更 / Retrieve Method Mode Changes

```
现有三模式不变 / Existing three modes unchanged:
  SQLite-only → FTS5 单路 / FTS5 single channel
  Qdrant-only → Qdrant 单路 / Qdrant single channel
  Hybrid → FTS5 + Qdrant 双路 RRF / two-way RRF

新增：当 graphStore != nil 且 graph_enabled=true 时
New: when graphStore != nil AND graph_enabled=true:
  Hybrid → 三路加权 RRF：FTS5 + Qdrant + Graph
  / Three-way weighted RRF: FTS5 + Qdrant + Graph
  SQLite-only → 双路加权 RRF：FTS5 + Graph
  / Two-way weighted RRF: FTS5 + Graph
  Qdrant-only → 双路加权 RRF：Qdrant + Graph
  / Two-way weighted RRF: Qdrant + Graph

Graph 路为空时（无实体命中），自动退化为原有模式
When Graph channel is empty (no entity hits), automatically degrades to existing mode
```

## 3. 请求/响应模型变更 / Request/Response Model Changes

### 3.1 RetrieveRequest 增加字段 / New field（`internal/model/request.go`）

```go
type RetrieveRequest struct {
    // ... 现有字段不变 / existing fields unchanged
    GraphEnabled *bool // 新增：覆盖配置中的 graph_enabled（nil 则用配置默认值）
                       // new: override config graph_enabled (nil uses config default)
}
```

### 3.2 SearchResult.Source 扩展 / Source field extension

```go
type SearchResult struct {
    // ... 现有字段 / existing fields
    Source string // "sqlite" | "qdrant" | "hybrid" | "graph"（新增 graph）
    // 注意：现有代码使用 "sqlite" 而非 "fts5"，保持一致
    // Note: existing code uses "sqlite" not "fts5", maintaining consistency
}
```

> **无需新增 API 端点 / No new API endpoints:** `POST /v1/retrieve` 不变，内部多了一路 Graph 检索。调用方可通过 `graph_enabled` 字段控制开关。
> `POST /v1/retrieve` unchanged, Graph retrieval added internally. Callers can control via `graph_enabled` field.

## 4. 配置变更 / Config Changes

### 4.1 `internal/config/config.go` 新增 / Addition

```go
// RetrievalConfig 检索配置 / Retrieval config
type RetrievalConfig struct {
    GraphEnabled    bool    `mapstructure:"graph_enabled"`     // 默认 true / default true
    GraphDepth      int     `mapstructure:"graph_depth"`       // 默认 1 / default 1
    GraphWeight     float64 `mapstructure:"graph_weight"`      // 默认 0.8 / default 0.8
    FTSWeight       float64 `mapstructure:"fts_weight"`        // 默认 1.0 / default 1.0
    QdrantWeight    float64 `mapstructure:"qdrant_weight"`     // 默认 1.0 / default 1.0
    GraphFTSTop     int     `mapstructure:"graph_fts_top"`     // 默认 5 / default 5
    GraphEntityLimit int    `mapstructure:"graph_entity_limit"` // 默认 10 / default 10
}

// Config 顶层配置新增 / Top-level config addition
type Config struct {
    // ... 现有字段 / existing fields ...
    Retrieval RetrievalConfig `mapstructure:"retrieval"` // 新增 / new
}
```

**Viper 默认值 / Viper defaults:**
```go
viper.SetDefault("retrieval.graph_enabled", true)
viper.SetDefault("retrieval.graph_depth", 1)
viper.SetDefault("retrieval.graph_weight", 0.8)
viper.SetDefault("retrieval.fts_weight", 1.0)
viper.SetDefault("retrieval.qdrant_weight", 1.0)
viper.SetDefault("retrieval.graph_fts_top", 5)
viper.SetDefault("retrieval.graph_entity_limit", 10)
```

### 4.2 `config.yaml` 新增段 / New config section

```yaml
retrieval:
  graph_enabled: true
  graph_depth: 1
  graph_weight: 0.8
  fts_weight: 1.0
  qdrant_weight: 1.0
  graph_fts_top: 5
  graph_entity_limit: 10
```

## 5. 启动集成 / Startup Integration（`cmd/server/main.go`）

```go
// Retriever 构造时注入 graphStore + llmProvider + cfg
// Inject graphStore + llmProvider + cfg when constructing Retriever
retriever = search.NewRetriever(
    stores.MemoryStore,
    stores.VectorStore,
    embedder,
    stores.GraphStore,   // 可为 nil / may be nil
    llmProvider,          // 可为 nil / may be nil
    cfg.Retrieval,
)
```

## 6. 错误处理与边界情况 / Error Handling & Edge Cases

### 6.1 错误处理策略 / Error Handling Strategy

Graph 通道采用与 Qdrant 向量写入相同的"最佳努力"模式——所有 GraphStore 调用的错误静默处理，不影响 FTS5/Qdrant 路的正常返回。

The Graph channel uses the same "best-effort" pattern as Qdrant vector writes — all GraphStore errors are silently handled, never blocking FTS5/Qdrant results.

> **不新增 Sentinel Errors / No new sentinel errors:** Graph 检索失败不会向调用方暴露错误，而是降级为双路/单路 RRF。这与现有 Qdrant 最佳努力模式一致——Qdrant 失败时也不返回错误，只记日志。
> Graph retrieval failure is never exposed to callers — it degrades to two-way/single-way RRF. Consistent with existing Qdrant best-effort pattern.

### 6.2 Scope 过滤说明 / Scope Filtering Note

> Graph 遍历本身不按 scope 过滤——它依赖初始 FTS5 查询（已含 teamID/scope 过滤）命中的记忆来"锚定"实体。从这些实体出发的图谱遍历可能跨 scope（如 Alice 在 scope=team1 和 scope=team2 都有关联），这是预期行为——图谱的价值正是发现跨域关联。
> Graph traversal itself does not filter by scope — it relies on the initial FTS5 query (which already filters by teamID/scope) to anchor entities. Graph traversal from these entities may cross scopes (e.g., Alice has associations in both scope=team1 and scope=team2). This is intentional — the value of graph retrieval is discovering cross-domain associations.

### 6.3 构造函数变更说明 / Constructor Change Note

> `NewRetriever` 增加 3 个参数（graphStore, llm, cfg）是破坏性变更，所有调用方需同步更新。当前调用方只有 `cmd/server/main.go` 和测试文件，影响可控。此变更与现有构造函数模式一致（可选依赖传 nil）。
> `NewRetriever` adding 3 parameters is a breaking change. All callers must be updated. Current callers are only `cmd/server/main.go` and tests — limited blast radius. This change is consistent with existing constructor patterns (optional dependencies passed as nil).

### 6.4 密度检索（延迟至后续）/ Density-Based Retrieval (Deferred)

> 原规划中的"疏密检索（根据实体密度调整扩展深度）"本阶段不实现。`graph_depth` 为静态配置值。动态深度调整可在 Phase 3 中根据实际使用数据来设计。
> The originally planned "density-based retrieval (adjusting depth based on entity density)" is deferred. `graph_depth` is a static config value. Dynamic depth adjustment can be designed in Phase 3 based on real usage data.

## 7. 性能考量 / Performance Considerations

### 6.1 Graph 检索延迟分析 / Graph Retrieval Latency Analysis

```
阶段 1（FTS5 反查）：~1-5ms（现有 FTS5 查询 + N 次 GetMemoryEntities）
Phase 1 (FTS5 reverse): ~1-5ms (existing FTS5 query + N GetMemoryEntities calls)

阶段 1.5（LLM fallback）：~1-3s（仅在 FTS5 无命中时触发）
Phase 1.5 (LLM fallback): ~1-3s (only triggered when FTS5 returns nothing)

阶段 2（图谱遍历）：~1-5ms（depth=1 时 N 次 GetEntityRelations）
Phase 2 (graph traversal): ~1-5ms (N GetEntityRelations calls at depth=1)

阶段 3（获取记忆）：~1-10ms（M 次 GetEntityMemories）
Phase 3 (fetch memories): ~1-10ms (M GetEntityMemories calls)

总计（无 LLM）：~3-20ms，不影响 P99 ≤ 300ms 目标
Total (no LLM): ~3-20ms, within P99 ≤ 300ms target

总计（含 LLM fallback）：~1-3s，可接受（仅 fallback 场景）
Total (with LLM fallback): ~1-3s, acceptable (fallback scenario only)
```

### 6.2 结果集大小控制 / Result Set Size Control

```
GraphEntityLimit（默认 10）限制每个实体返回的记忆数
/ GraphEntityLimit (default 10) limits memories per entity

depth=1, 假设初始 5 个实体，每个实体平均 3 个关系：
/ depth=1, assuming 5 initial entities, 3 relations each:
  扩展实体数：5 + 15 = 20
  / expanded entities: 5 + 15 = 20
  最大记忆数：20 × 10 = 200（RRF 后按 limit 截断）
  / max memories: 20 × 10 = 200 (truncated by limit after RRF)
```

### 7.3 N+1 查询模式说明 / N+1 Query Pattern Note

> Graph 通道涉及多次独立 SQLite 查询（Phase 1: ~5 次 GetMemoryEntities, Phase 2: ~25 次 GetEntityRelations, Phase 3: ~20 次 GetEntityMemories），总计约 50 次查询。SQLite 单次查询 ~0.1ms，总计 ~5ms，在性能目标内。
> 已知优化点：后续可引入批量查询（如 `GetEntitiesForMemories([]string)`）减少查询次数。Phase 2 当前方案可接受。
>
> The Graph channel involves multiple individual SQLite queries (~50 total). At ~0.1ms per SQLite query, total ~5ms, within performance targets.
> Known optimization: batch queries (e.g., `GetEntitiesForMemories([]string)`) can reduce query count. Current approach acceptable for Phase 2.

## 8. 测试计划 / Test Plan

### 7.1 Graph 检索通道测试 / Graph Retrieval Channel Tests（`testing/search/graph_retrieval_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestGraphRetrieval_BasicFlow | FTS5 命中→反查实体→遍历关系→获取关联记忆 / FTS5 hit → entity lookup → traverse → fetch |
| TestGraphRetrieval_Depth1 | 深度 1 只取直接关联 / Depth 1 fetches direct associations only |
| TestGraphRetrieval_Depth2 | 深度 2 取二度关联 / Depth 2 fetches second-degree associations |
| TestGraphRetrieval_FTSEmpty_LLMFallback | FTS5 无命中时 LLM fallback 抽取实体 / LLM fallback when FTS5 returns nothing |
| TestGraphRetrieval_FTSEmpty_NoLLM | FTS5 无命中且无 LLM，返回空 / FTS5 empty + no LLM, returns empty |
| TestGraphRetrieval_NoEntities | 无实体关联，Graph 路返回空 / No entity associations, Graph returns empty |
| TestGraphRetrieval_Disabled | graph_enabled=false 时跳过 / Skipped when graph_enabled=false |
| TestGraphRetrieval_GraphStoreNil | graphStore 为 nil 时跳过 / Skipped when graphStore is nil |
| TestGraphRetrieval_CycleProtection | 图谱有环时不死循环 / No infinite loop when graph has cycles |
| TestGraphRetrieval_DeduplicateWithFTS | 去除与 FTS5/Qdrant 重复的记忆 / Dedup with FTS5/Qdrant results |
| TestGraphRetrieval_LLMFallbackTimeout | LLM fallback 超时返回空 / LLM fallback timeout returns empty |
| TestGraphRetrieval_GraphStoreError | GraphStore 调用出错时静默降级 / Graceful degradation on GraphStore errors |
| TestGraphRetrieval_ResultLimitedByRRF | 大结果集经 RRF 后正确按 limit 截断 / Large result set properly truncated by limit after RRF |

### 7.2 加权 RRF 测试 / Weighted RRF Tests（`testing/search/rrf_weighted_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestWeightedRRF_EqualWeights | 等权退化为标准 RRF / Equal weights degrades to standard RRF |
| TestWeightedRRF_GraphLowerWeight | Graph 权重低时排名靠后 / Graph results rank lower with low weight |
| TestWeightedRRF_ThreeWayFusion | 三路融合正确排序 / Three-way fusion correct ranking |
| TestWeightedRRF_TwoWayFusion | 双路融合（Graph 空）退化为现有行为 / Two-way (empty Graph) degrades to existing behavior |
| TestWeightedRRF_ZeroWeight | 权重为 0 时该路不参与 / Zero weight excludes channel |

## 9. 文件变更清单 / File Change List

### 新增 2 个文件 / 2 New Files

| 文件 / File | 说明 / Description |
|-------------|---------------------|
| `testing/search/graph_retrieval_test.go` | Graph 检索通道测试 / Graph retrieval channel tests |
| `testing/search/rrf_weighted_test.go` | 加权 RRF 测试 / Weighted RRF tests |

### 修改 6 个文件 / 6 Modified Files

| 文件 / File | 变更 / Changes |
|-------------|----------------|
| `internal/search/retriever.go` | Retriever 增加 graphStore/llm/cfg 字段 + graphRetrieve() + Retrieve() 三路集成 / Add fields + graph method + three-way integration |
| `internal/search/rrf.go` | 新增 MergeWeightedRRF（保留现有 MergeRRF/MergeRRFWithK）/ Add MergeWeightedRRF (preserve existing) |
| `internal/model/request.go` | RetrieveRequest.GraphEnabled 字段 / Add GraphEnabled field |
| `internal/config/config.go` | RetrievalConfig + Config.Retrieval + Viper defaults / Add RetrievalConfig |
| `internal/api/router.go` | Retriever 构造参数调整（如需）/ Retriever constructor parameter adjustment (if needed) |
| `cmd/server/main.go` | Retriever 构造注入 graphStore + llmProvider + cfg / Inject graphStore + llmProvider + cfg |

## 10. 性能目标 / Performance Targets

| 指标 / Metric | 目标 / Target |
|---------------|---------------|
| 三路 RRF 检索 P99（无 LLM fallback）/ Three-way RRF P99 (no LLM) | ≤ 300ms |
| Graph 通道延迟（无 LLM）/ Graph channel latency (no LLM) | ≤ 20ms |
| LLM fallback 超时 / LLM fallback timeout | 10s |
