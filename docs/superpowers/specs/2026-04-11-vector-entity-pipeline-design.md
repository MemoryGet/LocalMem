# Vector-Driven Entity Pipeline Design
# 向量驱动实体抽取 Pipeline 设计

**Date:** 2026-04-11
**Status:** Approved
**Scope:** 替换 LLM 实体抽取，改用三层向量驱动方案 + 候选晋升 + 批量优化

---

## 1. Background / 背景

当前系统使用 LLM 做实体抽取（`memory.Extractor`），每条记忆耗时秒级，严重拖慢记忆落库流程。本设计用三层向量驱动方案替代，全程毫秒级，零 LLM 依赖。

未来数据源主要是会话流（飞书/微信）和知识文档，批量写入频繁，LLM 抽取无法承受吞吐量要求。

---

## 2. Full Pipeline / 完整流程

```
会话/文档进入
  → ① 噪声过滤（已实现，长度+模式匹配）
  → ② 批量落库 SQLite（已实现）
  → ③ 批量向量化 → Qdrant（已实现）
  → ④ 三层实体解析（新）
  → ⑤ 合并 + 置信度评分（新）
  → ⑥ UpdateRelationStats（已实现）
  → ⑦ 异步更新实体质心向量（新）
  → ⑧ 无实体记录软删除（已实现）
```

步骤①②③⑥⑧已在 Plan A/B 中实现。本设计覆盖步骤④⑤⑦及相关基础设施。

---

## 3. Three-Layer Entity Resolution / 三层实体解析

### 3.1 Layer 1: Tokenizer Exact Match / 分词精确匹配

**Confidence: 0.9**

使用现有 Jieba/Gse 分词器：
1. 对记忆内容分词
2. 过滤停用词，保留有意义名词（长度 ≥ 2 字符）
3. 每个词调用 `FindEntitiesByName(ctx, term, scope, 1)` 匹配已知实体
4. 命中 → 创建 memory_entity 关联（confidence=0.9）
5. 未命中的有意义词 → 存入 `entity_candidates` 表

**性能**：纯内存分词 + 数据库索引查询，微秒到毫秒级。

### 3.2 Layer 2: Entity Centroid Matching / 实体质心匹配

**Confidence: 0.7**

每个实体维护一个质心向量（关联记忆向量的加权平均），存储在 Qdrant 独立 collection `entity_centroids`。

1. 新记忆的向量 → 查询 `entity_centroids` collection，找 Top-N 相似实体
2. 相似度超过阈值（默认 0.6）→ 创建 memory_entity 关联（confidence=0.7）

**解决场景**："系统性能优化"没提 Redis，但 Redis 实体质心（缓存/性能相关）与其向量相似度高 → 关联。

**质心更新**：异步。每次新增 memory_entity 关联时，该实体的质心需要重算。使用增量更新公式避免重查所有向量：
```
new_centroid = (old_centroid × old_count + new_vector × confidence) / (old_count + confidence)
```

### 3.3 Layer 3: Neighbor Propagation / 近邻传播

**Confidence: 0.5**

1. 新记忆的向量 → 查询 memory Qdrant collection，找 Top-K 近邻（默认 K=10）
2. 对每个近邻，获取其关联实体（`GetMemoriesEntities` 批量查询）
3. 统计实体出现频次：出现在 ≥ 2 个近邻中的实体 → 传播（confidence=0.5）

**解决场景**：质心可能遗漏的长尾关联。近邻的具体子话题更精准。

### 3.4 Merge + Dedup / 合并去重

三层结果合并：
- 同一实体被多层命中 → confidence = max(各层) + 0.1 加成，上限 1.0
- 去重后得到最终 `[(entityID, confidence)]` 列表
- 批量创建 memory_entity 关联

---

## 4. Confidence Scoring / 置信度评分

### 4.1 Schema Change

`memory_entities` 表新增 `confidence` 列：
```sql
ALTER TABLE memory_entities ADD COLUMN confidence REAL DEFAULT 0.9;
```

### 4.2 Confidence Usage

| 用途 | 说明 |
|------|------|
| 质心更新权重 | 高置信度记忆对质心影响大，低置信度影响小 |
| 清理优先级 | 低置信度 + 低 mention_count → 优先清理 |
| 查询排序 | EntityProfile 可按 confidence 排序关联记忆 |

---

## 5. Entity Candidate Auto-Promotion / 候选实体自动晋升

### 5.1 Schema

新表 `entity_candidates`：
```sql
CREATE TABLE entity_candidates (
    name       TEXT NOT NULL,
    scope      TEXT DEFAULT '',
    first_seen DATETIME NOT NULL,
    hit_count  INTEGER DEFAULT 1,
    memory_ids TEXT DEFAULT '[]',  -- JSON array of memory IDs
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(name, scope)
);
CREATE INDEX idx_entity_candidates_hit ON entity_candidates(hit_count);
```

### 5.2 Promotion Logic

Heartbeat 定期扫描（复用现有 heartbeat 机制）：

条件：`hit_count >= 3`（可配置）
1. 创建正式 Entity（entity_type 默认 "concept"）
2. 解析 `memory_ids` → 批量创建 memory_entity 关联（confidence=0.9，精确匹配）
3. 获取关联记忆的向量 → 计算初始质心 → 写入 entity_centroids collection
4. 删除候选记录

