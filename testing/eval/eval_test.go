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
