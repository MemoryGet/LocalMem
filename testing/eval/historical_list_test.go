package eval_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHistoricalListQuery 验证「之前我都做了哪些事情」类查询的端到端效果
// End-to-end: seed task history → "之前我都做了哪些事情" → aggregation pipeline + non-empty results
func TestHistoricalListQuery(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	// 种入历史任务记录 / Seed historical task records
	seeds := []eval.SeedMemory{
		{Content: "完成了文档解析流程的 spec 设计，已写好规范文档", Kind: "event", MemoryClass: "episodic"},
		{Content: "实现了 ExhaustiveStage，支持无实体时回退全量时序扫描", Kind: "event", MemoryClass: "episodic"},
		{Content: "修复了聚合查询路由问题，之前会错误路由到 exploration 管线", Kind: "event", MemoryClass: "episodic"},
		{Content: "添加了历史列举模式正则，识别「之前/以前/过去 + 都/哪些/所有」", Kind: "event", MemoryClass: "episodic"},
		{Content: "跑了全量测试，testing/api 的 OOM 是环境问题与代码无关", Kind: "event", MemoryClass: "episodic"},
	}

	for _, s := range seeds {
		require.NoError(t, runner.SeedOne(ctx, s))
	}

	// 初始化管线并获取 Retriever / Initialize pipeline and get retriever
	retriever := runner.Retriever()
	retriever.InitPipeline()

	result, err := retriever.RetrieveWithDebug(ctx, &model.RetrieveRequest{
		Query: "之前我都做了哪些事情",
		Limit: 20,
		Debug: true,
	})
	require.NoError(t, err)

	t.Logf("Pipeline selected: %s", result.PipelineInfo.PipelineName)
	t.Logf("Results returned: %d", len(result.Results))
	for i, m := range result.Results {
		t.Logf("  [%d] %s", i+1, m.Memory.Content)
	}

	// 核心断言：路由到聚合管线 / Core assertion: routed to aggregation pipeline
	require.NotNil(t, result.PipelineInfo)
	assert.Equal(t, "aggregation", result.PipelineInfo.PipelineName,
		"historical list query must route to aggregation pipeline")

	// 核心断言：返回了实际记录 / Core assertion: actual records returned
	assert.Greater(t, len(result.Results), 0,
		"should return task history memories from timeline fallback")
}

// TestHistoricalListQuery_VsPointRetrieval 对照：点检索查询不走聚合管线
// Control: point-retrieval query must NOT route to aggregation pipeline
func TestHistoricalListQuery_VsPointRetrieval(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := eval.NewTestRunner(t)
	defer cleanup()

	require.NoError(t, runner.SeedOne(ctx, eval.SeedMemory{
		Content: "实现了文档解析流程的核心解析器", Kind: "event",
	}))

	retriever := runner.Retriever()
	retriever.InitPipeline()

	result, err := retriever.RetrieveWithDebug(ctx, &model.RetrieveRequest{
		Query: "文档解析流程的解析器怎么实现的",
		Limit: 10,
		Debug: true,
	})
	require.NoError(t, err)

	require.NotNil(t, result.PipelineInfo)
	t.Logf("Point query pipeline: %s", result.PipelineInfo.PipelineName)
	assert.NotEqual(t, "aggregation", result.PipelineInfo.PipelineName,
		"point-retrieval query must not route to aggregation")
}
