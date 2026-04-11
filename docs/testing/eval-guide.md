# LocalMem 100 题全流程评测指南

本文档描述从**数据输入**到**结果返回**的完整 Pipeline 流程，以及如何本地运行 100 题评测验证每一步。

---

## 一、完整 Pipeline 流程图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           写入阶段（Write Path）                             │
│                                                                             │
│  ① 输入数据 ──→ ② 噪声过滤 ──→ ③ 落库 SQLite ──→ ④ 向量化 Qdrant          │
│  (会话/文档)    (长度<10丢弃)    (memories表)       (embedding API)          │
│                                                                             │
│       ↓ 异步                                                                │
│                                                                             │
│  ⑤ 实体抽取（三层）──→ ⑥ 写入关系 SQL                                       │
│     L1: 分词匹配          memory_entities (关联)                             │
│     L2: 质心匹配          entity_relations (共现)                            │
│     L3: 近邻传播          entity_candidates (候选)                           │
│                                                                             │
│  ⑦ 候选晋升（heartbeat 周期）                                                │
│     candidates → entities → 回溯关联 → 计算质心向量                          │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                           读取阶段（Read Path）                              │
│                                                                             │
│  ⑧ 输入问题 ──→ ⑨ 意图分析 ──→ ⑩ 选择管线 ──→ ⑪ 多路检索 ──→ ⑫ 返回结果   │
│  (query)       (LLM/规则)     (6种管线)       (FTS+向量+图谱)   (排序+披露)  │
│                可关闭                                                        │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 二、写入阶段各步骤详解

### ① 输入数据

来源：飞书消息、微信对话、知识文档、API 调用、MCP 工具

```
入口: Manager.Create(ctx, &CreateMemoryRequest{
    Content:    "张三决定用 Rust 重写数据处理模块",
    Kind:       "conversation",
    SourceType: "feishu",
    SourceRef:  "feishu://chat/group-eng/msg/001",
    Scope:      "project/x",
})
```

**代码位置**: `internal/memory/manager.go:83`

### ② 噪声过滤

在批量 ingest 入口处拦截明显噪声：
- content 长度 < 10 字符（如"好的"、"嗯"）→ 直接丢弃
- 匹配自定义噪声模式 → 丢弃

```yaml
# 配置
ingest:
  noise_filter:
    min_content_length: 10
    patterns: ["ok", "收到", "👍"]
```

**代码位置**: `internal/memory/noise_filter.go` → `IsNoiseContent()`
**调用位置**: `internal/memory/manager_lifecycle.go` → `buildConversationMemories()`

### ③ 落库 SQLite

写入 `memories` 表（35 列），包括：
- 内容哈希去重（`content_hash` SHA-256）
- 自动生成 ID、时间戳、默认 retention_tier
- FTS5 全文索引同步写入（content/excerpt/summary）

```sql
INSERT INTO memories (id, content, content_hash, kind, source_type, source_ref, scope, ...)
```

**代码位置**: `internal/store/sqlite.go` → `Create()`

### ④ 向量化到 Qdrant

调用 Embedding API 将内容转为向量，写入 Qdrant：

```
内容 "张三决定用 Rust 重写数据处理模块"
  → Embedding API (text-embedding-3-small)
  → [0.012, -0.034, 0.056, ...] (1536 维)
  → Qdrant.Upsert(memoryID, vector, payload)
```

```yaml
# 配置
storage:
  qdrant:
    enabled: true
    url: "http://localhost:6333"
    collection: "memories"
    dimension: 1536
llm:
  embedding:
    provider: openai
    model: text-embedding-3-small
```

**代码位置**: `internal/memory/manager.go:121` → `handleVectorWrite()`
**注意**: Qdrant 不可用时自动跳过，不阻断写入

### ⑤ 实体抽取（三层 EntityResolver）

异步执行，不阻塞写入返回：

