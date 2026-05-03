# 召回质量提升评测 — 本地快速上手

## 前提条件

- Docker Desktop 已安装并运行
- Go 1.25+ 已安装
- Python 3.10+（如使用本地 Python 方式启动 BGE）
- OpenAI API Key

---

## 目录结构

```
config/templates/
  eval-path-a-vector.yaml    # Path A: FTS + Vector (最小依赖)
  eval-path-b-full.yaml      # Path B: FTS + Vector + BGE Reranker + 时序优化 (全路)
tools/
  reranker_server.py         # BGE-Reranker HTTP sidecar
deploy/
  docker-compose.yml         # Qdrant + Reranker 容器定义
```

---

## Step 1: 拷贝配置文件

根据要测试的路径，将对应模版复制为 `config.yaml`：

```bash
# Path A — 只测向量搜索增益
cp config/templates/eval-path-a-vector.yaml config.yaml

# Path B — 全路合并测试（推荐最终验证）
cp config/templates/eval-path-b-full.yaml config.yaml
```

---

## Step 2: 启动 Qdrant（两个 Path 都需要）

```bash
docker compose -f deploy/docker-compose.yml up qdrant -d

# 验证健康
curl http://localhost:6333/healthz
# 预期: {"title":"qdrant - healthy"}
```

---

## Step 3: 启动 BGE-Reranker（仅 Path B 需要）

### 方式 A — Docker（推荐，自动下载模型 ~500MB）

```bash
docker compose -f deploy/docker-compose.yml --profile reranker up reranker -d

# 等待模型加载（首次约 2-5 分钟）
docker compose -f deploy/docker-compose.yml logs -f reranker

# 验证健康
curl http://localhost:8868/healthz
# 预期: {"model":"BAAI/bge-reranker-v2-m3","status":"ok"}
```

### 方式 B — 本地 Python

```bash
pip install FlagEmbedding flask
python tools/reranker_server.py &

# 等待模型加载（约 30-60 秒）
curl http://localhost:8868/healthz
```

---

## Step 4: 运行评测

```bash
export OPENAI_API_KEY=sk-...

# 20 题标准评测
EVAL_MAX_QUESTIONS=20 \
  go test ./testing/eval/ -run TestLongMemEvalSharedDB -v -timeout 600s 2>&1 | tail -40

# 单题调试（index 0-19）
EVAL_QUESTION_INDEX=5 \
  go test ./testing/eval/ -run TestLongMemEvalSingleVerbose -v -timeout 120s
```

---

## 预期结果对比

| 配置 | HitRate | MRR | NDCG@10 |
|------|---------|-----|---------|
| 基线 (FTS+Graph, 无模版) | 90.0% | 0.687 | 0.726 |
| Path A (+Vector) | ~92-94% | — | — |
| Path B (全路合并) | ~93-95% | — | — |

---

## 停止服务

```bash
# 停止所有容器
docker compose -f deploy/docker-compose.yml --profile reranker down

# 只停止 Qdrant
docker compose -f deploy/docker-compose.yml stop qdrant

# 本地 Python BGE 进程
kill $(lsof -ti:8868)
```

---

## 常见问题

**Q: Qdrant 启动后第一次写入很慢？**
A: 正常，首次写入会创建 collection 并验证维度。

**Q: BGE 模型下载失败（网络问题）？**
A: 设置 `RERANKER_MODEL=BAAI/bge-reranker-base` 使用更小的模型（~100MB）。

**Q: eval 报 "dial tcp 127.0.0.1:6333: connect refused"？**
A: Qdrant 未启动或启动失败，检查 `docker ps` 和 `docker compose logs qdrant`。
