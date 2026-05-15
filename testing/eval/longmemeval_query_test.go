package eval_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/require"
)

// runLongMemEvalQuery 通用查询辅助：加载数据集 → 运行评测 → 打印报告 → 对比基线
func runLongMemEvalQuery(t *testing.T, tier eval.Tier, maxQ int) {
	t.Helper()
	dbPath := defaultEvalDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: eval DB not found at %s (run TestLongMemEvalSeedFTS first)", dbPath)
	}

	datasetPath := filepath.Join("testdata", "longmemeval-oracle.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/longmemeval-oracle.json not found")
	}

	entries, err := eval.LoadLongMemEval(datasetPath)
	require.NoError(t, err)
	t.Logf("Loaded %d questions, running %d with tier [%s]", len(entries), maxQ, tier.Name)

	report, err := eval.RunLongMemEval(context.Background(), entries, dbPath, tier, maxQ)
	require.NoError(t, err)
	eval.PrintReport(report)

	// 与已有基线对比 / Compare with existing baseline
	baselineName := fmt.Sprintf("longmemeval-%s-v1", tier.Name)
	if baseline, err := eval.LoadBaseline(baselineName, "baselines"); err == nil {
		regressions := eval.CompareBaseline(report, baseline, eval.DefaultThresholds)
		eval.PrintComparison(report, baseline, regressions)
		for _, r := range regressions {
			t.Errorf("REGRESSION [%s]: %s %.3f → %.3f (delta %.3f)",
				tier.Name, r.Metric, r.Baseline, r.Current, r.Delta)
		}
	}

	require.NoError(t, eval.SaveBaseline(report, baselineName, "baselines"))
	t.Logf("[%s] HitRate %.1f%%  MRR %.3f  Duration %s",
		tier.Name, report.Metrics.HitRate, report.Metrics.MRR, report.Duration.Round(0))
}

// TestLongMemEvalFTS 层级 1：纯 FTS 检索，无管线无图谱
// Tier 1: pure FTS, no pipeline, no graph
func TestLongMemEvalFTS(t *testing.T) {
	runLongMemEvalQuery(t, eval.TierFTS, 100)
}

// TestLongMemEvalPipeline 层级 2：FTS + Cascade 意图分类器
// Tier 2: FTS + cascade intent classifier
func TestLongMemEvalPipeline(t *testing.T) {
	runLongMemEvalQuery(t, eval.TierPipeline, 100)
}

// TestLongMemEvalGraph 层级 3：FTS + 管线 + 图谱（实体抽取 + 图谱检索）
// Tier 3: FTS + pipeline + graph (entity extraction + graph stage)
func TestLongMemEvalGraph(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY required for graph stage")
	}
	runLongMemEvalQuery(t, eval.TierGraph, 100)
}

// TestLongMemEvalGraphW05 Graph 层级 GraphWeight=0.5 消噪对比
func TestLongMemEvalGraphW05(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY required for graph stage")
	}
	tier := eval.TierGraph
	tier.Name = "fts+pipeline+graph-w0.5"
	tier.GraphWeight = 0.5
	runLongMemEvalQuery(t, tier, 100)
}

// TestLongMemEvalGraphW03 Graph 层级 GraphWeight=0.3 消噪对比
func TestLongMemEvalGraphW03(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY required for graph stage")
	}
	tier := eval.TierGraph
	tier.Name = "fts+pipeline+graph-w0.3"
	tier.GraphWeight = 0.3
	runLongMemEvalQuery(t, tier, 100)
}

// TestLongMemEvalVector 层级 4：FTS + 管线 + 图谱 + Qdrant 向量（需要 Qdrant）
// Tier 4: FTS + pipeline + graph + Qdrant vector (requires Qdrant)
func TestLongMemEvalVector(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY required for graph stage")
	}
	runLongMemEvalQuery(t, eval.TierVector, 100)
}

// TestLongMemEvalFull 层级 5：全通道 + LLM 精排（需要 Qdrant + LLM）
// Tier 5: all channels + LLM rerank (requires Qdrant + LLM)
func TestLongMemEvalFull(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY required")
	}
	runLongMemEvalQuery(t, eval.TierFull, 100)
}

// TestRunLongMemEval_RerankDoesNotPanic 烟雾测试：tier.Rerank=true 时 RunLongMemEval 不应 panic
// Smoke test: RunLongMemEval should not panic when tier.Rerank=true.
func TestRunLongMemEval_RerankDoesNotPanic(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("skip: OPENAI_API_KEY required")
	}
	dbPath := defaultEvalDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: eval DB not found at %s", dbPath)
	}
	datasetPath := filepath.Join("testdata", "longmemeval-oracle.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata not found")
	}
	entries, err := eval.LoadLongMemEval(datasetPath)
	require.NoError(t, err)

	tier := eval.TierPipeline
	tier.Rerank = true
	report, err := eval.RunLongMemEval(context.Background(), entries, dbPath, tier, 5)
	require.NoError(t, err)
	require.Equal(t, 5, report.Metrics.Total)
}