```
Layer 1（分词精确匹配, confidence=0.9）:
  "张三决定用Rust重写数据处理模块"
  → 分词: [张三, 决定, 用, Rust, 重写, 数据处理, 模块]
  → 匹配已知实体: "张三"(person) ✓, "Rust"(tool) ✓
  → 未命中: "数据处理模块" → 存入 entity_candidates

Layer 2（质心匹配, confidence=0.7）:
  → 记忆向量 vs entity_centroids collection
  → "项目X"实体的质心与此记忆相似度 0.72 > 阈值 0.6 → 关联

Layer 3（近邻传播, confidence=0.5）:
  → Qdrant Top-10 近邻记忆 → 它们关联的实体
  → "Go"出现在 3 个近邻中 → 传播关联

合并: {张三: 0.9, Rust: 0.9, 项目X: 0.7, Go: 0.5}
```

```yaml
# 配置
extract:
  use_llm: false         # 关闭 LLM 抽取
  resolver:
    enabled: true        # 启用向量解析器
    centroid_threshold: 0.6
    neighbor_k: 10
    neighbor_min_count: 2
    candidate_promote_min: 3
```

**代码位置**: `internal/memory/entity_resolver.go` → `ResolveWithEmbeddings()`
**调用位置**: `internal/memory/manager_create_helpers.go` → `handleAutoExtract()`

### ⑥ 写入关系 SQL

为每个命中的实体写入关联和共现关系：

```sql
-- 记忆-实体关联
INSERT INTO memory_entities (memory_id, entity_id, role, confidence) VALUES (?, ?, 'mentioned', 0.9);

-- 实体间共现关系（upsert: 存在则 mention_count++）
UPDATE entity_relations SET mention_count = mention_count + 1, last_seen_at = NOW()
  WHERE source_id = '张三' AND target_id = 'Rust' AND relation_type = 'related_to';
-- 不存在则 INSERT（weight=1.0, mention_count=1）
```

**代码位置**: `internal/memory/entity_resolver.go` → `writeAssociations()`
**Store 方法**: `GraphStore.CreateMemoryEntity()`, `GraphStore.UpdateRelationStats()`

### ⑦ 候选晋升（Heartbeat 周期任务）

```
entity_candidates 表:
  "数据处理模块" hit_count=5, memory_ids=[m1,m3,m7,m12,m15]

Heartbeat 扫描 (candidate_promote_min_hits=3):
  → 晋升为 Entity{name:"数据处理模块", type:"concept"}
  → 回溯: 为 m1,m3,m7,m12,m15 创建 memory_entities 关联
  → 计算质心向量 → 写入 entity_centroids collection
  → 删除候选记录
```

```yaml
# 配置
heartbeat:
  enabled: true
  interval: 30m
  candidate_promote_min_hits: 3
```

**代码位置**: `internal/heartbeat/candidate_promotion.go` → `runCandidatePromotion()`

---

## 三、读取阶段各步骤详解

### ⑧ 输入问题

```
入口: Retriever.Retrieve(ctx, &RetrieveRequest{
    Query: "张三最近在用什么编程语言？",
    Limit: 10,
})
```

**代码位置**: `internal/search/retriever.go:203`

### ⑨ 意图分析（Strategy Agent，可关闭）

分析查询意图，决定检索策略：

```
"张三最近在用什么编程语言？"
  → 分类: factual（事实型）
  → 提取: 时间意图=recent, 实体候选=[张三, 编程语言]
```

**两种模式**:
- `strategy.use_llm: true` → LLM 分析（更准，有延迟）
- `strategy.use_llm: false` → 规则分类（更快，fallback 到 exploration 管线）

```yaml
# 配置（关闭 LLM 意图分析）
retrieval:
  strategy:
    use_llm: false
    fallback_pipeline: exploration
```

**代码位置**: `internal/search/retriever.go:113` → `selectPipelineWithPlan()`
**策略 Agent**: `internal/search/strategy/`

### ⑩ 选择管线

根据意图分析结果选择检索管线：

