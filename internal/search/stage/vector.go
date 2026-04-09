package stage

import (
	"context"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// defaultMinVectorScore 最小向量相似度阈值 / Minimum cosine similarity threshold
const defaultMinVectorScore = 0.3

// defaultVectorLimit 向量检索默认返回数量 / Default result limit for vector search
const defaultVectorLimit = 30

// VectorStage 向量检索阶段 / Vector search pipeline stage
type VectorStage struct {
	searcher VectorSearcher
	embedder Embedder
	limit    int
	minScore float64
}

// NewVectorStage 创建向量检索阶段 / Create a new vector search stage
func NewVectorStage(searcher VectorSearcher, embedder Embedder, limit int, minScore float64) *VectorStage {
	if limit <= 0 {
		limit = defaultVectorLimit
	}
	if minScore <= 0 {
		minScore = defaultMinVectorScore
	}
	return &VectorStage{
		searcher: searcher,
		embedder: embedder,
		limit:    limit,
		minScore: minScore,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *VectorStage) Name() string {
	return "vector"
}

// Execute 执行向量检索 / Execute vector search stage
func (s *VectorStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// nil searcher → 跳过 / nil searcher → skip
	if s.searcher == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "searcher is nil",
		})
		return state, nil
	}

	// 解析 embedding / Resolve embedding
	embedding := s.resolveEmbedding(ctx, state)
	if len(embedding) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "no embedding available",
		})
		return state, nil
	}

	// 执行检索 / Execute search
	results, err := s.search(ctx, embedding, state)
	if err != nil {
		logger.Warn("vector stage search failed", zap.Error(err))
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: 0,
			Note:        "search error: " + err.Error(),
		})
		return state, nil
	}

	// 过滤低相似度结果 / Filter out low-similarity results
	filtered := make([]*model.SearchResult, 0, len(results))
	for _, r := range results {
		if r.Score >= s.minScore {
			filtered = append(filtered, r)
		}
	}

	// 追加结果 / Append results
	state.Candidates = append(state.Candidates, filtered...)

	return state, nil
}

// resolveEmbedding 从 Metadata 或 embedder 获取向量 / Resolve embedding from metadata or generate via embedder
func (s *VectorStage) resolveEmbedding(ctx context.Context, state *pipeline.PipelineState) []float32 {
	// 优先使用 state 中预置的 embedding / Prefer pre-set embedding from state
	if len(state.Embedding) > 0 {
		return state.Embedding
	}

	// 通过 embedder 生成 / Generate via embedder
	if s.embedder == nil || state.Query == "" {
		return nil
	}

	// 使用 semantic query（如有）/ Use semantic query if available
	query := state.Query
	if state.Plan != nil && state.Plan.SemanticQuery != "" {
		query = state.Plan.SemanticQuery
	}

	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		logger.Warn("vector: embedding generation failed", zap.Error(err))
		return nil
	}
	return emb
}

// search 根据是否有过滤条件选择检索方法 / Choose search method based on filters presence
func (s *VectorStage) search(ctx context.Context, embedding []float32, state *pipeline.PipelineState) ([]*model.SearchResult, error) {
	if state.Filters != nil {
		return s.searcher.SearchFiltered(ctx, embedding, state.Filters, s.limit)
	}
	return s.searcher.Search(ctx, embedding, state.Identity, s.limit)
}
