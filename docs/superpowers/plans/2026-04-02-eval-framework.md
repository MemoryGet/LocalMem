# B1 评测闭环实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 Go in-process 检索评测框架，输出标准 IR 指标，固化基线，支持回归检测。

**Architecture:** `testing/eval/` 包含纯函数指标计算、数据集加载、评测运行器和基线管理。运行器直接实例化 SQLite + Manager + Retriever（无 HTTP），逐 query 执行并聚合指标。基线以 JSON 快照保存，回归检测集成到 `go test`。

**Tech Stack:** Go 1.25+, SQLite (modernc.org/sqlite), testify, 现有 `tools/retrieval_test_500.py --dump-dataset` 导出 JSON

**Spec:** `docs/superpowers/specs/2026-04-02-eval-framework-design.md`

---

## 文件结构

| 文件 | 职责 |
|------|------|
| `testing/eval/metrics.go` | MRR, NDCG@k, Recall@k, HitRate 纯函数计算 |
| `testing/eval/metrics_test.go` | 指标计算表驱动单测 |
| `testing/eval/dataset.go` | 数据集 JSON 加载 + 内置 500 组数据集导出 |
| `testing/eval/dataset_test.go` | 数据集加载测试 |
| `testing/eval/runner.go` | 评测运行器：创建临时 DB → seed → query → score → aggregate |
| `testing/eval/runner_test.go` | 集成测试（10 条 seed + 5 个 query 快速验证） |
| `testing/eval/baseline.go` | 基线快照保存/加载/对比/回归检测 |
| `testing/eval/baseline_test.go` | 基线管理测试 |
| `testing/eval/baselines/.gitkeep` | 基线快照目录占位 |

---

### Task 1: 指标计算 — metrics.go + metrics_test.go

**Files:**
- Create: `testing/eval/metrics.go`
- Create: `testing/eval/metrics_test.go`

- [ ] **Step 1: 创建 metrics_test.go — 写失败测试**

```go
package eval_test

import (
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
)

func TestMRR(t *testing.T) {
	tests := []struct {
		name   string
		ranks  []int // -1 = miss
		expect float64
	}{
		{"all hit rank 1", []int{1, 1, 1}, 1.0},
		{"mixed ranks", []int{1, 2, 3}, (1.0 + 0.5 + 1.0/3) / 3},
		{"some miss", []int{1, -1, 3}, (1.0 + 0 + 1.0/3) / 3},
		{"all miss", []int{-1, -1}, 0.0},
		{"empty", []int{}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eval.MRR(tt.ranks)
			assert.InDelta(t, tt.expect, got, 0.001)
		})
	}
}

func TestRecallAtK(t *testing.T) {
	tests := []struct {
		name   string
		ranks  []int
		k      int
		expect float64
	}{
		{"all in top 3", []int{1, 2, 3}, 3, 1.0},
		{"some in top 3", []int{1, 5, -1}, 3, 1.0 / 3},
		{"none in top 1", []int{2, 3, -1}, 1, 0.0},
		{"empty", []int{}, 5, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eval.RecallAtK(tt.ranks, tt.k)
			assert.InDelta(t, tt.expect, got, 0.001)
		})
	}
}

func TestNDCGAtK(t *testing.T) {
	tests := []struct {
		name   string
		ranks  []int
		k      int
		expect float64
	}{
		{"hit at rank 1", []int{1}, 10, 1.0},
		{"hit at rank 2", []int{2}, 10, 1.0 / math.Log2(3)},
		{"miss", []int{-1}, 10, 0.0},
		{"empty", []int{}, 10, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eval.NDCGAtK(tt.ranks, tt.k)
			assert.InDelta(t, tt.expect, got, 0.001)
		})
	}
}

func TestHitRate(t *testing.T) {
	tests := []struct {
		name   string
		ranks  []int
		expect float64
	}{
		{"all hit", []int{1, 3, 5}, 100.0},
		{"half hit", []int{1, -1, 3, -1}, 50.0},
		{"none", []int{-1, -1}, 0.0},
		{"empty", []int{}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eval.HitRate(tt.ranks)
			assert.InDelta(t, tt.expect, got, 0.001)
		})
	}
}

func TestAggregate(t *testing.T) {
	results := []eval.CaseResult{
		{Rank: 1, Hit: true},
		{Rank: 3, Hit: true},
		{Rank: -1, Hit: false},
	}
	m := eval.Aggregate(results)
	assert.Equal(t, 3, m.Total)
	assert.InDelta(t, 66.667, m.HitRate, 0.01)
	assert.True(t, m.MRR > 0)
	assert.True(t, m.RecallAt1 > 0)
}
```

