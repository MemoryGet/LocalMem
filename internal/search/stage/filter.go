package stage

import (
	"context"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// defaultMinScoreRatio 默认最低分数比例 / Default minimum score ratio
const defaultMinScoreRatio = 0.3

// FilterStage 分数比例过滤阶段 / Score ratio filter pipeline stage
type FilterStage struct {
	minScoreRatio float64
}

// NewFilterStage 创建分数过滤阶段 / Create a new score ratio filter stage
func NewFilterStage(minScoreRatio float64) *FilterStage {
	if minScoreRatio <= 0 {
		minScoreRatio = defaultMinScoreRatio
	}
	return &FilterStage{
		minScoreRatio: minScoreRatio,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *FilterStage) Name() string {
	return "filter"
}

// Execute 执行分数过滤 / Execute score ratio filtering
func (s *FilterStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	if len(state.Candidates) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        "filter",
			Duration:    time.Since(start),
			InputCount:  0,
			OutputCount: 0,
			Note:        "no candidates to filter",
		})
		return state, nil
	}

	// 假设候选已按分数降序排列 / Candidates assumed sorted by score descending
	topScore := state.Candidates[0].Score
	if topScore <= 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        "filter",
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "top score <= 0, skipping filter",
		})
		return state, nil
	}

	threshold := topScore * s.minScoreRatio
	filtered := make([]*model.SearchResult, 0, len(state.Candidates))
	for _, c := range state.Candidates {
		if c.Score >= threshold {
			filtered = append(filtered, c)
		}
	}

	state.Candidates = filtered

	state.AddTrace(pipeline.StageTrace{
		Name:        "filter",
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(filtered),
	})

	return state, nil
}
