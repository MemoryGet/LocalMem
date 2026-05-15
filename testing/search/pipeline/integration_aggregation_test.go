package pipeline_test

import (
	"context"
	"testing"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/search/pipeline"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAggregationPipeline_EndToEnd 聚合管线端到端路由测试
// Verifies: aggregation query → PipelineAggregation → ContextType="aggregation"
func TestAggregationPipeline_EndToEnd(t *testing.T) {
	ctx := context.Background()

	cfg := config.RetrievalConfig{FTSWeight: 1.0}
	// nil graphStore — ExhaustiveStage 即使无数据也必须设置 ContextType
	// nil graphStore — ExhaustiveStage returns empty but must still set ContextType
	retriever := search.NewRetriever(nil, nil, nil, nil, nil, cfg, nil, nil)
	retriever.InitPipeline()

	result, err := retriever.RetrieveWithDebug(ctx, &model.RetrieveRequest{
		Query: "how much total did I spend on bikes",
		Limit: 20,
		Debug: true,
	})
	require.NoError(t, err)

	// 路由断言：必须选中聚合管线 / Routing assertion: must select aggregation pipeline
	require.NotNil(t, result.PipelineInfo, "debug info should be present")
	assert.Equal(t, "aggregation", result.PipelineInfo.PipelineName)

	// 上下文类型断言 / Context type assertion
	assert.Equal(t, model.RetrievalContextAggregation, result.ContextType)
}

// TestNonAggregationQuery_ContextTypeIsPoint 非聚合查询返回 point 类型
// Non-aggregation queries must return ContextType="point"
func TestNonAggregationQuery_ContextTypeIsPoint(t *testing.T) {
	ctx := context.Background()

	cfg := config.RetrievalConfig{FTSWeight: 1.0}
	retriever := search.NewRetriever(nil, nil, nil, nil, nil, cfg, nil, nil)
	retriever.InitPipeline()

	result, err := retriever.RetrieveWithDebug(ctx, &model.RetrieveRequest{
		Query: "what did I eat for lunch yesterday",
		Limit: 10,
		Debug: true,
	})
	require.NoError(t, err)

	assert.Equal(t, model.RetrievalContextPoint, result.ContextType)
	// 必须不路由到聚合管线 / Must NOT route to aggregation pipeline
	require.NotNil(t, result.PipelineInfo, "debug info must be present when Debug=true")
	assert.NotEqual(t, "aggregation", result.PipelineInfo.PipelineName,
		"non-aggregation query must not route to aggregation pipeline")
}

// TestInitPipeline_AcceptsExtraPostStages 验证 InitPipeline 可注入额外后处理 stage
// Verifies that InitPipeline accepts variadic extra post-stages.
func TestInitPipeline_AcceptsExtraPostStages(t *testing.T) {
	cfg := config.RetrievalConfig{FTSWeight: 1.0}
	retriever := search.NewRetriever(nil, nil, nil, nil, nil, cfg, nil, nil)

	called := false
	spy := &spyStage{name: "spy", fn: func() { called = true }}
	retriever.InitPipeline(spy)

	_, _ = retriever.RetrieveWithDebug(context.Background(), &model.RetrieveRequest{
		Query: "test", Limit: 1, Debug: true,
	})
	if !called {
		t.Error("extra post-stage was not called")
	}
}

// TestInitPipeline_ExtraStageRunsBeforeTrim 验证额外 stage 在 Trim 之前执行
// Extra post-stages must run BEFORE trim so they see the full candidate list.
func TestInitPipeline_ExtraStageRunsBeforeTrim(t *testing.T) {
	cfg := config.RetrievalConfig{FTSWeight: 1.0}
	retriever := search.NewRetriever(nil, nil, nil, nil, nil, cfg, nil, nil)

	counting := &countingStage{name: "counter"}
	retriever.InitPipeline(counting)

	result, err := retriever.RetrieveWithDebug(context.Background(), &model.RetrieveRequest{
		Query: "test", Limit: 1, Debug: true,
	})
	if err != nil {
		t.Fatalf("RetrieveWithDebug error: %v", err)
	}

	// The extra stage should appear in traces BEFORE "trim" or "disclosure"
	traces := result.PipelineInfo.Traces
	counterIdx, trimIdx := -1, -1
	for i, tr := range traces {
		if tr.Name == "counter" {
			counterIdx = i
		}
		if tr.Name == "trim" || tr.Name == "disclosure" {
			trimIdx = i
		}
	}
	if counterIdx == -1 {
		t.Error("counter stage not found in traces")
	}
	if trimIdx != -1 && counterIdx > trimIdx {
		t.Errorf("extra stage (idx %d) ran AFTER trim (idx %d); want BEFORE", counterIdx, trimIdx)
	}
}

type countingStage struct {
	name      string
	seenCount int
}

func (s *countingStage) Name() string { return s.name }
func (s *countingStage) Execute(_ context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	s.seenCount = len(state.Candidates)
	return state, nil
}

type spyStage struct {
	name string
	fn   func()
}

func (s *spyStage) Name() string { return s.name }
func (s *spyStage) Execute(_ context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	s.fn()
	return state, nil
}
