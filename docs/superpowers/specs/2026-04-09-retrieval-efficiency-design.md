# 检索效率与精度平衡框架设计

> Date: 2026-04-09
> Status: Draft (v2 — 基于共享单库实验和跨会话关联讨论更新)
> Author: Tao + Claude
> Depends: 2026-04-08-pipeline-retrieval-design.md (管线架构已实现)

## 1. 背景与问题

### 1.1 评测发现

基于 LongMemEval oracle 数据集的评测结果：

| 模式 | 题数 | Hit Rate | Recall@10 | 查询 LLM 成本 |
|------|------|----------|-----------|---------------|
| FTS 基线 (每题独立 DB) | 500 | 82.4% | 82.4% | 零 |
| Pipeline 默认 (每题独立 DB) | 500 | 83.6% | 82.4% | ~0（规则分类器） |
| Graph 管线 (LLM 实体抽取，每题独立) | 100 | **91.0%** | 91.0% | 零（图谱已持久化） |
| Full 管线 (Graph + LLM rerank，每题独立) | 100 | 87.0% | 87.0% | ~500 token/查询 |
| **共享单库 (无图谱，纯 FTS)** | **500** | **66.0%** | — | **零** |

### 1.2 按查询类别分析（Pipeline 默认，500 题）

| 类别 | Hit Rate | MRR | 题数 |
|------|----------|-----|------|
| single-session-assistant | 98.2% | 0.879 | 56 |
| single-session-user | 94.3% | 0.835 | 70 |
| knowledge-update | 93.6% | 0.664 | 78 |
| temporal-reasoning | 88.0% | 0.548 | 133 |
| **multi-session** | **66.2%** | **0.410** | **133** |
| **single-session-preference** | **63.3%** | **0.288** | **30** |

### 1.3 核心发现

1. **每题独立 DB 的评测不真实**：只有 ~25 条记忆，无噪声干扰，人为拔高了精度
2. **共享单库才是真实场景**：9752 条混合记忆，FTS 噪声导致精度暴跌到 66%
3. **图谱是穿透噪声的关键**：每题独立 DB + 图谱 = 91%，说明图谱在精准检索中不可替代
4. **跨会话关联 (multi-session) 是最大短板**：66.2%，恰好是图谱跨上下文连接能解决的问题
5. **LLM rerank 过度过滤**：full 管线 (87%) 比 graph 管线 (91%) 低，`min_relevance=0.3` 误杀了正确结果
6. **实体抽取是一次性写入成本**，图谱持久化后查询零 LLM 成本

### 1.4 设计目标

- **共享单库 + 全局图谱**场景下 hit rate 达到 85%+（从 66% 恢复）
- 写入快速响应（<10ms 用户感知），图谱异步增强
- 大多数查询零 LLM 成本，模糊场景按需触发
- 图谱跨会话/跨项目共享，知识自动积累
- 评测框架正确反映真实生产场景

## 2. 图谱隔离策略

### 2.1 安全隔离 vs 知识共享

| 层级 | 隔离？ | 理由 |
|------|:------:|------|
| **跨团队** (team_id) | **硬隔离** | 数据安全，A 团队数据对 B 团队不可见 |
| **跨用户** (owner_id) | 可选共享 | 同团队内项目知识可共享 |
| **跨会话** (session scope) | **不隔离** | 记忆系统核心价值——跨会话知识积累 |
| **跨项目** (project scope) | **不隔离** | 同一实体在不同项目中应自动关联 |

### 2.2 实体合并规则

同名实体在同一 `team_id` 内自动合并为同一节点：

```
会话 A 记忆: "点券系统需要重构"     → 实体 "点券" (team=T1)
会话 B 记忆: "点券存在 user_points 表" → 实体 "点券" (team=T1) ← 同一节点

图谱:
  "点券" ←── 会话A记忆
    │
    ├──── 会话B记忆
    │
    └──→ "user_points" ──→ "balance"
```

