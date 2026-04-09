# 检索效率与精度平衡框架设计

> Date: 2026-04-09
> Status: Draft
> Author: Tao + Claude
> Depends: 2026-04-08-pipeline-retrieval-design.md (管线架构已实现)

## 1. 背景与问题

### 评测发现

基于 LongMemEval oracle 数据集的评测结果：

| 模式 | Hit Rate | Recall@10 | 查询 LLM 成本 |
|------|----------|-----------|---------------|
| FTS 基线 | 83.8% | 83.8% | 零 |
| Pipeline 默认 | 84.4% | 82.2% | ~0（规则分类器） |
| Graph 管线 (LLM 实体抽取) | 91.0% | 91.0% | 零（图谱已持久化） |
| Full 管线 (Graph + LLM rerank) | 87.0% | 87.0% | ~500 token/查询 |

### 核心发现

1. **图谱是最大精度提升来源** (+7.2%)，且查询时零 LLM 成本（图谱持久化在 DB）
2. **LLM rerank 反而降低了 hit rate** (91% → 87%)，`min_relevance=0.3` 过度过滤
3. **实体抽取是唯一的重成本**，但它是一次性写入成本，不是查询成本
4. **评测方法有缺陷**：每题重建 DB，混淆了写入成本和查询成本

### 设计目标

- 写入快速，图谱异步增强
- 查询分级：大多数场景零 LLM 成本，模糊场景按需触发
- 图谱持久化 + 增量更新，不重复构建
- 评测框架正确分离写入/查询成本

## 2. 写入路径

### 2.1 两阶段写入

```
Memory 写入
  │
  ├─ 同步（立即完成，<10ms）
  │   ├─ SQLite 主表写入
  │   ├─ FTS5 索引更新
  │   └─ 规则 NER（正则 + 分词提取基础实体，零 LLM 成本）
  │
  └─ 异步队列（后台增强，不阻塞）
      ├─ LLM 实体精抽取 → 写入 entity + relation 表
      ├─ 与已有图谱建立新关联（增量添加边）
      └─ 标记 memory.graph_enriched = true
```

### 2.2 规则 NER（同步，零成本）

在 `Extractor` 中增加规则预提取层，跳过简单内容的 LLM 调用：

- 正则匹配：引号内容、代码标识符（`camelCase`、`snake_case`）、URL、数字+单位
- 分词提取：gse/jieba 分词后，取 TF-IDF 高分词作为实体候选
- 输出基础 entity 写入图谱，供查询时使用

```go
func (e *Extractor) Extract(ctx context.Context, mem *Memory) error {
    // 1. 规则提取（即时，同步）
    entities := e.ruleExtract(mem.Content)
    e.saveEntities(ctx, mem.ID, entities)
    
    // 2. 入队 LLM 精抽取（异步，后台）
    e.enqueue(mem.ID)
    return nil
}
```

### 2.3 异步批量抽取

后台 worker 从队列消费，批量调 LLM：

```go
// 每次处理一批（如 8 条），一次 LLM 调用提取多条记忆的实体
func (w *ExtractionWorker) processBatch(ctx context.Context, batch []*Memory) {
    // 一次 LLM 调用处理 8 条 → 调用次数降 8x
    results := w.llm.ExtractBatch(ctx, batch)
    for i, mem := range batch {
        w.mergeEntities(ctx, mem.ID, results[i]) // 与规则 NER 结果合并
        w.markEnriched(ctx, mem.ID)
    }
}
```

### 2.4 图谱增量更新

新实体写入时自动与已有图谱建立关联：

```
新实体 "user_points" 写入
  → 检查已有实体中是否有关联（名称相似、同记忆共现）
  → 自动建立 relation: "user_points" ←[belongs_to]→ "点券系统"
```

不需要全量重建，只在增量点上扩展图谱。

## 3. 查询路径：三级递进

### 3.1 总览

