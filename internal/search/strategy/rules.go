// Package strategy 管线选择策略 / Pipeline selection strategies
package strategy

import (
	"regexp"

	"iclude/internal/search/pipeline"
)

// 时间关键词（复制自 preprocess.go，避免耦合）/ Temporal keywords (copied from preprocess.go to avoid coupling)
var temporalPatterns = regexp.MustCompile(`(?i)\b(recent|latest|last\s+week|last\s+month|last\s+quarter|yesterday|today|this\s+week|this\s+month|past\s+few\s+days)\b|最近|上周|上月|前天|昨天|今天|本周|本月|这几天|之前`)

// 关联关键词（复制自 preprocess.go，避免耦合）/ Relational keywords (copied from preprocess.go to avoid coupling)
var relationalPatterns = regexp.MustCompile(`(?i)\b(related\s+to|associated\s+with|connected\s+to|regarding|depends\s+on|dependencies\s+of)\b|相关|关于|有关|关联|之间|依赖`)

// 探索性关键词（复制自 preprocess.go，避免耦合）/ Exploratory keywords (copied from preprocess.go to avoid coupling)
var exploratoryPatterns = regexp.MustCompile(`(?i)\b(how|why|what|when|where|which|explain|describe|summarize|overview)\b|怎么|为什么|什么|如何|谁|哪里|解释|描述|总结|概述|哪些`)

// 聚合关键词（需要跨多条记忆做计算的模式，刻意保守以避免误路由）
// Aggregation keywords (patterns requiring computation across memories — kept conservative to avoid misrouting)
// Excluded: "how many/much/often/long" alone — too ambiguous; they match point-retrieval and temporal queries.
var aggregationPatterns = regexp.MustCompile(`(?i)\b(total|average|sum\s+of|in\s+total|altogether|across\s+all|how\s+much\s+total)\b|一共|总共|总计|多少(个|次|钱|天)|平均|合计|累计|所有.*加起来`)

// 历史列举模式：时间作用域词（非具体锚点）+ 列举意图 → aggregation
// Historical listing: general past-scope word + listing intent → aggregation
// Matches: "之前我都做了哪些事情", "以前都做了什么", "过去做过哪些任务"
// Does NOT match specific anchors like "上周我都做了哪些事情" (上周 not in scope group)
var historicalListPatterns = regexp.MustCompile(`(之前|以前|历史上|过去).*(都|哪些|所有)`)

// intentToPipeline 意图→管线映射 / Intent to pipeline mapping
var intentToPipeline = map[string]string{
	"keyword":      pipeline.PipelinePrecision,
	"semantic":     pipeline.PipelineSemantic,
	"temporal":     pipeline.PipelineExploration,
	"relational":   pipeline.PipelineAssociation,
	"aggregation":  pipeline.PipelineAggregation,
}

// shortQueryThreshold 短查询阈值（rune 数）/ Short query threshold in runes
const shortQueryThreshold = 5

// RuleClassifier 规则分类器（无 LLM 时的 fallback）/ Rule-based pipeline classifier (no-LLM fallback)
type RuleClassifier struct {
	fallbackPipeline string
}

// NewRuleClassifier 创建规则分类器 / Create rule classifier
func NewRuleClassifier(fallbackPipeline string) *RuleClassifier {
	if fallbackPipeline == "" {
		fallbackPipeline = pipeline.PipelineExploration
	}
	return &RuleClassifier{fallbackPipeline: fallbackPipeline}
}

// Select 根据查询和意图选择管线 / Select pipeline based on query and intent
//
// Selection rules (in priority order):
//  1. Query length < 5 runes → "fast"
//  1.5. Historical listing (general past scope + listing intent) → "aggregation"
//  2. Temporal patterns match → "exploration"
//  2.5. Aggregation patterns match (only when no temporal anchor) → "aggregation"
//  3. Relational patterns match → "association"
//  4. Intent-based mapping (keyword/semantic/temporal/relational/aggregation)
//  5. Exploratory patterns match (how/why/what) → "exploration"
//  6. Default → fallbackPipeline
func (c *RuleClassifier) Select(query string, intent string) string {
	runes := []rune(query)
	hasExplicitIntent := intent != "" && intent != "general"

	// 规則 1: 短查询（无显式意图时）→ fast / Rule 1: short query (no explicit intent) → fast
	if !hasExplicitIntent && len(runes) > 0 && len(runes) < shortQueryThreshold {
		return pipeline.PipelineFast
	}

	// 规则 1.5: 历史列举（时间作用域词 + 列举意图）→ aggregation
	// Rule 1.5: historical listing (general past scope + listing intent) → aggregation
	// Checked before temporal so "之前我都做了哪些事情" reaches aggregation instead of exploration.
	if historicalListPatterns.MatchString(query) {
		return pipeline.PipelineAggregation
	}

	// 规则 2: 时间模式 → exploration / Rule 2: temporal patterns → exploration
	if temporalPatterns.MatchString(query) {
		return pipeline.PipelineExploration
	}

	// 规则 2.5: 聚合模式（无时间锚点时）→ aggregation / Rule 2.5: aggregation patterns (only when no temporal anchor) → aggregation
	if aggregationPatterns.MatchString(query) && !temporalPatterns.MatchString(query) {
		return pipeline.PipelineAggregation
	}

	// 规则 3: 关联模式 → association / Rule 3: relational patterns → association
	if relationalPatterns.MatchString(query) {
		return pipeline.PipelineAssociation
	}

	// 规则 4: 意图映射 / Rule 4: intent-based mapping
	if hasExplicitIntent {
		if pipeline, ok := intentToPipeline[intent]; ok {
			return pipeline
		}
	}

	// 规则 1b: 短查询兜底 → fast / Rule 1b: short query fallback → fast
	if len(runes) > 0 && len(runes) < shortQueryThreshold {
		return pipeline.PipelineFast
	}

	// 规则 5: 探索性模式 → exploration / Rule 5: exploratory patterns → exploration
	if exploratoryPatterns.MatchString(query) {
		return pipeline.PipelineExploration
	}

	// 规则 6: 默认回退 / Rule 6: default fallback
	return c.fallbackPipeline
}
