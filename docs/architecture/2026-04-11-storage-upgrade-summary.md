# LocalMem 存储架构升级 & 向量驱动实体 Pipeline 总览

**日期:** 2026-04-11
**范围:** SQLite 职责重定义 + 实体关系生命周期 + 渐进式披露 + 向量驱动实体抽取

---

## 一、背景与问题

系统瓶颈在 **LLM 实体抽取**（记忆落库时秒级延迟）。搜索端已优化（关键词直接匹配替代 LLM），但落库端仍依赖 LLM。

未来数据源主要是会话流（飞书/微信/知识文档），批量写入频繁，LLM 抽取无法承受吞吐量要求。

**核心决策:** 重新划分向量数据库与关系数据库职责——

| 组件 | 新职责 |
|------|--------|
| **Qdrant（向量数据库）** | 语义检索 + 实体关系强度评估（向量聚合度）+ 实体质心匹配 |
| **SQLite（关系数据库）** | 原始数据管理 + 实体关系生命周期 + 记忆组织与披露 |

---

## 二、SQLite 关系数据库三大职责

### 2.1 原始数据存储 + 多维索引

#### 来源标识 URI 化
`source_type` 从内部类型扩展为平台级标识，`source_ref` 统一为 URI 格式：

| 场景 | source_type | source_ref |
|------|-------------|------------|
| 飞书群消息 | `feishu` | `feishu://chat/{group_id}/msg/{msg_id}` |
| 微信私聊 | `wechat` | `wechat://contact/{user_id}/msg/{msg_id}` |
| 飞书文档 | `feishu_doc` | `feishu://doc/{doc_id}#block_{block_id}` |
| Claude Code | `claude_code` | `claude://session/{session_id}` |

现有内部类型（reflect/consolidation/system）保持不变。

#### 会话检索
不新增 session_id 字段：
- **同源聚合**: `source_ref` 前缀匹配（B-tree 索引）
- **跨源聚合**: `context_id`（ContextType=session）
- **精确定位**: `source_ref` 完整 URI

#### 噪声清理（两刀）
- **第一刀（入库前）**: content 长度 < 10 字符或匹配噪声模式 → 直接丢弃，省下游全部计算
- **第二刀（抽取后）**: 无实体且无独立价值 → 软删除

#### 实体聚合视图
`GraphManager.GetEntityProfile()` 组合三个现有 Store 查询（并行），Go 层分组：
- `BySource`: 按 source_type:source_ref 分组
- `ByTimeline`: 按月份分组
- `ByScope`: 跨 scope 分布计数

Store 层零改动。

#### 实体正向发现
- **A 路径（被动发现）**: 检索结果自动附带实体（`GetMemoriesEntities` 批量查询）
- **B 路径（主动探索）**: `GET /v1/entities/:id/profile` + `GET /v1/entities/search`

---

### 2.2 实体关系生命周期治理

#### 动态权重
```
weight = vector_cohesion(共现记忆向量聚合度) × confidence_factor(mention_count)
```
- 共现（mention_count ≥ 1）是硬门槛
- 向量聚合度是质量评估（紧密=聚焦关系，分散=噪声）

#### 关系演化
不建显式替代语义（无 superseded_by / event log）：
- 现状问题 → 动态权重回答（weight 最高排前面）
- 历史问题 → 记忆时间线回答（共现记忆按时间对比）

#### 时间衰减
查询时懒计算：
```
effective_weight = weight × e^(-λ × days_since_last_seen)
```
- λ = 0.015（默认，可配置）
- 知识文档 → λ = 0.005（慢衰减）
- 即时通讯 → λ = 0.03（快衰减）

三条件阈值清理（heartbeat 定期任务）：
1. effective_weight < 0.05
2. mention_count < 3
3. last_seen_at < now - 90 天

#### 删除策略
| 对象 | 策略 | 理由 |
|------|------|------|
| entity_relations | 硬删除 | 历史可从记忆时间线反查 |
| entities | 软删除（deleted_at） | 级联影响大，误删需重跑抽取 |
| 孤儿实体 | 软删 → N 天后硬删 | 缓冲期可恢复 |

