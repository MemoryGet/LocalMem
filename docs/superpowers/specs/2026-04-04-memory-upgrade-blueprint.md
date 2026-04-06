# LocalMem Memory Upgrade Blueprint

**Date**: 2026-04-04
**Status**: Draft
**Author**: Codex + user discussion

---

## 1. Context

LocalMem 当前已经具备较完整的记忆系统基础能力：

- `Memory + Entity + EntityRelation + MemoryEntity` 基础模型
- `FTS + Vector + Graph + Temporal` 多通道检索
- conversation ingest / reflect / timeline / MCP tools
- strength / decay / retention / memory_class 等生命周期能力

但从“下一代记忆引擎”视角看，当前系统仍有明显边界：

1. 图谱已经存在，但图边目前主要还是遍历通道，不是一等检索对象
2. 关系类型存在，但缺少“证据层”和“因果层”
3. 图检索主要依赖实体扩散和 hop/depth 衰减，缺少路径成本与边语义评分
4. 记忆结果能返回命中的 memory，但很难回答“为什么命中它”

本设计的目标，是在不推翻现有 LocalMem 工程架构的前提下，将系统从“图增强检索”升级为“带因果和证据解释的图驱动记忆系统”。

---

## 2. Executive Summary

### 2.1 Core Judgment

对 LocalMem 来说，最合理的升级方向不是简单照搬 M-flow，也不是只增加因果边，而是：

**在现有通用记忆平台之上，引入因果/证据关系层，并吸收 M-flow 的边语义参与检索思想。**

### 2.2 What To Keep

应继续保留并强化以下现有能力：

- 现有 `Memory` 作为通用记忆主存储
- `FTS + Vector + Graph + Temporal` 多通道融合思路
- 现有 MCP / API / timeline / reflect / ingest 工具体系
- 现有 `memory_class`、strength、retention 等工程化能力

### 2.3 What To Add

应新增三种关键能力：

1. **因果边**：表达 why / depends_on / enables / blocks 等因果结构
2. **证据边**：表达一条结论、关系、因果链由哪些记忆或文档支持
3. **边语义检索**：让边本身参与召回、评分、路径解释，而不是只在遍历时“顺带经过”

---

## 3. Comparison: M-flow vs LocalMem

### 3.1 M-flow Strong Points Worth Learning

M-flow 体现出的最值得借鉴之处，不是“它用了图”，而是：

1. **边不是哑连接**
   边本身有语义，可参与相关性判断。
2. **路径不是按 hop 简单扩散**
   检索是沿图传播，并按路径代价筛选。
3. **多粒度组织**
   不是只有 chunk / memory，而是包含更高层次的事件、主题、原子事实结构。
4. **检索结果可解释**
   不只是“命中了什么”，而是“沿哪条路径命中的”。

### 3.2 LocalMem Current Strengths

LocalMem 当前强项并不在认知结构设计，而在平台能力：

- 更完整的 API / MCP / runtime / ingest / reflect / timeline 体系
- 更清晰的工程分层
- 更适合做通用记忆底座和长期运行服务
- 已经有图检索雏形，可在现有实现上渐进升级

### 3.3 Strategic Positioning

因此，LocalMem 的最佳路径不是成为 Python 版 M-flow，而是：

**保持 LocalMem 作为记忆操作系统 / 通用记忆平台，同时把图边能力升级到接近 M-flow 的检索层级。**

---

## 4. Design Principles

本次升级遵循以下原则：

### 4.1 Principle A: Memory First, Graph Second

`Memory` 仍然是系统主记录单元。图谱不是替代 memory，而是赋予 memory 更强的组织和推理能力。

### 4.2 Principle B: Not All Knowledge Is Causal

不是所有知识都适合建模成因果关系。

以下内容通常不应强制因果化：

- 用户画像
- 术语定义
- 配置项
- 常识事实
- 标签与分类

因果边应作为高价值关系层，而不是唯一关系层。

### 4.3 Principle C: Every Important Edge Should Be Traceable

