package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"iclude/internal/api"
	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/search"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLMProvider Mock LLM提供者，用于测试 / Mock LLM provider for testing
type mockLLMProvider struct {
	response *llm.ChatResponse
	err      error
}

// Chat 模拟LLM对话调用 / Simulate LLM chat call
func (m *mockLLMProvider) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// setupReflectRouter 初始化带有 ReflectEngine 的路由器 / Set up router with ReflectEngine
func setupReflectRouter(t *testing.T, llmProvider llm.Provider) (http.Handler, *memory.Manager, func()) {
	t.Helper()

	// 使用内存 SQLite 数据库，按测试名称隔离 / In-memory SQLite, isolated per test name
	dbPath := "file:" + t.Name() + "?mode=memory&cache=shared"
	tok := tokenizer.NewSimpleTokenizer()
	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	require.NoError(t, err)

	err = s.Init(context.Background())
	require.NoError(t, err)

	mgr := memory.NewManager(s, nil, nil, nil, nil, nil)
	ret := search.NewRetriever(s, nil, nil, nil, nil, config.RetrievalConfig{}, nil)

	reflectCfg := config.ReflectConfig{
		MaxRounds:    3,
		TokenBudget:  4096,
		RoundTimeout: 30_000_000_000, // 30s in nanoseconds
		AutoSave:     false,
	}

	engine := reflectpkg.NewReflectEngine(ret, mgr, llmProvider, reflectCfg)

	router := api.SetupRouter(&api.RouterDeps{
		MemManager:    mgr,
		Retriever:     ret,
		ReflectEngine: engine,
	})

	cleanup := func() {
		s.Close()
	}

	return router, mgr, cleanup
}

// TestReflectAPI_Success 验证正常反思推理流程 / Verify successful reflect reasoning flow
func TestReflectAPI_Success(t *testing.T) {
	// 返回有效 JSON 结论的 Mock LLM / Mock LLM that returns a valid JSON conclusion
	mockLLM := &mockLLMProvider{
		response: &llm.ChatResponse{
			Content:     `{"action":"conclusion","reasoning":"The memories contain relevant information about SQLite.","conclusion":"SQLite is an embedded relational database used in IClude for structured storage and FTS5 full-text search.","next_query":""}`,
			TotalTokens: 50,
		},
	}

	router, mgr, cleanup := setupReflectRouter(t, mockLLM)
	defer cleanup()

	// 预先创建包含关键词的记忆 / Seed a memory whose content contains keywords from the question
	_, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content:    "SQLite is an embedded relational database used for structured storage and FTS5 full-text search.",
		Kind:       "fact",
		TeamID:     "default",
		Visibility: model.VisibilityTeam,
	})
	require.NoError(t, err)

	// POST /v1/reflect
	body := map[string]any{
		"question": "SQLite",
	}
	code, resp := doRequest(t, router, "POST", "/v1/reflect", body)

	assert.Equal(t, http.StatusOK, code, "expected 200 OK")
	assert.Equal(t, 0, resp.Code, "expected api code=0")

	// 验证 data 包含 result 字段 / Verify data contains result field
	var data map[string]any
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	result, ok := data["result"].(string)
	assert.True(t, ok, "result field should be a string")
	assert.NotEmpty(t, result, "result should not be empty")

	// 验证 trace 字段存在 / Verify trace field exists
	trace, ok := data["trace"].([]any)
	assert.True(t, ok, "trace should be an array")
	assert.GreaterOrEqual(t, len(trace), 1, "trace should have at least one round")

	t.Logf("Reflect result: %q", result)
	t.Logf("Rounds used: %v", data["metadata"])
}

// TestReflectAPI_MissingQuestion 验证缺少 question 字段时返回 400 / Verify 400 when question field is missing
func TestReflectAPI_MissingQuestion(t *testing.T) {
	mockLLM := &mockLLMProvider{
		response: &llm.ChatResponse{Content: `{"action":"conclusion","reasoning":"ok","conclusion":"ok"}`, TotalTokens: 10},
	}

	router, _, cleanup := setupReflectRouter(t, mockLLM)
	defer cleanup()

	tests := []struct {
		name string
		body any
	}{
		{
			name: "empty_object",
			body: map[string]any{},
		},
		{
			name: "empty_question_string",
			body: map[string]any{"question": ""},
		},
		{
			name: "whitespace_question",
			body: map[string]any{"question": "   "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, resp := doRequest(t, router, "POST", "/v1/reflect", tt.body)
			assert.Equal(t, http.StatusBadRequest, code, "expected 400 Bad Request for case %q", tt.name)
			assert.NotEqual(t, 0, resp.Code, "expected non-zero api error code")
			t.Logf("  case=%q http=%d api_code=%d msg=%s", tt.name, code, resp.Code, resp.Message)
		})
	}
}

// TestReflectAPI_LLMFailure 验证 LLM 失败时返回 502 / Verify 502 when LLM call fails
func TestReflectAPI_LLMFailure(t *testing.T) {
	// Mock LLM 始终返回错误 / Mock LLM that always returns an error
	mockLLM := &mockLLMProvider{
		err: errors.New("connection refused: LLM service unavailable"),
	}

	router, mgr, cleanup := setupReflectRouter(t, mockLLM)
	defer cleanup()

	// 预先创建包含关键词的记忆，确保检索步骤通过 / Seed memory so retrieval succeeds
	_, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content:    "memory about golang programming language features and tooling",
		Kind:       "fact",
		TeamID:     "default",
		Visibility: model.VisibilityTeam,
	})
	require.NoError(t, err)

	body := map[string]any{
		"question": "golang",
	}
	code, resp := doRequest(t, router, "POST", "/v1/reflect", body)

	assert.Equal(t, http.StatusBadGateway, code, "expected 502 Bad Gateway when LLM fails")
	assert.Equal(t, 502, resp.Code, "expected api code=502")
	t.Logf("LLM failure response: http=%d api_code=%d msg=%s", code, resp.Code, resp.Message)
}
