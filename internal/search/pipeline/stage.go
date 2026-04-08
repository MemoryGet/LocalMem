package pipeline

import (
	"context"
	"time"

	"iclude/internal/model"
)

// QueryPlan 管线内部查询计划（从 search.QueryPlan 映射，避免循环依赖）
// Pipeline-internal query plan (mapped from search.QueryPlan to avoid circular import)
type QueryPlan struct {
	OriginalQuery  string
	SemanticQuery  string
	Keywords       []string
	Entities       []string
	Intent         string
	Temporal       bool
	TemporalCenter *time.Time
	TemporalRange  time.Duration
	HyDEDoc        string
}

// Stage 检索管线阶段接口 / Pipeline stage interface
type Stage interface {
	Name() string
	Execute(ctx context.Context, state *PipelineState) (*PipelineState, error)
}

// PipelineState 在 Stage 之间流转的状态 / State flowing between stages
type PipelineState struct {
	Query        string
	Identity     *model.Identity
	Plan         *QueryPlan
	Candidates   []*model.SearchResult
	Confidence   string                 // "high" | "low" | "none" | ""
	Metadata     map[string]interface{}
	Traces       []StageTrace
	PipelineName string
}

// StageTrace 单个 stage 的执行记录 / Execution trace for a single stage
type StageTrace struct {
	Name        string        `json:"name"`
	Duration    time.Duration `json:"duration"`
	InputCount  int           `json:"in"`
	OutputCount int           `json:"out"`
	Skipped     bool          `json:"skipped,omitempty"`
	Note        string        `json:"note,omitempty"`
}

// NewState 创建初始状态 / Create initial pipeline state
func NewState(query string, identity *model.Identity) *PipelineState {
	return &PipelineState{
		Query:    query,
		Identity: identity,
		Metadata: make(map[string]interface{}),
	}
}

// AddTrace 追加 stage 执行记录 / Append stage trace
func (s *PipelineState) AddTrace(t StageTrace) {
	s.Traces = append(s.Traces, t)
}

// Clone 浅拷贝状态（用于降级链，保留原始查询）/ Shallow-clone state for fallback chain
func (s *PipelineState) Clone() *PipelineState {
	c := *s
	c.Candidates = nil
	c.Traces = append([]StageTrace(nil), s.Traces...)
	c.Metadata = make(map[string]interface{})
	for k, v := range s.Metadata {
		c.Metadata[k] = v
	}
	return &c
}
