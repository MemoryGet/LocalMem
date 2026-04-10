# Storage Architecture Upgrade Design
# 存储架构升级设计

**Date:** 2026-04-10
**Status:** Approved
**Scope:** SQLite 关系数据库职责重新定义 + 实体关系生命周期 + 渐进式披露

---

## 1. Background / 背景

当前系统瓶颈在 LLM 实体抽取（记忆落库时），搜索端已通过关键词直接匹配优化。本设计重新划分向量数据库与关系数据库的职责边界：

- **向量数据库（Qdrant）**：语义检索 + 实体关系强度评估（向量聚合度）
- **关系数据库（SQLite）**：原始数据管理 + 实体关系生命周期 + 记忆组织与披露

未来数据源主要是会话流（飞书/微信/知识文档），核心约束：一个实体对应多个会话（多对多），实体跨会话聚合。

---

## 2. Raw Data Storage + Multi-Dimensional Indexing / 原始数据存储 + 多维索引

### 2.1 Source URI Standardization / 来源标识 URI 化

`source_type` 从内部类型（manual/conversation/document/api/reflect/consolidation）扩展为平台级标识。`source_ref` 统一为 URI 格式。

| 场景 | source_type | source_ref |
|------|-------------|------------|
| 飞书群消息 | `feishu` | `feishu://chat/{group_id}/msg/{msg_id}` |
| 微信私聊 | `wechat` | `wechat://contact/{user_id}/msg/{msg_id}` |
| 飞书文档 | `feishu_doc` | `feishu://doc/{doc_id}#block_{block_id}` |
| Claude Code | `claude_code` | `claude://session/{session_id}` |

现有内部类型（reflect/consolidation/session_summary/system）保持不变。

### 2.2 Session Retrieval / 会话检索

不新增 session_id 字段，复用现有字段：

- **同源聚合**：`source_ref` 前缀匹配（如 `WHERE source_ref LIKE 'feishu://chat/group-eng/%'`），需加 B-tree 索引
- **跨源聚合**：`context_id`（ContextType=session），多个来源的记忆关联到同一个 context
- **精确定位**：`source_ref` 完整 URI

SearchFilters 新增 `SourceRefPrefix` 字段支持前缀匹配。

### 2.3 Noise Cleanup / 噪声清理

会话数据噪声比例高（50%+），分两刀清理：

**第一刀：入库前预过滤**（向量化和实体抽取之前）
- content 长度 < 10 字符 → 直接丢弃
- 匹配噪声模式（纯表情、纯语气词、纯回应词）→ 直接丢弃
- 成本极低，纯字符串匹配

**第二刀：抽取后判定**（实体抽取完成后）
- 有实体 → 正常保留
- 无实体但 kind=skill/profile 或 memory_class=procedural → 保留
- 无实体但内容有明确观点/决策/偏好（向量相似度验证）→ 保留
- 都不满足 → 直接软删除

不使用 ephemeral 降级过渡——被挽救概率极低，过渡期还污染检索结果。

### 2.4 Entity Profile View / 实体聚合视图

`GraphManager.GetEntityProfile(ctx, entityID, opts) (*EntityProfile, error)`

组合现有 Store 查询（并行），Go 层分组，Store 层零改动：

```go
type EntityProfile struct {
    Entity     *Entity                    // 基础信息
    Relations  []*EntityRelation           // 关联实体
    BySource   map[string][]*Memory        // 按 source_type:source_ref 分组
    ByTimeline map[string][]*Memory        // 按月份分组
    ByScope    map[string]int              // 跨 scope 分布计数
}
```

API 端点：`GET /v1/entities/:id/profile`

### 2.5 Entity Forward Discovery / 实体正向发现

**A 路径（被动发现）**：检索 pipeline 返回结果后，调 `GetMemoriesEntities` 批量加载实体。SearchResult 附带 Entities 字段，响应附带命中实体汇总。

**B 路径（主动探索）**：`GET /v1/entities/search?q=xxx` + `GET /v1/entities/:id/profile`

闭环：搜索 → A 自动附带实体 → 发现感兴趣的 → B 调 EntityProfile 深入。

### 2.6 Entity Context Snippet / 实体上下文片段

**不加 snippet 字段**。数据源（会话消息/文档 chunk）粒度已够细。需要高亮时查询时做字符串定位，是展示层的事。

---

## 3. Entity Relationship Lifecycle / 实体关系生命周期治理

### 3.1 Dynamic Weight / 动态权重

权重由向量语义驱动，共现为硬门槛：

```
weight = vector_cohesion(共现记忆向量聚合度) × confidence_factor(mention_count)
```

**两层机制**：
1. 共现门槛（mention_count ≥ 1）：实体 A 和 B 必须在同一条记忆中被抽取出来
2. 向量聚合度：取 A 和 B 共现记忆在 Qdrant 中的向量，计算聚类紧密度

向量聚合度选型理由：
- 选项1（全量记忆交叉）计算量大且信号稀释
- 选项3（实体名 embedding）太浅
- **选项2（共现记忆聚合度）** 恰好回答"这两个实体的共现是聚焦关系还是噪声"

entity_relations 新增字段：`mention_count INTEGER DEFAULT 1`、`last_seen_at DATETIME`、`updated_at DATETIME`。

每次实体抽取发现共现时：mention_count++、last_seen_at 更新、异步计算向量聚合度更新 weight。

### 3.2 Relationship Evolution / 关系演化

不建显式替代语义（无 superseded_by / event log / version history）：