---

### 2.3 Scope 间记忆组织 + 渐进式披露

#### 跨 Scope 检索
实体作为跨 scope 桥梁：
- 主 scope 优先（高权重）
- 实体关联的跨 scope 记忆补充（低权重，visibility 控制）

#### 多管线渐进式披露
调用方只传 `query + token_budget`，系统自动做多维度最优分配：

| 管线 | 默认预算 | 输出级别 |
|------|---------|---------|
| 核心事实 | 40% | full content |
| 上下文补充 | 25% | summary |
| 实体网络 | 20% | brief + entity graph |
| 时间线 | 15% | excerpt + timestamps |

管线权重按策略自动调整（无 LLM，复用已有 strategy 分类或走固定默认）：
- factual → core:60, context:20, entity:10, timeline:10
- exploration → 四管线均分
- temporal → timeline:50
- default → 40/25/20/15

超预算记忆降级展示（full → summary → excerpt → pointer），不砍掉。

---

## 三、向量驱动实体抽取 Pipeline

### 3.1 核心决策：弃用 LLM，改用三层向量驱动

LLM 实体抽取每条记忆耗时秒级。新方案全程毫秒级，零 LLM 依赖。

### 3.2 完整 Pipeline

```
会话/文档进入
  → ① 噪声过滤（长度+模式匹配）
  → ② 批量落库 SQLite
  → ③ 批量向量化 → Qdrant（1次API调用）
  → ④ 三层实体解析（2次并行Qdrant查询）
  → ⑤ 合并 + 置信度评分
  → ⑥ 共现关系 upsert（UpdateRelationStats）
  → ⑦ 异步更新实体质心向量
  → ⑧ 无实体记录软删除
```

### 3.3 三层实体解析

#### Layer 1: 分词精确匹配（confidence = 0.9）
- 使用现有 Jieba/Gse 分词器
- 分词 → 过滤停用词 → 匹配已知实体名（`FindEntitiesByName`）
- 命中 → 关联；未命中 → 存入 `entity_candidates` 表
- 成本：纯内存分词 + 数据库索引查询

#### Layer 2: 实体质心匹配（confidence = 0.7）
- 每个实体维护一个质心向量（关联记忆向量的加权平均）
- 存储在 Qdrant 独立 collection（`entity_centroids`）
- 新记忆向量 vs 全部质心 → 相似度超阈值（0.6）的关联
- 解决"语义相关但没提名字"场景

#### Layer 3: 近邻传播（confidence = 0.5）
- 新记忆向量 → Qdrant Top-K 近邻
- 近邻已有实体 → 出现 ≥ 2 次的传播
- 捕捉质心可能遗漏的长尾关联

#### Layer 2 和 Layer 3 并行执行（sync.WaitGroup）

### 3.4 合并 + 置信度

```
同一实体被多层命中：
  confidence = max(各层 confidence) + 0.1 (重叠加成)
  上限 1.0
```

置信度影响：
- **质心更新权重**: 高置信度记忆对质心影响大
- **清理优先级**: 低置信度 + 低 mention_count → 优先清理
- **自我修正飞轮**: 正确关联→强化质心→更准匹配→更多正确关联

### 3.5 候选实体自动晋升

解决新实体发现 + 冷启动：

```
分词提取的未匹配关键词 → entity_candidates 表
  字段: name, scope, hit_count, memory_ids[]
  后续记忆再次提取到 → hit_count++

Heartbeat 定期扫描:
  hit_count >= 3（可配置） → 晋升为正式 Entity
  → 回溯: memory_ids 批量创建 memory_entities
  → 计算初始质心向量
  → 删除候选记录
```

冷启动期只有 Layer 1 工作，候选逐步积累晋升，Layer 2/3 逐渐生效。

### 3.6 批量优化

同一会话 20-50 条消息批量处理：
- 批量向量化（1 次 embedding API 调用 vs N 次）
- 批量 Qdrant 查询（1 次 batch search vs N 次）
- 会话级实体池：同会话内共享实体上下文