注意：测试文件需要 `import "math"` 给 NDCG 测试用。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/eval/ -v -count=1 2>&1 | head -5`
Expected: 编译失败 — `eval` 包不存在

- [ ] **Step 3: 实现 metrics.go**

```go
package eval

import "math"

// CaseResult 单用例评测结果 / Single evaluation case result
type CaseResult struct {
	Query       string  `json:"query"`
	Expected    string  `json:"expected"`
	Category    string  `json:"category"`
	Difficulty  string  `json:"difficulty"`
	Hit         bool    `json:"hit"`
	Rank        int     `json:"rank"`  // 1-based, -1 = miss
	Score       float64 `json:"score"` // retrieval score of hit
	ResultCount int     `json:"result_count"`
}

// AggregateMetrics 聚合指标 / Aggregated evaluation metrics
type AggregateMetrics struct {
	Total    int     `json:"total"`
	HitRate  float64 `json:"hit_rate"`
	MRR      float64 `json:"mrr"`
	NDCG5    float64 `json:"ndcg@5"`
	NDCG10   float64 `json:"ndcg@10"`
	RecallAt1  float64 `json:"recall@1"`
	RecallAt3  float64 `json:"recall@3"`
	RecallAt5  float64 `json:"recall@5"`
	RecallAt10 float64 `json:"recall@10"`
}

// MRR 计算 Mean Reciprocal Rank / Calculate Mean Reciprocal Rank
// ranks: 1-based hit rank, -1 = miss
func MRR(ranks []int) float64 {
	if len(ranks) == 0 {
		return 0
	}
	sum := 0.0
	for _, r := range ranks {
		if r > 0 {
			sum += 1.0 / float64(r)
		}
	}
	return sum / float64(len(ranks))
}

// RecallAtK 计算 Recall@K / Calculate Recall at K
func RecallAtK(ranks []int, k int) float64 {
	if len(ranks) == 0 {
		return 0
	}
	hits := 0
	for _, r := range ranks {
		if r > 0 && r <= k {
			hits++
		}
	}
	return float64(hits) / float64(len(ranks))
}

// NDCGAtK 计算 NDCG@K（二元相关性）/ Calculate NDCG at K with binary relevance
// 每个 query 最多一个正确答案，命中=1，未命中=0
func NDCGAtK(ranks []int, k int) float64 {
	if len(ranks) == 0 {
		return 0
	}
	// 理想 DCG = 1/log2(2) = 1.0（单个相关文档排在第 1 位）
	idealDCG := 1.0
	sum := 0.0
	for _, r := range ranks {
		if r > 0 && r <= k {
			sum += 1.0 / math.Log2(float64(r+1))
		}
	}
	return sum / (float64(len(ranks)) * idealDCG)
}

// HitRate 计算命中率（百分比）/ Calculate hit rate as percentage
func HitRate(ranks []int) float64 {
	if len(ranks) == 0 {
		return 0
	}
	hits := 0
	for _, r := range ranks {
		if r > 0 {
			hits++
		}
	}
	return float64(hits) / float64(len(ranks)) * 100
}

