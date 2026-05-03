# Retrieval Quality Boost Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 FTS+图谱（90% HitRate）基础上，通过三路并行优化（向量搜索、本地 BGE 重排、时序优化）将 LongMemEval oracle 20 题 HitRate 推向 93-95%。

**Architecture:** 三项改动均为配置/部署级别，核心代码已就位。Path A 启用 Qdrant 第三检索通道；Path B 新增本地 BGE-Reranker HTTP sidecar；Path C 分析时序失败题、调优 cascade 阈值。每条路独立评测，最后合并对比。

**Tech Stack:** Docker (Qdrant v1.7.4), Python (FlagEmbedding + Flask), OpenAI text-embedding-3-small (1536-dim), BAAI/bge-reranker-v2-m3, Go test suite

---

## 文件变更地图

| 文件 | 操作 | 说明 |
|------|------|------|
| `config.yaml` | 修改 | qdrant 启用、维度修正、rerank 配置 |
| `tools/reranker_server.py` | 新建 | BGE-Reranker Flask HTTP 服务 |
| `deploy/docker-compose.yml` | 修改 | 添加 reranker 服务（可选本地运行） |

---

## Task 1: 修正 Qdrant 维度并启动容器

**Files:**
- Modify: `config.yaml:15-19`
- Modify: `deploy/docker-compose.yml`

当前 `config.yaml` 中 `dimension: 384` 与 `text-embedding-3-small`（1536 维）不匹配，必须先修正否则服务启动时维度校验会 Fatal。

- [ ] **Step 1: 修正 config.yaml 的 qdrant 段**

将 `config.yaml` 中 qdrant 段改为：

```yaml
qdrant:
  enabled: true
  url: "http://localhost:6333"
  collection: "memories"
  dimension: 1536
```

同时确认 embedding 段配置正确（已正确）：

```yaml
embedding:
  provider: "openai"
  model: "text-embedding-3-small"
```

- [ ] **Step 2: 启动 Qdrant 容器**

```bash
cd /root/LocalMem
docker compose -f deploy/docker-compose.yml up qdrant -d
```

预期输出：
```
✔ Container localmem-qdrant-1  Started
```

- [ ] **Step 3: 验证 Qdrant 健康**

```bash
curl -s http://localhost:6333/healthz
```

预期输出：`{"title":"qdrant - healthy"}`（或 HTTP 200）

- [ ] **Step 4: 验证维度配置正确（dry-run）**

```bash
OPENAI_API_KEY="<your-key>" go run ./cmd/server/ &
sleep 3
curl -s http://localhost:8080/v1/memories -H "Content-Type: application/json" \
  -d '{"content":"test memory","team_id":"t1","owner_id":"u1"}' | python3 -m json.tool
kill %1
```

预期：服务启动时日志出现 `embedder dimension verified dimension=1536`，不出现 Fatal。

- [ ] **Step 5: Commit**

```bash
git add config.yaml
git commit -m "feat(config): enable qdrant with correct 1536-dim for text-embedding-3-small"
```

---

## Task 2: 评测 Path A — 向量搜索基线

**Files:**
- Test: `testing/eval/eval_test.go`（复用 TestLongMemEvalSharedDB）

- [ ] **Step 1: 运行 20 题 eval（向量+FTS+图谱三路）**

```bash
OPENAI_API_KEY="<your-key>" EVAL_MAX_QUESTIONS=20 \
  go test ./testing/eval/ -run TestLongMemEvalSharedDB -v -timeout 600s 2>&1 | tail -30
```

- [ ] **Step 2: 记录结果**

在此记录输出中的关键指标：
```
Path A (FTS + Vector + Graph):
  HitRate:  ____%   MRR: ____  NDCG@10: ____
  vs baseline (FTS+Graph 90.0%): ΔHitRate = ____
```

- [ ] **Step 3: Commit 基线记录**

```bash
git add testing/eval/baselines/
git commit -m "eval: record path-a vector+fts+graph baseline"
```

---

## Task 3: 创建本地 BGE-Reranker 服务

**Files:**
- Create: `tools/reranker_server.py`

`rerank_remote.go` 的请求格式：`POST /rerank`，body `{"model":"...", "query":"...", "documents":[...], "top_n": N}`，response `{"results":[{"index":0,"relevance_score":0.9},...]}` 。