查询"点券在哪个字段" → 图谱从"点券"出发 → 遍历到"user_points" → "balance" → 命中会话 B 记忆。跨会话关联自动建立，知识越用越饱满。

### 2.3 消歧策略

同名不同义（如"balance" = 余额 vs "balance" = 负载均衡）通过以下方式处理：

- LLM 抽取时附带 `entity_type`（concept / code_symbol / person / ...）区分
- 同名同类型 → 合并
- 同名不同类型 → 不合并，各自独立节点

## 3. 写入路径

### 3.1 两阶段写入

```
Memory 写入
  │
  ├─ 同步（立即完成，<10ms）
  │   ├─ SQLite 主表写入
  │   ├─ FTS5 索引更新
  │   ├─ Qdrant 向量写入（如果启用）
  │   └─ 规则 NER（正则 + 分词提取基础实体）
  │       → 写入 entity 表（与已有同名实体自动合并）
  │       → 标记 enrichment_level = "rule"
  │
  └─ 异步队列（后台增强，不阻塞写入）
      ├─ LLM 批量实体精抽取（8 条/次）
      │   → 提取隐含关系、同义实体、因果链
      │   → 与已有图谱合并（增量添加节点和边）
      ├─ 标记 enrichment_level = "llm"
      └─ 标记 enriched_at = now()
```

### 3.2 规则 NER（同步，零成本）

在 `Extractor` 中增加规则预提取层：

- 正则匹配：引号内容、代码标识符（`camelCase`、`snake_case`）、URL、数字+单位
- 分词提取：gse/jieba 分词后，取 TF-IDF 高分词作为实体候选
- 输出基础 entity 写入图谱，供查询时使用

```go
func (e *Extractor) Extract(ctx context.Context, mem *Memory) error {
    // 1. 规则提取（即时，同步）
    entities := e.ruleExtract(mem.Content)
    e.saveAndMergeEntities(ctx, mem.ID, entities) // 自动合并同名实体
    
    // 2. 入队 LLM 精抽取（异步，后台）
    e.enqueue(mem.ID)
    return nil
}
```

### 3.3 异步批量抽取

后台 worker 从队列消费，批量调 LLM：

```go
// 每次处理一批（8 条），一次 LLM 调用提取多条记忆的实体
func (w *ExtractionWorker) processBatch(ctx context.Context, batch []*Memory) {
    results := w.llm.ExtractBatch(ctx, batch)
    for i, mem := range batch {
        w.mergeEntities(ctx, mem.ID, results[i]) // 与规则 NER + 已有图谱合并
        w.markEnriched(ctx, mem.ID)
    }
}
```

### 3.4 图谱增量更新

新实体写入时自动与已有图谱建立关联：

```
新记忆: "user_points 表的 balance 字段记录点券余额"
  → 抽取实体: ["user_points", "balance", "点券"]
  → "点券" 已存在于图谱 → 合并到已有节点，新建边: 点券 ←→ user_points
  → "user_points" 不存在 → 创建新节点，建边: user_points ←→ balance
  → 图谱自动扩展，不需要全量重建
```

## 4. 查询路径：三级递进

### 4.1 总览

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

### 4.2 Level 2 触发条件

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

### 4.3 管线与 Level 映射

| 管线 | Level 0 | Level 1 | Level 2 (LLM) |
|------|:-------:|:-------:|:--------------:|
| `precision` | 规则/LLM | graph + fts → graph_aware → graph_rerank | 按需（置信度检查） |
| `exploration` | 规则/LLM | fts + temporal → rrf → overlap_rerank | 按需 |
| `semantic` | 规则/LLM | vector + fts → rrf → overlap_rerank | 按需 |
| `association` | 规则/LLM | graph(depth=3) → graph_rerank | 按需 |
| `fast` | 规则 | fts(limit=10) | 不触发 |
| `full` | 规则/LLM | graph + fts + vector → graph_aware | 无条件触发 |

