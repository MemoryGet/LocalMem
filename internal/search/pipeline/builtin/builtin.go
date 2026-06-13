// Package builtin 内置管线定义与注册 / Built-in pipeline definitions and registration
package builtin

import (
	"iclude/internal/config"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
	"iclude/pkg/tokenizer"
)

// Deps 管线构建所需的依赖集 / Dependencies for pipeline construction
type Deps struct {
	FTSSearcher      stage.FTSSearcher
	GraphStore       stage.GraphRetriever
	VectorStore      stage.VectorSearcher
	Embedder         stage.Embedder
	Timeline         stage.TimelineSearcher
	CoreProvider     stage.CoreProvider
	SalienceChecker  stage.SalienceChecker // 可选：hub 节点检测 / Optional: hub node detector
	SalienceMaxCount int                   // hub 阈值，0 = 禁用 / Hub threshold, 0 = disabled
	FTSFirstSeeds    bool                  // Option B: FTS 优先种子策略 / FTS-first seed strategy
	GraphTermExpander stage.GraphTermExpander // optional: for FTS retry graph expansion
	HyDEGenerator    stage.HyDEGenerator    // optional: on-demand HyDE for FTS retry (only when FTS insufficient)
	Tokenizer        tokenizer.Tokenizer    // optional: for bilingual content term extraction in FTS retry
	Cfg              config.RetrievalConfig
	VectorQueryInstruction string           // optional: asymmetric retrieval instruction prefix / 可选非对称检索指令前缀
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
	registry.Register(buildAggregation(deps))

	return postStages
}

// graphOpts 构建 GraphStage 选项列表，有向量证据时自动注入 / Build GraphStage options, inject vector evidence when available
func graphOpts(deps Deps, extra ...stage.GraphOption) []stage.GraphOption {
	opts := append([]stage.GraphOption{
		stage.WithMaxDepth(2), stage.WithLimit(30),
		stage.WithFTSTop(5), stage.WithEntityLimit(10),
		stage.WithMaxVisited(deps.Cfg.GraphMaxVisited),
		stage.WithDecayLambda(deps.Cfg.RelationDecayLambda),
		stage.WithPathConfidence(1.0),
	}, extra...)
	if deps.Embedder != nil && deps.VectorStore != nil {
		opts = append(opts, stage.WithVectorEvidence(deps.Embedder, deps.VectorStore))
	}
	if deps.SalienceChecker != nil && deps.SalienceMaxCount > 0 {
		opts = append(opts, stage.WithSalienceFilter(deps.SalienceChecker, deps.SalienceMaxCount))
	}
	if deps.FTSFirstSeeds {
		opts = append(opts, stage.WithFTSFirstSeeds())
	}
	return opts
}

// buildPrecision 精确检索管线: parallel(graph, fts) → merge(fts_primary) → score_filter(0.3) → rerank_graph
// Precision pipeline: graph + FTS parallel → FTS-primary merge → filter → graph rerank.
// FTS results are protected: graph supplements only appear after all FTS candidates.
func buildPrecision(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelinePrecision,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewGraphStage(deps.GraphStore, deps.FTSSearcher, graphOpts(deps)...),
				stage.NewFTSStage(deps.FTSSearcher, 30),
			}},
			{Stages: []pipeline.Stage{stage.NewFTSRewriteRetryStage(deps.FTSSearcher, deps.GraphTermExpander, deps.HyDEGenerator, deps.Cfg.FTSRewrite, deps.VectorStore, deps.Embedder, deps.GraphStore).WithTokenizer(deps.Tokenizer)}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(stage.MergeStrategyFTSPrimary, 0, 0, deps.Cfg.AccessAlpha)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.3)}},
			{Stages: []pipeline.Stage{stage.NewRerankGraphStage(deps.GraphStore, 0.6, 0.2)}},
		},
		Fallback: pipeline.PipelineExploration,
	}
}

// buildExploration 探索检索管线: parallel(graph, fts, temporal) → merge(graph_aware|rrf) → temporal_filter → score_filter(0.05) → rerank_overlap
// Exploration pipeline: graph + FTS + temporal parallel → adaptive merge → temporal_filter → filter → overlap rerank.
// Merge strategy is adaptive: GraphAware when graph source is active (cross-validation bonus for graph+FTS overlap),
// RRF otherwise (fair rank-based competition between remaining sources).
// VectorStage is intentionally excluded here — adding it would trigger an embedding call for every "what/how/where"
// query (nearly all queries) at ~265ms each with zero measured benefit on semantic-gap failures.
// graph stage 在 graphStore 为 nil 时自动跳过 / graph stage auto-skips when graphStore is nil
func buildExploration(deps Deps) *pipeline.Pipeline {
	mergeStrategy := stage.MergeStrategyRRF
	if deps.GraphStore != nil {
		mergeStrategy = stage.MergeStrategyGraphAware
	}

	return &pipeline.Pipeline{
		Name: pipeline.PipelineExploration,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewGraphStage(deps.GraphStore, deps.FTSSearcher, graphOpts(deps)...),
				stage.NewFTSStage(deps.FTSSearcher, 30),
				stage.NewTemporalStage(deps.Timeline, 30),
			}},
			{Stages: []pipeline.Stage{stage.NewFTSRewriteRetryStage(deps.FTSSearcher, deps.GraphTermExpander, deps.HyDEGenerator, deps.Cfg.FTSRewrite, deps.VectorStore, deps.Embedder, deps.GraphStore).WithTokenizer(deps.Tokenizer)}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(mergeStrategy, 0, 0, deps.Cfg.AccessAlpha)}},
			{Stages: []pipeline.Stage{stage.NewTemporalFilterStage(3)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.005)}},
			{Stages: []pipeline.Stage{stage.NewOverlapRerankStage(0, 0)}},
		},
		// 无 fallback — 终端降级管线 / No fallback — terminal fallback pipeline
	}
}

