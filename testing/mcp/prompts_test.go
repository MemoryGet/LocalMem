// Package mcp_test MCP 提示模板单元测试 / MCP prompts unit tests
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"iclude/internal/mcp"
	"iclude/internal/mcp/prompts"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPromptRetriever 测试用记忆检索存根 / Memory retriever stub for prompt tests
type mockPromptRetriever struct {
	results  []*model.SearchResult
	err      error
	captured *model.RetrieveRequest
}

func (m *mockPromptRetriever) Retrieve(_ context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	m.captured = req
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// TestMemoryContextPrompt_Definition 验证提示定义字段 / Verify prompt definition fields
func TestMemoryContextPrompt_Definition(t *testing.T) {
	p := prompts.NewMemoryContextPrompt(&mockPromptRetriever{})
	def := p.Definition()

	assert.Equal(t, "memory_context", def.Name)
	assert.NotEmpty(t, def.Description)

	// question 参数必须存在且 Required=true
	var questionArg *mcp.PromptArgument
	for i := range def.Arguments {
		if def.Arguments[i].Name == "question" {
			questionArg = &def.Arguments[i]
		}
	}
	require.NotNil(t, questionArg, "argument 'question' must be present")
	assert.True(t, questionArg.Required)

	// scope 和 limit 参数存在但非必需
	argNames := make(map[string]bool)
	for _, a := range def.Arguments {
		argNames[a.Name] = true
	}
	assert.True(t, argNames["scope"])
	assert.True(t, argNames["limit"])
}

// TestMemoryContextPrompt_Get_success 成功检索并生成系统消息 / Retrieves memories and returns system + user messages
func TestMemoryContextPrompt_Get_success(t *testing.T) {
	mem := &model.Memory{ID: "m1", Content: "IClude is a memory system"}
	retriever := &mockPromptRetriever{results: []*model.SearchResult{{Memory: mem, Score: 0.9}}}
	p := prompts.NewMemoryContextPrompt(retriever)

	result, err := p.Get(context.Background(), map[string]string{
		"question": "What is IClude?",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Messages, 2)

	// 系统消息包含 JSON 和 memories 内容
	sysMsg := result.Messages[0]
	assert.Equal(t, "system", sysMsg.Role)
	assert.Equal(t, "text", sysMsg.Content.Type)
	assert.Contains(t, sysMsg.Content.Text, "```json")
	assert.Contains(t, sysMsg.Content.Text, "m1")

	// 用户消息包含原始问题
	userMsg := result.Messages[1]
	assert.Equal(t, "user", userMsg.Role)
	assert.Equal(t, "text", userMsg.Content.Type)
	assert.Equal(t, "What is IClude?", userMsg.Content.Text)

	// Description 包含问题
	assert.Contains(t, result.Description, "What is IClude?")
}

// TestMemoryContextPrompt_Get_missingQuestion 缺少 question 参数返回错误 / Empty question returns error
func TestMemoryContextPrompt_Get_missingQuestion(t *testing.T) {
	p := prompts.NewMemoryContextPrompt(&mockPromptRetriever{})

	result, err := p.Get(context.Background(), map[string]string{})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "question")
}

// TestMemoryContextPrompt_Get_withScope scope 参数正确传递到 SearchFilters / Scope argument propagates to SearchFilters
func TestMemoryContextPrompt_Get_withScope(t *testing.T) {
	retriever := &mockPromptRetriever{results: []*model.SearchResult{}}
	p := prompts.NewMemoryContextPrompt(retriever)

	_, err := p.Get(context.Background(), map[string]string{
		"question": "test question",
		"scope":    "project-x",
	})

	require.NoError(t, err)
	require.NotNil(t, retriever.captured)
	require.NotNil(t, retriever.captured.Filters)
	assert.Equal(t, "project-x", retriever.captured.Filters.Scope)
}

// TestMemoryContextPrompt_Get_withLimit limit 参数正确解析 / Limit argument is parsed correctly
func TestMemoryContextPrompt_Get_withLimit(t *testing.T) {
	retriever := &mockPromptRetriever{results: []*model.SearchResult{}}
	p := prompts.NewMemoryContextPrompt(retriever)

	_, err := p.Get(context.Background(), map[string]string{
		"question": "test question",
		"limit":    "5",
	})

	require.NoError(t, err)
	require.NotNil(t, retriever.captured)
	assert.Equal(t, 5, retriever.captured.Limit)
}

// TestMemoryContextPrompt_Get_defaultLimit limit 默认值为 10 / Default limit is 10
func TestMemoryContextPrompt_Get_defaultLimit(t *testing.T) {
	retriever := &mockPromptRetriever{results: []*model.SearchResult{}}
	p := prompts.NewMemoryContextPrompt(retriever)

	_, err := p.Get(context.Background(), map[string]string{
		"question": "test",
	})

	require.NoError(t, err)
	assert.Equal(t, 10, retriever.captured.Limit)
}

// TestMemoryContextPrompt_Get_invalidLimit 无效 limit 回退到默认值 / Invalid limit falls back to 10
func TestMemoryContextPrompt_Get_invalidLimit(t *testing.T) {
	retriever := &mockPromptRetriever{results: []*model.SearchResult{}}
	p := prompts.NewMemoryContextPrompt(retriever)

	_, err := p.Get(context.Background(), map[string]string{
		"question": "test",
		"limit":    "not-a-number",
	})

	require.NoError(t, err)
	assert.Equal(t, 10, retriever.captured.Limit)
}

// TestMemoryContextPrompt_Get_retrieverError 检索失败时返回错误 / Returns error when retriever fails
func TestMemoryContextPrompt_Get_retrieverError(t *testing.T) {
	retriever := &mockPromptRetriever{err: errors.New("database unavailable")}
	p := prompts.NewMemoryContextPrompt(retriever)

	result, err := p.Get(context.Background(), map[string]string{
		"question": "test",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to retrieve memories")
}

// TestMemoryContextPrompt_Get_withIdentity 身份注入 TeamID 正确传递 / Identity TeamID propagated to request
func TestMemoryContextPrompt_Get_withIdentity(t *testing.T) {
	retriever := &mockPromptRetriever{results: []*model.SearchResult{}}
	p := prompts.NewMemoryContextPrompt(retriever)

	id := &model.Identity{TeamID: "team-abc"}
	ctx := mcp.WithIdentity(context.Background(), id)

	_, err := p.Get(ctx, map[string]string{
		"question": "team question",
	})

	require.NoError(t, err)
	require.NotNil(t, retriever.captured)
	assert.Equal(t, "team-abc", retriever.captured.TeamID)
}

// TestMemoryContextPrompt_Get_emptyMemories 无记忆时仍返回有效结果 / Valid result returned when no memories found
func TestMemoryContextPrompt_Get_emptyMemories(t *testing.T) {
	retriever := &mockPromptRetriever{results: []*model.SearchResult{}}
	p := prompts.NewMemoryContextPrompt(retriever)

	result, err := p.Get(context.Background(), map[string]string{
		"question": "anything?",
	})

	require.NoError(t, err)
	require.Len(t, result.Messages, 2)

	sysText := result.Messages[0].Content.Text
	// JSON のコードブロックは常に含まれる / JSON code block always present
	assert.True(t, strings.Contains(sysText, "```json"))
}

// TestMemoryContextPrompt_Get_systemMessageContainsJSON JSON 序列化正确嵌入 / JSON is correctly embedded in system message
func TestMemoryContextPrompt_Get_systemMessageContainsJSON(t *testing.T) {
	mem := &model.Memory{ID: "abc-123", Content: "some remembered fact"}
	retriever := &mockPromptRetriever{results: []*model.SearchResult{{Memory: mem, Score: 0.9}}}
	p := prompts.NewMemoryContextPrompt(retriever)

	result, err := p.Get(context.Background(), map[string]string{
		"question": "recall fact",
	})

	require.NoError(t, err)
	sysText := result.Messages[0].Content.Text

	// 系统消息包含标准格式 JSON
	var parsed []map[string]any
	marker := "```json\n"
	markerPos := strings.Index(sysText, marker)
	require.True(t, markerPos >= 0, "```json marker not found")
	startIdx := markerPos + len(marker)
	// 从 startIdx 开始查找关闭的 ``` / Find closing ``` starting after the opening marker
	closingMarker := "\n```"
	endIdx := strings.Index(sysText[startIdx:], closingMarker)
	require.True(t, endIdx >= 0, "closing ``` not found")
	jsonPart := sysText[startIdx : startIdx+endIdx]
	require.NoError(t, json.Unmarshal([]byte(jsonPart), &parsed))
	// SearchResult JSON 结构: {"memory": {...}, "score": ...} / SearchResult JSON: {"memory": {...}, "score": ...}
	memMap, ok := parsed[0]["memory"].(map[string]any)
	require.True(t, ok, "expected memory field in SearchResult JSON")
	assert.Equal(t, "abc-123", memMap["id"])
}
