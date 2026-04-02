package eval_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestEvalFTS(t *testing.T) {
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), miniDataset(), "fts")
	require.NoError(t, err)
	eval.PrintReport(report)
	assert.True(t, report.Metrics.Total > 0)
}

func TestEvalHybrid(t *testing.T) {
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	report, err := runner.Run(context.Background(), miniDataset(), "hybrid")
	require.NoError(t, err)
	eval.PrintReport(report)
	assert.True(t, report.Metrics.Total > 0)
}

func TestEvalFull500(t *testing.T) {
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
	eval.PrintReport(report)
	t.Logf("FTS HitRate: %.1f%%, MRR: %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

func TestSaveFirstBaseline(t *testing.T) {
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

// TestEvalHybridFull500 运行 hybrid 模式全量评测（需要 LLM API key）
func TestEvalHybridFull500(t *testing.T) {
	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY not set, hybrid mode requires LLM")
	}

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	runner, cleanup, err := eval.NewRunner(filepath.Join(tmpDir, "hybrid.db"), "hybrid")
	require.NoError(t, err)
	defer cleanup()

	report, err := runner.Run(context.Background(), ds, "hybrid")
	require.NoError(t, err)
	eval.PrintReport(report)

	require.NoError(t, eval.SaveBaseline(report, "hybrid-v1", "baselines"))
	t.Logf("Hybrid baseline saved: HitRate %.1f%%, MRR %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

// TestEvalHybridRerankFull500 运行 hybrid+rerank 模式全量评测
func TestEvalHybridRerankFull500(t *testing.T) {
	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY not set, hybrid+rerank mode requires LLM")
	}

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	runner, cleanup, err := eval.NewRunner(filepath.Join(tmpDir, "rerank.db"), "hybrid+rerank")
	require.NoError(t, err)
	defer cleanup()

	report, err := runner.Run(context.Background(), ds, "hybrid+rerank")
	require.NoError(t, err)
	eval.PrintReport(report)

	require.NoError(t, eval.SaveBaseline(report, "hybrid-rerank-v1", "baselines"))
	t.Logf("Hybrid+rerank baseline saved: HitRate %.1f%%, MRR %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

// TestEvalGseFull500 gse 分词器 FTS-only 评测
func TestEvalGseFull500(t *testing.T) {
	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found")
	}

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	runner, cleanup, err := eval.NewRunner(filepath.Join(tmpDir, "gse.db"), "fts", eval.WithTokenizer("gse"))
	require.NoError(t, err)
	defer cleanup()

	report, err := runner.Run(context.Background(), ds, "fts (gse)")
	require.NoError(t, err)
	eval.PrintReport(report)

	require.NoError(t, eval.SaveBaseline(report, "fts-gse-v1", "baselines"))
	t.Logf("GSE baseline saved: HitRate %.1f%%, MRR %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

// TestEvalJiebaFull500 jieba 分词器 FTS-only 评测（需要 jieba 服务）
func TestEvalJiebaFull500(t *testing.T) {
	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found")
	}

	// 检查 jieba 服务是否可用
	resp, err := http.Post("http://localhost:8866/tokenize", "application/json",
		strings.NewReader(`{"text":"测试","cut_all":false}`))
	if err != nil || resp.StatusCode != 200 {
		t.Skip("skip: jieba service not available at localhost:8866")
	}
	resp.Body.Close()

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	runner, cleanup, err := eval.NewRunner(filepath.Join(tmpDir, "jieba.db"), "fts", eval.WithTokenizer("jieba"))
	require.NoError(t, err)
	defer cleanup()

	report, err := runner.Run(context.Background(), ds, "fts (jieba)")
	require.NoError(t, err)
	eval.PrintReport(report)

	require.NoError(t, eval.SaveBaseline(report, "fts-jieba-v1", "baselines"))
	t.Logf("Jieba baseline saved: HitRate %.1f%%, MRR %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

// TestLongMemEvalS 运行 LongMemEval _s 变体（48 sessions/question，含干扰）
func TestLongMemEvalS(t *testing.T) {
	datasetPath := filepath.Join("testdata", "longmemeval-s.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/longmemeval-s.json not found")
	}

	entries, err := eval.LoadLongMemEval(datasetPath)
	require.NoError(t, err)
	t.Logf("Loaded %d LongMemEval _s questions", len(entries))

	tmpDir := t.TempDir()
	report, err := eval.RunLongMemEval(context.Background(), entries, tmpDir)
	require.NoError(t, err)

	// 覆盖 mode 标记
	report.Mode = "fts (simple) — LongMemEval _s"
	report.Dataset = "longmemeval-s"

	eval.PrintReport(report)

	require.NoError(t, eval.SaveBaseline(report, "longmemeval-s-fts-v1", "baselines"))
	t.Logf("LongMemEval _s baseline saved: HitRate %.1f%%, MRR %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

// TestLongMemEvalOracle 运行 LongMemEval oracle 数据集（Simple tokenizer, FTS-only）
func TestLongMemEvalOracle(t *testing.T) {
	datasetPath := filepath.Join("testdata", "longmemeval-oracle.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/longmemeval-oracle.json not found, run longmemeval_adapter.py first")
	}

	entries, err := eval.LoadLongMemEval(datasetPath)
	require.NoError(t, err)
	t.Logf("Loaded %d LongMemEval questions", len(entries))

	tmpDir := t.TempDir()
	report, err := eval.RunLongMemEval(context.Background(), entries, tmpDir)
	require.NoError(t, err)
	eval.PrintReport(report)

	require.NoError(t, eval.SaveBaseline(report, "longmemeval-oracle-fts-v1", "baselines"))
	t.Logf("LongMemEval baseline saved: HitRate %.1f%%, MRR %.3f", report.Metrics.HitRate, report.Metrics.MRR)
}

func TestRegressionCheck(t *testing.T) {
	baseDir := "baselines"
	names, err := eval.ListBaselines(baseDir)
	if err != nil || len(names) == 0 {
		t.Skip("skip: no baselines found, run TestSaveFirstBaseline first")
	}

	datasetPath := filepath.Join("testdata", "retrieval-500.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/retrieval-500.json not found")
	}

	ds, err := eval.LoadDatasetFromJSON(datasetPath)
	require.NoError(t, err)

	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

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
