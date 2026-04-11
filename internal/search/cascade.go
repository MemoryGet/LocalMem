package search

import (
	"context"
	"fmt"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"

	"go.uber.org/zap"
)

// CascadeRetriever 降级链检索器 / Cascade retriever with intent-driven degradation
type CascadeRetriever struct {
	classifier *IntentClassifier
	graphStage *stage.GraphStage
	ftsStage   *stage.FTSStage
	vecStage   *stage.VectorStage   // 可 nil / may be nil
	tempStage  *stage.TemporalStage // 可 nil / may be nil
	postStages []pipeline.Stage     // 后处理阶段 / Post-processing stages
	cfg        config.CascadeConfig
}

// NewCascadeRetriever 创建降级链检索器 / Create cascade retriever
func NewCascadeRetriever(
	classifier *IntentClassifier,
	graphStage *stage.GraphStage,
	ftsStage *stage.FTSStage,
	vecStage *stage.VectorStage,
	tempStage *stage.TemporalStage,
	postStages []pipeline.Stage,
	cfg config.CascadeConfig,
) *CascadeRetriever {
	return &CascadeRetriever{
		classifier: classifier,
		graphStage: graphStage,
		ftsStage:   ftsStage,
		vecStage:   vecStage,
		tempStage:  tempStage,
		postStages: postStages,
		cfg:        cfg,
	}
}

// Retrieve 执行降级链检索 / Execute cascade retrieval
func (c *CascadeRetriever) Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	if req.Query == "" {
		return nil, fmt.Errorf("query is required: %w", model.ErrInvalidInput)
	}

	// 1. 意图分类 / Intent classification
	intent, meta := c.classifier.Classify(ctx, req.Query)
	logger.Debug("cascade: intent classified",
		zap.String("intent", string(intent)),
		zap.Int("entity_hits", meta.EntityHits),
		zap.Bool("temporal", meta.TemporalHint),
	)

	// 2. 构建初始状态 / Build initial state
	identity := &model.Identity{TeamID: req.TeamID, OwnerID: req.OwnerID}
	if identity.OwnerID == "" {
		identity.OwnerID = model.SystemOwnerID
	}
	state := pipeline.NewState(req.Query, identity)
	state.Filters = req.Filters
	if len(req.Embedding) > 0 {
		state.Embedding = req.Embedding
	}
	// 将实体探测结果写入 Plan，供 GraphStage 使用 / Set entity probe results for GraphStage
	if len(meta.EntityIDs) > 0 || len(meta.Keywords) > 0 {
		state.Plan = &pipeline.QueryPlan{
			OriginalQuery: req.Query,
			Keywords:      meta.Keywords,
			Entities:      meta.EntityIDs,
			Temporal:      meta.TemporalHint,
		}
	}

	// 3. 按意图走降级链 / Execute degradation chain by intent
	switch intent {
	case CascadeIntentEntity:
		c.cascadeEntity(ctx, state)
	case CascadeIntentTemporal:
		c.cascadeTemporal(ctx, state)
	case CascadeIntentConceptual:
		c.cascadeConceptual(ctx, state)
	default:
		c.cascadeDefault(ctx, state)
	}

	// 4. 后处理 / Post-processing
	for _, ps := range c.postStages {
		var err error
		state, err = ps.Execute(ctx, state)
		if err != nil {
			logger.Warn("cascade: post-stage failed", zap.String("stage", ps.Name()), zap.Error(err))
		}
	}

	return state.Candidates, nil
}

// cascadeEntity 实体意图降级链: Graph → +FTS → +Vector
func (c *CascadeRetriever) cascadeEntity(ctx context.Context, state *pipeline.PipelineState) {
	// L1: Graph
	if c.graphStage != nil {
		c.execStage(ctx, "L1:graph", c.graphStage, state)
		if c.sufficient(state, c.cfg.GraphMinResults, c.cfg.GraphMinScore) {
			logger.Debug("cascade: entity L1 sufficient", zap.Int("results", len(state.Candidates)))
			return
		}
	}

	// L2: +FTS
	if c.ftsStage != nil {
		c.execStage(ctx, "L2:fts", c.ftsStage, state)
		if c.sufficient(state, c.cfg.L2MinResults, 0) {
			logger.Debug("cascade: entity L2 sufficient", zap.Int("results", len(state.Candidates)))
			return
		}
	}

	// L3: +Vector (if available)
	if c.vecStage != nil {
		c.execStage(ctx, "L3:vector", c.vecStage, state)
	}
}

