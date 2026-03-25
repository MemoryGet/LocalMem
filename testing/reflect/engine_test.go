// Package reflect_test 反思引擎单元测试 / Unit tests for ReflectEngine
package reflect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/search"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLMProvider mock LLM提供者 / Mock LLM provider for testing
type mockLLMProvider struct {
	responses []*llm.ChatResponse
	errors    []error
	callIndex int
}

func (m *mockLLMProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return nil, fmt.Errorf("no more mock responses")
	}
	idx := m.callIndex
	m.callIndex++
	if m.errors != nil && idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	return m.responses[idx], nil
}

// conclusionJSON 构造conclusion JSON响应 / Build conclusion JSON response
func conclusionJSON(conclusion, reasoning string) string {
	b, _ := json.Marshal(map[string]string{"action": "conclusion", "conclusion": conclusion, "reasoning": reasoning})
	return string(b)
}

// needMoreJSON 构造need_more JSON响应 / Build need_more JSON response
func needMoreJSON(nextQuery, reasoning string) string {
	b, _ := json.Marshal(map[string]string{"action": "need_more", "next_query": nextQuery, "reasoning": reasoning})
	return string(b)
}

// setupTestEngine 创建测试用引擎 / Set up test engine with in-memory SQLite and simple tokenizer
func setupTestEngine(t *testing.T, mockLLM *mockLLMProvider) (*reflectpkg.ReflectEngine, *memory.Manager, store.MemoryStore) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	cfg := config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled: true,
				Path:    dbPath,
				Search: config.SearchConfig{
					BM25Weights: config.BM25WeightsConfig{
						Content:  10.0,
						Abstract: 5.0,
						Summary:  3.0,
					},
				},
				Tokenizer: config.TokenizerConfig{
					Provider: "simple",
				},
			},
		},
		Reflect: config.ReflectConfig{
			MaxRounds:    3,
			TokenBudget:  4096,
			RoundTimeout: 30 * time.Second,
			AutoSave:     true,
		},
	}

	stores, err := store.InitStores(context.Background(), cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		stores.Close()
		os.RemoveAll(dir)
	})

	mgr := memory.NewManager(stores.MemoryStore, nil, nil, nil, nil, nil)
	retriever := search.NewRetriever(stores.MemoryStore, nil, nil, nil, nil, config.RetrievalConfig{}, nil, nil)
	engine := reflectpkg.NewReflectEngine(retriever, mgr, mockLLM, cfg.Reflect)

	return engine, mgr, stores.MemoryStore
}

// seedMemory 向存储插入测试记忆 / Insert a test memory into the store
func seedMemory(t *testing.T, mgr *memory.Manager, content string) *model.Memory {
	t.Helper()
	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content: content,
	})
	require.NoError(t, err)
	return mem
}

// TestReflect_SingleRound LLM首轮返回结论 / LLM returns conclusion on first call
func TestReflect_SingleRound(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("The answer is 42", "Sufficient info found"), TotalTokens: 100},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	// Seed memory that contains the question keyword "meaning"
	seedMemory(t, mgr, "meaning of life is forty two")

	autoSave := true
	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "meaning",
		AutoSave: &autoSave,
	})

	require.NoError(t, err)
	assert.Equal(t, "The answer is 42", resp.Result)
	assert.Len(t, resp.Trace, 1)
	assert.Equal(t, 1, resp.Metadata.RoundsUsed)
	assert.NotEmpty(t, resp.NewMemoryID, "auto_save=true should produce a NewMemoryID")
}

// TestReflect_MultiRound LLM先返回need_more再返回结论 / LLM returns need_more then conclusion
func TestReflect_MultiRound(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("purpose", "Need more info"), TotalTokens: 100},
			{Content: conclusionJSON("Final answer after research", "Now I have enough"), TotalTokens: 100},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "meaning life complex purpose growth")

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question:  "meaning",
		MaxRounds: 3,
	})

	require.NoError(t, err)
	assert.Equal(t, "Final answer after research", resp.Result)
	assert.Len(t, resp.Trace, 2)
	assert.Equal(t, 2, resp.Metadata.RoundsUsed)
}

// TestReflect_MaxRoundsExceeded LLM一直返回need_more直到达到最大轮次 / LLM always returns need_more, stops at maxRounds
func TestReflect_MaxRoundsExceeded(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("alpha", "Need more"), TotalTokens: 100},
			{Content: needMoreJSON("beta", "Still need more"), TotalTokens: 100},
			{Content: needMoreJSON("gamma", "Need even more"), TotalTokens: 100},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "content alpha beta gamma data")

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question:  "content",
		MaxRounds: 3,
	})

	// Engine exhausts max rounds — result may be empty but no hard error
	require.NoError(t, err)
	assert.LessOrEqual(t, resp.Metadata.RoundsUsed, 3)
}

// TestReflect_AutoSaveTrue 验证AutoSave=true时写入记忆的字段 / Verify saved memory has kind=mental_model, source_type=reflect
func TestReflect_AutoSaveTrue(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("Mental model conclusion", "Derived from memories"), TotalTokens: 50},
		},
	}
	engine, mgr, memStore := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "knowledge mental model entry test")

	autoSave := true
	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "knowledge",
		AutoSave: &autoSave,
	})

	require.NoError(t, err)
	require.NotEmpty(t, resp.NewMemoryID)

	// Verify the saved memory has correct fields
	saved, err := memStore.Get(context.Background(), resp.NewMemoryID)
	require.NoError(t, err)
	assert.Equal(t, "mental_model", saved.Kind)
	assert.Equal(t, "reflect", saved.SourceType)
	assert.Equal(t, "Mental model conclusion", saved.Content)
}

