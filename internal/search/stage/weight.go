package stage

import (
	"context"
	"sort"
	"strings"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/pkg/scoring"
)

// defaultAccessAlpha 默认访问频率阻尼系数 / Default access frequency damping coefficient
const defaultAccessAlpha = 0.1

// minEffectiveStrength 最低有效强度下限 / Minimum effective strength floor
const minEffectiveStrength = 0.05

// weightCap 最大权重上限 / Max weight cap to prevent over-amplification
const weightCap = 2.0

// kindWeights 记忆类型权重 / Memory kind weights
var kindWeights = map[string]float64{
	"skill":        1.5,
	"rule":         1.4,
	"mental_model": 1.3,
	"preference":   1.2,
	"goal":         1.2,
	"note":         1.0,
	"event":        0.9,
	"error":        0.8,
}

// subKindWeights 子类型权重加成 / Sub-kind weight boost
var subKindWeights = map[string]float64{
	"core_belief":    1.4,
	"working_memory": 0.7,
}

// classWeights 记忆层级权重 / Memory class weights
var classWeights = map[string]float64{
	"procedural": 1.5,
	"semantic":   1.2,
	"core":       1.4,
	"episodic":   1.0,
}

// scopePriorityBoost scope 优先级加成 / Scope priority boost factors (keyed by prefix without trailing slash)
var scopePriorityBoost = map[string]float64{
	"session": 1.3,
	"project": 1.1,
	"user":    1.0,
	"agent":   1.0,
	"global":  0.9,
}

// WeightStage 综合加权阶段 / Combined weight pipeline stage
// 合并 kind/class、scope、strength 四种加权 / Combines kind/class, scope, and strength weighting
type WeightStage struct {
	accessAlpha float64
}

// NewWeightStage 创建综合加权阶段 / Create a new weight stage
func NewWeightStage(accessAlpha float64) *WeightStage {
	if accessAlpha <= 0 {
		accessAlpha = defaultAccessAlpha
	}
	return &WeightStage{
		accessAlpha: accessAlpha,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *WeightStage) Name() string {
	return "weight"
}

// Execute 执行综合加权 / Execute combined weighting
func (s *WeightStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
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

	now := time.Now().UTC()
	weighted := make([]*model.SearchResult, 0, len(state.Candidates))

	for _, r := range state.Candidates {
		if r == nil || r.Memory == nil {
			continue
		}

		// 1. Kind + Class 加权 / Kind + Class weighting
		kw := s.applyKindAndClassWeight(r)

		// 2. Scope 优先级 / Scope priority
		sw := s.applyScopePriority(r)

		// 3. Strength 衰减 + 访问加成 / Strength decay + access boost
		// 过滤已过期 / Filter expired
		if r.Memory.ExpiresAt != nil && r.Memory.ExpiresAt.Before(now) {
			continue
		}
		effective := scoring.CalculateEffectiveStrength(
			r.Memory.Strength, r.Memory.DecayRate, r.Memory.LastAccessedAt,
			r.Memory.RetentionTier, r.Memory.AccessCount, s.accessAlpha,
		)
		if effective < minEffectiveStrength {
			effective = minEffectiveStrength
		}

		r.Score *= kw * sw * effective
		weighted = append(weighted, r)
	}

	// 重排序 / Re-sort by score
	sort.Slice(weighted, func(i, j int) bool {
		return weighted[i].Score > weighted[j].Score
	})

	state.Candidates = weighted

	return state, nil
}

// applyKindAndClassWeight 计算 kind + class 权重 / Calculate kind + class weight
func (s *WeightStage) applyKindAndClassWeight(r *model.SearchResult) float64 {
	w := 1.0
	if kw, ok := kindWeights[r.Memory.Kind]; ok {
		w = kw
	}
	if sw, ok := subKindWeights[r.Memory.SubKind]; ok {
		w *= sw
	}
	if cw, ok := classWeights[r.Memory.MemoryClass]; ok {
		w *= cw
	}
	if w > weightCap {
		w = weightCap
	}
	return w
}

// applyScopePriority 计算 scope 优先级 / Calculate scope priority boost
func (s *WeightStage) applyScopePriority(r *model.SearchResult) float64 {
	if r.Memory.Scope == "" {
		return 1.0
	}
	boost := 1.0
	prefix := strings.SplitN(r.Memory.Scope, "/", 2)[0]
	if b, ok := scopePriorityBoost[prefix]; ok {
		boost = b
	}
	// core memory 在 user/ scope 下额外提权 / Extra boost for core class under user/ scope
	if strings.HasPrefix(r.Memory.Scope, "user/") && r.Memory.MemoryClass == "core" {
		boost *= 1.15
	}
	return boost
}

