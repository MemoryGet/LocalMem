// Package eval 会话模拟评测 / Conversation simulation for eval
package eval

import (
	"context"
	"strings"
	"time"

	"iclude/internal/memory"
	"iclude/internal/model"
)

// ConvTurn 单个对话轮次 / Single conversation turn
type ConvTurn struct {
	Speaker   string
	Text      string
	Timestamp time.Time
}

// ConvSession 完整会话 / Complete conversation session
type ConvSession struct {
	ID    string
	Turns []ConvTurn
}

// RetentionDecision 保留决策 / Retention decision
type RetentionDecision struct {
	ShouldRetain bool
	Summary      string
	Keywords     []string
}

// MockAIRetainer 基于规则模拟 AI 判断（不调用 LLM）/ Rule-based AI retention simulation (no LLM)
type MockAIRetainer struct {
	MinContentRunes int
	SkipPhrases     []string
}

// NewMockAIRetainer 默认规则 / Default rules
func NewMockAIRetainer() *MockAIRetainer {
	return &MockAIRetainer{
		MinContentRunes: 20,
		SkipPhrases:     []string{"ok", "got it", "sure", "sounds good", "alright", "noted", "thanks"},
	}
}

// Decide 判断轮次是否值得保留 / Decide if a turn is worth retaining
func (r *MockAIRetainer) Decide(turn ConvTurn) RetentionDecision {
	text := strings.TrimSpace(turn.Text)
	lower := strings.ToLower(text)

	if len([]rune(text)) < r.MinContentRunes {
		return RetentionDecision{ShouldRetain: false}
	}
	for _, skip := range r.SkipPhrases {
		if lower == skip || lower == skip+"." || lower == skip+"!" {
			return RetentionDecision{ShouldRetain: false}
		}
	}
	return RetentionDecision{
		ShouldRetain: true,
		Summary:      r.buildSummary(turn),
		Keywords:     r.extractKeywords(text),
	}
}

// buildSummary 构造保留记忆的摘要（CJK 安全截断）/ Build retained-memory summary (CJK-safe truncation)
func (r *MockAIRetainer) buildSummary(turn ConvTurn) string {
	text := turn.Text
	if runes := []rune(text); len(runes) > 200 {
		text = string(runes[:200]) + "..."
	}
	return turn.Speaker + " stated: " + text
}

// extractKeywords 提取首字母大写的候选关键词（最多 5 个）/ Extract capitalized keyword candidates (max 5)
func (r *MockAIRetainer) extractKeywords(text string) []string {
	var kw []string
	for w := range strings.FieldsSeq(text) {
		w = strings.Trim(w, `.,!?"'`)
		if len(w) > 2 && w[0] >= 'A' && w[0] <= 'Z' {
			kw = append(kw, w)
		}
	}
	if len(kw) > 5 {
		kw = kw[:5]
	}
	return kw
}

// ConvSimResult 模拟运行结果 / Simulation run result
type ConvSimResult struct {
	TotalTurns    int
	RetainedTurns int
	SkippedTurns  int
	WithSummary   int
	WithDerived   int
}

// Retriever 最小检索接口（用于 recall）/ Minimal retriever interface for recall
type Retriever interface {
	Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error)
}

// RunConvSim 逐轮判断保留，先 recall 再 retain / Per-turn retention with recall-before-retain
func RunConvSim(ctx context.Context, session ConvSession, mgr *memory.Manager, retriever Retriever) (*ConvSimResult, error) {
	retainer := NewMockAIRetainer()
	result := &ConvSimResult{TotalTurns: len(session.Turns)}

	for _, turn := range session.Turns {
		decision := retainer.Decide(turn)
		if !decision.ShouldRetain {
			result.SkippedTurns++
			continue
		}

		var derivedFrom []string
		if len(decision.Keywords) > 0 && retriever != nil {
			query := strings.Join(decision.Keywords, " ")
			recalls, err := retriever.Retrieve(ctx, &model.RetrieveRequest{Query: query, Limit: 3})
			if err == nil {
				for _, rc := range recalls {
					derivedFrom = append(derivedFrom, rc.Memory.ID)
				}
			}
		}

		created, err := mgr.Create(ctx, &model.CreateMemoryRequest{
			Content:     turn.Text,
			Summary:     decision.Summary,
			Kind:        "episodic",
			SourceType:  "manual",
			DerivedFrom: derivedFrom,
			AutoExtract: false,
		})
		if err != nil {
			continue
		}

		result.RetainedTurns++
		if created.Summary != "" {
			result.WithSummary++
		}
		if len(derivedFrom) > 0 {
			result.WithDerived++
		}
	}

	return result, nil
}