// TestReflect_AutoSaveFalse 验证AutoSave=false时NewMemoryID为空 / Verify NewMemoryID is empty when AutoSave=false
func TestReflect_AutoSaveFalse(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("Some conclusion", "Good reasoning"), TotalTokens: 50},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "memory autosave false verification test")

	autoSave := false
	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "autosave",
		AutoSave: &autoSave,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, resp.Result)
	assert.Empty(t, resp.NewMemoryID, "AutoSave=false should not produce a NewMemoryID")
}

// TestReflect_QueryDedup LLM返回相同的next_query两次 / LLM returns same next_query twice, verify QueryDeduped=true
func TestReflect_QueryDedup(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("duplicate", "Need more info"), TotalTokens: 50},
			// Second round gets same query — engine detects dedup and breaks
			{Content: needMoreJSON("duplicate", "Same query again"), TotalTokens: 50},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "relevant duplicate dedup memory")

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question:  "relevant",
		MaxRounds: 5,
	})

	require.NoError(t, err)
	assert.True(t, resp.Metadata.QueryDeduped, "QueryDeduped should be true when same query appears twice")
}

// TestReflect_EmptyRetrieval 无记忆时应返回错误 / No memories in store should return error
func TestReflect_EmptyRetrieval(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{},
	}
	engine, _, _ := setupTestEngine(t, mockLLM)
	// Do NOT seed any memories

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "knowledge",
	})

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, model.ErrReflectNoMemories)
}

// TestReflect_InvalidRequest 空问题应返回错误 / Empty question should return error
func TestReflect_InvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *model.ReflectRequest
	}{
		{name: "nil request", req: nil},
		{name: "empty question", req: &model.ReflectRequest{Question: ""}},
		{name: "whitespace question", req: &model.ReflectRequest{Question: "   "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLLM := &mockLLMProvider{}
			engine, _, _ := setupTestEngine(t, mockLLM)

			resp, err := engine.Reflect(context.Background(), tt.req)
			assert.Nil(t, resp)
			require.Error(t, err)
			assert.ErrorIs(t, err, model.ErrReflectInvalidRequest)
		})
	}
}

// TestReflect_TokenBudgetExceeded 首轮3000tokens，第二轮2000tokens，预算4096，第二轮后停止
// First round uses 3000 tokens, second round 2000. Budget=4096. Verify stops after second round.
func TestReflect_TokenBudgetExceeded(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("followup", "Need more data"), TotalTokens: 3000},
			{Content: needMoreJSON("another", "Still need more"), TotalTokens: 2000},
			// Third call should never be reached: cumulative=5000 > 4096 budget after round 2
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "budget token memory followup another")

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question:    "budget",
		MaxRounds:   5,
		TokenBudget: 4096,
	})

	require.NoError(t, err)
	// After round 2 cumulative=5000 which exceeds 4096 budget, so at most 2 rounds
	assert.LessOrEqual(t, resp.Metadata.RoundsUsed, 2)
	assert.Equal(t, 2, mockLLM.callIndex, "Only 2 LLM calls should be made before budget exceeded")
}

// TestParseReflectOutput_ValidJSON 正常JSON响应使用json解析方式 / Normal JSON uses parse_method="json"
func TestParseReflectOutput_ValidJSON(t *testing.T) {
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("Valid JSON conclusion", "Parsed as JSON"), TotalTokens: 50},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "json parse valid test data")

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "json",
	})

	require.NoError(t, err)
	require.Len(t, resp.Trace, 1)
	assert.Equal(t, reflectpkg.ParseMethodJSON, resp.Trace[0].ParseMethod)
	assert.Equal(t, 0, resp.Metadata.ParseFallbacks)
}

// TestParseReflectOutput_ExtractFromText LLM返回含嵌入JSON的文本 / LLM returns text with embedded JSON, parse_method="extract"
func TestParseReflectOutput_ExtractFromText(t *testing.T) {
	embeddedJSON := `Here is: {"action":"conclusion","conclusion":"extracted","reasoning":"from text"} end`
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: embeddedJSON, TotalTokens: 80},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "extract parse test memory data")

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "extract",
	})

	require.NoError(t, err)
	require.Len(t, resp.Trace, 1)
	assert.Equal(t, reflectpkg.ParseMethodExtract, resp.Trace[0].ParseMethod)
	assert.Equal(t, "extracted", resp.Result)
	assert.Greater(t, resp.Metadata.ParseFallbacks, 0)
}

// TestParseReflectOutput_Fallback LLM返回纯文本，重试也返回文本，使用fallback解析 / LLM returns pure text, retry also text, parse_method="fallback"
func TestParseReflectOutput_Fallback(t *testing.T) {
	pureText := "This is just plain text without any JSON structure at all"
	// First call: main round LLM response
	// Second call: L3 retry from parseOutput
	mockLLM := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: pureText, TotalTokens: 60},
			{Content: "Still plain text response", TotalTokens: 40}, // retry response, also not valid JSON
		},
	}
	engine, mgr, _ := setupTestEngine(t, mockLLM)
	seedMemory(t, mgr, "fallback parse test plain memory")

	resp, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "fallback",
	})

	require.NoError(t, err)
	require.Len(t, resp.Trace, 1)
	assert.Equal(t, reflectpkg.ParseMethodFallback, resp.Trace[0].ParseMethod)
	assert.Greater(t, resp.Metadata.ParseFallbacks, 0)
}