| 管线 | 适用场景 | 检索通道 |
|------|---------|---------|
| **precision** | 精确查找 | FTS + Graph 并行 → 图谱感知排序 |
| **exploration** | 开放探索 | FTS + Graph + Temporal → RRF 融合 |
| **semantic** | 语义理解 | Vector + FTS → RRF 融合 |
| **association** | 关联发现 | Graph(depth=3) → 图谱重排 |
| **fast** | 快速检索 | FTS only |
| **full** | 全通道 | FTS + Graph + Vector → 图谱感知排序 |

无意图分析时默认走 `exploration`。

**代码位置**: `internal/search/pipeline/builtin/builtin.go` → `RegisterBuiltins()`

### ⑪ 多路检索（Pipeline 执行）

以 `exploration` 管线为例：

```
并行执行:
  ├── FTS 通道: "张三 编程语言" → BM25 全文检索 → 命中记忆列表
  ├── Graph 通道: "张三" → FindEntitiesByName → BFS 遍历图谱 → 关联记忆
  │     打分: depthScore × timeDecay = 1/(depth+1) × e^(-λ×days)
  └── Temporal 通道: 时间范围过滤（如有时间意图）

合并: RRF 融合三路结果
  score = Σ weight × 1/(k + rank + 1)

后处理:
  → 去重
  → 强度加权 (strength × score)
  → Rerank (可选)
  → MMR 多样性重排 (可选)
  → Token 预算裁剪 / 渐进式披露
  → 实体发现: 批量加载结果记忆的关联实体
```

```yaml
# 配置
retrieval:
  graph_enabled: true
  graph_depth: 2
  graph_weight: 0.8
  fts_weight: 1.0
  qdrant_weight: 0.6
  relation_decay_lambda: 0.015    # 图谱时间衰减
```

**代码位置**:
- FTS: `internal/search/stage/fts.go`
- Graph: `internal/search/stage/graph.go`（含时间衰减 `decayWeight()`）
- Vector: `internal/search/stage/vector.go`
- 融合: `internal/search/stage/merge.go`
- 披露: `internal/search/stage/disclosure.go`
- 实体发现: `internal/search/retriever.go:168` → `enrichWithEntities()`

### ⑫ 返回结果

```json
{
  "results": [
    {
      "memory": {"id": "m1", "content": "张三决定用 Rust 重写...", "source_ref": "feishu://chat/..."},
      "score": 0.85,
      "source": "graph",
      "entities": [
        {"id": "e1", "name": "张三", "entity_type": "person"},
        {"id": "e2", "name": "Rust", "entity_type": "tool"}
      ]
    },
    ...
  ],
  "disclosure": {
    "pipelines": [
      {"name": "core", "budget": 800, "used_tokens": 750, "items": [...]},
      {"name": "context", "budget": 500, "used_tokens": 480, "items": [...]},
      ...
    ],
    "total_budget": 2000,
    "total_used": 1850
  }
}
```

**渐进式披露**（可选开启）:
```yaml
retrieval:
  disclosure:
    enabled: true
    core_weight: 0.4
    context_weight: 0.25
    entity_weight: 0.2
    timeline_weight: 0.15
```

---

## 四、评测模式

### 模式 A：Layer 1 Only（零依赖，最快）

只测 ①③⑤(L1)⑥⑦ → ⑧⑩⑪⑫，无向量化：

```bash
git clone https://github.com/MemoryGet/LocalMem.git && cd LocalMem
go mod download
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverDB -timeout 30m
```

覆盖步骤: ①→③→⑤(Layer1)→⑥→⑦→⑧→⑩→⑪(FTS+Graph)→⑫
跳过: ②(噪声过滤) ④(向量化) ⑤(Layer2/3) ⑨(意图分析)
耗时: 2-5 分钟

### 模式 B：完整三层（推荐正式评测）

全 Pipeline 覆盖 ①②③④⑤⑥⑦ → ⑧⑩⑪⑫：

```bash
# 前置
docker run -d --name qdrant -p 6333:6333 qdrant/qdrant:latest
export EMBEDDING_API_KEY="sk-your-key"

# 运行
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverFull -timeout 60m
```