```
Query
  │
  ▼
Level 0: 策略选择
  │  有 LLM → 一次调用（选管线 + 预处理）  ~200ms, ~100 token
  │  无 LLM → 规则分类器                   <5ms, 零成本
  │
  ▼
Level 1: 本地检索（零 LLM 成本）
  │  Graph 遍历 + FTS + Temporal → 合并 → 过滤 → 精排
  │  延迟 <50ms
  │
  ▼ 置信度检查
  │
  ├─ 置信度高 → 直接返回
  │
  ▼ 置信度低
Level 2: LLM 增强（按需触发）
  │  LLM rerank top-K → 过滤 → 置信度标记
  │  延迟 ~500ms, ~500 token
  │
  ▼
固定尾巴: weight → MMR → core_inject → trim
```

### 3.2 Level 2 触发条件

不是所有查询都需要 LLM rerank。触发条件：

```go
func shouldTriggerLLMRerank(candidates []*SearchResult) bool {
    if len(candidates) < 2 {
        return false
    }
    top1 := candidates[0].Score
    top2 := candidates[1].Score
    
    // 条件 1: top1 与 top2 分差 < 20%（排序模糊）
    if top1 > 0 && (top1-top2)/top1 < 0.2 {
        return true
    }
    
    // 条件 2: top5 分数标准差 < 阈值（结果分散，不确定）
    if len(candidates) >= 5 && scoreStdDev(candidates[:5]) < 0.05 {
        return true
    }
    
    return false
}
```

### 3.3 管线与 Level 映射

| 管线 | Level 0 | Level 1 | Level 2 (LLM) |
|------|:-------:|:-------:|:--------------:|
| `precision` | 规则/LLM | graph + fts → graph_aware → graph_rerank | 按需（置信度检查） |
| `exploration` | 规则/LLM | fts + temporal → rrf → overlap_rerank | 按需 |
| `semantic` | 规则/LLM | vector + fts → rrf → overlap_rerank | 按需 |
| `association` | 规则/LLM | graph(depth=3) → graph_rerank | 按需 |
| `fast` | 规则 | fts(limit=10) | 不触发 |
| `full` | 规则/LLM | graph + fts + vector → graph_aware | 无条件触发 |

`fast` 管线永远不触发 Level 2。`full` 管线无条件触发。其他管线按置信度检查决定。

## 4. 图谱生命周期

### 4.1 三级图谱构建

| 层级 | 时机 | 方法 | 成本 | 精度 |
|------|------|------|------|------|
| Tier 1 | 写入时同步 | FTS 索引 + 规则 NER | 零 | 基础 |
| Tier 2 | 写入后异步 | LLM 批量实体精抽取 | ~200 token/条 | 高 |
| Tier 3 | 查询时兜底 | FTS 共现多跳遍历 | 零 | 中 |

### 4.2 查询时图谱状态适配

```go
func (s *GraphStage) Execute(ctx, state) {
    // 1. 优先用已有图谱（Tier 1 + 2 的结果）
    if s.graphStore != nil && s.hasEntities(ctx, queryEntities) {
        return s.traverseGraph(ctx, state) // 已有图谱遍历
    }
    
    // 2. 图谱未就绪 → FTS 共现兜底（Tier 3）
    return s.ftsMultiHopTraverse(ctx, state)
}
```

### 4.3 图谱状态对精度的影响

| 记忆状态 | 图谱层级 | 预估精度 |
|---------|---------|---------|
| 刚写入 (<1s) | Tier 1 (FTS + 规则 NER) | ~86% |
| 规则 NER + LLM 待处理 | Tier 1 | ~86% |
| LLM 抽取完成 | Tier 1 + 2 | ~91% |
| 图谱不可用时查询 | Tier 3 (FTS 共现) | ~85% |

## 5. 成本模型

### 5.1 写入成本

| 操作 | LLM 调用 | 延迟（用户感知） | Token |
|------|---------|----------------|-------|
| 写入 1 条 | 0（同步）+ 1（异步） | <10ms | ~200（异步） |
| 批量 100 条 | 0（同步）+ ~13（异步批量 8/次） | <1s | ~2,600 |
| 批量 1000 条 | 0（同步）+ ~125（异步批量） | <5s | ~26,000 |

