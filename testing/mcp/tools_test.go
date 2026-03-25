// Package mcp_test MCP 工具单元测试 / MCP tools unit tests
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"iclude/internal/mcp"
	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMemoryRetriever 测试用记忆检索存根 / Memory retriever stub for testing
type mockMemoryRetriever struct {
	memories []*model.Memory
	err      error
}

func (m *mockMemoryRetriever) Retrieve(_ context.Context, req *model.RetrieveRequest) ([]*model.Memory, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

// capturingRetriever 捕获请求的存根 / Retriever stub that captures the incoming request
type capturingRetriever struct {
	onRetrieve func(*model.RetrieveRequest) ([]*model.Memory, error)
}

func (c *capturingRetriever) Retrieve(_ context.Context, req *model.RetrieveRequest) ([]*model.Memory, error) {
	return c.onRetrieve(req)
}

func TestRecallTool_Execute_success(t *testing.T) {
	ret := &mockMemoryRetriever{memories: []*model.Memory{{ID: "m1", Content: "answer"}}}
	tool := tools.NewRecallTool(ret)
	args, _ := json.Marshal(map[string]any{"query": "what is the answer?"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "m1")
}

func TestRecallTool_Execute_missingQuery(t *testing.T) {
	tool := tools.NewRecallTool(&mockMemoryRetriever{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestRecallTool_Execute_invalidJSON(t *testing.T) {
	tool := tools.NewRecallTool(&mockMemoryRetriever{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid arguments")
}

func TestRecallTool_Execute_retrieverError(t *testing.T) {
	ret := &mockMemoryRetriever{err: errors.New("store unavailable")}
	tool := tools.NewRecallTool(ret)
	args, _ := json.Marshal(map[string]any{"query": "test"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "retrieval failed")
}

func TestRecallTool_Execute_defaultLimit(t *testing.T) {
	var capturedReq *model.RetrieveRequest
	ret := &capturingRetriever{onRetrieve: func(req *model.RetrieveRequest) ([]*model.Memory, error) {
		capturedReq = req
		return nil, nil
	}}
	tool := tools.NewRecallTool(ret)
	args, _ := json.Marshal(map[string]any{"query": "test"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 10, capturedReq.Limit)
}

func TestRecallTool_Execute_scopeFilter(t *testing.T) {
	var capturedReq *model.RetrieveRequest
	ret := &capturingRetriever{onRetrieve: func(req *model.RetrieveRequest) ([]*model.Memory, error) {
		capturedReq = req
		return nil, nil
	}}
	tool := tools.NewRecallTool(ret)
	args, _ := json.Marshal(map[string]any{"query": "test", "scope": "project-x"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.NotNil(t, capturedReq.Filters)
	assert.Equal(t, "project-x", capturedReq.Filters.Scope)
}

func TestRecallTool_Execute_withIdentity(t *testing.T) {
	var capturedReq *model.RetrieveRequest
	ret := &capturingRetriever{onRetrieve: func(req *model.RetrieveRequest) ([]*model.Memory, error) {
		capturedReq = req
		return nil, nil
	}}
	tool := tools.NewRecallTool(ret)

	id := &model.Identity{TeamID: "team-42", OwnerID: "owner-1"}
	ctx := mcp.WithIdentity(context.Background(), id)

	args, _ := json.Marshal(map[string]any{"query": "hello"})
	_, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.Equal(t, "team-42", capturedReq.TeamID)
}

func TestRecallTool_Definition(t *testing.T) {
	tool := tools.NewRecallTool(&mockMemoryRetriever{})
	def := tool.Definition()
	assert.Equal(t, "iclude_recall", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.NotEmpty(t, def.InputSchema)
}

// --- RetainTool tests ---

// mockMemoryCreator stub 实现 / stub implementation for MemoryCreator
type mockMemoryCreator struct {
	created *model.Memory
	err     error
}

func (m *mockMemoryCreator) Create(_ context.Context, mem *model.Memory) (*model.Memory, error) {
	if m.err != nil {
		return nil, m.err
	}
	mem.ID = "mem-001"
	m.created = mem
	return mem, nil
}

func TestRetainTool_Execute_success(t *testing.T) {
	creator := &mockMemoryCreator{}
	tool := tools.NewRetainTool(creator)
	args, _ := json.Marshal(map[string]any{"content": "The answer is 42", "scope": "test"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "mem-001")
}

func TestRetainTool_Execute_missingContent(t *testing.T) {
	tool := tools.NewRetainTool(&mockMemoryCreator{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestRetainTool_Execute_invalidJSON(t *testing.T) {
	tool := tools.NewRetainTool(&mockMemoryCreator{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid arguments")
}

func TestRetainTool_Execute_creatorError(t *testing.T) {
	creator := &mockMemoryCreator{err: errors.New("db unavailable")}
	tool := tools.NewRetainTool(creator)
	args, _ := json.Marshal(map[string]any{"content": "some memory"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "failed to save memory")
}

func TestRetainTool_Execute_withIdentity(t *testing.T) {
	creator := &mockMemoryCreator{}
	tool := tools.NewRetainTool(creator)

	id := &model.Identity{TeamID: "team-42", OwnerID: "owner-1"}
	ctx := mcp.WithIdentity(context.Background(), id)

	args, _ := json.Marshal(map[string]any{"content": "important fact"})
	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "team-42", creator.created.TeamID)
	assert.Equal(t, "owner-1", creator.created.OwnerID)
}

func TestRetainTool_Definition(t *testing.T) {
	tool := tools.NewRetainTool(&mockMemoryCreator{})
	def := tool.Definition()
	assert.Equal(t, "iclude_retain", def.Name)
	assert.NotEmpty(t, def.Description)
	// validate InputSchema is valid JSON with required field
	var schema map[string]any
	require.NoError(t, json.Unmarshal(def.InputSchema, &schema))
	required, ok := schema["required"].([]any)
	require.True(t, ok)
	assert.Contains(t, required, "content")
}