覆盖步骤: ①→③→④(Qdrant)→⑤(L1+L2+L3)→⑥→⑦→⑧→⑩→⑪(FTS+Vector+Graph)→⑫
跳过: ②(噪声过滤, eval 数据无噪声) ⑨(意图分析, eval 固定管线)
耗时: 5-15 分钟

### 模式 C：LLM 对比（旧方案基线）

```bash
export OPENAI_API_KEY="sk-your-key"
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalSharedDB -timeout 60m
```

覆盖步骤: ①→③→⑤(LLM抽取)→⑥→⑧→⑩→⑪(FTS+Graph)→⑫
耗时: 30-60 分钟

### 模式 D：FTS 基线

```bash
go test -v ./testing/eval/ -run TestLongMemEvalOracle -timeout 30m
```

覆盖步骤: ①→③→⑧→⑪(FTS only)→⑫
耗时: 2 分钟

---

## 五、环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `EVAL_MAX_QUESTIONS` | 100 | 评测题数 |
| `EMBEDDING_API_KEY` | — | Embedding API Key（模式 B） |
| `EMBEDDING_MODEL` | text-embedding-3-small | Embedding 模型 |
| `EMBEDDING_DIMENSION` | 1536 | 向量维度 |
| `QDRANT_URL` | http://localhost:6333 | Qdrant 地址 |
| `OPENAI_API_KEY` | — | LLM API Key（模式 C） |
| `OPENAI_BASE_URL` | https://api.openai.com/v1 | API 基址 |

---

## 六、输出指标

```
=== Eval: full-resolver (L1+L2+L3, no LLM) — 1200 memories, 350 entities ===
  HitRate:  72.0%     ← 100题中命中72题（Top-10内包含正确答案）
  MRR:      0.583     ← 平均倒数排名（正确答案越靠前越高，满分1.0）
  NDCG@10:  0.512     ← 归一化折损累积增益
  Recall@1: 48.0%     ← 第1条就命中的比例
  Recall@3: 62.0%     ← 前3条内命中
  Recall@5: 68.0%     ← 前5条内命中
  Recall@10:72.0%     ← 前10条内命中
```

### 预期对比

| 模式 | HitRate | MRR | 备注 |
|------|---------|-----|------|
| D: FTS Only | ~55-65% | ~0.40-0.50 | 基线 |
| A: Layer 1 | ~60-70% | ~0.45-0.55 | +图谱增强 |
| **B: 完整三层** | ~65-75% | ~0.50-0.60 | **+向量匹配** |
| C: LLM 抽取 | ~70-80% | ~0.55-0.65 | 旧方案上限 |

---

## 七、全模式对比一键脚本

```bash
export OPENAI_API_KEY="sk-your-key"
docker run -d --name qdrant -p 6333:6333 qdrant/qdrant:latest

# D → A → B → C 四种模式依次对比
go test -v ./testing/eval/ -run TestLongMemEvalOracle -timeout 30m
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverDB -timeout 30m
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverFull -timeout 60m
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalSharedDB -timeout 60m

# 查看基线对比
go test -v ./testing/eval/ -run TestRegressionCheck -timeout 5m
```

---

## 八、故障排查

| 问题 | 原因 | 解决 |
|------|------|------|
| `testdata/longmemeval-oracle.json not found` | 数据集缺失 | 确认文件存在于 `testing/eval/testdata/` |
| `init qdrant: connection refused` | Qdrant 未启动 | `docker run -d -p 6333:6333 qdrant/qdrant:latest` |
| `EMBEDDING_API_KEY required` | 未设置 Key | `export EMBEDDING_API_KEY="sk-xxx"` |
| `embed batch: 401` | Key 无效 | 检查 Key |
| `embed batch: 429` | 限流 | 等待重试 / 换高配额 Key |
| HitRate 很低 | 分词器质量 | 换 Gse/Jieba 分词器 |
| 评测很慢 | Embedding API 慢 | 用本地 Ollama: `OPENAI_BASE_URL=http://localhost:11434/v1` |