// cascadeTemporal 时间意图降级链: Temporal+Graph → +FTS → +Vector
func (c *CascadeRetriever) cascadeTemporal(ctx context.Context, state *pipeline.PipelineState) {
	// L1: Temporal + Graph
	if c.tempStage != nil {
		c.execStage(ctx, "L1:temporal", c.tempStage, state)
	}
	if c.graphStage != nil {
		c.execStage(ctx, "L1:graph", c.graphStage, state)
	}
	if c.sufficient(state, c.cfg.GraphMinResults, c.cfg.GraphMinScore) {
		logger.Debug("cascade: temporal L1 sufficient", zap.Int("results", len(state.Candidates)))
		return
	}

	// L2: +FTS
	if c.ftsStage != nil {
		c.execStage(ctx, "L2:fts", c.ftsStage, state)
		if c.sufficient(state, c.cfg.L2MinResults, 0) {
			return
		}
	}

	// L3: +Vector
	if c.vecStage != nil {
		c.execStage(ctx, "L3:vector", c.vecStage, state)
	}
}

// cascadeConceptual 概念意图降级链: FTS+Vector → +Graph
func (c *CascadeRetriever) cascadeConceptual(ctx context.Context, state *pipeline.PipelineState) {
	// L1: FTS + Vector（概念题图谱帮助小）/ Graph is less useful for conceptual queries
	if c.ftsStage != nil {
		c.execStage(ctx, "L1:fts", c.ftsStage, state)
	}
	if c.vecStage != nil {
		c.execStage(ctx, "L1:vector", c.vecStage, state)
	}
	if c.sufficient(state, c.cfg.GraphMinResults, c.cfg.GraphMinScore) {
		logger.Debug("cascade: conceptual L1 sufficient", zap.Int("results", len(state.Candidates)))
		return
	}

	// L2: +Graph
	if c.graphStage != nil {
		c.execStage(ctx, "L2:graph", c.graphStage, state)
	}
}

// cascadeDefault 默认降级链: Graph → +FTS+Vector
func (c *CascadeRetriever) cascadeDefault(ctx context.Context, state *pipeline.PipelineState) {
	// L1: Graph
	if c.graphStage != nil {
		c.execStage(ctx, "L1:graph", c.graphStage, state)
		if c.sufficient(state, c.cfg.GraphMinResults, c.cfg.GraphMinScore) {
			logger.Debug("cascade: default L1 sufficient", zap.Int("results", len(state.Candidates)))
			return
		}
	}

	// L2: +FTS + Vector
	if c.ftsStage != nil {
		c.execStage(ctx, "L2:fts", c.ftsStage, state)
	}
	if c.vecStage != nil {
		c.execStage(ctx, "L2:vector", c.vecStage, state)
	}
}

// execStage 执行单个阶段并记录 / Execute a single stage with logging
func (c *CascadeRetriever) execStage(ctx context.Context, label string, s pipeline.Stage, state *pipeline.PipelineState) {
	before := len(state.Candidates)
	if _, err := s.Execute(ctx, state); err != nil {
		logger.Warn("cascade: stage failed", zap.String("label", label), zap.Error(err))
	}
	after := len(state.Candidates)
	logger.Debug("cascade: stage completed",
		zap.String("label", label),
		zap.Int("added", after-before),
		zap.Int("total", after),
	)
}

// sufficient 检查结果是否够用 / Check if results are sufficient
func (c *CascadeRetriever) sufficient(state *pipeline.PipelineState, minResults int, minScore float64) bool {
	if len(state.Candidates) < minResults {
		return false
	}
	if minScore > 0 && len(state.Candidates) > 0 && state.Candidates[0].Score < minScore {
		return false
	}
	return true
}
