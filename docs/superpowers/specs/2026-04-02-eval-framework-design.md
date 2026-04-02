# B1 评测闭环设计文档

**日期**: 2026-04-02
**目标**: 建立可复现的检索质量评测框架，固化基线，支持回归检测

---

## 1. 问题与动机

当前有两个测试工具（`tools/retrieval_test_500.py` + `cmd/retrieval-benchmark/main.go`），但缺少：
- 标准 IR 指标（MRR、NDCG、Recall@k）
- 基线快照与回归检测
- 多模式对比（FTS-only / hybrid / hybrid+rerank / reflect）
- LongMemEval 官方数据集对接

没有可量化的评测闭环，后续所有优化都只能靠"感觉"判断好坏。

## 2. 设计方案

### 2.1 目录结构

```
testing/eval/
  metrics.go          — MRR, NDCG, Recall@k, HitRate 计算（纯函数，零依赖）
  metrics_test.go     — 指标计算单测（表驱动）
  dataset.go          — 数据集加载（JSON 格式，支持内置 500 组 + 外部 LongMemEval）
  runner.go           — 评测运行器：seed → query → score → aggregate
  runner_test.go      — 集成测试（小数据集快速验证）
  baseline.go         — 基线快照管理：保存/加载/对比/回归检测
  baselines/          — 基线 JSON 快照文件
    hybrid-v1.json
tools/
  longmemeval_adapter.py  — LongMemEval → LocalMem JSON 格式转换（后续）
```

### 2.2 核心数据结构

```go
// EvalCase 单个评测用例
type EvalCase struct {
    Query      string   `json:"query"`
    Expected   []string `json:"expected"`   // 期望命中的关键词列表（任一命中即算 hit）
    Category   string   `json:"category"`
    Difficulty string   `json:"difficulty"`
}

// EvalDataset 评测数据集
type EvalDataset struct {
    Name         string        `json:"name"`
    Description  string        `json:"description"`
    SeedMemories []SeedMemory  `json:"seed_memories"`
    Cases        []EvalCase    `json:"cases"`
}

// SeedMemory 种子数据
type SeedMemory struct {
    Content string `json:"content"`
    Kind    string `json:"kind"`
    SubKind string `json:"sub_kind"`
}

// CaseResult 单用例评测结果
type CaseResult struct {
    Case       EvalCase
    Hit        bool
    Rank       int     // 命中排名，-1 表示未命中
    Score      float64 // 命中项的检索分数
    TopScores  []float64 // Top-K 分数列表（用于 NDCG 计算）
    ResultCount int
    Latency    time.Duration
}

// EvalReport 评测报告
type EvalReport struct {
    Mode       string             `json:"mode"`        // fts / hybrid / hybrid+rerank / reflect
    Dataset    string             `json:"dataset"`
    Timestamp  time.Time          `json:"timestamp"`
    Metrics    AggregateMetrics   `json:"metrics"`
    ByCategory map[string]AggregateMetrics `json:"by_category"`
    ByDifficulty map[string]AggregateMetrics `json:"by_difficulty"`
    Cases      []CaseResult       `json:"cases"`
    Duration   time.Duration      `json:"duration"`
}

// AggregateMetrics 聚合指标
type AggregateMetrics struct {
    Total    int     `json:"total"`
    HitRate  float64 `json:"hit_rate"`   // 命中率 %
    MRR      float64 `json:"mrr"`        // Mean Reciprocal Rank
    NDCG5    float64 `json:"ndcg@5"`     // NDCG@5
    NDCG10   float64 `json:"ndcg@10"`    // NDCG@10
    RecallAt1  float64 `json:"recall@1"`
    RecallAt3  float64 `json:"recall@3"`
    RecallAt5  float64 `json:"recall@5"`
    RecallAt10 float64 `json:"recall@10"`
    AvgLatency time.Duration `json:"avg_latency"`
}
```

### 2.3 指标定义

| 指标 | 定义 | 用途 |
|------|------|------|
| **HitRate** | 命中用例数 / 总用例数 | 最基础的覆盖率 |
| **MRR** | Σ(1/rank_i) / N，未命中 rank=0 | 衡量命中位置，越靠前越好 |
| **Recall@k** | 在 top-k 内命中的比例 | k=1,3,5,10 |
| **NDCG@k** | 归一化折损累积增益 | 考虑排序质量的标准 IR 指标 |

对于 LocalMem 的场景：每个 query 只有一个正确答案（expected keyword match），因此 NDCG 简化为二元相关性（命中=1，未命中=0）。

### 2.4 评测模式

运行器通过直接调用 Go 代码（in-process），不走 HTTP：

| 模式 | 配置 | 说明 |
|------|------|------|
| `fts` | SQLite only, preprocess=false, rerank=off | 纯 FTS5 BM25 基线 |
| `hybrid` | SQLite + preprocess=true, rerank=off | 当前默认配置 |
| `hybrid+rerank` | hybrid + rerank=overlap | overlap 精排效果 |
| `reflect` | 通过 ReflectEngine.Reflect() | 多轮推理（需要 LLM，可选） |

`reflect` 模式因依赖 LLM 调用，在 CI 中默认跳过（`-tags=eval_reflect` 控制）。

### 2.5 运行器设计