| 问题 | 谁来答 | 怎么答 |
|------|--------|--------|
| 现在用什么？ | 关系层（动态权重） | weight 最高的排前面 |
| 什么时候转的？ | 原始数据层（记忆时间线） | 两个实体的共现记忆按时间对比 |
| 替代还是并存？ | 两层结合 | weight 是否归零 + 最近有无新共现 |

### 3.3 Time Decay / 时间衰减

**查询时懒计算**（不批量刷新 weight）：

```
effective_weight = weight × e^(-λ × days_since_last_seen)
```

- λ = 0.015（默认，config 可配）
- 知识文档场景 → λ = 0.005（衰减慢）
- 即时通讯场景 → λ = 0.03（衰减快）

衰减在 graph 检索阶段 BFS 打分时计算。

**三条件阈值清理**（heartbeat 定期任务，三条同时满足）：
1. effective_weight < 0.05
2. mention_count < 3
3. last_seen_at < now - 90天

不设地板值——死关系永远不消失会回到无限膨胀问题。

### 3.4 Delete Strategy / 删除策略

| 对象 | 策略 | 理由 |
|------|------|------|
| entity_relations | 硬删除 | 历史存在感可从记忆时间线反查；衰减清理三条件已很保守 |
| entities | 软删除（deleted_at） | 级联影响大，误删需重跑抽取才能恢复 |
| memory_entities | 跟随两端 | 记忆软删→不可见，实体软删→隐藏 |

清理流程：
1. 时间衰减阈值清理 → 硬删 entity_relations
2. 实体无任何活跃关系 → 软删 entity（孤儿实体，留缓冲）
3. 软删超过 N 天 → 硬删 entity + 级联清理 memory_entities

所有 entities 查询加 `WHERE deleted_at IS NULL`。

---

## 4. Scope Organization + Progressive Disclosure / Scope 间记忆组织 + 渐进式披露

### 4.1 Cross-Scope Retrieval / 跨 Scope 检索

实体作为跨 scope 桥梁，不额外建模：
- 主 scope 优先（高权重）
- 实体关联的跨 scope 记忆作为补充（低权重，visibility 控制）

### 4.2 Multi-Pipeline Progressive Disclosure / 多管线渐进式披露

调用方只传 `query + token_budget`，系统自动做多维度最优分配。

**四条管线并行**：

| 管线 | 默认预算 | 输出级别 | 内容 |
|------|---------|---------|------|
| 核心事实 | 40% | full | 直接命中的记忆完整内容 |
| 上下文补充 | 25% | standard | 背景相关记忆的 summary |
| 实体网络 | 20% | brief | 实体关系图 + 跨 scope 关联 |
| 时间线 | 15% | brief | 时间脉络 excerpt + timestamps |

**权重调整策略**：第一版使用固定默认权重，不依赖 LLM。复用现有 pipeline strategy 分类结果（如有）做简单映射：

```
strategy=factual     → core:60  context:20  entity:10  timeline:10
strategy=exploration → core:25  context:25  entity:25  timeline:25
strategy=temporal    → core:20  context:15  entity:15  timeline:50
strategy=default     → core:40  context:25  entity:20  timeline:15
```

无 strategy 分类时全部走 default。未来可按需加关键词启发式，不是第一版必须做的。

**Assembler**：汇总四条管线结果，卡总预算，输出结构化响应。尾部附扩展指针（memory_id + 标题 + entity names），可按 ID fetch 深入。

在现有 search pipeline stages 之后加 disclosure stage。

---

## 5. Schema Changes / Schema 变更

```sql
-- V26: entity_relations 新增生命周期字段
ALTER TABLE entity_relations ADD COLUMN mention_count INTEGER DEFAULT 1;
ALTER TABLE entity_relations ADD COLUMN last_seen_at DATETIME;
ALTER TABLE entity_relations ADD COLUMN updated_at DATETIME;

-- V26: entities 新增软删除
ALTER TABLE entities ADD COLUMN deleted_at DATETIME DEFAULT NULL;

-- V26: 新增索引
CREATE INDEX idx_memories_source_ref_prefix ON memories(source_ref);
CREATE INDEX idx_entities_deleted_at ON entities(deleted_at);
CREATE INDEX idx_entity_relations_last_seen ON entity_relations(last_seen_at);
```

所有 ALTER TABLE 使用 `IsColumnExistsError` 守护保证幂等。

---

## 6. New API Endpoints / 新增 API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/entities/:id/profile` | 实体聚合视图（BySource/ByTimeline/ByScope） |
| GET | `/v1/entities/search?q=xxx` | 实体名称搜索 |

---

## 7. New Config Fields / 新增配置项

```yaml
retrieval:
  relation_decay_lambda: 0.015        # 关系时间衰减系数 λ
  disclosure:
    enabled: true
    core_weight: 0.4                  # 核心事实管线预算占比
    context_weight: 0.25              # 上下文补充管线
    entity_weight: 0.2                # 实体网络管线
    timeline_weight: 0.15             # 时间线管线

ingest:
  noise_filter:
    min_content_length: 10            # 最小内容长度（低于此丢弃）
    patterns: []                      # 自定义噪声模式列表
```

---

## 8. Compatibility / 兼容性

- 现有 source_type 值（manual/conversation/document/api/reflect/consolidation）保持不变
- 新增平台级 source_type 不影响现有数据
- entities 加 deleted_at 后，所有查询加过滤，行为与 memories 一致
- entity_relations 新增字段均有默认值，现有数据不受影响
- 披露管线为新增 stage，不影响现有 pipeline 行为（可通过 disclosure.enabled 控制）
- 无 strategy 分类时管线权重走固定默认值，零 LLM 依赖
