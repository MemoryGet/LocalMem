package pipeline

// 内置管线名称常量 / Built-in pipeline name constants
const (
	PipelinePrecision   = "precision"
	PipelineExploration = "exploration"
	PipelineSemantic    = "semantic"
	PipelineAssociation = "association"
	PipelineFast        = "fast"
	PipelineFull        = "full"
)

// Metadata key 常量 / Metadata key constants
const (
	// MetaForceRerank full 管线强制 LLM rerank / Force LLM rerank in full pipeline
	MetaForceRerank = "force_llm_rerank"
)