高价值边必须尽量有依据，至少应能回答：

- 这条边来自哪条 memory 或文档
- 是 LLM 推断、显式陈述，还是人工确认
- 置信度是多少

### 4.4 Principle D: Retrieval Must Prefer Explanatory Paths

图检索不只要“找得回”，还要“说得清”。

系统应优先召回：

- 路径短
- 关系类型匹配
- 有证据支持
- 因果/依赖方向正确
- 边权高、证据新、冲突少

---

## 5. Target Architecture

### 5.1 Four-Layer Knowledge Structure

建议将 LocalMem 的知识结构抽象为四层：

| Layer | Purpose | Current Mapping | Upgrade Direction |
|------|---------|-----------------|------------------|
| `memory` | 原始记忆单元 | `Memory` | 保留为主存储 |
| `entity` | 人/事/物/概念节点 | `Entity` | 保留并增强规范化 |
| `relation` | 实体间结构关系 | `EntityRelation` | 扩展为多类型、可解释边 |
| `claim/path` | 结论与推理链 | 新增 | 支撑因果和证据追溯 |

### 5.2 Relation Taxonomy

建议把边至少分成以下几类：

| Category | Relation Types | Meaning |
|---------|----------------|---------|
| Structural | `related_to`, `belongs_to`, `part_of` | 一般结构关系 |
| Behavioral | `uses`, `calls`, `owns`, `works_with` | 行为或角色关系 |
| Dependency | `depends_on`, `required_for`, `blocks` | 条件与依赖关系 |
| Causal | `causes`, `enables`, `prevents`, `results_in` | 因果关系 |
| Evidence | `evidenced_by`, `supports`, `contradicts` | 证据支持或冲突 |
| Temporal | `before`, `after`, `during` | 时序关系 |

### 5.3 Edge Levels

边应分为三层语义强度：

| Level | Description | Typical Source |
|------|-------------|----------------|
| `observed` | 文本中直接出现 | extractor / parser |
| `inferred` | 由多条记忆综合推断 | reflect / reasoning |
| `confirmed` | 人工确认或高置信规则确认 | explicit user / system |

---

## 6. Proposed Data Model

### 6.1 Keep Existing Core Models

继续保留以下现有模型：

- `Memory`
- `Entity`
- `EntityRelation`
- `MemoryEntity`

### 6.2 Extend EntityRelation

建议将当前 `EntityRelation` 从“轻量边”升级为“可解释边”：

```go
type EntityRelation struct {
    ID              string
    SourceID        string
    TargetID        string
    RelationType    string
    Weight          float64
    Directional     bool
    SemanticText    string
    Confidence      float64
    RelationLevel   string
    SourceKind      string
    PrimaryEvidence string
    Metadata        map[string]any
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

字段说明：

- `SemanticText`: 边的可检索自然语言描述
- `Directional`: 是否需要方向敏感
- `Confidence`: 关系可信度
- `RelationLevel`: `observed / inferred / confirmed`
- `SourceKind`: `extractor / reflect / user / imported`
- `PrimaryEvidence`: 主证据 ID

### 6.3 New RelationEvidence Table

建议新增 `relation_evidences`，不要把所有证据都塞进 relation metadata。

```text
relation_evidences
- id
- relation_id
- memory_id
- document_id
- excerpt
- support_type        // supports / contradicts / neutral
- confidence
- happened_at
- created_at
```

用途：

- 一条关系可对应多条证据
- 支持冲突分析
- 支持路径解释
- 支持后续因果验证和证据裁剪

### 6.4 Optional Claim Layer

若后续要做更强解释能力，可新增 `claims`：

```text
claims
- id
- content
- claim_type          // fact / conclusion / hypothesis / policy
- confidence
- status              // draft / active / deprecated
- scope
- source_memory_id
- created_at
- updated_at
```

适用场景：

- reflect 产出结论
- 项目状态摘要
- 用户偏好总结
- 可被多条 evidence 支持或反驳的“结论型知识”

首版不是必须，但建议在设计上留口。

---

## 7. Retrieval Upgrade Path

### 7.1 Current State

当前图检索核心思路是：

1. 文本或预处理命中实体
2. 沿 `entity_relations` 扩散
3. 收集相关 memory
4. 通过深度衰减给分
5. 与其他通道做加权 RRF

这个版本已经能做“图增强召回”，但仍存在问题：

- 没有 relation-level 语义评分
- 没有证据权重
- 没有方向敏感
- 没有路径级解释
- 没有对因果/依赖查询做专门优化

### 7.2 Target Graph Scoring Model

建议将 graph score 升级为：

```text
graph_score =
  seed_match_score
  + relation_type_score
  + relation_text_score
  + edge_weight_score
  + evidence_score
  + direction_score
  - depth_penalty
  - contradiction_penalty