### 5.2 查询成本

| 场景 | LLM 调用 | 延迟 | Token |
|------|---------|------|-------|
| 大多数查询 (Level 1) | 0-1 (strategy) | <50ms | 0-100 |
| 模糊场景 (Level 2 触发) | 1-2 | ~700ms | 300-600 |
| full 管线 | 2 | ~800ms | 500-700 |
| fast 管线 | 0 | <20ms | 0 |

### 5.3 预估月度成本（活跃使用）

假设：每天写入 50 条记忆，每天 200 次查询（80% Level 1, 15% Level 2, 5% full）

| 项目 | 月调用 | 月 Token | 月成本 (GPT-4o 价格) |
|------|--------|----------|---------------------|
| 写入抽取 | ~190 | ~300K | ~$0.9 |
| 查询 strategy | ~6,000 | ~600K | ~$1.8 |
| 查询 rerank | ~1,200 | ~600K | ~$1.8 |
| **合计** | **~7,400** | **~1.5M** | **~$4.5** |

## 6. 实现变更

### 6.1 需要新增的代码

| 文件 | 内容 | 改动量 |
|------|------|--------|
| `internal/memory/extractor_rules.go` | 规则 NER（正则 + 分词提取） | ~150 行 |
| `internal/memory/extractor_queue.go` | 异步抽取队列 + 批量处理 | ~200 行 |
| `internal/memory/extractor_batch.go` | 批量 LLM 实体抽取 prompt | ~100 行 |
| `internal/search/stage/graph.go` | 增加 `ftsMultiHopTraverse` 兜底 | ~80 行 |
| `internal/search/stage/rerank_llm.go` | 增加 `shouldTriggerLLMRerank` 条件触发 | ~30 行 |

### 6.2 需要修改的代码

| 文件 | 变更 | 改动量 |
|------|------|--------|
| `internal/memory/manager.go` | `Create()` 改为同步规则 NER + 异步入队 | ~30 行 |
| `internal/search/pipeline/builtin/builtin.go` | 管线定义加 Level 2 条件触发 | ~20 行 |
| `testing/eval/longmemeval.go` | 评测改为共享单库模式 | ~150 行 |

### 6.3 不变的代码

- 管线架构（Stage/Executor/Registry）— 已实现，不动
- 6 条内置管线定义 — 只调参数，不改结构
- Strategy Agent — 不变
- API/MCP 接口 — 不变

## 7. 评测框架修正

### 7.1 共享单库评测模式

```
Phase 1 — 一次性建库（写入成本独立统计）:
  创建 DB → seed 全部记忆 → 同步规则 NER → 异步 LLM 抽取（等待完成）
  统计: 抽取总 token, 抽取总耗时

Phase 2 — 逐题查询（查询成本独立统计）:
  for each question:
    retrieve(query) → 记录 hit/miss, rank, 延迟, LLM 调用数
  统计: 查询总 token, 平均延迟, hit_rate, MRR, NDCG

Phase 3 — 输出分离报告:
  写入报告: token 消耗, 每条记忆平均成本, 批量效率
  查询报告: 精度指标, 延迟分布, LLM 触发率, 每次查询平均成本
```

### 7.2 评测维度

| 维度 | 指标 |
|------|------|
| 精度 | hit_rate, MRR, NDCG@5, NDCG@10, recall@K |
| 效率 | 平均查询延迟, P99 延迟 |
| 成本 | 每查询 LLM token, Level 2 触发率 |
| 图谱 | 实体覆盖率, 关系密度, Tier 2 完成率 |

## 8. 非目标

- **不做实时图谱重建**：图谱只增量更新，不全量重建
- **不做 LLM 实体抽取的同步模式**：写入路径永远不等 LLM
- **不做图谱版本控制**：图谱是 append-only 的，不需要回滚
- **不做多模型实体抽取**：统一用 config 中配置的 LLM provider