### 5.3 Cold Start

- 系统初期只有 Layer 1 工作
- 分词不断产出候选
- 候选积累到阈值自动晋升
- Layer 2/3 随实体增多逐渐生效
- 无需人工干预或 LLM 种子

---

## 6. Batch Optimization / 批量优化

同一会话多条消息批量处理：

### 6.1 Batch Vectorization

已有：embedding API 支持批量调用。N 条消息 → 1 次 API 调用。

### 6.2 Batch Qdrant Queries

Layer 2 和 Layer 3 的 Qdrant 查询支持 batch search：
- `entity_centroids` batch search：N 条记忆 → 1 次 batch 请求
- `memory` collection batch search：N 条记忆 → 1 次 batch 请求

### 6.3 Session-Level Entity Pool

同一会话内共享实体上下文：
- M3 通过 Layer 1 识别到"张三"
- M7 没提"张三"但在同一会话中
- 会话级实体池让 M7 也可以关联"张三"（confidence=0.4，会话传播）

实现：批量解析完成后，收集会话内所有高置信度实体（confidence ≥ 0.7），对会话内无实体的记忆追加关联。

---

## 7. Entity Centroid Collection / 实体质心 Collection

### 7.1 Qdrant Setup

新建 Qdrant collection `entity_centroids`：
- 向量维度：与 memory collection 一致
- payload：`entity_id`（TEXT），`entity_name`（TEXT），`scope`（TEXT），`memory_count`（INT）
- 每个实体一条记录

### 7.2 Centroid Update

触发时机：每次 memory_entity 创建后异步触发。

增量更新（避免重查所有向量）：
```
new_centroid = (old_centroid × old_count + new_vector × confidence) / (old_count + confidence)
new_count = old_count + 1
```

全量重算：heartbeat 定期全量重算一次（修正累积误差），频率低（每天一次）。

---

## 8. New Component: EntityResolver / 新组件

替代现有 `memory.Extractor`。

```go
type EntityResolver struct {
    tokenizer  tokenizer.Tokenizer    // 分词器
    graphStore store.GraphStore        // 实体/关系查询
    vecStore   store.VectorStore       // Qdrant 查询
    embedder   embed.Embedder          // 向量化（质心更新用）
    cfg        EntityResolverConfig
}

type EntityResolverConfig struct {
    CentroidCollection  string   // Qdrant collection name for centroids
    CentroidThreshold   float64  // 质心匹配阈值，默认 0.6
    NeighborK           int      // 近邻数量，默认 10
    NeighborMinCount    int      // 近邻传播最小出现次数，默认 2
    CandidatePromoteMin int      // 候选晋升最小 hit_count，默认 3
    SessionPropagation  bool     // 会话级实体传播开关
}
```

### 8.1 Core Method

```go
// Resolve 对一批记忆执行三层实体解析 / Three-layer entity resolution for a batch of memories
func (r *EntityResolver) Resolve(ctx context.Context, memories []*model.Memory, vectors [][]float32) error
```

### 8.2 Integration Point

替换 `Manager.Create()` 中的异步 Extractor 调用：
```go
// 旧：go r.extractor.Extract(ctx, memory)
// 新：go r.resolver.Resolve(ctx, []*model.Memory{memory}, [][]float32{embedding})
```

批量场景（IngestConversation）：
```go
r.resolver.Resolve(ctx, memories, embeddings)
```

现有 LLM Extractor 保留为可选 fallback（配置开关 `extract.use_llm: true`）。

---

## 9. Schema Changes / Schema 变更

```sql
-- V27: memory_entities 新增 confidence
ALTER TABLE memory_entities ADD COLUMN confidence REAL DEFAULT 0.9;

-- V27: entity_candidates 表
CREATE TABLE entity_candidates (
    name       TEXT NOT NULL,
    scope      TEXT DEFAULT '',
    first_seen DATETIME NOT NULL,
    hit_count  INTEGER DEFAULT 1,
    memory_ids TEXT DEFAULT '[]',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(name, scope)
);
CREATE INDEX idx_entity_candidates_hit ON entity_candidates(hit_count);
```

---

## 10. New Config Fields / 新增配置项

```yaml
extract:
  use_llm: false                          # LLM 抽取开关（fallback）
  resolver:
    enabled: true
    centroid_collection: "entity_centroids"
    centroid_threshold: 0.6               # 质心匹配最小相似度
    neighbor_k: 10                        # 近邻数量
    neighbor_min_count: 2                 # 传播最小出现次数
    candidate_promote_min: 3              # 候选晋升最小 hit_count
    session_propagation: true             # 会话级实体传播
```

---

## 11. Compatibility / 兼容性

- 现有 LLM Extractor 保留，通过 `extract.use_llm` 开关控制（默认 false）
- memory_entities 新增 confidence 列有默认值 0.9，现有数据不受影响
- entity_candidates 为新表，不影响现有功能
- entity_centroids 为新 Qdrant collection，不影响现有 memory collection
- EntityResolver 为新组件，通过依赖注入替换 Extractor，不修改 Manager 接口