```

说明：

- `seed_match_score`: 起点实体与查询的匹配程度
- `relation_type_score`: 查询意图与边类型的一致性
- `relation_text_score`: 查询与 `SemanticText` 的匹配程度
- `edge_weight_score`: 业务权重
- `evidence_score`: 是否有高质量证据支撑
- `direction_score`: 对 `depends_on / causes / before` 等方向边给予方向一致加分
- `depth_penalty`: 路径越深越扣分
- `contradiction_penalty`: 被高置信反证命中的边降分

### 7.3 Query Intent to Edge Preference

建议让 query intent 直接影响边类型偏好：

| Query Intent | Preferred Edge Types |
|-------------|----------------------|
| `relational` | `related_to`, `uses`, `works_with` |
| `causal` | `causes`, `enables`, `prevents`, `results_in` |
| `dependency` | `depends_on`, `required_for`, `blocks` |
| `evidence` | `evidenced_by`, `supports`, `contradicts` |
| `temporal` | `before`, `after`, `during` |

### 7.4 Edge Retrieval as First-Class Channel

建议新增一条独立检索通道：

**Edge Retrieval Channel**

流程：

1. 用查询匹配实体
2. 用查询同时匹配边的 `SemanticText`
3. 合并得到候选路径
4. 按路径分数召回 memory / claim / evidence

这样 Graph 通道就不再只是 BFS，而是“边驱动的路径检索”。

### 7.5 Explainable Result Shape

建议扩展检索结果结构，支持返回解释信息：

```text
SearchResultExplain
- matched_entity_ids
- matched_relation_ids
- relation_types
- path
- evidence_memory_ids
- explanation
```

示例：

```text
命中 memory: "服务超时与 Redis 连接池耗尽有关"
路径:
RedisPool --blocks--> RequestProcessing
RequestProcessing --results_in--> Timeout
证据:
memory_123, memory_456
```

---

## 8. Causal Memory Strategy

### 8.1 Why Causality Matters

因果关系的价值主要在于：

- 让知识有来路
- 支持“为什么”类问题
- 支持根因分析
- 支持决策回放
- 支持冲突检查

### 8.2 Why Causality Alone Is Not Enough

不能只做因果边，原因如下：

1. 很多知识不是因果知识
2. 因果抽取错误率更高
3. 即使有因果边，如果边不参与检索评分，图仍然难以被有效利用

因此正确路线是：

**因果边 + 证据边 + 边语义检索**

### 8.3 Recommended Causal Edge Set

首版建议只开放少量高价值因果边：

- `causes`
- `enables`
- `prevents`
- `depends_on`
- `required_for`
- `blocks`
- `results_in`

这样既表达核心逻辑，又避免关系体系过早爆炸。

### 8.4 Fast Answer Strategy

如果目标不只是“更会检索”，而是“更快给用户准确答案”，则不应把所有能力都压在查询时在线推理上。

更合理的结构是三层：

1. **Direct Answer Layer**
   将高频、稳定、可追溯的知识沉淀为可直接回答的单元，例如 `semantic memory`、后续可选的 `claim`、context snapshot、user/project profile。
2. **Edge Fast Path**
   在线查询时先命中上述结晶层，再做 1-hop 或有限 2-hop 的高置信 relation-aware retrieval，用于补充答案或提供解释。
3. **Slow Reasoning Fallback**
   只有在 direct answer 和轻量图检索都不足时，才进入 reflect 或更重的多跳推理。

这意味着 LocalMem 应将更多复杂度前移到：

- ingest 后台整理
- session summarization
- consolidation / crystallization
- reflect 结论沉淀

而不是让每次用户提问都从原始 episodic memories 临时拼装答案。

### 8.5 Why Edge Reasoning Should Not Be the Default Online Path

边推理很重要，但不适合作为所有查询的默认在线路径，原因如下：

1. 路径选择、方向校验、证据检查本身会增加查询时延
2. 图规模增长后，边扩散和候选路径数会快速上升
3. inferred relation 尤其是 causal relation 的稳定性弱于直接命中的结晶知识
4. 用户多数场景要的是答案本身，而不是每次都重跑一次推理链

因此，边推理更适合承担两类职责：

- **离线知识结晶**：把碎片事实和关系整理成可直接回答的知识单元
- **在线结果解释**：在结果命中后补充 why / path / evidence

一句话概括：

**边推理应优先用于知识结晶和结果解释，而不是默认在查询时逐步展开。**

### 8.6 Performance Positioning vs M-flow

M-flow 的重要启发，不只是“边参与检索”，还有它将复杂度从查询时 LLM 推理转移为更可控的检索计算。

对 LocalMem 而言，性能定位应明确区分两类成本：

- **写入/后台成本上升**：抽取关系、生成 evidence、产出 semantic/claim/snapshot、做 consolidation
- **查询时成本受控**：优先 direct answer，其次轻量 edge fast path，最后才进入 reflect

这也是比直接照搬“全量在线图推理”更适合 LocalMem 的地方：

- 保留现有 Memory-first 架构
- 吸收 M-flow 式 edge semantics / path scoring / direct-hit penalty 思想
- 避免把所有查询都变成重图搜索或多轮 LLM 反思

最终目标不是“每次都推理得更深”，而是：

**把复杂推理前移到离线，把简单命中留给在线。**

---

## 9. Implementation Plan

### Phase 1: Edge-Aware Scoring Without Migration

目标：

- 在不新增表的前提下，先让现有 Graph 通道更聪明

实施内容：

1. 在 graph retrieve 中纳入 `relation_type`、`weight`、方向、实体重叠度评分
2. 为 relational / dependency / causal intent 加入边类型偏好
3. 返回基础路径解释
4. 为 graph result 增加 `matched_relations` 元信息

优点：

- 成本最低
- 风险最小
- 能快速验证“边评分”是否显著提升效果

### Phase 1.5: Build Fast Answer Path

目标：

- 让常见问题优先命中已经结晶好的答案层，而不是默认走慢推理

实施内容：

1. 增加 semantic / summary / snapshot 优先检索策略
2. 为 query intent 区分 direct-answer query 与 deep-reasoning query
3. 新增 fast path 命中阈值；命中足够高时直接返回答案
4. 仅在 fast path 不足时回退到 graph retrieve / reflect

优点：

- 可显著降低常见问答的平均时延
- 可减少不必要的在线推理波动
- 与后续 claim layer 和 evidence layer 自然兼容

### Phase 2: Introduce Evidence Layer

目标：

- 让高价值边可追溯、有证据

实施内容：

1. 新增 `relation_evidences`
2. extractor/reflect 在写 relation 时同步写 evidence
3. 检索打分纳入 evidence count、confidence、freshness
4. 在结果中返回 evidence memory ids 和 excerpt

### Phase 3: Add Causal Edge Family

目标：

- 让系统支持 why / root cause / dependency 类问题

实施内容：

1. 扩展 extractor relation types
2. 增加 causal intent / dependency intent
3. 对方向敏感边做方向校验
4. 新增 causal retrieval tests

### Phase 4: Optional Claim Layer

目标：

- 让系统能管理“结论”而不是只管理原始记忆

实施内容：

1. 新增 `claims`
2. reflect 产出结论型节点
3. claim 与 evidence / relation / memory 建图
4. 支持“结论 -> 证据 -> 原始记忆”的完整追溯

---

## 10. File-Level Change Suggestions

建议按以下代码位置推进：

### Retrieval

- `internal/search/retriever.go`
  - 重写 graph score 逻辑
  - 增加 relation-aware traversal
  - 支持 explainable graph results
  - 增加 direct answer fast path 与 fallback orchestration

- `internal/search/preprocess.go`
  - 新增 `causal` / `dependency` / `evidence` 意图
  - 根据 query 识别边类型偏好
  - 区分 fast-answer query 与 deep-reasoning query

### Model

- `internal/model/graph.go`
  - 扩展 `EntityRelation`
  - 新增 relation evidence / optional claim DTO

- `internal/model/request.go`
  - 扩展 retrieve request，支持 explain / path / evidence 开关

### Memory / Extractor

- `internal/memory/extractor.go`
  - 关系抽取升级为支持因果关系和 evidence 绑定

- `internal/memory/graph_manager.go`
  - 新增 evidence 读写接口
  - 新增按 relation 查询 evidence 的方法

- `internal/memory/summarizer.go`
  - 增加可直接回答的 semantic summary / snapshot 产出策略

- `internal/memory/consolidation.go`
  - 将高重复、高稳定记忆簇结晶为更适合 direct answer 的浓缩知识

### API

- `internal/api/search_handler.go`
  - 支持 fast path / explain / evidence 开关

- 新增可选 `/v1/answer`
  - 优先 direct answer
  - 不足时回退 graph / reflect

### Store

- `internal/store/interfaces.go`
  - 扩展 `GraphStore` 接口

- `internal/store/sqlite_graph.go`
  - 支持 relation evidence CRUD
  - 支持 relation semantic text 检索

- `internal/store/sqlite_migration_*.go`
  - 添加 relation evidence 表及索引

### Testing

- `testing/search/graph_retrieval_test.go`
  - 增加 relation-aware scoring case
  - 增加 direction-sensitive case
  - 增加 evidence-aware case

- 新增 `testing/search/causal_retrieval_test.go`
- 新增 `testing/store/sqlite_relation_evidence_test.go`

---

## 11. Evaluation Metrics

升级后应重点关注以下指标：

### Retrieval Quality

- Recall@K
- MRR / NDCG
- causal query hit rate
- dependency query hit rate
- explainability coverage

### Edge Quality

- 边抽取 precision
- 因果边 precision
- evidence coverage
- contradiction rate

### Product Quality

- 用户是否更容易理解“为什么命中”
- 调试/复盘场景是否明显改善
- reflect 产出的结论是否更稳定
- direct answer hit rate
- p50 / p95 answer latency
- fast path -> fallback 回退率

---

## 12. Risks

### 12.1 Over-Modeling

如果一开始就强推 claim 层、全量因果图、全量路径解释，系统会明显变重。

控制策略：

- 先做 Phase 1
- 用最小关系集合起步
- 先验证边评分收益，再扩 schema

### 12.2 False Causality

错误因果比普通错误关系危害更大。

控制策略：

- 将 `causal` 与 `related` 严格区分
- 默认给 inferred causal edge 更低置信度
- 必须配 evidence 才能高权重参与检索

### 12.3 Graph Explosion

边过多、关系类型过多、遍历过深，都会导致图检索噪声膨胀。

控制策略：

- 限制默认 depth
- 引入路径成本
- 对低置信和低证据边降权

建议进一步补充以下工程约束：

- 用最小关系集合起步，优先保留 `related_to`、高价值 dependency、少量 causal edge；避免首版开放完整 relation taxonomy
- 将“可追溯”作为高价值边准入条件：至少记录来源 memory/document、source kind、confidence；缺少这些信息的 inferred edge 默认低权或不入库
- 对同一 `(source_id, target_id, relation_type)` 做 relation 去重；新增证据优先写入 `relation_evidences`，而不是重复创建语义相近的边
- 为每个实体节点设置 fan-out budget；单 relation type 只保留 top-K 高 confidence / 高 evidence / 高 freshness 的边
- 将 query intent 作为边选择器；causal / dependency / evidence 查询默认只扩展对应边族，而不是穿透整个关系图
- 将“存在边”和“可强扩散边”分开；低质量边可以保留用于离线分析，但默认不进入高权重检索路径
- 对长期无证据、长期低命中、被高置信反证覆盖的边做 decay / archive / prune，避免图只增不减

可将其抽象为一句实现原则：

**少类型、强准入、可去重、有预算、能衰减。**

---

## 13. Recommended Final Direction

最终建议路线可以概括为一句话：

**LocalMem 不应只做“存记忆的数据库”，而应演进为“带证据、因果解释与快速答复能力的图驱动记忆平台”。**

具体上分四步：

1. 先把边做成一等检索对象
2. 再建立 direct answer fast path
3. 再把高价值关系升级为因果/依赖/证据边
4. 最后把检索结果升级为可解释路径输出

---

## 14. Immediate Next Step

最推荐的下一步不是直接大迁移，而是：

**先做 Phase 1 的 relation-aware graph scoring 改造。**

原因：

- 改动面最小
- 与现有架构兼容
- 可以最快验证 M-flow 式“边参与评分”是否对 LocalMem 有真实收益

若验证有效，再进入：

- direct answer fast path
- `relation_evidences`
- 因果边族
- claim layer

---

## 15. Decision Snapshot

当前讨论已形成的稳定结论：

1. 因果关系值得引入，但不应替代所有关系
2. 因果边不能替代 M-flow 式边思想
3. 最合理的方向是“因果边 + 证据边 + 边语义检索”
4. LocalMem 的优势在平台完整度，升级应保持渐进式演进
5. 快速答复能力应依赖结晶层 + 轻量图检索 + 慢推理 fallback 的分层结构
6. 边推理更适合用于知识结晶和结果解释，而不是默认在线全量展开
7. 下一步优先级应是 relation-aware scoring，而不是一次性重构全部存储层

---

## 16. Final Architecture Snapshot

如果按本 blueprint 持续演进，LocalMem 最终应从“混合检索的记忆库”升级为“分层的图驱动记忆系统”。

其最终形态可概括为四层：

1. **Memory Layer**
   继续以 `Memory` 作为主记录单元，承载原始 episodic、semantic、procedural 记忆，以及文档、会话、reflect、consolidation 等来源。
2. **Relation Layer**
   将 `EntityRelation` 升级为可解释边，支持 structural / behavioral / dependency / causal / evidence / temporal 等关系，并引入 relation confidence、direction、semantic text、evidence。
3. **Answer Layer**
   将 semantic summary、context snapshot、claim、稳定 profile 等结晶知识作为可直接回答的单元，优先服务常见问答。
4. **Reasoning Layer**
   用 relation-aware scoring、bounded path retrieval、evidence tracing、reflect fallback 支撑 why / dependency / root-cause / cross-memory synthesis 等复杂问题。

查询路径应稳定收敛为：

`direct answer fast path -> edge fast path -> slow reasoning fallback`

其中：

- `direct answer fast path` 负责低延迟命中结晶知识
- `edge fast path` 负责轻量路径补充和解释
- `slow reasoning fallback` 只在高复杂度问题或证据不足时启用

这会把 LocalMem 变成一种系统：

- 平时在后台持续整理知识
- 在线优先快速回答
- 必要时再做有限图推理
- 返回结果时尽量说明 why / path / evidence

因此，最终的目标系统不再只是：

- 存储记忆
- 做相似检索
- 返回若干相关文本

而是：

- 产出可直接回答的知识单元
- 用图边组织依赖、因果、证据和时序关系
- 在查询时按路径和证据选择答案
- 在结果中提供解释和追溯

一句话总结：

**LocalMem 的终态应是一个兼具快速答复、图路径解释、证据追溯与有限推理能力的认知型记忆引擎。**