// Aggregate 从 CaseResult 列表聚合指标 / Aggregate metrics from case results
func Aggregate(results []CaseResult) AggregateMetrics {
	if len(results) == 0 {
		return AggregateMetrics{}
	}
	ranks := make([]int, len(results))
	for i, r := range results {
		if r.Hit {
			ranks[i] = r.Rank
		} else {
			ranks[i] = -1
		}
	}
	return AggregateMetrics{
		Total:      len(results),
		HitRate:    HitRate(ranks),
		MRR:        MRR(ranks),
		NDCG5:      NDCGAtK(ranks, 5),
		NDCG10:     NDCGAtK(ranks, 10),
		RecallAt1:  RecallAtK(ranks, 1),
		RecallAt3:  RecallAtK(ranks, 3),
		RecallAt5:  RecallAtK(ranks, 5),
		RecallAt10: RecallAtK(ranks, 10),
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./testing/eval/ -v -count=1`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add testing/eval/metrics.go testing/eval/metrics_test.go
git commit -m "feat(eval): add IR metrics — MRR, NDCG@k, Recall@k, HitRate"
```

---

### Task 2: 数据集加载 — dataset.go + dataset_test.go

**Files:**
- Create: `testing/eval/dataset.go`
- Create: `testing/eval/dataset_test.go`

- [ ] **Step 1: 创建 dataset_test.go — 写失败测试**

```go
package eval_test

import (
	"os"
	"path/filepath"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDatasetFromJSON(t *testing.T) {
	// 构造临时 JSON
	content := `{
		"name": "test-dataset",
		"description": "unit test",
		"seed_memories": [
			{"content": "Go 是一门编程语言", "kind": "fact", "sub_kind": ""}
		],
		"cases": [
			{"query": "Go 语言", "expected": ["编程语言"], "category": "exact", "difficulty": "easy"}
		]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "dataset.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	ds, err := eval.LoadDatasetFromJSON(path)
	require.NoError(t, err)
	assert.Equal(t, "test-dataset", ds.Name)
	assert.Len(t, ds.SeedMemories, 1)
	assert.Len(t, ds.Cases, 1)
	assert.Equal(t, "Go 语言", ds.Cases[0].Query)
	assert.Contains(t, ds.Cases[0].Expected, "编程语言")
}

func TestLoadDatasetFromJSON_FileNotFound(t *testing.T) {
	_, err := eval.LoadDatasetFromJSON("/nonexistent/path.json")
	assert.Error(t, err)
}

func TestExportBuiltinDataset(t *testing.T) {
	// 仅在 Python 可用时运行
	dir := t.TempDir()
	outPath := filepath.Join(dir, "builtin.json")
	err := eval.ExportBuiltinDataset("../../tools/retrieval_test_500.py", outPath)
	if err != nil {
		t.Skipf("skip builtin export (python unavailable): %v", err)
	}
	ds, err := eval.LoadDatasetFromJSON(outPath)
	require.NoError(t, err)
	assert.True(t, len(ds.SeedMemories) >= 50, "expected 50+ seed memories, got %d", len(ds.SeedMemories))
	assert.True(t, len(ds.Cases) >= 100, "expected 100+ cases, got %d", len(ds.Cases))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/eval/ -run TestLoadDataset -v -count=1 2>&1 | head -5`
Expected: 编译失败

- [ ] **Step 3: 实现 dataset.go**

```go
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// SeedMemory 种子记忆 / Seed memory for evaluation
type SeedMemory struct {
	Content string `json:"content"`
	Kind    string `json:"kind"`
	SubKind string `json:"sub_kind"`
}

// EvalCase 评测用例 / Single evaluation case
type EvalCase struct {
	Query      string   `json:"query"`
	Expected   []string `json:"expected"`
	Category   string   `json:"category"`
	Difficulty string   `json:"difficulty"`
}

// EvalDataset 评测数据集 / Evaluation dataset
type EvalDataset struct {
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	SeedMemories []SeedMemory `json:"seed_memories"`
	Cases        []EvalCase   `json:"cases"`
}

// LoadDatasetFromJSON 从 JSON 文件加载数据集 / Load dataset from JSON file
func LoadDatasetFromJSON(path string) (*EvalDataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset file: %w", err)
	}
	var ds EvalDataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("parse dataset JSON: %w", err)
	}
	return &ds, nil
}

// ExportBuiltinDataset 调用 Python 脚本导出内置 500 组数据集 / Export builtin 500-query dataset via Python script
func ExportBuiltinDataset(scriptPath string, outputPath string) error {
	cmd := exec.Command("python3", scriptPath, "--dump-dataset")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("run python dataset export: %w", err)
	}

	// 解析 Python 输出的格式（seed_memories + test_queries）并转换为 EvalDataset
	var raw struct {
		SeedMemories []SeedMemory `json:"seed_memories"`
		TestQueries  [][]any      `json:"test_queries"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return fmt.Errorf("parse python output: %w", err)
	}

	ds := EvalDataset{
		Name:         "retrieval-500",
		Description:  "Built-in 500-query retrieval benchmark",
		SeedMemories: raw.SeedMemories,
		Cases:        make([]EvalCase, 0, len(raw.TestQueries)),
	}
	for _, tq := range raw.TestQueries {
		if len(tq) != 4 {
			continue
		}
		query, _ := tq[0].(string)
		expected, _ := tq[1].(string)
		category, _ := tq[2].(string)
		difficulty, _ := tq[3].(string)
		if query == "" {
			continue
		}
		ds.Cases = append(ds.Cases, EvalCase{
			Query:      query,
			Expected:   []string{expected},
			Category:   category,
			Difficulty: difficulty,
		})
	}

	outData, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	return os.WriteFile(outputPath, outData, 0644)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./testing/eval/ -run TestLoadDataset -v -count=1`
Expected: PASS（`TestExportBuiltinDataset` 可能 skip 如果无 Python）

- [ ] **Step 5: 导出内置数据集为 JSON 文件提交到仓库**

```bash
# 导出一份固定的 JSON 数据集，避免运行时依赖 Python
cd /root/LocalMem
python3 tools/retrieval_test_500.py --dump-dataset > testing/eval/testdata/retrieval-500.json
```

验证导出正确后：
```bash
mkdir -p testing/eval/testdata
git add testing/eval/dataset.go testing/eval/dataset_test.go testing/eval/testdata/retrieval-500.json
git commit -m "feat(eval): add dataset loader + export builtin 500-query dataset"
```

---

### Task 3: 评测运行器 — runner.go + runner_test.go

**Files:**
- Create: `testing/eval/runner.go`
- Create: `testing/eval/runner_test.go`

- [ ] **Step 1: 创建 runner_test.go — 写失败测试**

```go
package eval_test

import (
	"context"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 小数据集用于快速集成测试
func miniDataset() *eval.EvalDataset {
	return &eval.EvalDataset{
		Name:        "mini",
		Description: "minimal integration test",
		SeedMemories: []eval.SeedMemory{
			{Content: "Go 语言适合编写高并发后端服务", Kind: "fact"},
			{Content: "Python 是数据科学领域的首选语言", Kind: "fact"},
			{Content: "用户偏好使用 Vim 编辑器", Kind: "profile", SubKind: "preference"},
			{Content: "项目部署在阿里云 ECS 上海区域", Kind: "fact", SubKind: "entity"},
			{Content: "数据库从 PostgreSQL 迁移到了 SQLite", Kind: "fact", SubKind: "event"},
		},
		Cases: []eval.EvalCase{
			{Query: "Go 语言的优势", Expected: []string{"高并发"}, Category: "exact", Difficulty: "easy"},
			{Query: "数据科学用什么语言", Expected: []string{"Python"}, Category: "synonym", Difficulty: "medium"},
			{Query: "数据库迁移", Expected: []string{"PostgreSQL", "SQLite"}, Category: "exact", Difficulty: "easy"},
		},
	}
}

func TestRunner_FTSMode(t *testing.T) {
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), miniDataset(), "fts")
	require.NoError(t, err)
	assert.Equal(t, "fts", report.Mode)
	assert.Equal(t, 3, report.Metrics.Total)
	assert.True(t, report.Metrics.HitRate > 0, "expected at least some hits")
	assert.Len(t, report.Cases, 3)

	// 分组统计应包含 exact 和 synonym
	_, hasExact := report.ByCategory["exact"]
	assert.True(t, hasExact)
}

func TestRunner_HybridMode(t *testing.T) {
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), miniDataset(), "hybrid")
	require.NoError(t, err)
	assert.Equal(t, "hybrid", report.Mode)
	assert.Equal(t, 3, report.Metrics.Total)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/eval/ -run TestRunner -v -count=1 2>&1 | head -5`
Expected: 编译失败

- [ ] **Step 3: 实现 runner.go**

```go
package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/store"
)

// EvalReport 评测报告 / Evaluation report
type EvalReport struct {
	Mode         string                        `json:"mode"`
	Dataset      string                        `json:"dataset"`
	Timestamp    time.Time                     `json:"timestamp"`
	Metrics      AggregateMetrics              `json:"metrics"`
	ByCategory   map[string]AggregateMetrics   `json:"by_category"`
	ByDifficulty map[string]AggregateMetrics   `json:"by_difficulty"`
	Cases        []CaseResult                  `json:"cases"`
	Duration     time.Duration                 `json:"duration"`
	GitCommit    string                        `json:"git_commit,omitempty"`
}

// Runner 评测运行器 / Evaluation runner
type Runner struct {
	memStore  store.MemoryStore
	manager   *memory.Manager
	retriever *search.Retriever
	dbPath    string
}

// NewTestRunner 创建测试用运行器（自动清理）/ Create test runner with auto-cleanup
func NewTestRunner(t *testing.T) (*Runner, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "eval.db")

	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	if err != nil {
		t.Fatalf("create eval store: %v", err)
	}
	if err := memStore.Init(context.Background()); err != nil {
		t.Fatalf("init eval store: %v", err)
	}

	mgr := memory.NewManager(memStore, nil, nil, nil, nil, nil, nil, memory.ManagerConfig{})

	cfg := config.RetrievalConfig{
		FTSWeight:   1.0,
		GraphWeight: 0.8,
		AccessAlpha: 0.15,
		Preprocess: config.PreprocessConfig{
			Enabled: false,
		},
	}

	ret := search.NewRetriever(memStore, nil, nil, nil, nil, cfg, nil, nil)

	runner := &Runner{
		memStore:  memStore,
		manager:   mgr,
		retriever: ret,
		dbPath:    dbPath,
	}

	cleanup := func() {
		_ = memStore.Close()
	}
	return runner, cleanup
}

// NewRunner 创建指定模式的运行器 / Create runner with specified mode config
func NewRunner(dbPath string, mode string) (*Runner, func(), error) {
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create store: %w", err)
	}
	if err := memStore.Init(context.Background()); err != nil {
		_ = memStore.Close()
		return nil, nil, fmt.Errorf("init store: %w", err)
	}

	mgr := memory.NewManager(memStore, nil, nil, nil, nil, nil, nil, memory.ManagerConfig{})

	cfg := config.RetrievalConfig{
		FTSWeight:   1.0,
		GraphWeight: 0.8,
		AccessAlpha: 0.15,
	}

	var preprocessor *search.Preprocessor
	if mode == "hybrid" || mode == "hybrid+rerank" {
		cfg.Preprocess = config.PreprocessConfig{Enabled: true}
		preprocessor = search.NewPreprocessor(nil, nil, nil, cfg)
	}

	if mode == "hybrid+rerank" {
		cfg.Rerank = config.RerankConfig{
			Enabled:     true,
			Provider:    "overlap",
			TopK:        20,
			ScoreWeight: 0.7,
		}
	}

	ret := search.NewRetriever(memStore, nil, nil, nil, nil, cfg, preprocessor, nil)

	runner := &Runner{
		memStore:  memStore,
		manager:   mgr,
		retriever: ret,
		dbPath:    dbPath,
	}
	cleanup := func() { _ = memStore.Close() }
	return runner, cleanup, nil
}

// Run 执行评测 / Run evaluation
func (r *Runner) Run(ctx context.Context, ds *EvalDataset, mode string) (*EvalReport, error) {
	start := time.Now()

	// 1. Seed memories
	for _, sm := range ds.SeedMemories {
		kind := sm.Kind
		if kind == "" {
			kind = "note"
		}
		_, err := r.manager.Create(ctx, &model.CreateMemoryRequest{
			Content: sm.Content,
			Kind:    kind,
			SubKind: sm.SubKind,
			Scope:   "eval/test",
		})
		if err != nil {
			return nil, fmt.Errorf("seed memory failed: %w", err)
		}
	}

	// 2. Run queries
	cases := make([]CaseResult, 0, len(ds.Cases))
	for _, ec := range ds.Cases {
		results, err := r.retriever.Retrieve(ctx, &model.RetrieveRequest{
			Query: ec.Query,
			Limit: 10,
		})
		if err != nil {
			cases = append(cases, CaseResult{
				Query:    ec.Query,
				Expected: strings.Join(ec.Expected, "|"),
				Category: ec.Category,
				Difficulty: ec.Difficulty,
				Hit:      false,
				Rank:     -1,
			})
			continue
		}

		hit, rank, score := checkHit(results, ec.Expected)
		cases = append(cases, CaseResult{
			Query:       ec.Query,
			Expected:    strings.Join(ec.Expected, "|"),
			Category:    ec.Category,
			Difficulty:  ec.Difficulty,
			Hit:         hit,
			Rank:        rank,
			Score:       score,
			ResultCount: len(results),
		})
	}

	// 3. Aggregate
	report := &EvalReport{
		Mode:      mode,
		Dataset:   ds.Name,
		Timestamp: time.Now(),
		Metrics:   Aggregate(cases),
		Cases:     cases,
		Duration:  time.Since(start),
	}

	// 4. Group by category and difficulty
	report.ByCategory = groupAggregate(cases, func(c CaseResult) string { return c.Category })
	report.ByDifficulty = groupAggregate(cases, func(c CaseResult) string { return c.Difficulty })

	return report, nil
}

// checkHit 检查检索结果是否命中 / Check if results hit any expected keyword
func checkHit(results []*model.SearchResult, expected []string) (bool, int, float64) {
	for i, r := range results {
		if r == nil || r.Memory == nil {
			continue
		}
		content := strings.ToLower(r.Memory.Content)
		abstract := strings.ToLower(r.Memory.Abstract)
		for _, exp := range expected {
			target := strings.ToLower(exp)
			if strings.Contains(content, target) || strings.Contains(abstract, target) {
				return true, i + 1, r.Score
			}
		}
	}
	return false, -1, 0
}

// groupAggregate 按分组键聚合指标 / Aggregate metrics grouped by key
func groupAggregate(cases []CaseResult, keyFn func(CaseResult) string) map[string]AggregateMetrics {
	groups := map[string][]CaseResult{}
	for _, c := range cases {
		key := keyFn(c)
		groups[key] = append(groups[key], c)
	}
	result := make(map[string]AggregateMetrics, len(groups))
	for key, group := range groups {
		result[key] = Aggregate(group)
	}
	return result
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./testing/eval/ -run TestRunner -v -count=1`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add testing/eval/runner.go testing/eval/runner_test.go
git commit -m "feat(eval): add evaluation runner with FTS and hybrid modes"
```

---

### Task 4: 基线管理 — baseline.go + baseline_test.go

**Files:**
- Create: `testing/eval/baseline.go`
- Create: `testing/eval/baseline_test.go`
- Create: `testing/eval/baselines/.gitkeep`

- [ ] **Step 1: 创建 baseline_test.go — 写失败测试**

```go
package eval_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndLoadBaseline(t *testing.T) {
	dir := t.TempDir()
	report := &eval.EvalReport{
		Mode:      "hybrid",
		Dataset:   "test",
		Timestamp: time.Now(),
		Metrics: eval.AggregateMetrics{
			Total:   100,
			HitRate: 72.4,
			MRR:     0.584,
			NDCG10:  0.612,
		},
	}

	err := eval.SaveBaseline(report, "hybrid-test", dir)
	require.NoError(t, err)

	loaded, err := eval.LoadBaseline("hybrid-test", dir)
	require.NoError(t, err)
	assert.Equal(t, "hybrid", loaded.Mode)
	assert.InDelta(t, 72.4, loaded.Metrics.HitRate, 0.01)
	assert.InDelta(t, 0.584, loaded.Metrics.MRR, 0.001)
}

func TestLoadBaseline_NotFound(t *testing.T) {
	_, err := eval.LoadBaseline("nonexistent", t.TempDir())
	assert.Error(t, err)
}

func TestCompareBaseline_NoRegression(t *testing.T) {
	baseline := &eval.EvalReport{
		Metrics: eval.AggregateMetrics{HitRate: 70.0, MRR: 0.55, NDCG10: 0.60},
	}
	current := &eval.EvalReport{
		Metrics: eval.AggregateMetrics{HitRate: 72.0, MRR: 0.57, NDCG10: 0.62},
	}
	regressions := eval.CompareBaseline(current, baseline, eval.DefaultThresholds)
	assert.Empty(t, regressions)
}

func TestCompareBaseline_WithRegression(t *testing.T) {
	baseline := &eval.EvalReport{
		Metrics: eval.AggregateMetrics{HitRate: 75.0, MRR: 0.60, NDCG10: 0.65},
	}
	current := &eval.EvalReport{
		Metrics: eval.AggregateMetrics{HitRate: 70.0, MRR: 0.55, NDCG10: 0.60},
	}
	regressions := eval.CompareBaseline(current, baseline, eval.DefaultThresholds)
	assert.NotEmpty(t, regressions)

	// 至少 HitRate 应该报回归（下降 5 个百分点，阈值 2.0）
	found := false
	for _, r := range regressions {
		if r.Metric == "HitRate" {
			found = true
			assert.InDelta(t, -5.0, r.Delta, 0.01)
		}
	}
	assert.True(t, found, "expected HitRate regression")
}

func TestListBaselines(t *testing.T) {
	dir := t.TempDir()
	report := &eval.EvalReport{
		Mode:    "hybrid",
		Dataset: "test",
		Metrics: eval.AggregateMetrics{HitRate: 70.0},
	}
	require.NoError(t, eval.SaveBaseline(report, "hybrid-v1", dir))
	require.NoError(t, eval.SaveBaseline(report, "hybrid-v2", dir))

	names, err := eval.ListBaselines(dir)
	require.NoError(t, err)
	assert.Len(t, names, 2)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/eval/ -run TestSave -v -count=1 2>&1 | head -5`
Expected: 编译失败

- [ ] **Step 3: 实现 baseline.go**

```go
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Regression 回归检测结果 / Regression detection result
type Regression struct {
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
	Delta    float64 `json:"delta"`
}

// RegressionThresholds 回归阈值 / Regression detection thresholds
type RegressionThresholds struct {
	HitRateDrop float64 // 命中率下降百分点阈值 / Hit rate drop threshold in percentage points
	MRRDrop     float64 // MRR 下降阈值 / MRR drop threshold
	NDCGDrop    float64 // NDCG@10 下降阈值 / NDCG@10 drop threshold
}

// DefaultThresholds 默认回归阈值 / Default regression thresholds
var DefaultThresholds = RegressionThresholds{
	HitRateDrop: 2.0,
	MRRDrop:     0.02,
	NDCGDrop:    0.02,
}

// SaveBaseline 保存评测报告为基线快照 / Save evaluation report as baseline snapshot
func SaveBaseline(report *EvalReport, name string, baseDir string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("create baseline dir: %w", err)
	}
	path := filepath.Join(baseDir, name+".json")
	return os.WriteFile(path, data, 0644)
}

// LoadBaseline 加载基线快照 / Load baseline snapshot
func LoadBaseline(name string, baseDir string) (*EvalReport, error) {
	path := filepath.Join(baseDir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %q: %w", name, err)
	}
	var report EvalReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse baseline %q: %w", name, err)
	}
	return &report, nil
}

// ListBaselines 列出所有基线名称 / List all baseline names
func ListBaselines(baseDir string) ([]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("read baseline dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names, nil
}

// CompareBaseline 对比当前结果与基线，返回回归项 / Compare current results with baseline, return regressions
func CompareBaseline(current, baseline *EvalReport, thresholds RegressionThresholds) []Regression {
	var regressions []Regression

	check := func(metric string, cur, base, threshold float64) {
		delta := cur - base
		if delta < -threshold {
			regressions = append(regressions, Regression{
				Metric:   metric,
				Baseline: base,
				Current:  cur,
				Delta:    delta,
			})
		}
	}

	check("HitRate", current.Metrics.HitRate, baseline.Metrics.HitRate, thresholds.HitRateDrop)
	check("MRR", current.Metrics.MRR, baseline.Metrics.MRR, thresholds.MRRDrop)
	check("NDCG@10", current.Metrics.NDCG10, baseline.Metrics.NDCG10, thresholds.NDCGDrop)

	return regressions
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./testing/eval/ -run "TestSave|TestLoad|TestCompare|TestList" -v -count=1`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
mkdir -p testing/eval/baselines
touch testing/eval/baselines/.gitkeep
git add testing/eval/baseline.go testing/eval/baseline_test.go testing/eval/baselines/.gitkeep
git commit -m "feat(eval): add baseline snapshot management + regression detection"
```

---

### Task 5: 终端报告输出 + 首版基线

**Files:**
- Modify: `testing/eval/runner.go` — 添加 `PrintReport` 方法
- Create: `testing/eval/eval_test.go` — 完整评测 + 基线保存入口

- [ ] **Step 1: 在 runner.go 底部添加 PrintReport 函数**

```go
// PrintReport 终端输出评测报告 / Print evaluation report to terminal
func PrintReport(report *EvalReport) {
	fmt.Printf("\n=== Eval: %s | %s ===\n", report.Mode, report.Dataset)
	m := report.Metrics
	fmt.Printf("  HitRate:  %5.1f%%  MRR: %.3f  NDCG@10: %.3f\n", m.HitRate, m.MRR, m.NDCG10)
	fmt.Printf("  Recall@1: %5.1f%%  @3: %.1f%%  @5: %.1f%%  @10: %.1f%%\n",
		m.RecallAt1*100, m.RecallAt3*100, m.RecallAt5*100, m.RecallAt10*100)
	fmt.Printf("  Duration: %s | Cases: %d\n", report.Duration.Round(time.Millisecond), m.Total)

	if len(report.ByCategory) > 0 {
		fmt.Printf("\n  By Category:\n")
		for cat, cm := range report.ByCategory {
			fmt.Printf("    %-12s %5.1f%% (MRR %.3f, %d cases)\n", cat+":", cm.HitRate, cm.MRR, cm.Total)
		}
	}
	if len(report.ByDifficulty) > 0 {
		fmt.Printf("\n  By Difficulty:\n")
		for diff, dm := range report.ByDifficulty {
			fmt.Printf("    %-8s %5.1f%% (MRR %.3f, %d cases)\n", diff+":", dm.HitRate, dm.MRR, dm.Total)
		}
	}
	fmt.Println()
}

// PrintComparison 输出基线对比 / Print baseline comparison
func PrintComparison(current, baseline *EvalReport, regressions []Regression) {
	fmt.Printf("  vs baseline %s:\n", baseline.Mode)
	printDelta := func(name string, cur, base float64, pct bool) {
		delta := cur - base
		symbol := "✓"
		for _, r := range regressions {
			if r.Metric == name {
				symbol = "✗ REGRESSION"
				break
			}
		}
		if pct {
			fmt.Printf("    %-10s %5.1f%% → %5.1f%% (%+.1f) %s\n", name+":", base, cur, delta, symbol)
		} else {
			fmt.Printf("    %-10s %.3f → %.3f (%+.3f) %s\n", name+":", base, cur, delta, symbol)
		}
	}
	printDelta("HitRate", current.Metrics.HitRate, baseline.Metrics.HitRate, true)
	printDelta("MRR", current.Metrics.MRR, baseline.Metrics.MRR, false)
	printDelta("NDCG@10", current.Metrics.NDCG10, baseline.Metrics.NDCG10, false)
	fmt.Println()
}
```

需要在 runner.go 顶部 import 中添加 `"fmt"` 和 `"time"`（如果还没有的话）。

- [ ] **Step 2: 创建 eval_test.go — 完整评测入口测试**

```go
package eval_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvalFTS 运行 FTS 模式评测（mini 数据集）
func TestEvalFTS(t *testing.T) {
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), miniDataset(), "fts")
	require.NoError(t, err)
	eval.PrintReport(report)
	assert.True(t, report.Metrics.Total > 0)
}

// TestEvalHybrid 运行 hybrid 模式评测（mini 数据集）
func TestEvalHybrid(t *testing.T) {
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), miniDataset(), "hybrid")
	require.NoError(t, err)
	eval.PrintReport(report)
	assert.True(t, report.Metrics.Total > 0)
}

// TestEvalFull500 运行完整 500 组评测（需要数据集文件）
func TestEvalFull500(t *testing.T) {
	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found, run ExportBuiltinDataset first")
	}

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), ds, "fts")
	require.NoError(t, err)
	eval.PrintReport(report)
	t.Logf("FTS HitRate: %.1f%%, MRR: %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

// TestSaveBaseline 保存当前评测结果为基线
func TestSaveBaseline(t *testing.T) {
	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found")
	}

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), ds, "fts")
	require.NoError(t, err)

	baseDir := "baselines"
	require.NoError(t, eval.SaveBaseline(report, "fts-v1", baseDir))
	eval.PrintReport(report)
	t.Logf("Baseline saved to %s/fts-v1.json", baseDir)
}

// TestRegressionCheck 对比当前结果与基线，回归时失败
func TestRegressionCheck(t *testing.T) {
	baseDir := "baselines"
	names, err := eval.ListBaselines(baseDir)
	if err != nil || len(names) == 0 {
		t.Skip("skip: no baselines found, run TestSaveBaseline first")
	}

	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found")
	}

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	// 找到最新的 fts 基线
	var baselineName string
	for _, n := range names {
		if len(n) >= 3 && n[:3] == "fts" {
			baselineName = n
		}
	}
	if baselineName == "" {
		t.Skip("skip: no fts baseline found")
	}

	baseline, err := eval.LoadBaseline(baselineName, baseDir)
	require.NoError(t, err)

	report, err := runner.Run(context.Background(), ds, "fts")
	require.NoError(t, err)

	regressions := eval.CompareBaseline(report, baseline, eval.DefaultThresholds)
	eval.PrintReport(report)
	eval.PrintComparison(report, baseline, regressions)

	if len(regressions) > 0 {
		for _, r := range regressions {
			t.Errorf("REGRESSION: %s dropped from %.3f to %.3f (delta: %.3f)",
				r.Metric, r.Baseline, r.Current, r.Delta)
		}
	}
}
```

- [ ] **Step 3: 运行 mini 数据集测试确认通过**

Run: `go test ./testing/eval/ -run "TestEvalFTS|TestEvalHybrid" -v -count=1`
Expected: PASS，终端输出指标

- [ ] **Step 4: 运行完整 500 组测试（如果数据集已导出）**

Run: `go test ./testing/eval/ -run TestEvalFull500 -v -count=1 -timeout 300s`
Expected: PASS 或 Skip

- [ ] **Step 5: 提交**

```bash
git add testing/eval/runner.go testing/eval/eval_test.go
git commit -m "feat(eval): add terminal report output + full evaluation test entries"
```

---

### Task 6: 更新开发文档 + 路线图标记

**Files:**
- Modify: `docs/开发文档.md` — B1 #1, #2 标记完成
- Modify: `docs/IClude产品概述.md` — 同步状态

- [ ] **Step 1: 更新开发文档中 B1 任务状态**

在 `docs/开发文档.md` 的 Benchmark Track B1 表格中：

```
| 1 | LongMemEval Evaluation Framework | ✅ | 2周 | ...
| 2 | Baseline 固化 | ✅ | 2天 | ...
```

在 `docs/IClude产品概述.md` 的 Benchmark Track 章节中同步更新。

- [ ] **Step 2: 提交**

```bash
git add docs/开发文档.md docs/IClude产品概述.md
git commit -m "docs: mark B1 eval framework + baseline as complete"
```

---

## 执行顺序依赖

```
Task 1 (metrics) ─── 无依赖
Task 2 (dataset) ─── 无依赖
Task 3 (runner)  ─── 依赖 Task 1 + Task 2
Task 4 (baseline) ── 依赖 Task 1（使用 AggregateMetrics 和 EvalReport）
Task 5 (报告+基线) ─ 依赖 Task 3 + Task 4
Task 6 (文档)    ─── 依赖 Task 5
```

Task 1 和 Task 2 可以并行执行。Task 3 和 Task 4 在 Task 1 完成后可以并行（Task 4 只依赖类型定义）。
