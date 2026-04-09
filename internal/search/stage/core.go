package stage

import (
	"context"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// coreFixedScore core 记忆固定高分 / Fixed high score for core memories
const coreFixedScore = 2.0

// CoreStage 核心记忆注入阶段 / Core memory injection pipeline stage
type CoreStage struct {
	provider CoreProvider
}

// NewCoreStage 创建核心记忆注入阶段 / Create a new core memory injection stage
func NewCoreStage(provider CoreProvider) *CoreStage {
	return &CoreStage{
		provider: provider,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *CoreStage) Name() string {
	return "core"
}

// Execute 执行核心记忆注入 / Execute core memory injection
func (s *CoreStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	// nil provider → 跳过 / nil provider → skip
	if s.provider == nil {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "provider is nil",
		})
		return state, nil
	}

	// 构建要查询的 scope 列表 / Build scope list to query
	scopes := s.resolveScopes(state)
	if len(scopes) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "no scopes resolved",
		})
		return state, nil
	}

	coreBlocks, err := s.provider.GetCoreBlocksMultiScope(ctx, scopes, state.Identity)
	if err != nil {
		logger.Debug("core injection failed, skipping", zap.Error(err))
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "provider error: " + err.Error(),
		})
		return state, nil
	}
	if len(coreBlocks) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "no core blocks found",
		})
		return state, nil
	}

	// 去重：排除已在检索结果中的 core 记忆 / Deduplicate: skip core blocks already in results
	existingIDs := make(map[string]bool, len(state.Candidates))
	for _, res := range state.Candidates {
		if res.Memory != nil {
			existingIDs[res.Memory.ID] = true
		}
	}

	var injected []*model.SearchResult
	for _, m := range coreBlocks {
		if existingIDs[m.ID] {
			continue
		}
		injected = append(injected, &model.SearchResult{
			Memory: m,
			Score:  coreFixedScore,
			Source: SourceCore,
		})
	}

	if len(injected) > 0 {
		logger.Debug("core memories injected",
			zap.Int("count", len(injected)),
			zap.Int("scopes", len(scopes)),
		)
	}

	// core 置顶 + 原结果 / Core first + original results
	state.Candidates = append(injected, state.Candidates...)

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(state.Candidates),
	})

	return state, nil
}

// resolveScopes 从 state 中解析 scope 列表 / Resolve scope list from state
func (s *CoreStage) resolveScopes(state *pipeline.PipelineState) []string {
	var scopes []string

	// 从 filters 提取 scope / Extract scope from filters
	if state.Filters != nil && state.Filters.Scope != "" {
		scopes = append(scopes, state.Filters.Scope)
	}

	// 始终包含 user/ scope / Always include user scope
	if state.Identity != nil && state.Identity.OwnerID != "" {
		userScope := "user/" + state.Identity.OwnerID
		found := false
		for _, s := range scopes {
			if s == userScope {
				found = true
				break
			}
		}
		if !found {
			scopes = append(scopes, userScope)
		}
	}

	return scopes
}