## 5. 图谱生命周期

### 5.1 三级图谱构建

| 层级 | 时机 | 方法 | 成本 | 精度 |
|------|------|------|------|------|
| Tier 1 | 写入时同步 | FTS 索引 + 规则 NER | 零 | 基础 |
| Tier 2 | 写入后异步 | LLM 批量实体精抽取 | ~200 token/条 | 高 |
| Tier 3 | 查询时兜底 | FTS 共现多跳遍历 | 零 | 中 |

### 5.2 查询时图谱状态适配

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

### 5.3 图谱状态对精度的影响（预估）

| 环境 | 图谱层级 | 预估精度 |
|------|---------|---------|
| 共享单库，无图谱（纯 FTS） | 无 | **66%** (已验证) |
| 共享单库，规则 NER | Tier 1 | ~72% (预估) |
| 共享单库，规则 NER + LLM 抽取 | Tier 1+2 | **85%+** (目标) |
| 每题独立 DB，无图谱 | 无 | 83.6% (已验证) |
| 每题独立 DB，LLM 抽取 | Tier 1+2 | 91.0% (已验证) |

**核心假设**：共享单库 + 全局图谱应能从 66% 恢复到 85%+。需要评测验证。

## 6. 成本模型

### 6.1 写入成本

| 操作 | 同步延迟 | 异步 LLM 调用 | Token |
|------|---------|--------------|-------|
| 写入 1 条 | <10ms | 1 次（异步） | ~200 |
| 批量 100 条 | <1s | ~13 次（批量 8/次） | ~2,600 |
| 批量 1000 条 | <5s | ~125 次 | ~26,000 |

### 6.2 查询成本

| 场景 | LLM 调用 | 延迟 | Token |
|------|---------|------|-------|
| 大多数查询 (Level 1) | 0-1 (strategy) | <50ms | 0-100 |
| 模糊场景 (Level 2 触发) | 1-2 | ~700ms | 300-600 |
| full 管线 | 2 | ~800ms | 500-700 |
| fast 管线 | 0 | <20ms | 0 |

### 6.3 预估月度成本（活跃使用）

假设：每天写入 50 条记忆，每天 200 次查询（80% Level 1, 15% Level 2, 5% full）

| 项目 | 月调用 | 月 Token | 月成本 (GPT-4o 价格) |
|------|--------|----------|---------------------|
| 写入抽取 | ~190 | ~300K | ~$0.9 |
| 查询 strategy | ~6,000 | ~600K | ~$1.8 |
| 查询 rerank | ~1,200 | ~600K | ~$1.8 |
| **合计** | **~7,400** | **~1.5M** | **~$4.5** |

## 7. 评测框架

### 7.1 共享单库评测（反映真实场景）

```
Phase 1 — 一次性建库 + 图谱构建（成本独立统计）:
  创建单个 DB
  → seed 全部 9752 条记忆（全局，不隔离 scope）
  → 同步规则 NER → 基础图谱就位
  → 异步 LLM 批量抽取 → 全局图谱完成
  → 实体同名自动合并（team_id 内）
  → 保存 DB 文件供复用
  统计: 抽取总 token, 总耗时, 实体数, 关系数

Phase 2 — 逐题查询（查询成本独立统计）:
  加载已有 DB（零抽取成本）
  for each question:
    retrieve(query, team_id=eval) → 图谱优先检索
    记录: hit/miss, rank, 延迟, LLM 调用数, 选中管线
  统计: hit_rate, MRR, NDCG, 平均延迟, LLM 触发率

Phase 3 — 输出分离报告:
  写入报告: token 消耗, 实体覆盖率, 关系密度
  查询报告: 精度指标, 延迟分布, 按类别/难度细分
```

### 7.2 DB 文件复用

