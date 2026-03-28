// Package mcp_test MCP fetch 工具单元测试 / MCP fetch tool unit tests
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMemoryGetter 测试用记忆获取存根 / Memory getter stub for testing
type mockMemoryGetter struct {
	memories map[string]*model.Memory
}

func (m *mockMemoryGetter) Get(_ context.Context, id string) (*model.Memory, error) {
	mem, ok := m.memories[id]
	if !ok {
		return nil, model.ErrMemoryNotFound
	}
	return mem, nil
}

// TestFetchTool_Definition 验证工具名称为 iclude_fetch / Verify tool name is iclude_fetch
func TestFetchTool_Definition(t *testing.T) {
	tool := tools.NewFetchTool(&mockMemoryGetter{memories: map[string]*model.Memory{}})
	def := tool.Definition()
	assert.Equal(t, "iclude_fetch", def.Name)
	assert.NotEmpty(t, def.Description)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(def.InputSchema, &schema))
	required, ok := schema["required"].([]any)
	require.True(t, ok)
	assert.Contains(t, required, "ids")
}

// TestFetchTool_Execute_BatchFetch 两个 ID 均存在时返回两条完整记忆 / Returns 2 items when both IDs found
func TestFetchTool_Execute_BatchFetch(t *testing.T) {
	getter := &mockMemoryGetter{
		memories: map[string]*model.Memory{
			"mem-001": {ID: "mem-001", Content: "Full content of memory one", Kind: "fact"},
			"mem-002": {ID: "mem-002", Content: "Full content of memory two", Kind: "note"},
		},
	}
	tool := tools.NewFetchTool(getter)
	args, _ := json.Marshal(map[string]any{"ids": []string{"mem-001", "mem-002"}})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var items []tools.FetchResultItem
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &items))
	require.Len(t, items, 2)

	assert.Equal(t, "mem-001", items[0].Memory.ID)
	assert.Equal(t, "Full content of memory one", items[0].Memory.Content)
	assert.Equal(t, "mem-002", items[1].Memory.ID)
	assert.Equal(t, "Full content of memory two", items[1].Memory.Content)
}

// TestFetchTool_Execute_EmptyIDs 空 IDs 列表应返回错误 / Empty IDs should return error result
func TestFetchTool_Execute_EmptyIDs(t *testing.T) {
	tool := tools.NewFetchTool(&mockMemoryGetter{memories: map[string]*model.Memory{}})
	args, _ := json.Marshal(map[string]any{"ids": []string{}})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "ids is required")
}

// TestFetchTool_Execute_TooManyIDs 超过 20 个 ID 应返回错误 / More than 20 IDs should return error
func TestFetchTool_Execute_TooManyIDs(t *testing.T) {
	ids := make([]string, 21)
	for i := range ids {
		ids[i] = "mem-extra"
	}
	tool := tools.NewFetchTool(&mockMemoryGetter{memories: map[string]*model.Memory{}})
	args, _ := json.Marshal(map[string]any{"ids": ids})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "maximum 20 ids per request")
}

// TestFetchTool_Execute_PartialNotFound 部分找到时跳过缺失条目 / Skips missing IDs and returns found ones
func TestFetchTool_Execute_PartialNotFound(t *testing.T) {
	getter := &mockMemoryGetter{
		memories: map[string]*model.Memory{
			"mem-found": {ID: "mem-found", Content: "Found memory content", Kind: "fact"},
		},
	}
	tool := tools.NewFetchTool(getter)
	args, _ := json.Marshal(map[string]any{"ids": []string{"mem-found", "mem-missing"}})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)

	var items []tools.FetchResultItem
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &items))
	require.Len(t, items, 1)
	assert.Equal(t, "mem-found", items[0].Memory.ID)
}
