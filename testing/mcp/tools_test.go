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
	results []*model.SearchResult
	err     error
}

func (m *mockMemoryRetriever) Retrieve(_ context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// capturingRetriever 捕获请求的存根 / Retriever stub that captures the incoming request
type capturingRetriever struct {
	onRetrieve func(*model.RetrieveRequest) ([]*model.SearchResult, error)
}

func (c *capturingRetriever) Retrieve(_ context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	return c.onRetrieve(req)
}

func TestRecallTool_Execute_success(t *testing.T) {
	ret := &mockMemoryRetriever{results: []*model.SearchResult{{Memory: &model.Memory{ID: "m1", Content: "answer"}, Score: 0.9}}}
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
	ret := &capturingRetriever{onRetrieve: func(req *model.RetrieveRequest) ([]*model.SearchResult, error) {
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
	ret := &capturingRetriever{onRetrieve: func(req *model.RetrieveRequest) ([]*model.SearchResult, error) {
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
	ret := &capturingRetriever{onRetrieve: func(req *model.RetrieveRequest) ([]*model.SearchResult, error) {
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
	tool := tools.NewRetainTool(creator, nil)
	args, _ := json.Marshal(map[string]any{"content": "The answer is 42", "scope": "project/test"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "mem-001")
}

func TestRetainTool_Execute_missingContent(t *testing.T) {
	tool := tools.NewRetainTool(&mockMemoryCreator{}, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestRetainTool_Execute_invalidJSON(t *testing.T) {
	tool := tools.NewRetainTool(&mockMemoryCreator{}, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid arguments")
}

func TestRetainTool_Execute_creatorError(t *testing.T) {
	creator := &mockMemoryCreator{err: errors.New("db unavailable")}
	tool := tools.NewRetainTool(creator, nil)
	args, _ := json.Marshal(map[string]any{"content": "some memory"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "failed to save memory")
}

func TestRetainTool_Execute_withIdentity(t *testing.T) {
	creator := &mockMemoryCreator{}
	tool := tools.NewRetainTool(creator, nil)

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
	tool := tools.NewRetainTool(&mockMemoryCreator{}, nil)
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

// --- ReflectTool tests ---

// mockReflectEngine 测试用反思引擎存根 / Reflect engine stub for testing
type mockReflectEngine struct {
	result string
	err    error
}

func (m *mockReflectEngine) Reflect(_ context.Context, req *model.ReflectRequest) (*model.ReflectResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &model.ReflectResponse{
		Result:   m.result,
		Sources:  []string{"mem-1", "mem-2"},
		Metadata: model.ReflectMeta{RoundsUsed: 1},
	}, nil
}

func TestReflectTool_Execute_success(t *testing.T) {
	engine := &mockReflectEngine{result: "The capital is Paris"}
	tool := tools.NewReflectTool(engine)
	args, _ := json.Marshal(map[string]any{"question": "What is the capital of France?"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "Paris")
	assert.Contains(t, result.Content[0].Text, "rounds_used")
}

func TestReflectTool_Execute_missingQuestion(t *testing.T) {
	tool := tools.NewReflectTool(&mockReflectEngine{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "question is required")
}

func TestReflectTool_Execute_invalidJSON(t *testing.T) {
	tool := tools.NewReflectTool(&mockReflectEngine{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid arguments")
}

func TestReflectTool_Execute_engineError(t *testing.T) {
	engine := &mockReflectEngine{err: errors.New("llm unavailable")}
	tool := tools.NewReflectTool(engine)
	args, _ := json.Marshal(map[string]any{"question": "anything"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "reflect failed")
}

func TestReflectTool_Execute_withScopeAndMaxRounds(t *testing.T) {
	engine := &mockReflectEngine{result: "synthesized insight"}
	tool := tools.NewReflectTool(engine)
	args, _ := json.Marshal(map[string]any{
		"question":   "summarize project learnings",
		"scope":      "project-alpha",
		"max_rounds": 3,
	})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "synthesized insight")
}

func TestReflectTool_Execute_withIdentity(t *testing.T) {
	engine := &mockReflectEngine{result: "team insight"}
	tool := tools.NewReflectTool(engine)

	id := &model.Identity{TeamID: "team-99", OwnerID: "owner-2"}
	ctx := mcp.WithIdentity(context.Background(), id)

	args, _ := json.Marshal(map[string]any{"question": "what did team-99 learn?"})
	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "team insight")
}

func TestReflectTool_Definition(t *testing.T) {
	tool := tools.NewReflectTool(&mockReflectEngine{})
	def := tool.Definition()
	assert.Equal(t, "iclude_reflect", def.Name)
	assert.NotEmpty(t, def.Description)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(def.InputSchema, &schema))
	required, ok := schema["required"].([]any)
	require.True(t, ok)
	assert.Contains(t, required, "question")
}

// --- IngestConversationTool tests ---

// mockConversationIngester 测试用对话摄取存根 / Conversation ingester stub for testing
type mockConversationIngester struct {
	ctxID string
	err   error
}

func (m *mockConversationIngester) IngestConversation(_ context.Context, req *model.IngestConversationRequest, identity *model.Identity) (string, []*model.Memory, error) {
	if m.err != nil {
		return "", nil, m.err
	}
	mems := make([]*model.Memory, len(req.Messages))
	for i := range mems {
		mems[i] = &model.Memory{}
	}
	return m.ctxID, mems, nil
}

func TestIngestConversationTool_Execute(t *testing.T) {
	ingester := &mockConversationIngester{ctxID: "ctx-abc"}
	tool := tools.NewIngestConversationTool(ingester)
	args, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hi there"},
		},
		"provider": "claude",
	})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "ctx-abc")
}

func TestIngestConversationTool_Execute_savedCount(t *testing.T) {
	ingester := &mockConversationIngester{ctxID: "ctx-xyz"}
	tool := tools.NewIngestConversationTool(ingester)
	args, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "msg1"},
			{"role": "assistant", "content": "msg2"},
			{"role": "user", "content": "msg3"},
		},
	})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "3")
}

func TestIngestConversationTool_Execute_missingMessages(t *testing.T) {
	tool := tools.NewIngestConversationTool(&mockConversationIngester{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestIngestConversationTool_Execute_emptyMessages(t *testing.T) {
	tool := tools.NewIngestConversationTool(&mockConversationIngester{})
	args, _ := json.Marshal(map[string]any{"messages": []map[string]string{}})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestIngestConversationTool_Execute_invalidJSON(t *testing.T) {
	tool := tools.NewIngestConversationTool(&mockConversationIngester{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid arguments")
}

func TestIngestConversationTool_Execute_ingesterError(t *testing.T) {
	ingester := &mockConversationIngester{err: errors.New("store unavailable")}
	tool := tools.NewIngestConversationTool(ingester)
	args, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "ingest failed")
}

func TestIngestConversationTool_Execute_withIdentity(t *testing.T) {
	ingester := &mockConversationIngester{ctxID: "ctx-team"}
	tool := tools.NewIngestConversationTool(ingester)

	id := &model.Identity{TeamID: "team-77", OwnerID: "owner-5"}
	ctx := mcp.WithIdentity(context.Background(), id)

	args, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "ctx-team")
}

func TestIngestConversationTool_Definition(t *testing.T) {
	tool := tools.NewIngestConversationTool(&mockConversationIngester{})
	def := tool.Definition()
	assert.Equal(t, "iclude_ingest_conversation", def.Name)
	assert.NotEmpty(t, def.Description)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(def.InputSchema, &schema))
	required, ok := schema["required"].([]any)
	require.True(t, ok)
	assert.Contains(t, required, "messages")
}

// --- TimelineTool tests ---

// mockTimelineRetriever 测试用时间线查询存根 / Timeline querier stub for testing
type mockTimelineRetriever struct {
	memories []*model.Memory
	err      error
}

func (m *mockTimelineRetriever) Timeline(_ context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

// capturingTimelineQuerier 捕获请求的时间线存根 / Timeline querier stub that captures the request
type capturingTimelineQuerier struct {
	onTimeline func(*model.TimelineRequest) ([]*model.Memory, error)
}

func (c *capturingTimelineQuerier) Timeline(_ context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	return c.onTimeline(req)
}

func TestTimelineTool_Execute(t *testing.T) {
	ret := &mockTimelineRetriever{memories: []*model.Memory{{ID: "m1", Content: "old fact"}}}
	tool := tools.NewTimelineTool(ret)
	args, _ := json.Marshal(map[string]any{"scope": "project-x", "limit": 10})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestTimelineTool_Execute_containsID(t *testing.T) {
	ret := &mockTimelineRetriever{memories: []*model.Memory{{ID: "m-timeline-1", Content: "timeline content"}}}
	tool := tools.NewTimelineTool(ret)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "m-timeline-1")
}

func TestTimelineTool_Execute_invalidJSON(t *testing.T) {
	tool := tools.NewTimelineTool(&mockTimelineRetriever{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid arguments")
}

func TestTimelineTool_Execute_querierError(t *testing.T) {
	ret := &mockTimelineRetriever{err: errors.New("db down")}
	tool := tools.NewTimelineTool(ret)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "timeline query failed")
}

func TestTimelineTool_Execute_defaultLimit(t *testing.T) {
	var capturedReq *model.TimelineRequest
	ret := &capturingTimelineQuerier{onTimeline: func(req *model.TimelineRequest) ([]*model.Memory, error) {
		capturedReq = req
		return nil, nil
	}}
	tool := tools.NewTimelineTool(ret)
	args, _ := json.Marshal(map[string]any{})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 20, capturedReq.Limit)
}

func TestTimelineTool_Execute_withIdentity(t *testing.T) {
	var capturedReq *model.TimelineRequest
	ret := &capturingTimelineQuerier{onTimeline: func(req *model.TimelineRequest) ([]*model.Memory, error) {
		capturedReq = req
		return nil, nil
	}}
	tool := tools.NewTimelineTool(ret)

	id := &model.Identity{TeamID: "team-55", OwnerID: "owner-9"}
	ctx := mcp.WithIdentity(context.Background(), id)

	args, _ := json.Marshal(map[string]any{"scope": "my-scope"})
	_, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.Equal(t, "team-55", capturedReq.TeamID)
	assert.Equal(t, "owner-9", capturedReq.OwnerID)
}

func TestTimelineTool_Execute_invalidAfter(t *testing.T) {
	tool := tools.NewTimelineTool(&mockTimelineRetriever{})
	args, _ := json.Marshal(map[string]any{"after": "not-a-date"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "invalid after timestamp")
}

func TestTimelineTool_Definition(t *testing.T) {
	tool := tools.NewTimelineTool(&mockTimelineRetriever{})
	def := tool.Definition()
	assert.Equal(t, "iclude_timeline", def.Name)
	assert.NotEmpty(t, def.Description)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(def.InputSchema, &schema))
	assert.NotEmpty(t, schema["properties"])
}
