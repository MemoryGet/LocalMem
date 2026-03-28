// Package mcp_test MCP scan 工具单元测试 / MCP scan tool unit tests
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScanTool_Definition 验证工具名称为 iclude_scan / Verify tool name is iclude_scan
func TestScanTool_Definition(t *testing.T) {
	tool := tools.NewScanTool(&mockMemoryRetriever{}, nil)
	def := tool.Definition()
	assert.Equal(t, "iclude_scan", def.Name)
	assert.NotEmpty(t, def.Description)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(def.InputSchema, &schema))
	required, ok := schema["required"].([]any)
	require.True(t, ok)
	assert.Contains(t, required, "query")
}

// TestScanTool_Execute_ReturnsCompactIndex 验证返回紧凑索引条目 / Verify compact index items are returned
func TestScanTool_Execute_ReturnsCompactIndex(t *testing.T) {
	happenedAt := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	longContent := "This is a fairly long memory content that should be used to estimate token count and also to verify that the title truncation works correctly when abstract is empty and the content exceeds one hundred rune characters."

	ret := &mockMemoryRetriever{
		results: []*model.SearchResult{
			{
				Memory: &model.Memory{
					ID:         "scan-001",
					Content:    longContent,
					Abstract:   "Short abstract title",
					Kind:       "fact",
					HappenedAt: &happenedAt,
				},
				Score:  0.92,
				Source: "hybrid",
			},
			{
				Memory: &model.Memory{
					ID:      "scan-002",
					Content: "Brief note",
					Kind:    "note",
				},
				Score:  0.75,
				Source: "sqlite",
			},
		},
	}

	tool := tools.NewScanTool(ret, nil)
	args, _ := json.Marshal(map[string]any{"query": "search query", "limit": 5})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// 解析并验证条目 / Parse and verify items
	var items []tools.ScanResultItem
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &items))
	require.Len(t, items, 2)

	// 验证第一个条目使用 Abstract 作为标题 / Verify first item uses Abstract as title
	assert.Equal(t, "scan-001", items[0].ID)
	assert.Equal(t, "Short abstract title", items[0].Title)
	assert.Equal(t, 0.92, items[0].Score)
	assert.Equal(t, "hybrid", items[0].Source)
	assert.Equal(t, "fact", items[0].Kind)
	assert.NotNil(t, items[0].HappenedAt)
	assert.Greater(t, items[0].TokenEstimate, 0)

	// 验证第二个条目内容短于 100 字符时不截断 / Verify short content is not truncated
	assert.Equal(t, "scan-002", items[1].ID)
	assert.Equal(t, "Brief note", items[1].Title)
	assert.Equal(t, 0.75, items[1].Score)
	assert.Equal(t, "sqlite", items[1].Source)

	// 验证紧凑性：条目中不含完整 content 字段 / Verify compactness: items do not contain full content field
	rawText := result.Content[0].Text
	for _, item := range items {
		// 标题是紧凑摘要，不是原始长内容 / Title is compact summary, not raw long content
		assert.LessOrEqual(t, len([]rune(item.Title)), 103, "title should be compact (≤100 chars + ellipsis)")
	}
	_ = rawText
}

// TestScanTool_Execute_TruncatesLongContent 验证长内容截断为 100 字符并加省略号 / Verify long content is truncated to 100 chars with ellipsis
func TestScanTool_Execute_TruncatesLongContent(t *testing.T) {
	longContent := "abcdefghij" // repeated to make > 100 chars
	for len(longContent) <= 100 {
		longContent += "abcdefghij"
	}

	ret := &mockMemoryRetriever{
		results: []*model.SearchResult{
			{
				Memory: &model.Memory{
					ID:      "scan-long",
					Content: longContent,
					// Abstract 为空，应触发截断 / Abstract empty, should trigger truncation
				},
				Score:  0.5,
				Source: "sqlite",
			},
		},
	}

	tool := tools.NewScanTool(ret, nil)
	args, _ := json.Marshal(map[string]any{"query": "long content"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var items []tools.ScanResultItem
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &items))
	require.Len(t, items, 1)

	// 标题应以省略号结尾 / Title should end with ellipsis
	assert.True(t, len([]rune(items[0].Title)) == 103, "title should be 100 runes + '...'")
	assert.Contains(t, items[0].Title, "...")
}

// TestScanTool_Execute_EmptyQuery 空查询应返回错误结果 / Empty query should return error result
func TestScanTool_Execute_EmptyQuery(t *testing.T) {
	tool := tools.NewScanTool(&mockMemoryRetriever{}, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "query is required")
}

// TestScanTool_Execute_InvalidJSON 无效 JSON 参数应返回错误 / Invalid JSON should return error
func TestScanTool_Execute_InvalidJSON(t *testing.T) {
	tool := tools.NewScanTool(&mockMemoryRetriever{}, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid arguments")
}

// TestScanTool_Execute_RetrieverError 检索器错误时应返回错误结果 / Should return error when retriever fails
func TestScanTool_Execute_RetrieverError(t *testing.T) {
	ret := &mockMemoryRetriever{err: assert.AnError}
	tool := tools.NewScanTool(ret, nil)
	args, _ := json.Marshal(map[string]any{"query": "test"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "retrieval failed")
}
