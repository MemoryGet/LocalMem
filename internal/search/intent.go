package search

import (
	"context"
	"strings"

	"iclude/internal/logger"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// CascadeIntent 级联检索查询意图类型 / Cascade retrieval query intent type
type CascadeIntent string

const (
	CascadeIntentEntity     CascadeIntent = "entity"     // 实体相关 / Entity-related
	CascadeIntentTemporal   CascadeIntent = "temporal"   // 时间相关 / Time-related
	CascadeIntentConceptual CascadeIntent = "conceptual" // 概念/定义 / Conceptual/definitional
	CascadeIntentDefault    CascadeIntent = "default"    // 通用 / Default
)

// IntentMeta 意图分类附加信息 / Intent classification metadata
type IntentMeta struct {
	EntityHits   int      // 命中的实体数 / Number of entity hits
	EntityIDs    []string // 命中的实体 ID / Hit entity IDs
	TemporalHint bool     // 是否包含时间线索 / Contains temporal hint
	Keywords     []string // 提取的关键词 / Extracted keywords
}

// IntentClassifier 轻量意图分类器 / Lightweight intent classifier
type IntentClassifier struct {
	graphStore store.GraphStore
	tokenizer  tokenizer.Tokenizer
}

// NewIntentClassifier 创建意图分类器 / Create intent classifier
func NewIntentClassifier(graphStore store.GraphStore, tok tokenizer.Tokenizer) *IntentClassifier {
	return &IntentClassifier{
		graphStore: graphStore,
		tokenizer:  tok,
	}
}

// cascadeTemporalKeywords 级联检索时间相关关键词 / Cascade temporal keywords
var cascadeTemporalKeywords = []string{
	"最近", "上周", "昨天", "今天", "上个月", "这周", "前天",
	"recently", "yesterday", "today", "last week", "this week", "last month",
	"什么时候", "when", "多久", "何时",
}

// cascadeConceptualKeywords 级联检索概念相关模式 / Cascade conceptual patterns
var cascadeConceptualKeywords = []string{
	"什么是", "如何", "为什么", "怎么", "怎样",
	"what is", "how to", "why", "explain", "define",
}

// Classify 分类查询意图 / Classify query intent
func (c *IntentClassifier) Classify(ctx context.Context, query string) (CascadeIntent, *IntentMeta) {
	meta := &IntentMeta{}
	lower := strings.ToLower(query)

	// 1. 时间词检测 / Temporal pattern detection
	for _, p := range cascadeTemporalKeywords {
		if strings.Contains(lower, p) {
			meta.TemporalHint = true
			break
		}
	}

	// 2. 概念词检测 / Conceptual pattern detection
	isConceptual := false
	for _, p := range cascadeConceptualKeywords {
		if strings.Contains(lower, p) {
			isConceptual = true
			break
		}
	}

	// 3. 实体探测 / Entity probe
	if c.tokenizer != nil && c.graphStore != nil {
		tokenized, err := c.tokenizer.Tokenize(ctx, query)
		if err == nil {
			terms := strings.Fields(tokenized)
			seen := make(map[string]bool)
			for _, term := range terms {
				if len([]rune(term)) < 2 {
					continue
				}
				termLower := strings.ToLower(term)
				if seen[termLower] {
					continue
				}
				seen[termLower] = true
				meta.Keywords = append(meta.Keywords, term)

				entities, err := c.graphStore.FindEntitiesByName(ctx, term, "", 1)
				if err != nil {
					logger.Debug("intent: entity probe failed", zap.String("term", term), zap.Error(err))
					continue
				}
				if len(entities) > 0 {
					meta.EntityHits++
					meta.EntityIDs = append(meta.EntityIDs, entities[0].ID)
				}
			}
		}
	}

	// 4. 决策 / Decision
	// 时间 + 实体 → temporal（优先级最高）/ Temporal + entity → temporal (highest priority)
	if meta.TemporalHint && meta.EntityHits > 0 {
		return CascadeIntentTemporal, meta
	}
	// 纯时间 → temporal / Pure temporal
	if meta.TemporalHint {
		return CascadeIntentTemporal, meta
	}
	// 概念且无实体 → conceptual / Conceptual without entities
	if isConceptual && meta.EntityHits == 0 {
		return CascadeIntentConceptual, meta
	}
	// 有实体 → entity / Has entities
	if meta.EntityHits >= 1 {
		return CascadeIntentEntity, meta
	}
	// 概念（有实体也可能是概念）→ conceptual / Conceptual (may also have entities)
	if isConceptual {
		return CascadeIntentConceptual, meta
	}

	return CascadeIntentDefault, meta
}
