package eval

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndLoadBaseline(t *testing.T) {
	dir := t.TempDir()
	report := &EvalReport{
		Mode:      "hybrid",
		Dataset:   "retrieval-500",
		Timestamp: time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC),
		Metrics: AggregateMetrics{
			Total:   100,
			HitRate: 75.0,
			MRR:     0.65,
			NDCG10:  0.60,
		},
		GitCommit: "abc123",
	}

	err := SaveBaseline(report, "v1", dir)
	require.NoError(t, err)

	loaded, err := LoadBaseline("v1", dir)
	require.NoError(t, err)

	assert.Equal(t, report.Mode, loaded.Mode)
	assert.Equal(t, report.Dataset, loaded.Dataset)
	assert.Equal(t, report.Metrics.Total, loaded.Metrics.Total)
	assert.InDelta(t, report.Metrics.HitRate, loaded.Metrics.HitRate, 0.001)
	assert.InDelta(t, report.Metrics.MRR, loaded.Metrics.MRR, 0.001)
	assert.InDelta(t, report.Metrics.NDCG10, loaded.Metrics.NDCG10, 0.001)
	assert.Equal(t, report.GitCommit, loaded.GitCommit)
}

func TestLoadBaseline_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadBaseline("nonexistent", dir)
	require.Error(t, err)
}

func TestCompareBaseline_NoRegression(t *testing.T) {
	baseline := &EvalReport{
		Metrics: AggregateMetrics{HitRate: 70.0, MRR: 0.60, NDCG10: 0.55},
	}
	current := &EvalReport{
		Metrics: AggregateMetrics{HitRate: 72.0, MRR: 0.62, NDCG10: 0.57},
	}

	regressions := CompareBaseline(current, baseline, DefaultThresholds)
	assert.Empty(t, regressions)
}

func TestCompareBaseline_WithRegression(t *testing.T) {
	baseline := &EvalReport{
		Metrics: AggregateMetrics{HitRate: 70.0, MRR: 0.60, NDCG10: 0.55},
	}
	// HitRate drops 5 points (> 2.0 threshold), MRR drops 0.05 (> 0.02 threshold)
	current := &EvalReport{
		Metrics: AggregateMetrics{HitRate: 65.0, MRR: 0.55, NDCG10: 0.55},
	}

	regressions := CompareBaseline(current, baseline, DefaultThresholds)
	require.NotEmpty(t, regressions)

	metrics := make(map[string]Regression, len(regressions))
	for _, r := range regressions {
		metrics[r.Metric] = r
	}

	hitReg, ok := metrics["HitRate"]
	require.True(t, ok, "expected HitRate regression")
	assert.InDelta(t, 70.0, hitReg.Baseline, 0.001)
	assert.InDelta(t, 65.0, hitReg.Current, 0.001)
	assert.InDelta(t, -5.0, hitReg.Delta, 0.001)

	mrrReg, ok := metrics["MRR"]
	require.True(t, ok, "expected MRR regression")
	assert.InDelta(t, 0.60, mrrReg.Baseline, 0.001)
	assert.InDelta(t, 0.55, mrrReg.Current, 0.001)

	// NDCG10 did not drop, should not be in regressions
	_, found := metrics["NDCG@10"]
	assert.False(t, found, "NDCG@10 should not be a regression")
}

func TestListBaselines(t *testing.T) {
	dir := t.TempDir()

	report := &EvalReport{Mode: "fts", Dataset: "test"}

	require.NoError(t, SaveBaseline(report, "baseline-a", dir))
	require.NoError(t, SaveBaseline(report, "baseline-b", dir))

	names, err := ListBaselines(dir)
	require.NoError(t, err)
	assert.Len(t, names, 2)
	assert.Contains(t, names, "baseline-a")
	assert.Contains(t, names, "baseline-b")
}
