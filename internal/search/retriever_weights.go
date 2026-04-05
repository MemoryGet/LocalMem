// Package search 检索结果加权 / Search result weighting
package search

import (
	"strings"

	"iclude/internal/model"
)

// kindWeights 记忆类型权重 / Memory kind weights
var kindWeights = map[string]float64{
	"skill":   1.5,
	"profile": 1.2,
	"fact":    1.0,
	"note":    1.0,
}

// subKindWeights 子类型权重加成 / Sub-kind weight boost
var subKindWeights = map[string]float64{
	"pattern": 1.3,
	"case":    1.3,
}

// classWeights 记忆层级权重 / Memory class weights
var classWeights = map[string]float64{
	"procedural": 1.5,
	"semantic":   1.2,
	"episodic":   1.0,
}

// weightCap 最大权重上限，防止叠乘过度放大 / Max weight cap to prevent over-amplification
const weightCap = 2.0

// ApplyKindAndClassWeights 按 kind + memory_class 加权 / Weight results by kind and memory class
func ApplyKindAndClassWeights(results []*model.SearchResult) []*model.SearchResult {
	for _, r := range results {
		if r.Memory == nil {
			continue
		}
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
		r.Score *= w
	}
	return results
}

// scopePriorityBoost scope 优先级加成 / Scope priority boost factors
// session/* > project/* > user/*+core > user/*+semantic > other
var scopePriorityBoost = []struct {
	prefix string
	boost  float64
}{
	{"session/", 1.3},
	{"project/", 1.2},
	{"user/", 1.1},
	{"agent/", 1.0},
}

// ApplyScopePriority 按 scope + memory_class 组合加权 / Weight results by scope priority
// 设计来源：Universal Memory Layer 固定检索优先级
func ApplyScopePriority(results []*model.SearchResult) []*model.SearchResult {
	for _, r := range results {
		if r.Memory == nil || r.Memory.Scope == "" {
			continue
		}
		boost := 1.0
		for _, sp := range scopePriorityBoost {
			if strings.HasPrefix(r.Memory.Scope, sp.prefix) {
				boost = sp.boost
				break
			}
		}
		// core memory 在 user/ scope 下额外提权 / Extra boost for core class under user/ scope
		if strings.HasPrefix(r.Memory.Scope, "user/") && r.Memory.MemoryClass == "core" {
			boost *= 1.15
		}
		r.Score *= boost
	}
	return results
}
