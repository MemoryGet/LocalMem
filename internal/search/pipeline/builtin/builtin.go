// Package builtin 内置管线定义与注册 / Built-in pipeline definitions and registration
package builtin

import (
	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// defaultTrimTokens 默认 token 截断上限 / Default token trim budget
const defaultTrimTokens = 4000

// Deps 管线构建所需的依赖集 / Dependencies for pipeline construction
type Deps struct {
	FTSSearcher  stage.FTSSearcher
	GraphStore   stage.GraphRetriever
	VectorStore  stage.VectorSearcher
	Embedder     stage.Embedder
	Timeline     stage.TimelineSearcher
	CoreProvider stage.CoreProvider
	LLM          llm.Provider
	Cfg          config.RetrievalConfig
}

// RegisterBuiltins 注册所有内置管线并返回共享后处理 stage
// Register all built-in pipelines and return shared post-processing stages
func RegisterBuiltins(registry *pipeline.Registry, deps Deps) []pipeline.Stage {
	postStages := buildPostStages(deps)

	registry.Register(buildPrecision(deps))
	registry.Register(buildExploration(deps))
	registry.Register(buildSemantic(deps))
	registry.Register(buildAssociation(deps))
	registry.Register(buildFast(deps))
	registry.Register(buildFull(deps))

	return postStages
}

// buildPrecision 精确检索管线: parallel(graph, fts) → merge(graph_aware) → score_filter(0.3) → rerank_graph
// Precision pipeline: graph + FTS parallel → graph-aware merge → filter → graph rerank
func buildPrecision(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelinePrecision,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewGraphStage(deps.GraphStore, deps.FTSSearcher,
					stage.WithMaxDepth(2), stage.WithLimit(30),
					stage.WithFTSTop(5), stage.WithEntityLimit(10),
				),
				stage.NewFTSStage(deps.FTSSearcher, 30),
			}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(stage.MergeStrategyGraphAware, 60, 100)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.3)}},
			{Stages: []pipeline.Stage{stage.NewRerankGraphStage(deps.GraphStore, 0.6, 0.2)}},
		},
		Fallback: pipeline.PipelineExploration,
	}
}

// buildExploration 探索检索管线: parallel(fts, temporal) → merge(rrf) → score_filter(0.2) → rerank_overlap
// Exploration pipeline: FTS + temporal parallel → RRF merge → filter → overlap rerank
func buildExploration(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineExploration,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewFTSStage(deps.FTSSearcher, 30),
				stage.NewTemporalStage(deps.Timeline, 30),
			}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(stage.MergeStrategyRRF, 60, 100)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.05)}},
			{Stages: []pipeline.Stage{stage.NewOverlapRerankStage(20, 0.7)}},
		},
		// 无 fallback — 终端降级管线 / No fallback — terminal fallback pipeline
	}
}

// buildSemantic 语义检索管线: parallel(vector, fts) → merge(rrf) → score_filter(0.3) → rerank_overlap
// Semantic pipeline: vector + FTS parallel → RRF merge → filter → overlap rerank
func buildSemantic(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineSemantic,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewVectorStage(deps.VectorStore, deps.Embedder, 30, 0.3),
				stage.NewFTSStage(deps.FTSSearcher, 30),
			}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(stage.MergeStrategyRRF, 60, 100)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.3)}},
			{Stages: []pipeline.Stage{stage.NewOverlapRerankStage(20, 0.7)}},
		},
		Fallback: pipeline.PipelineExploration,
	}
}

// buildAssociation 关联检索管线: graph(depth=3) → rerank_graph → score_filter(0.2)
// Association pipeline: deep graph traversal → graph rerank → filter
func buildAssociation(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineAssociation,
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{
				stage.NewGraphStage(deps.GraphStore, deps.FTSSearcher,
					stage.WithMaxDepth(3), stage.WithLimit(30),
					stage.WithFTSTop(5), stage.WithEntityLimit(10),
				),
			}},
			{Stages: []pipeline.Stage{stage.NewRerankGraphStage(deps.GraphStore, 0.6, 0.2)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.2)}},
		},
		Fallback: pipeline.PipelinePrecision,
	}
}

// buildFast 快速检索管线: fts(limit=10) → score_filter(0.3)
// Fast pipeline: FTS only with low limit → filter
func buildFast(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineFast,
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{stage.NewFTSStage(deps.FTSSearcher, 10)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.05)}},
		},
		// 无 fallback / No fallback
	}
}

// buildFull 全量检索管线: parallel(graph, fts, vector) → merge(graph_aware) → score_filter(0.3) → rerank_llm
// Full pipeline: graph + FTS + vector parallel → graph-aware merge → filter → LLM rerank
func buildFull(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineFull,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewGraphStage(deps.GraphStore, deps.FTSSearcher,
					stage.WithMaxDepth(2), stage.WithLimit(30),
					stage.WithFTSTop(5), stage.WithEntityLimit(10),
				),
				stage.NewFTSStage(deps.FTSSearcher, 30),
				stage.NewVectorStage(deps.VectorStore, deps.Embedder, 30, 0.3),
			}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(stage.MergeStrategyGraphAware, 60, 100)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.3)}},
			{Stages: []pipeline.Stage{stage.NewRerankLLMStage(deps.LLM, 20, 0.7, 0.3, 0)}},
		},
		Fallback: pipeline.PipelinePrecision,
	}
}

// buildPostStages 构建共享后处理 stage 列表 / Build shared post-processing stages
func buildPostStages(deps Deps) []pipeline.Stage {
	return []pipeline.Stage{
		stage.NewWeightStage(deps.Cfg.AccessAlpha),
		stage.NewMMRStage(deps.VectorStore, deps.Cfg.MMR.Lambda, 0), // 0 = 使用输入长度 / 0 = use input length
		stage.NewCoreStage(deps.CoreProvider),
		stage.NewTrimStage(defaultTrimTokens),
	}
}
