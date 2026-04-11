# LocalMem 100 题评测指南

## 一、环境准备

### 1. 克隆项目

```bash
git clone https://github.com/MemoryGet/LocalMem.git
cd LocalMem
go mod download
go build ./...
```

### 2. 依赖服务

根据你要跑的评测模式，需要不同的依赖：

| 模式 | Qdrant | Embedding API | LLM API | 说明 |
|------|--------|--------------|---------|------|
| **Layer 1 Only** | ❌ | ❌ | ❌ | 仅分词匹配，零外部依赖 |
| **完整三层** | ✅ | ✅ | ❌ | 分词 + 质心 + 近邻，需要向量化 |
| FTS Only | ❌ | ❌ | ❌ | 纯关键词，无图谱 |
| SharedDB (LLM) | ❌ | ❌ | ✅ | LLM 实体抽取（旧方案对比用） |

### 3. 启动 Qdrant（完整三层模式需要）

```bash
# Docker 方式
docker run -d --name qdrant -p 6333:6333 -p 6334:6334 qdrant/qdrant:latest

# 验证
curl http://localhost:6333/collections
```

### 4. 配置 Embedding API（完整三层模式需要）

```bash
# OpenAI Embedding
export EMBEDDING_API_KEY="sk-your-key"
export EMBEDDING_MODEL="text-embedding-3-small"    # 默认值，可不设

# 或者用兼容 OpenAI 格式的其他服务（如 DeepSeek/Ollama）
export EMBEDDING_API_KEY="your-key"
export OPENAI_BASE_URL="http://localhost:11434/v1"  # Ollama 地址
export EMBEDDING_MODEL="nomic-embed-text"
export EMBEDDING_DIMENSION="768"                    # 非 1536 时必须指定
```

---

## 二、评测模式说明

### 模式 A：Layer 1 Only（推荐首次测试）

**特点**: 零外部依赖，仅测试分词精确匹配 + 图谱增强检索

**流程**:
1. 创建 SQLite 数据库
2. Seed 记忆到数据库
3. EntityResolver Layer 1：分词 → 匹配实体 → 候选积累 → 晋升 → 二次匹配
4. FTS + 图谱检索 100 题

```bash
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverDB -timeout 30m
```

预计耗时：2-5 分钟

### 模式 B：完整三层（推荐正式评测）

**特点**: Layer 1 分词 + Layer 2 质心匹配 + Layer 3 近邻传播，最接近生产环境

**前置条件**: Qdrant 运行中 + Embedding API 可用

**流程**:
1. 创建 SQLite + Qdrant collection
2. Seed 记忆（同时写入 SQLite 和 Qdrant）
3. 批量 Embedding（100 条/批）
4. EntityResolver 三层解析（两轮：候选积累 → 晋升 → 二次解析）
5. 晋升实体计算质心向量写入 Qdrant
6. FTS + 向量 + 图谱混合检索 100 题

```bash
# 必须设置
export EMBEDDING_API_KEY="sk-your-key"

# 可选（有默认值）
export QDRANT_URL="http://localhost:6333"          # 默认 localhost:6333
export EMBEDDING_MODEL="text-embedding-3-small"     # 默认 text-embedding-3-small
export EMBEDDING_DIMENSION="1536"                   # 默认 1536

# 运行
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverFull -timeout 60m
```

预计耗时：5-15 分钟（取决于 Embedding API 速度）

### 模式 C：FTS Only（基线对比）

**特点**: 纯关键词检索，无图谱，作为最低基线

```bash
go test -v ./testing/eval/ -run TestLongMemEvalOracle -timeout 30m
```

### 模式 D：SharedDB + LLM（旧方案对比）

**特点**: LLM 实体抽取，用于和新方案对比效果

```bash
export OPENAI_API_KEY="sk-your-key"
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalSharedDB -timeout 60m
```

---

## 三、环境变量汇总

| 变量 | 必须 | 默认值 | 说明 |
|------|------|--------|------|
| `EVAL_MAX_QUESTIONS` | 否 | 100 | 评测题数 |
| `EMBEDDING_API_KEY` | 模式 B | — | Embedding API Key（也可用 OPENAI_API_KEY） |
| `EMBEDDING_MODEL` | 否 | text-embedding-3-small | Embedding 模型名 |
| `EMBEDDING_DIMENSION` | 否 | 1536 | 向量维度（非 OpenAI 模型时需设置） |
| `QDRANT_URL` | 否 | http://localhost:6333 | Qdrant 地址 |
| `OPENAI_API_KEY` | 模式 D | — | LLM API Key（仅 SharedDB 模式） |
| `OPENAI_BASE_URL` | 否 | https://api.openai.com/v1 | LLM/Embedding API 基址 |