```go
// Runner 评测运行器
type Runner struct {
    memStore  store.MemoryStore
    retriever *search.Retriever
    reflect   *reflect.ReflectEngine // 可为 nil
    dbPath    string                 // 临时 SQLite 路径
}

// Run 执行评测
func (r *Runner) Run(ctx context.Context, ds *EvalDataset, mode string) (*EvalReport, error)

// RunAll 执行所有模式并返回对比结果
func (r *Runner) RunAll(ctx context.Context, ds *EvalDataset) (map[string]*EvalReport, error)
```

运行流程：
1. 创建临时 SQLite 数据库
2. 写入 seed memories（通过 Manager.Create，确保 FTS 索引 + 摘要生成）
3. 逐个执行 query，收集 CaseResult
4. 聚合计算指标
5. 输出 EvalReport

### 2.6 基线管理

```go
// SaveBaseline 保存当前评测结果为基线
func SaveBaseline(report *EvalReport, name string) error

// LoadBaseline 加载基线
func LoadBaseline(name string) (*EvalReport, error)

// CompareBaseline 与基线对比，返回回归项
func CompareBaseline(current, baseline *EvalReport, thresholds RegressionThresholds) []Regression

type RegressionThresholds struct {
    HitRateDrop  float64 // 命中率下降超过此值报警，默认 2.0（百分点）
    MRRDrop      float64 // MRR 下降超过此值报警，默认 0.02
    NDCGDrop     float64 // NDCG@10 下降超过此值报警，默认 0.02
}

type Regression struct {
    Metric   string
    Baseline float64
    Current  float64
    Delta    float64
}
```

基线文件格式（`testing/eval/baselines/hybrid-v1.json`）：
```json
{
  "mode": "hybrid",
  "dataset": "retrieval-500",
  "timestamp": "2026-04-02T...",
  "metrics": { "hit_rate": 72.4, "mrr": 0.584, ... },
  "by_category": { "exact": { ... }, "synonym": { ... } },
  "git_commit": "76e050d"
}
```

### 2.7 数据集加载

内置 500 组数据集通过 `cmd/retrieval-benchmark/` 已有的 `--dump-dataset` 从 Python 脚本导出 JSON，Go 侧直接加载：

```go
// LoadDatasetFromJSON 从 JSON 文件加载数据集
func LoadDatasetFromJSON(path string) (*EvalDataset, error)

// LoadBuiltinDataset 加载内置 500 组数据集（从 embedded JSON 或调用 Python 导出）
func LoadBuiltinDataset() (*EvalDataset, error)
```

后续 LongMemEval 适配：`tools/longmemeval_adapter.py` 将官方数据集转为同一 JSON schema，Go 侧无需改动。

### 2.8 CLI 与 CI 集成

```bash
# 跑评测（所有模式）
go test ./testing/eval/ -v -count=1 -run TestEvalAll

# 只跑 hybrid 模式
go test ./testing/eval/ -v -count=1 -run TestEvalHybrid

# 保存基线
go test ./testing/eval/ -v -count=1 -run TestSaveBaseline

# 回归检测（CI 用）
go test ./testing/eval/ -v -count=1 -run TestRegressionCheck

# 用 benchmark 工具（更详细输出）
go run ./cmd/retrieval-benchmark/ --rerank=off
go run ./cmd/retrieval-benchmark/ --rerank=overlap
```

CI 中 `TestRegressionCheck` 对比当前结果与 `baselines/` 中最新快照，任何指标下降超过阈值即 FAIL。

### 2.9 输出格式

终端输出（简洁）：
```
=== Eval: hybrid | retrieval-500 ===
  HitRate:   72.4%  MRR: 0.584  NDCG@10: 0.612
  Recall@1:  48.2%  @3: 65.0%  @5: 70.8%  @10: 72.4%
  
  By Category:
    exact:      95.0%  synonym:   72.5%  fuzzy:     68.8%
    cross_lang: 55.0%  temporal:  60.0%  ...
  
  vs baseline hybrid-v1:
    HitRate:  72.4% → 72.4% (±0.0)  ✓
    MRR:      0.584 → 0.584 (±0.000) ✓
```

JSON 报告同时写入 `testing/eval/reports/` 供后续分析。

## 3. 不做的事

- **不实现 HTML 报告生成** — 已有 Python 版，不重复
- **不实现 Qdrant 模式评测** — 当前主力是 SQLite，Qdrant 评测后续按需加
- **不在此阶段对接 LongMemEval** — 先跑通 500 组，LongMemEval 适配作为独立 PR
- **不实现分布式评测** — 单机 in-process 足够

## 4. 风险与依赖

| 风险 | 缓解 |
|------|------|
| Seed 数据写入慢（LLM 摘要生成） | 评测模式下可关闭 LLM 摘要，或用固定摘要 |
| reflect 模式依赖 LLM API | 默认跳过，`-tags=eval_reflect` 显式启用 |
| 500 组测试跑完需要几分钟 | 提供 `--quick` 模式只跑 50 组快速验证 |

## 5. 成功标准

- [ ] `go test ./testing/eval/ -run TestEvalHybrid` 可在无外部依赖下跑通
- [ ] 输出 HitRate / MRR / NDCG@10 / Recall@1,3,5,10 六项指标
- [ ] 按 category 和 difficulty 分组统计
- [ ] 基线保存/加载/对比功能可用
- [ ] 回归检测在指标下降时 FAIL
- [ ] 首版 hybrid 基线数据产出并提交