```
第一次运行: 建库 + 抽取 (~30-60 min, ~$5)
  → 保存: testing/eval/testdata/longmemeval-oracle-enriched.db

后续运行: 加载 DB → 查询评测 (~1-2 min, 零 LLM 抽取成本)
  → 仅查询阶段的 strategy/rerank LLM 成本
```

### 7.3 评测维度

| 维度 | 指标 |
|------|------|
| 精度 | hit_rate, MRR, NDCG@5, NDCG@10, recall@K |
| 效率 | 平均查询延迟, P99 延迟 |
| 成本 | 每查询 LLM token, Level 2 触发率 |
| 图谱 | 实体总数, 关系总数, 平均每记忆实体数, 跨会话实体合并数 |
| 按类别 | multi-session, temporal-reasoning, knowledge-update 等细分精度 |

### 7.4 关键验证目标

| 场景 | 基线 | 目标 |
|------|------|------|
| 共享单库 + 全局图谱 (500 题) | 66% (无图谱) | **85%+** |
| 共享单库 multi-session 类别 | ~40% (预估) | **70%+** |
| 共享单库 hard 难度 | ~45% (预估) | **70%+** |

## 8. 实现变更

### 8.1 新增代码

| 文件 | 内容 | 改动量 |
|------|------|--------|
| `internal/memory/extractor_rules.go` | 规则 NER（正则 + 分词提取） | ~150 行 |
| `internal/memory/extractor_queue.go` | 异步抽取队列 + 批量处理 worker | ~200 行 |
| `internal/memory/extractor_batch.go` | 批量 LLM 实体抽取 prompt 构建 | ~100 行 |
| `internal/memory/entity_merge.go` | 实体同名合并 + 消歧逻辑 | ~120 行 |
| `internal/search/stage/graph.go` | 增加 `ftsMultiHopTraverse` 兜底 | ~80 行 |
| `internal/search/stage/rerank_llm.go` | 增加 `shouldTriggerLLMRerank` 条件触发 | ~30 行 |
| `testing/eval/longmemeval_shared.go` | 共享单库评测模式 | ~200 行 |
| `testing/eval/build_enriched_db.go` | 一次性建库 + 图谱构建脚本 | ~150 行 |

### 8.2 修改代码

| 文件 | 变更 | 改动量 |
|------|------|--------|
| `internal/memory/manager.go` | `Create()` 改为同步规则 NER + 异步入队 | ~30 行 |
| `internal/store/interfaces.go` | entity 表增加 `enrichment_level` 字段 | ~10 行 |
| `internal/store/sqlite_migration*.go` | 迁移：memories 加 enrichment_level 列 | ~30 行 |
| `internal/search/pipeline/builtin/builtin.go` | 管线定义加 Level 2 条件触发 | ~20 行 |

### 8.3 不变的代码

- 管线架构（Stage/Executor/Registry）— 已实现，不动
- 6 条内置管线定义 — 只调参数，不改结构
- Strategy Agent — 不变
- API/MCP 接口 — 不变

## 9. 数据库 schema 变更

### 9.1 memories 表

```sql
ALTER TABLE memories ADD COLUMN enrichment_level TEXT DEFAULT 'none';
-- 'none' | 'rule' | 'llm'
ALTER TABLE memories ADD COLUMN enriched_at TIMESTAMP;
```

### 9.2 entities 表（已有，补充索引）

实体合并依赖 `(name, entity_type, team_id)` 唯一性：

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_name_type_team
  ON entities(name, entity_type, team_id);
```

## 10. 非目标

- **不做实时图谱重建**：图谱只增量更新，不全量重建
- **不做 LLM 实体抽取的同步模式**：写入路径永远不等 LLM
- **不做图谱版本控制**：图谱是 append-only 的，不需要回滚
- **不做多模型实体抽取**：统一用 config 中配置的 LLM provider
- **不做 scope 级别实体隔离**：team_id 内实体全局共享，跨会话知识积累
- **不做自定义管线 DSL**：用户选内置管线 + 参数覆盖
