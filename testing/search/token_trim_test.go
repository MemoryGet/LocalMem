// Package search_test Token 估算与裁剪测试 / Token estimation and trimming tests
package search_test

import (
	"testing"

	"iclude/internal/model"
	"iclude/internal/search"

	"github.com/stretchr/testify/assert"
)

func TestEstimateTokens_Chinese(t *testing.T) {
	assert.Equal(t, 4, search.EstimateTokens("你好世界"))
}

func TestEstimateTokens_English(t *testing.T) {
	// "hello world" = 2 words × 1.3 ≈ 3 tokens（混合估算比纯 rune 更准）
	assert.Equal(t, 3, search.EstimateTokens("hello world"))
}

func TestEstimateTokens_Mixed(t *testing.T) {
	// "Hello你好" = 1 英文词(≈1 token) + 2 CJK(2 tokens) = 3 tokens
	assert.Equal(t, 3, search.EstimateTokens("Hello你好"))
}

func TestEstimateTokens_Empty(t *testing.T) {
	assert.Equal(t, 0, search.EstimateTokens(""))
}

func makeTrimResult(id string, content string) *model.SearchResult {
	return &model.SearchResult{
		Memory: &model.Memory{ID: id, Content: content},
		Score:  1.0,
		Source: "sqlite",
	}
}

func TestTrimByTokenBudget_NoTrim(t *testing.T) {
	results := []*model.SearchResult{
		makeTrimResult("a", "短文"),  // 2 tokens
		makeTrimResult("b", "也很短"), // 3 tokens
	}
	trimmed, total, truncated := search.TrimByTokenBudget(results, 100)
	assert.Len(t, trimmed, 2)
	assert.Equal(t, 5, total)
	assert.False(t, truncated)
}

func TestTrimByTokenBudget_Trim(t *testing.T) {
	results := []*model.SearchResult{
		makeTrimResult("a", "短文"),        // 2 tokens
		makeTrimResult("b", "这是一段较长的文本"), // 8 tokens
		makeTrimResult("c", "第三条"),       // 3 tokens
	}
	trimmed, total, truncated := search.TrimByTokenBudget(results, 5)
	assert.Len(t, trimmed, 1) // 只有 "a"（2 tokens），"b" 加上去会超过 5
	assert.Equal(t, 2, total)
	assert.True(t, truncated)
}

func TestTrimByTokenBudget_ZeroBudget(t *testing.T) {
	results := []*model.SearchResult{
		makeTrimResult("a", "内容"),
		makeTrimResult("b", "更多内容"),
	}
	trimmed, total, truncated := search.TrimByTokenBudget(results, 0)
	assert.Len(t, trimmed, 2) // 不裁剪
	assert.Equal(t, 6, total) // 2 + 4
	assert.False(t, truncated)
}

func TestTrimByTokenBudget_SingleResultExceedsBudget(t *testing.T) {
	results := []*model.SearchResult{
		makeTrimResult("a", "这是一段超过预算的很长的文本内容"), // 16 runes > budget 5
	}
	trimmed, total, truncated := search.TrimByTokenBudget(results, 5)
	assert.Len(t, trimmed, 1) // 至少返回 1 条
	assert.Equal(t, 16, total)
	assert.False(t, truncated) // 只有一条，不算"截断"
}

func TestTrimByTokenBudget_EmptyResults(t *testing.T) {
	trimmed, total, truncated := search.TrimByTokenBudget(nil, 100)
	assert.Len(t, trimmed, 0)
	assert.Equal(t, 0, total)
	assert.False(t, truncated)
}