- [ ] **Step 1: 安装依赖**

```bash
pip install FlagEmbedding flask 2>&1 | tail -5
```

预期：无报错，`FlagEmbedding` 和 `flask` 安装成功。

- [ ] **Step 2: 创建 tools/reranker_server.py**

```python
#!/usr/bin/env python3
"""BGE-Reranker HTTP sidecar — compatible with rerank_remote.go"""
import os
from flask import Flask, request, jsonify
from FlagEmbedding import FlagReranker

MODEL_NAME = os.getenv("RERANKER_MODEL", "BAAI/bge-reranker-v2-m3")
PORT = int(os.getenv("RERANKER_PORT", "8868"))

print(f"Loading reranker model: {MODEL_NAME} ...")
reranker = FlagReranker(MODEL_NAME, use_fp16=True)
print("Reranker model loaded.")

app = Flask(__name__)

@app.route("/healthz")
def health():
    return jsonify({"status": "ok", "model": MODEL_NAME})

@app.route("/rerank", methods=["POST"])
def rerank():
    body = request.get_json(force=True)
    query = body.get("query", "")
    documents = body.get("documents", [])
    top_n = body.get("top_n", len(documents))

    if not query or not documents:
        return jsonify({"results": []})

    pairs = [[query, doc] for doc in documents]
    scores = reranker.compute_score(pairs, normalize=True)
    if isinstance(scores, float):
        scores = [scores]

    indexed = sorted(enumerate(scores), key=lambda x: x[1], reverse=True)
    results = [
        {"index": idx, "relevance_score": float(score)}
        for idx, score in indexed[:top_n]
    ]
    return jsonify({"results": results})

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=PORT)
```

- [ ] **Step 3: 验证服务可启动**

```bash
python tools/reranker_server.py &
sleep 15   # 等待模型下载+加载
curl -s http://localhost:8868/healthz
```

预期：`{"model":"BAAI/bge-reranker-v2-m3","status":"ok"}`

- [ ] **Step 4: 验证 /rerank 接口**

```bash
curl -s http://localhost:8868/rerank \
  -H "Content-Type: application/json" \
  -d '{"query":"张三的工作","documents":["张三是工程师","今天天气不错","张三负责后端开发"],"top_n":3}' \
  | python3 -m json.tool
```

预期：`index: 0`（张三是工程师）和 `index: 2`（张三负责后端开发）的 `relevance_score` 高于 `index: 1`。

- [ ] **Step 5: Commit**

```bash
kill %2 2>/dev/null  # 停止后台服务
git add tools/reranker_server.py
git commit -m "feat(tools): add BGE-Reranker HTTP sidecar (BAAI/bge-reranker-v2-m3)"
```

---

## Task 4: 配置 Re-ranker 并评测 Path B

**Files:**
- Modify: `config.yaml:76-84`

- [ ] **Step 1: 更新 config.yaml rerank 段**

```yaml
rerank:
  enabled: true
  provider: "remote"
  base_url: "http://localhost:8868"
  api_key: ""
  model: "BAAI/bge-reranker-v2-m3"
  top_k: 20
  score_weight: 0.7
  timeout: 10s
```

- [ ] **Step 2: 启动 BGE 服务 + 运行 eval**

```bash
python tools/reranker_server.py &
sleep 15

OPENAI_API_KEY="<your-key>" EVAL_MAX_QUESTIONS=20 \
  go test ./testing/eval/ -run TestLongMemEvalSharedDB -v -timeout 600s 2>&1 | tail -30
```

- [ ] **Step 3: 记录结果**

```
Path B (FTS + Vector + Graph + BGE-Rerank):
  HitRate:  ____%   MRR: ____  NDCG@10: ____
  vs Path A: ΔHitRate = ____
```

- [ ] **Step 4: Commit**

```bash
git add config.yaml
git commit -m "feat(config): enable BGE remote reranker on port 8868"
```

---

## Task 5: 时序优化 — 分析失败题并调优

**Files:**
- Modify: `config.yaml`（cascade 阈值）

- [ ] **Step 1: 运行单题 verbose 找出失败的题**