// buildSemantic 语义检索管线: parallel(vector, fts) → merge(rrf) → rerank_overlap
// Semantic pipeline: vector + FTS parallel → RRF merge → overlap rerank.
// No score filter: RRF scores top out at ~0.033 (structural_weight / k+1), so any absolute
// threshold designed for raw BM25/cosine scores (0-1 range) would incorrectly discard results.
// Quality gating is handled by the TrimStage/DisclosureStage downstream.
func buildSemantic(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineSemantic,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewVectorStage(deps.VectorStore, deps.Embedder, 30, 0.3).WithQueryInstruction(deps.VectorQueryInstruction),
				stage.NewFTSStage(deps.FTSSearcher, 30),
			}},
			{Stages: []pipeline.Stage{stage.NewFTSRewriteRetryStage(deps.FTSSearcher, deps.GraphTermExpander, deps.HyDEGenerator, deps.Cfg.FTSRewrite, deps.VectorStore, deps.Embedder, deps.GraphStore).WithTokenizer(deps.Tokenizer)}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(stage.MergeStrategyRRF, 0, 0, deps.Cfg.AccessAlpha)}},
			{Stages: []pipeline.Stage{stage.NewOverlapRerankStage(0, 0)}},
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
				stage.NewGraphStage(deps.GraphStore, deps.FTSSearcher, graphOpts(deps, stage.WithMaxDepth(3))...),
			}},
			{Stages: []pipeline.Stage{stage.NewRerankGraphStage(deps.GraphStore, 0.6, 0.2)}},
			{Stages: []pipeline.Stage{stage.NewFilterStage(0.2)}},
		},
		Fallback: pipeline.PipelinePrecision,
	}
}

// buildAggregation 聚合查询管线: 穷举实体召回 → trim
// Aggregation pipeline: exhaustive entity recall sorted by time. No merge/rerank.
func buildAggregation(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineAggregation,
		Stages: []pipeline.StageGroup{
			{Stages: []pipeline.Stage{
				stage.NewExhaustiveStage(deps.GraphStore, deps.Timeline, 0), // 0 = default max (200)
			}},
		},
		// No fallback: ExhaustiveStage returns gracefully when no entities found
	}
}

// buildFast 快速检索管线: fts(limit=10) → score_filter(0.05)
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

// buildFull 全量检索管线: parallel(graph, fts, vector) → merge(graph_aware) → rerank_overlap
// Full pipeline: graph + FTS + vector parallel → graph-aware merge → overlap rerank.
// No score filter: same reason as semantic — GraphAware RRF scores top out at ~0.049
// and a 0.3 threshold would silently discard all merged results.
func buildFull(deps Deps) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Name: pipeline.PipelineFull,
		Stages: []pipeline.StageGroup{
			{Parallel: true, Stages: []pipeline.Stage{
				stage.NewGraphStage(deps.GraphStore, deps.FTSSearcher, graphOpts(deps)...),
				stage.NewFTSStage(deps.FTSSearcher, 30),
				stage.NewVectorStage(deps.VectorStore, deps.Embedder, 30, 0.3).WithQueryInstruction(deps.VectorQueryInstruction),
			}},
			{Stages: []pipeline.Stage{stage.NewFTSRewriteRetryStage(deps.FTSSearcher, deps.GraphTermExpander, deps.HyDEGenerator, deps.Cfg.FTSRewrite, deps.VectorStore, deps.Embedder, deps.GraphStore).WithTokenizer(deps.Tokenizer)}},
			{Stages: []pipeline.Stage{stage.NewMergeStage(stage.MergeStrategyGraphAware, 0, 0, deps.Cfg.AccessAlpha)}},
			{Stages: []pipeline.Stage{stage.NewOverlapRerankStage(0, 0)}},
		},
		Fallback: pipeline.PipelinePrecision,
	}
}

// buildPostStages 构建共享后处理 stage 列表 / Build shared post-processing stages
func buildPostStages(deps Deps) []pipeline.Stage {
	stages := []pipeline.Stage{
		stage.NewCoreStage(deps.CoreProvider),
	}

	// MMR 多样性重排：仅 cfg.MMR.Enabled=true 时启用，避免未配置时意外激活影响召回率
	// MMR diversity reranking: only when explicitly enabled — prevents accidental activation
	// that degrades recall when VectorStore is present but MMR is not intentionally configured.
	if deps.Cfg.MMR.Enabled {
		stages = append([]pipeline.Stage{stage.NewMMRStage(deps.VectorStore, deps.Cfg.MMR.Lambda, 0)}, stages...)
	}

	// 渐进式披露替代简单裁剪 / Progressive disclosure replaces simple trim
	if deps.Cfg.Disclosure.Enabled {
		stages = append(stages, stage.NewDisclosureStage(deps.Cfg.Disclosure, 0))
	} else {
		stages = append(stages, stage.NewTrimStage(0))
	}

	return stages
}
