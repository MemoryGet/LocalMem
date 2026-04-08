package stage

import (
	"context"
	"math"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/pkg/mathutil"

	"go.uber.org/zap"
)

// defaultMMRLambda 默认 MMR lambda 值 / Default MMR lambda value
const defaultMMRLambda = 0.7

// defaultMMRLimit 默认 MMR 返回数量 / Default MMR result limit
const defaultMMRLimit = 10

// MMRStage 最大边际相关性多样化阶段 / Maximal Marginal Relevance diversity stage
type MMRStage struct {
	vecSearcher VectorSearcher
	lambda      float64
	limit       int
}

// NewMMRStage 创建 MMR 多样化阶段 / Create a new MMR diversity stage
func NewMMRStage(vecSearcher VectorSearcher, lambda float64, limit int) *MMRStage {
	if lambda <= 0 {
		lambda = defaultMMRLambda
	}
	if limit <= 0 {
		limit = defaultMMRLimit
	}
	return &MMRStage{
		vecSearcher: vecSearcher,
		lambda:      lambda,
		limit:       limit,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *MMRStage) Name() string {
	return "mmr"
}

// Execute 执行 MMR 多样化重排 / Execute MMR diversity re-ranking
func (s *MMRStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// nil vecSearcher → 跳过 / nil vecSearcher → skip
	if s.vecSearcher == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    "mmr",
			Skipped: true,
			Note:    "vecSearcher is nil",
		})
		return state, nil
	}

	if len(state.Candidates) <= 1 {
		state.AddTrace(pipeline.StageTrace{
			Name:        "mmr",
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "insufficient candidates",
		})
		return state, nil
	}

	// 收集 ID / Collect memory IDs
	ids := make([]string, 0, len(state.Candidates))
	for _, r := range state.Candidates {
		if r.Memory != nil && r.Memory.ID != "" {
			ids = append(ids, r.Memory.ID)
		}
	}

	// 获取向量 / Get vectors
	vectors, err := s.vecSearcher.GetVectors(ctx, ids)
	if err != nil {
		logger.Warn("mmr: failed to get vectors, skipping", zap.Error(err))
		state.AddTrace(pipeline.StageTrace{
			Name:        "mmr",
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "get vectors error: " + err.Error(),
		})
		return state, nil
	}
	if len(vectors) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        "mmr",
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "no vectors available",
		})
		return state, nil
	}

	// 归一化 RRF 分数到 [0,1] / Normalize scores to [0,1]
	maxScore := state.Candidates[0].Score
	if maxScore <= 0 {
		maxScore = 1.0
	}

	topK := s.limit
	if topK > len(state.Candidates) {
		topK = len(state.Candidates)
	}

	// 贪心迭代选择 / Greedy iterative selection
	selected := make([]*model.SearchResult, 0, topK)
	remaining := make(map[int]bool, len(state.Candidates))
	for i := range state.Candidates {
		remaining[i] = true
	}

	// 第一个永远选最高分 / Always select the top-scoring first
	selected = append(selected, state.Candidates[0])
	delete(remaining, 0)

	for len(selected) < topK && len(remaining) > 0 {
		bestScore := math.Inf(-1)
		bestIdx := -1

		for i := range remaining {
			normScore := state.Candidates[i].Score / maxScore
			vec := vectors[state.Candidates[i].Memory.ID]
			if len(vec) == 0 {
				// 没有向量的结果只考虑相关性 / Results without vectors only consider relevance
				mmrScore := s.lambda * normScore
				if mmrScore > bestScore {
					bestScore = mmrScore
					bestIdx = i
				}
				continue
			}

			// 计算与已选集合的最大相似度 / Calculate max similarity to selected set
			maxSim := 0.0
			for _, sel := range selected {
				sv := vectors[sel.Memory.ID]
				if len(sv) == 0 {
					continue
				}
				sim := mathutil.CosineSimilarity(vec, sv)
				if sim > maxSim {
					maxSim = sim
				}
			}

			mmrScore := s.lambda*normScore - (1-s.lambda)*maxSim
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}
		selected = append(selected, state.Candidates[bestIdx])
		delete(remaining, bestIdx)
	}

	state.Candidates = selected

	state.AddTrace(pipeline.StageTrace{
		Name:        "mmr",
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(selected),
	})

	return state, nil
}
