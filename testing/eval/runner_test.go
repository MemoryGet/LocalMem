package eval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// miniDataset 返回用于测试的最小数据集 / Returns a minimal dataset for testing
func miniDataset() *EvalDataset {
	return &EvalDataset{
		Name:        "mini-test",
		Description: "minimal dataset for runner tests",
		SeedMemories: []SeedMemory{
			{Content: "Go 语言适合编写高并发后端服务", Kind: "fact"},
			{Content: "Python 是数据科学领域的首选语言", Kind: "fact"},
			{Content: "用户偏好使用 Vim 编辑器", Kind: "profile", SubKind: "preference"},
			{Content: "项目部署在阿里云 ECS 上海区域", Kind: "fact", SubKind: "entity"},
			{Content: "数据库从 PostgreSQL 迁移到了 SQLite", Kind: "fact", SubKind: "event"},
		},
		Cases: []EvalCase{
			{Query: "Go 语言的优势", Expected: []string{"高并发"}, Category: "exact", Difficulty: "easy"},
			{Query: "数据科学用什么语言", Expected: []string{"Python"}, Category: "synonym", Difficulty: "medium"},
			{Query: "数据库迁移", Expected: []string{"PostgreSQL", "SQLite"}, Category: "exact", Difficulty: "easy"},
		},
	}
}

// TestRunner_FTSMode 测试 FTS 模式下的评测运行器 / Tests the evaluation runner in FTS mode
func TestRunner_FTSMode(t *testing.T) {
	runner, cleanup := NewTestRunner(t)
	defer cleanup()

	ctx := context.Background()
	ds := miniDataset()

	report, err := runner.Run(ctx, ds, "fts")
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, "fts", report.Mode)
	assert.Equal(t, "mini-test", report.Dataset)
	assert.Equal(t, 3, report.Metrics.Total)
	assert.NotZero(t, report.Duration)
	assert.Len(t, report.Cases, 3)

	// 至少应有部分命中 / Should have at least some hits
	assert.Greater(t, report.Metrics.HitRate, 0.0)
}

// TestRunner_HybridMode 测试混合模式下的评测运行器 / Tests the evaluation runner in hybrid mode
func TestRunner_HybridMode(t *testing.T) {
	runner, cleanup := NewTestRunner(t)
	defer cleanup()

	ctx := context.Background()
	ds := miniDataset()

	report, err := runner.Run(ctx, ds, "hybrid")
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, "hybrid", report.Mode)
	assert.Equal(t, "mini-test", report.Dataset)
	assert.Equal(t, 3, report.Metrics.Total)
	assert.NotZero(t, report.Duration)
	assert.Len(t, report.Cases, 3)

	// 至少应有部分命中 / Should have at least some hits
	assert.Greater(t, report.Metrics.HitRate, 0.0)
}
