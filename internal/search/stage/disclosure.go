package stage

import (
	"context"
	"time"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/pkg/tokenutil"
)

// defaultDisclosureBudget 默认披露 token 预算 / Default disclosure token budget
const defaultDisclosureBudget = 4096

// DisclosureStage 多管线渐进式披露 / Multi-pipeline progressive disclosure
type DisclosureStage struct {
	cfg       config.DisclosureConfig
	maxTokens int
}

// NewDisclosureStage 创建披露阶段 / Create disclosure stage
func NewDisclosureStage(cfg config.DisclosureConfig, maxTokens int) *DisclosureStage {
	if maxTokens <= 0 {
		maxTokens = defaultDisclosureBudget
	}
	return &DisclosureStage{cfg: cfg, maxTokens: maxTokens}
}

// Name 返回阶段名称 / Return stage name
func (s *DisclosureStage) Name() string { return "disclosure" }

// Execute 执行多管线渐进式披露 / Execute multi-pipeline progressive disclosure
func (s *DisclosureStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	if !s.cfg.Enabled || inputCount == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
		})
		return state, nil
	}

	// 获取策略权重 / Get strategy weights
	coreW, ctxW, entityW, timeW := s.cfg.WeightsForStrategy(state.PipelineName)

	totalBudget := s.maxTokens
	coreBudget := int(float64(totalBudget) * coreW)
	ctxBudget := int(float64(totalBudget) * ctxW)
	entityBudget := int(float64(totalBudget) * entityW)
	timeBudget := totalBudget - coreBudget - ctxBudget - entityBudget

	pipelines := []*model.DisclosurePipeline{
		{Name: "core", Weight: coreW, Budget: coreBudget},
		{Name: "context", Weight: ctxW, Budget: ctxBudget},
		{Name: "entity", Weight: entityW, Budget: entityBudget},
		{Name: "timeline", Weight: timeW, Budget: timeBudget},
	}

	var overflow []*model.DisclosureItem

	for _, c := range state.Candidates {
		if c.Memory == nil {
			continue
		}
		item := &model.DisclosureItem{
			Memory:   c.Memory,
			Score:    c.Score,
			Source:   c.Source,
			Entities: c.Entities,
		}

		placed := false
		for _, p := range pipelines {
			remaining := p.Budget - p.UsedTokens
			level, content, tokens := selectDetailLevel(c.Memory, remaining)
			if tokens > 0 {
				item.DetailLevel = level
				item.Content = content
				item.Tokens = tokens
				p.Items = append(p.Items, item)
				p.UsedTokens += tokens
				placed = true
				break
			}
		}

		if !placed {
			item.DetailLevel = "pointer"
			item.Content = c.Memory.Excerpt
			item.Tokens = 0
			overflow = append(overflow, item)
		}
	}

	totalUsed := 0
	for _, p := range pipelines {
		totalUsed += p.UsedTokens
	}

	discResult := &model.DisclosureResult{
		Pipelines:   pipelines,
		TotalBudget: totalBudget,
		TotalUsed:   totalUsed,
		Overflow:    overflow,
	}

	if state.Metadata == nil {
		state.Metadata = make(map[string]interface{})
	}
	state.Metadata["disclosure"] = discResult

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: inputCount - len(overflow),
	})

	return state, nil
}

// selectDetailLevel 根据剩余预算选择展示级别 / Select detail level by remaining budget
func selectDetailLevel(m *model.Memory, remaining int) (string, string, int) {
	if remaining <= 0 {
		return "", "", 0
	}

	// 优先完整内容 / Prefer full content
	fullTokens := tokenutil.EstimateTokens(m.Content)
	if fullTokens <= remaining {
		return "full", m.Content, fullTokens
	}

	// 其次摘要 / Then summary
	if m.Summary != "" {
		sumTokens := tokenutil.EstimateTokens(m.Summary)
		if sumTokens <= remaining {
			return "summary", m.Summary, sumTokens
		}
	}

	// 最后一句话摘要 / Then excerpt
	if m.Excerpt != "" {
		excTokens := tokenutil.EstimateTokens(m.Excerpt)
		if excTokens <= remaining {
			return "excerpt", m.Excerpt, excTokens
		}
	}

	return "", "", 0
}