---

## 四、Schema 变更汇总

### V26 迁移
```sql
-- entity_relations 新增生命周期字段
ALTER TABLE entity_relations ADD COLUMN mention_count INTEGER DEFAULT 1;
ALTER TABLE entity_relations ADD COLUMN last_seen_at DATETIME;
ALTER TABLE entity_relations ADD COLUMN updated_at DATETIME;

-- entities 新增软删除
ALTER TABLE entities ADD COLUMN deleted_at DATETIME DEFAULT NULL;

-- 新增索引
CREATE INDEX idx_memories_source_ref_prefix ON memories(source_ref);
CREATE INDEX idx_entities_deleted_at ON entities(deleted_at);
CREATE INDEX idx_entity_relations_last_seen ON entity_relations(last_seen_at);
```

### V27 迁移
```sql
-- memory_entities 新增置信度
ALTER TABLE memory_entities ADD COLUMN confidence REAL DEFAULT 0.9;

-- 候选实体表
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

## 五、新增 API

| Method | Path | 说明 |
|--------|------|------|
| GET | `/v1/entities/:id/profile` | 实体聚合视图（BySource/ByTimeline/ByScope） |
| GET | `/v1/entities/search?q=xxx` | 实体名称搜索 |

---

## 六、新增配置项

```yaml
retrieval:
  relation_decay_lambda: 0.015          # 关系时间衰减系数 λ
  disclosure:
    enabled: false                       # 渐进式披露开关
    core_weight: 0.4
    context_weight: 0.25
    entity_weight: 0.2
    timeline_weight: 0.15

extract:
  use_llm: true                          # LLM 抽取 fallback 开关
  resolver:
    enabled: false                       # 向量解析器开关
    centroid_collection: "entity_centroids"
    centroid_threshold: 0.6
    neighbor_k: 10
    neighbor_min_count: 2
    candidate_promote_min: 3
    session_propagation: true

ingest:
  noise_filter:
    min_content_length: 10
    patterns: []

heartbeat:
  candidate_promote_min_hits: 3          # 候选晋升最小命中数
```

---

## 七、新增组件

| 组件 | 位置 | 职责 |
|------|------|------|
| **EntityResolver** | `internal/memory/entity_resolver.go` | 三层实体解析，替代 LLM Extractor |
| **CentroidManager** | `internal/memory/centroid_manager.go` | Qdrant 质心向量 CRUD |
| **CandidateStore** | `internal/store/sqlite_candidate.go` | 候选实体存储（upsert/list/delete） |
| **DisclosureStage** | `internal/search/stage/disclosure.go` | 多管线渐进式披露 |
| **relation_cleanup** | `internal/heartbeat/relation_cleanup.go` | 弱关系清理 + 孤儿实体处理 |
| **candidate_promotion** | `internal/heartbeat/candidate_promotion.go` | 候选实体自动晋升 |

---

## 八、实施记录

| Plan | 内容 | Commits | 状态 |
|------|------|---------|------|
| A: Foundation | V26 schema + Store 生命周期方法 + 配置 | 8 | ✅ |
| B: Entity Lifecycle | 时间衰减 + EntityProfile + 搜索实体 + heartbeat 清理 + 噪声过滤 | 6 | ✅ |
| C: Progressive Disclosure | DisclosureStage + 多管线 token 预算 | 6 | ✅ |
| D: Entity Resolver Foundation | V27 + CandidateStore + EntityResolver Layer 1 + CentroidManager | 8 | ✅ |
| E: Three-Layer Resolver | Layer 2/3 + merge + 候选晋升 + wiring | 5 | ✅ |
| **合计** | | **~35 commits** | |

---

## 九、兼容性

- 现有 source_type 值保持不变，新增平台级不影响现有数据
- EntityResolver 为可选替代，`extract.use_llm=true`（默认）保留 LLM 路径
- DisclosureStage 通过 `disclosure.enabled` 开关控制，默认关闭
- 所有新增列有默认值，现有数据不受影响
- entity_centroids 为新 Qdrant collection，不影响现有 memory collection
