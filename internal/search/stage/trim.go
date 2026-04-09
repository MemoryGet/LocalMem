package stage

import (
	"context"
	"time"

	"iclude/internal/search/pipeline"
	"iclude/pkg/tokenutil"
)

// defaultMaxTokens 默认最大 token 数 / Default max token budget
const defaultMaxTokens = 4096

// TrimStage Token 预算裁剪阶段 / Token budget trim pipeline stage
type TrimStage struct {
	maxTokens int
}

// NewTrimStage 创建 token 裁剪阶段 / Create a new token budget trim stage
func NewTrimStage(maxTokens int) *TrimStage {
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	return &TrimStage{
		maxTokens: maxTokens,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *TrimStage) Name() string {
	return "trim"
}

// Execute 执行 token 预算裁剪 / Execute token budget trimming
func (s *TrimStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()

	if len(state.Candidates) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  0,
			OutputCount: 0,
		})
		return state, nil
	}

	totalTokens := 0
	trimIdx := len(state.Candidates)

	for i, r := range state.Candidates {
		tokens := 0
		if r.Memory != nil {
			tokens = tokenutil.EstimateTokens(r.Memory.Content)
		}
		if totalTokens+tokens > s.maxTokens && i > 0 {
			trimIdx = i
			break
		}
		totalTokens += tokens
	}

	state.Candidates = state.Candidates[:trimIdx]

	return state, nil
}
