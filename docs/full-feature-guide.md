# LocalMem 全量功能部署指南

> 本文档介绍如何开启 LocalMem 的所有功能模块，包括分词引擎、向量检索、知识图谱、LLM 推理、文档摄入、自治维护等。

---

## 目录

- [架构概览](#架构概览)
- [检索流程详解](#检索流程详解)
- [分词引擎](#分词引擎)
- [LLM 配置](#llm-配置)
- [Embedding 配置](#embedding-配置)
- [全量功能配置示例](#全量功能配置示例)
- [各功能模块说明](#各功能模块说明)
  - [三路混合检索](#三路混合检索)
  - [查询预处理](#查询预处理)
  - [摘要与抽象生成](#摘要与抽象生成)
  - [实体抽取与知识图谱](#实体抽取与知识图谱)
  - [多轮推理引擎 Reflect](#多轮推理引擎-reflect)
  - [文档摄入管线](#文档摄入管线)
  - [自治维护 Heartbeat](#自治维护-heartbeat)
  - [记忆合并 Consolidation](#记忆合并-consolidation)
  - [记忆衰减与保留层级](#记忆衰减与保留层级)
  - [MMR 多样性重排](#mmr-多样性重排)
- [外部依赖服务](#外部依赖服务)
- [MCP 工具一览](#mcp-工具一览)

---

## 架构概览

```
查询输入
  ↓
[查询预处理] → 关键词提取 + 意图分类 + 实体匹配（可选 LLM 增强）
  ↓
[三路并行检索]
  ├─ SQLite FTS5 (BM25 全文检索)
  ├─ Qdrant Vector (向量语义检索)
  └─ Knowledge Graph (知识图谱关联)
  ↓
[加权 RRF 融合] (k=60)
  ↓
[强度加权] (衰减 × 访问频率增益)
  ↓
[MMR 多样性重排] (可选)
  ↓
最终结果
```

---

## 检索流程详解

### 1. 查询预处理 (Preprocessor)

查询进入后首先经过预处理，生成 `QueryPlan`：

| 步骤 | 说明 |
|------|------|
| 分词 + 停用词过滤 | 提取关键词列表 |
| 图谱实体匹配 | 在知识图谱中查找匹配的实体 |
| 意图分类 | 规则/LLM 判断查询意图类型 |
| 动态权重计算 | 根据意图调整三路检索权重 |

**意图类型与权重映射：**

| 意图 | FTS 权重 | Qdrant 权重 | Graph 权重 |
|------|---------|-------------|-----------|
| keyword (关键词) | 1.5 | 0.6 | 0.5 |
| semantic (语义) | 0.6 | 1.5 | 0.8 |
| temporal (时间) | 1.3 | 0.8 | 0.6 |
| relational (关系) | 0.4 | 0.7 | 1.8 |
| general (通用) | 1.0 | 1.0 | 1.0 |

### 2. 三路检索

- **SQLite FTS5**: 使用 BM25 算法，支持三列加权（content:10, abstract:5, summary:3）
- **Qdrant Vector**: 将查询转为 embedding 后进行余弦相似度检索
- **Knowledge Graph**: 从查询中提取实体 → 图谱遍历 → 关联记忆

### 3. RRF 融合

```
score(id) = Σ weight × 1/(60 + rank + 1)
```

多通道结果按此公式合并排序。

### 4. 强度加权

```
effectiveStrength = strength × exp(-decayRate × hours) × (1 + 0.15 × log2(accessCount + 1))
```

### 5. MMR 重排 (可选)

```
mmrScore = λ × relevance - (1-λ) × maxSimilarity(已选集合)
```

λ=0.7 时偏向相关性，降低重复。

---

## 分词引擎

通过 `storage.sqlite.tokenizer.provider` 选择，影响 FTS5 全文检索质量。

### simple（默认，无依赖）

```yaml
storage:
  sqlite:
    tokenizer:
      provider: simple
```

- CJK 字符逐字拆分，英文按空白分词
- 优点：零依赖，开箱即用
- 缺点：中文短查询效果差（"站会时间"拆为4个单字）

### jieba（推荐中文环境）

```yaml
storage:
  sqlite:
    tokenizer:
      provider: jieba
      jieba_url: "http://localhost:8866"
```

需要启动 Jieba 分词服务：

```bash
python tools/jieba_server.py  # 启动 → http://localhost:8866
```

- 精确中文分词（"站会时间" → "站会" "时间"）
- 支持全模式/精确模式
- 需要 Python 环境 + jieba 库

### gse（Go 原生，推荐无 Python 环境）

```yaml
storage:
  sqlite:
    tokenizer:
      provider: gse
      dict_path: ""  # 空=使用内置词典
      stopword_files:
        - "config/stopwords_zh.txt"
```

- Go 原生实现，无外部依赖
- 内置中文词典 + 停用词过滤
- 分词质量介于 simple 和 jieba 之间

### noop（透传）

```yaml
storage:
  sqlite:
    tokenizer:
      provider: noop
```

- 不做任何分词处理，直接传入 FTS5
- 适合纯英文或已预分词的场景

---

## LLM 配置

LLM 被以下模块使用：摘要生成、实体抽取、查询预处理增强、Reflect 推理、矛盾检测、记忆合并。

```yaml
llm:
  default_provider: openai  # openai | claude | ollama

  openai:
    api_key: "${OPENAI_API_KEY}"
    base_url: ""  # 空=官方 API，可改为兼容 API（DeepSeek/本地）
    model: "gpt-4o-mini"

  claude:
    api_key: "${ANTHROPIC_API_KEY}"
    model: "claude-3-opus-20240229"

  ollama:
    base_url: "http://localhost:11434"
    model: "llama2"

  fallback:  # 故障回退链
    - name: "deepseek"
      base_url: "https://api.deepseek.com/v1"
      api_key: "${DEEPSEEK_API_KEY}"
      model: "deepseek-chat"
```

**兼容 OpenAI 格式的 API 均可使用**（DeepSeek、通义千问、Ollama 等），只需设置 `base_url`。

---

## Embedding 配置

```yaml
llm:
  embedding:
    provider: openai  # openai | ollama
    model: "text-embedding-3-small"  # OpenAI: 1536维
    # 或 Ollama 本地:
    # provider: ollama
    # model: "bge-m3"  # 1024维，多语言
```

| Provider | 模型推荐 | 维度 | 说明 |
|----------|---------|------|------|
| openai | text-embedding-3-small | 1536 | 性价比高，英文/中文均可 |
| openai | text-embedding-3-large | 3072 | 最高质量 |
| ollama | bge-m3 | 1024 | 本地部署，多语言 |
| ollama | nomic-embed-text | 768 | 轻量本地 |

> 注意：Qdrant 的 `dimension` 必须与 embedding 模型维度匹配。

---

## 全量功能配置示例

以下是开启所有功能的完整 `config.yaml`：

```yaml
# ==================== 存储 ====================
storage:
  sqlite:
    enabled: true
    path: "./data/iclude.db"
    search:
      bm25_weights:
        content: 10.0
        abstract: 5.0
        summary: 3.0
    tokenizer:
      provider: jieba  # 推荐中文环境
      jieba_url: "http://localhost:8866"
      stopword_files:
        - "config/stopwords_zh.txt"
        - "config/stopwords_en.txt"

  qdrant:
    enabled: true
    url: "http://localhost:6333"
    collection: "memories"
    dimension: 1536  # 必须与 embedding 模型维度匹配

# ==================== LLM ====================
llm:
  default_provider: openai
  openai:
    api_key: "${OPENAI_API_KEY}"
    base_url: ""
    model: "gpt-4o-mini"
  embedding:
    provider: openai
    model: "text-embedding-3-small"
  fallback:
    - name: "deepseek"
      base_url: "https://api.deepseek.com/v1"
      api_key: "${DEEPSEEK_API_KEY}"
      model: "deepseek-chat"

# ==================== 检索 ====================
retrieval:
  graph_enabled: true
  graph_depth: 1
  fts_weight: 1.0
  qdrant_weight: 1.0
  graph_weight: 0.8
  graph_fts_top: 5
  graph_entity_limit: 10
  access_alpha: 0.15

  mmr:
    enabled: true
    lambda: 0.7

  preprocess:
    enabled: true
    use_llm: true        # LLM 增强查询改写
    llm_timeout: 5s

# ==================== 实体抽取 ====================
extract:
  max_entities: 20
  max_relations: 30
  normalize_enabled: true
  normalize_candidates: 20
  timeout: 30s

# ==================== 推理引擎 ====================
reflect:
  max_rounds: 3
  token_budget: 4096
  round_timeout: 30s
  auto_save: true  # 推理结论自动保存为 mental_model 记忆

# ==================== 自治维护 ====================
heartbeat:
  enabled: true
  interval: 6h
  contradiction_enabled: true       # LLM 矛盾检测
  contradiction_max_comparisons: 50
  decay_audit_min_age_days: 90
  decay_audit_threshold: 0.1

# ==================== 调度器 ====================
scheduler:
  enabled: true
  cleanup_interval: 6h          # 过期记忆清理
  access_flush_interval: 5m     # 访问计数批量写入
  consolidation_interval: 24h   # 记忆合并

# ==================== 记忆合并 ====================
consolidation:
  enabled: true
  min_age_days: 30
  similarity_threshold: 0.85
  min_cluster_size: 3
  max_memories_per_run: 200

# ==================== 文档摄入 ====================
document:
  enabled: true
  max_concurrent: 3
  process_timeout: 10m
  max_file_size: 104857600  # 100MB
  cleanup_after_parse: true
  allowed_types:
    - pdf
    - docx
    - pptx
    - xlsx
    - md
    - html
    - txt
  file_store:
    provider: local
    local:
      base_dir: "./data/uploads"
  docling:
    url: "http://localhost:5001"
    timeout: 120s
  tika:
    url: "http://localhost:9998"
    timeout: 60s
  chunking:
    max_tokens: 512
    overlap_tokens: 50
    context_prefix: true
    keep_table_intact: true
    keep_code_intact: true

# ==================== 服务 ====================
server:
  port: 8080

mcp:
  enabled: true
  port: 8081
  default_team_id: "default"
  default_owner_id: "local-user"
```

---

## 各功能模块说明

### 三路混合检索

| 通道 | 配置开关 | 依赖 | 适用场景 |
|------|---------|------|---------|
| SQLite FTS5 | `sqlite.enabled` | 无 | 关键词精确匹配 |
| Qdrant Vector | `qdrant.enabled` | Qdrant 服务 | 语义模糊匹配 |
| Knowledge Graph | `retrieval.graph_enabled` | SQLite + 实体抽取 | 关联推理 |

三通道结果通过 **加权 RRF** 融合，权重可全局配置或由查询意图动态调整。

### 查询预处理

```yaml
retrieval:
  preprocess:
    enabled: true     # 开启预处理
    use_llm: false    # 规则模式（快速，无 LLM 消耗）
    # use_llm: true   # LLM 增强模式（更精准，有延迟和成本）
```

- **规则模式**: 正则意图分类 + 停用词过滤 + 图谱实体匹配，无额外依赖
- **LLM 模式**: LLM 改写查询用于语义检索，提取更精准的关键词和意图

### 摘要与抽象生成

写入记忆时自动触发（content > 50 字符）：

1. **Abstract**: LLM 生成一行摘要，存入 `abstract` 字段
2. **Summary**: 更长的结构化摘要（保留关键信息）

摘要参与 FTS5 检索（BM25 多列加权），提升短查询命中率：

```yaml
storage:
  sqlite:
    search:
      bm25_weights:
        content: 10.0   # 正文权重
        abstract: 5.0    # 摘要权重
        summary: 3.0     # 概述权重
```

### 实体抽取与知识图谱

```yaml
extract:
  max_entities: 20
  max_relations: 30
  normalize_enabled: true  # 实体归一化（链接到已有节点）
```

写入记忆时自动抽取：
1. LLM 从内容中识别实体（人物/组织/概念/工具/地点）和关系
2. 归一化：将新实体与图谱中已有实体进行 LLM 比对，避免重复
3. 写入图谱：实体节点 + 关系边 + 记忆-实体关联

检索时图谱通道：
1. 从查询中提取实体 → 图谱遍历（BFS，深度可配）→ 关联记忆
2. 如果 FTS5 找不到实体，回退到 LLM 从查询中提取实体名

### 多轮推理引擎 Reflect

```yaml
reflect:
  max_rounds: 3        # 最多推理轮数
  token_budget: 4096   # 总 token 预算
  round_timeout: 30s   # 每轮超时
  auto_save: true      # 结论自动存为记忆
```

流程：
1. 第 1 轮：检索相关记忆 → LLM 综合分析 → 判断 "需要更多信息" 或 "可以结论"
2. 第 2~N 轮：LLM 生成后续查询 → 检索 → 再次综合（自动去重避免循环）
3. 结束：返回结论 + 推理链路 + 来源记忆 ID

### 文档摄入管线

```
文件上传 → Docling 解析（回退 Tika）→ 结构化分块 → Embedding → 存储
```

需要外部解析服务：
- **Docling** (推荐): `docker run -p 5001:5001 ds4sd/docling-serve`
- **Tika** (回退): `docker run -p 9998:9998 apache/tika`

分块策略：
- Markdown: 按标题/表格/代码块结构化拆分
- 纯文本: 递归字符拆分（512 token，50 重叠）
- 上下文前缀: 每个 chunk 前加标题路径信息

### 自治维护 Heartbeat

```yaml
heartbeat:
  enabled: true
  interval: 6h
  contradiction_enabled: true
```

周期性自动执行：
| 任务 | 依赖 | 说明 |
|------|------|------|
| 衰减审计 | 无 | 标记 strength < 0.1 的弱记忆 |
| 孤儿清理 | GraphStore | 删除无关联的图谱实体 |
| 矛盾检测 | Qdrant + LLM | 发现语义相近但内容矛盾的记忆对 |
| 摘要回填 | LLM | 补全缺失 abstract 的记忆 |

### 记忆合并 Consolidation

```yaml
consolidation:
  enabled: true
  min_age_days: 30
  similarity_threshold: 0.85
  min_cluster_size: 3
```

- 需要 Qdrant + LLM
- 向量聚类：余弦相似度 > 0.85 的记忆归为一组
- LLM 将每组合并为一条精炼的永久记忆
- 原记忆标记 `consolidated_into` 指向新记忆

### 记忆衰减与保留层级

| 层级 | 衰减率 | 过期时间 | 适用场景 |
|------|--------|---------|---------|
| permanent | 0 | 永不过期 | 核心事实、合并后的记忆 |
| long_term | 0.0005 | 365 天 | 重要项目信息 |
| standard (默认) | 0.001 | 90 天 | 一般工作记忆 |
| short_term | 0.01 | 14 天 | 临时笔记 |
| ephemeral | 0.1 | 1 天 | 会话上下文 |

每次访问（检索命中）会增加 `reinforced_count`，通过访问增益公式抵消衰减。

### MMR 多样性重排

```yaml
retrieval:
  mmr:
    enabled: true
    lambda: 0.7  # 0.0=纯多样性, 1.0=纯相关性
```

- 需要 Qdrant（需要向量计算余弦相似度）
- 贪心选择：每次选最大化 `λ×相关性 - (1-λ)×与已选集合最大相似度` 的结果
- 避免返回语义重复的记忆

---

## 外部依赖服务

| 服务 | 功能 | 端口 | 启动命令 |
|------|------|------|---------|
| Qdrant | 向量检索 | 6333 | `docker run -p 6333:6333 qdrant/qdrant` |
| Jieba | 中文分词 | 8866 | `python tools/jieba_server.py` |
| Docling | 文档解析 | 5001 | `docker run -p 5001:5001 ds4sd/docling-serve` |
| Tika | 文档解析(回退) | 9998 | `docker run -p 9998:9998 apache/tika` |

**最小化部署（仅 SQLite）**：无需任何外部服务，仅需配置 LLM API Key。

**全量部署**：需要 Qdrant + Jieba + Docling（+ Tika 回退）+ LLM API。

---

## MCP 工具一览

| 工具名 | 功能 | 关键参数 |
|--------|------|---------|
| `iclude_retain` | 存储记忆 | content, kind, scope, context_id |
| `iclude_scan` | 轻量索引搜索 | query, scope, limit |
| `iclude_recall` | 全文内容检索 | query, scope, limit, filters |
| `iclude_fetch` | 批量 ID 获取 | ids[] |
| `iclude_reflect` | 多轮 LLM 推理 | question, scope, max_rounds |
| `iclude_timeline` | 时间线查询 | scope, limit, after |
| `iclude_ingest_conversation` | 对话批量摄入 | messages[], scope |
| `iclude_create_session` | 创建上下文会话 | session_id, project_dir |

**推荐工作流**: `scan`(浏览) → `fetch`(详情) → `retain`(存储) → `reflect`(推理)