---

## 四、输出解读

### 指标说明

```
=== Eval: full-resolver (L1+L2+L3, no LLM) — 1200 memories, 350 entities, ~800 relations ===
  HitRate: 72.0%    ← 100 题中命中了 72 题
  MRR: 0.583        ← 平均倒数排名（越高越好，满分 1.0）
  NDCG@10: 0.512    ← Top-10 归一化折损累积增益
  Recall@1: 48.0%   ← Top-1 召回率
  Recall@3: 62.0%   ← Top-3 召回率
  Recall@5: 68.0%   ← Top-5 召回率
  Recall@10: 72.0%  ← Top-10 召回率
  Duration: 8m 30s
```

### 预期效果对比

| 模式 | HitRate | MRR | 说明 |
|------|---------|-----|------|
| FTS Only | ~55-65% | ~0.40-0.50 | 纯关键词基线 |
| **Layer 1 Only** | ~60-70% | ~0.45-0.55 | 分词 + 图谱增强 |
| **完整三层** | ~65-75% | ~0.50-0.60 | 分词 + 质心 + 近邻 + 向量检索 |
| SharedDB (LLM) | ~70-80% | ~0.55-0.65 | LLM 抽取（旧方案上限） |

> 注：具体数值取决于数据集和分词器质量。首次运行后会保存基线，后续可检测回归。

### 按类别/难度细分

输出会包含按 Category 和 Difficulty 的分组统计，帮助分析弱点。

---

## 五、基线管理

### 保存基线

评测完成后会自动保存到 `testing/eval/baselines/`：
- `longmemeval-oracle-resolver-v1.json` — Layer 1 Only 基线
- `longmemeval-oracle-resolver-full-v1.json` — 完整三层基线

### 对比基线

后续运行会自动对比已有基线，检测回归：
```
=== Regression Check ===
  HitRate: +2.0% (72.0% vs 70.0%)  ✅ IMPROVED
  MRR: -0.01 (0.58 vs 0.59)        ⚠️ SLIGHT REGRESSION
```

### 手动对比

```bash
go test -v ./testing/eval/ -run TestRegressionCheck -timeout 5m
```

---

## 六、快速开始（一键命令）

### 最快速度（零依赖）

```bash
git clone https://github.com/MemoryGet/LocalMem.git
cd LocalMem
go mod download
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverDB -timeout 30m
```

### 完整三层（推荐）

```bash
git clone https://github.com/MemoryGet/LocalMem.git
cd LocalMem
go mod download

# 启动 Qdrant
docker run -d --name qdrant -p 6333:6333 qdrant/qdrant:latest

# 设置 Embedding API
export EMBEDDING_API_KEY="sk-your-key"

# 运行 100 题
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverFull -timeout 60m
```

### 全模式对比

```bash
export OPENAI_API_KEY="sk-your-key"

# 1. FTS 基线
go test -v ./testing/eval/ -run TestLongMemEvalOracle -timeout 30m

# 2. Layer 1 Only
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverDB -timeout 30m

# 3. 完整三层
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalResolverFull -timeout 60m

# 4. LLM 抽取（对比）
EVAL_MAX_QUESTIONS=100 go test -v ./testing/eval/ -run TestLongMemEvalSharedDB -timeout 60m

# 5. 查看对比
go test -v ./testing/eval/ -run TestRegressionCheck -timeout 5m
```

---

## 七、故障排查

| 问题 | 原因 | 解决 |
|------|------|------|
| `skip: testdata/longmemeval-oracle.json not found` | 数据集文件缺失 | 确认 `testing/eval/testdata/` 目录存在该文件 |
| `init qdrant: ... connection refused` | Qdrant 未启动 | `docker run -d --name qdrant -p 6333:6333 qdrant/qdrant:latest` |
| `EMBEDDING_API_KEY required` | 未设置 API Key | `export EMBEDDING_API_KEY="sk-xxx"` |
| `embed batch: 401` | API Key 无效 | 检查 Key 是否正确 |
| `embed batch: 429` | API 限流 | 等待后重试，或换更高配额的 Key |
| 评测很慢 | Embedding API 慢 | 可用本地 Ollama 替代：`OPENAI_BASE_URL=http://localhost:11434/v1` |
| HitRate 很低 | 分词质量差 | 尝试 Gse/Jieba 分词器（需改代码中 tokenizer 初始化） |