```bash
OPENAI_API_KEY="<your-key>" EVAL_QUESTION_INDEX=0 \
  go test ./testing/eval/ -run TestLongMemEvalSingleVerbose -v -timeout 120s 2>&1 | grep -E "MISS|HIT|query|temporal|score" | head -30
```

逐个运行（index 0-19）直到找到 2 道 MISS 的题，记录 question index 和 query 内容。

- [ ] **Step 2: 确认 temporal 信号是否被正确识别**

观察 verbose 输出中是否出现 `temporal: true`。若未出现，说明 IntentClassifier 未识别时序查询。

- [ ] **Step 3: 添加 cascade 配置调优时序阈值**

在 `config.yaml` 的 `retrieval` 段末尾添加：

```yaml
  cascade:
    enabled: true
    temporal_score_threshold: 0.3    # 降低触发时序通道的门槛
    graph_min_results: 3
    graph_min_score: 0.2
```

- [ ] **Step 4: 若时序信号缺失，调整 preprocess LLM timeout**

```yaml
preprocess:
  enabled: true
  use_llm: true
  llm_timeout: 8s    # 从 5s 提升到 8s，避免时序解析超时
```

- [ ] **Step 5: 重跑失败题验证修复**

```bash
OPENAI_API_KEY="<your-key>" EVAL_QUESTION_INDEX=<失败题号> \
  go test ./testing/eval/ -run TestLongMemEvalSingleVerbose -v -timeout 120s 2>&1 | tail -20
```

- [ ] **Step 6: Commit**

```bash
git add config.yaml
git commit -m "feat(config): tune cascade temporal thresholds for better temporal-reasoning recall"
```

---

## Task 6: 全路合并评测与对比

**Files:**
- Test: `testing/eval/eval_test.go`

所有三路全部开启（Qdrant + BGE-Reranker + temporal 调优），运行完整对比。

- [ ] **Step 1: 确认服务状态**

```bash
curl -s http://localhost:6333/healthz  # Qdrant
curl -s http://localhost:8868/healthz  # BGE-Reranker
```

两个都返回 200 才继续。

- [ ] **Step 2: 运行 20 题完整 eval**

```bash
OPENAI_API_KEY="<your-key>" EVAL_MAX_QUESTIONS=20 \
  go test ./testing/eval/ -run TestLongMemEvalSharedDB -v -timeout 600s 2>&1 | tail -40
```

- [ ] **Step 3: 记录三阶段对比**

```
阶段                          | HitRate | MRR   | NDCG@10
-----------------------------|---------|-------|--------
Baseline (FTS+Graph)          | 90.0%   | 0.687 | 0.726
Path A (+Vector)              | ____%   | ____  | ____
Path B (+Vector+Rerank)       | ____%   | ____  | ____
Path C (+全部+时序优化)        | ____%   | ____  | ____
```

- [ ] **Step 4: 保存新基线**

```bash
# eval 框架会自动 SaveBaseline，确认 baselines/ 目录有新文件
ls -lt testing/eval/baselines/ | head -5
```

- [ ] **Step 5: 最终 Commit**

```bash
git add testing/eval/baselines/ config.yaml
git commit -m "eval: three-path quality boost results — FTS+Vector+BGE+Temporal"
```

---

## 预期结果

| 路径 | 预期 ΔHitRate | 原理 |
|------|-------------|------|
| +Vector | +2-4% | 语义相似召回补充关键词盲区 |
| +BGE Rerank | +2-3% | 重排消除低质量高频词误命中 |
| +Temporal 调优 | +1-2% | 修复 2 道时序题漏召回 |
| **全部合并** | **+4-7%** | **目标：93-95% HitRate** |

---

## 快速参考命令

```bash
# 启动基础设施
docker compose -f deploy/docker-compose.yml up qdrant -d
python tools/reranker_server.py &

# 运行评测
OPENAI_API_KEY="sk-..." EVAL_MAX_QUESTIONS=20 \
  go test ./testing/eval/ -run TestLongMemEvalSharedDB -v -timeout 600s

# 单题调试
OPENAI_API_KEY="sk-..." EVAL_QUESTION_INDEX=5 \
  go test ./testing/eval/ -run TestLongMemEvalSingleVerbose -v -timeout 120s

# 健康检查
curl http://localhost:6333/healthz  && curl http://localhost:8868/healthz
```
