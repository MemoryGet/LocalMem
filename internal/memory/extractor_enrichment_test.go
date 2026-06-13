// Package memory 内部测试：验证 extractLLMOutput 解析逻辑（需访问未导出类型）。
// 必须放在 internal/memory/ 目录内才能访问未导出的 extractLLMOutput 类型。
// Internal test: verify extractLLMOutput parsing. Must live in internal/memory/ to
// access the unexported extractLLMOutput type (the testing/memory/ external test
// directory cannot reach unexported symbols regardless of package name).
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enrichMockLLM 内部测试用 mock LLM（顺序返回预设响应）/ Internal-test mock LLM returning canned responses in order
type enrichMockLLM struct {
	responses []*llm.ChatResponse
	callIndex int
}

func (m *enrichMockLLM) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return nil, fmt.Errorf("no more mock responses")
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

// newEnrichExtractor 创建带真实 SQLite 存储的 Extractor（内部测试）/ Build an Extractor backed by a real SQLite store
func newEnrichExtractor(t *testing.T, mockLLM llm.Provider) (*Extractor, store.MemoryStore) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	storeCfg := config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled: true,
				Path:    dbPath,
				Search: config.SearchConfig{
					BM25Weights: config.BM25WeightsConfig{Content: 10.0, Excerpt: 5.0, Summary: 3.0},
				},
				Tokenizer: config.TokenizerConfig{Provider: "simple"},
			},
		},
	}

	stores, err := store.InitStores(context.Background(), storeCfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() { stores.Close() })

	graphManager := NewGraphManager(stores.GraphStore)
	cfg := config.ExtractConfig{
		MaxEntities:         20,
		MaxRelations:        30,
		NormalizeEnabled:    true,
		NormalizeCandidates: 20,
		Timeout:             30 * time.Second,
	}
	ext := NewExtractor(mockLLM, graphManager, stores.MemoryStore, nil, cfg)
	return ext, stores.MemoryStore
}

// TestExtractLLMOutput_ParsesSummary 校验 summary-only 输出解析 / Verify summary-only output parsing
func TestExtractLLMOutput_ParsesSummary(t *testing.T) {
	raw := `{"summary":"Caroline was promoted at Google.","entities":[],"relations":[]}`
	var output extractLLMOutput
	err := json.Unmarshal([]byte(raw), &output)
	require.NoError(t, err)
	assert.Equal(t, "Caroline was promoted at Google.", output.Summary)
	assert.Empty(t, output.Entities)
	assert.Empty(t, output.Relations)
	// L1 守卫应接受仅含 summary 的输出 / L1 guard should accept summary-only output
	assert.True(t, output.Summary != "" || len(output.Entities) > 0 || len(output.Relations) > 0,
		"L1 guard should accept summary-only output")
}

// TestExtractLLMOutput_ParsesSummaryWithEntities 校验 summary + entities 输出 / Verify summary + entities output
func TestExtractLLMOutput_ParsesSummaryWithEntities(t *testing.T) {
	raw := `{"summary":"Alice joined Acme Corp.","entities":[{"name":"Alice","entity_type":"person","description":""}],"relations":[]}`
	var output extractLLMOutput
	err := json.Unmarshal([]byte(raw), &output)
	require.NoError(t, err)
	assert.Equal(t, "Alice joined Acme Corp.", output.Summary)
	assert.Len(t, output.Entities, 1)
	assert.Equal(t, "Alice", output.Entities[0].Name)
	assert.Equal(t, "person", output.Entities[0].EntityType)
}

// TestExtractLLMOutput_EmptySummaryStillParsed 校验空 summary 仍能解析实体 / Verify empty summary still parses entities
func TestExtractLLMOutput_EmptySummaryStillParsed(t *testing.T) {
	raw := `{"summary":"","entities":[{"name":"Bob","entity_type":"person","description":""}],"relations":[]}`
	var output extractLLMOutput
	err := json.Unmarshal([]byte(raw), &output)
	require.NoError(t, err)
	assert.Empty(t, output.Summary)
	assert.Len(t, output.Entities, 1)
	assert.Equal(t, "Bob", output.Entities[0].Name)
}

// TestExtractLLMOutput_L2RegexMatchesSummaryKey 通过真实 parseExtractOutput 验证 L1/L2 路径接受 summary-only 输出。
// Exercise the real parseExtractOutput so the L1 guard (summary non-empty) is verified end-to-end.
func TestExtractLLMOutput_L2RegexMatchesSummaryKey(t *testing.T) {
	raw := `{"summary":"Caroline was promoted.","entities":[],"relations":[]}`
	assert.True(t, strings.Contains(raw, `"summary"`), "summary key present")

	// nil provider 不会被调用：L1 直接 JSON 解析成功（summary 非空）即返回，无需触发 L3 重试。
	// nil provider is never invoked: L1 direct unmarshal succeeds (summary non-empty), so no L3 retry.
	output, parseLevel := parseExtractOutput(t.Context(), raw, nil, nil)
	require.NotNil(t, output, "summary-only output must parse via L1 guard")
	assert.Equal(t, "Caroline was promoted.", output.Summary)
	assert.Equal(t, ExtractParseJSON, parseLevel, "should resolve at L1 direct JSON parse")
}

// TestExtractor_WritesSummaryToStore 校验抽取后将生成的摘要写回空摘要记忆。
// Verify the generated summary is written back to a memory that started with an empty summary.
func TestExtractor_WritesSummaryToStore(t *testing.T) {
	mock := &enrichMockLLM{responses: []*llm.ChatResponse{
		{Content: `{"summary":"Alice joined Acme Corp.","entities":[],"relations":[]}`},
	}}
	ext, memStore := newEnrichExtractor(t, mock)
	ctx := context.Background()

	mem := &model.Memory{Content: "Alice joined Acme Corp.", Scope: "test"}
	require.NoError(t, memStore.Create(ctx, mem))
	require.Empty(t, mem.Summary, "memory must start with an empty summary")

	_, err := ext.Extract(ctx, &model.ExtractRequest{
		MemoryID: mem.ID,
		Content:  mem.Content,
		Scope:    mem.Scope,
	})
	require.NoError(t, err)

	got, err := memStore.Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice joined Acme Corp.", got.Summary)
}

// TestExtractor_WritesSummary_MemoryIDNotFound 校验 MemoryID 不存在时 Extract 仍成功（摘要写回失败非致命）。
// Extractor is called with a MemoryID that does not exist in the store.
// Extract() must still succeed (return nil error) — summary write failure is non-fatal.
func TestExtractor_WritesSummary_MemoryIDNotFound(t *testing.T) {
	mock := &enrichMockLLM{responses: []*llm.ChatResponse{
		{Content: `{"summary":"Some summary.","entities":[],"relations":[]}`},
	}}
	ext, _ := newEnrichExtractor(t, mock)
	ctx := context.Background()

	_, err := ext.Extract(ctx, &model.ExtractRequest{
		MemoryID: "nonexistent-id-12345",
		Content:  "some content",
		Scope:    "test",
	})
	assert.NoError(t, err, "Extract must succeed even when MemoryID is not found")
}

// countingEnqueuer 统计 Enqueue 调用次数的 mock（线程安全）/ Thread-safe mock counting Enqueue calls
type countingEnqueuer struct {
	mu    sync.Mutex
	count int
}

func (e *countingEnqueuer) Enqueue(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
	return "task-id", nil
}

func (e *countingEnqueuer) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

// TestManager_SkipsExtractionForHookSource 校验 source_type=hook 的记忆不触发实体抽取。
// Verify memories with source_type=hook never trigger the Extractor (operational logs, not semantic memories).
func TestManager_SkipsExtractionForHookSource(t *testing.T) {
	// 真实 Extractor 提供非 nil 抽取器，确保 handleAutoExtract 走到入队分支（除非守卫拦截）。
	// A real Extractor makes m.extractor non-nil so handleAutoExtract reaches the enqueue branch unless the guard fires.
	ext, _ := newEnrichExtractor(t, &enrichMockLLM{})
	enq := &countingEnqueuer{}

	mgr := NewManager(ManagerDeps{
		MemStore:  ext.memStore, // 复用 Extractor 内部存储 / reuse the Extractor's store
		Extractor: ext,
		TaskQueue: enq,
	})

	tests := []struct {
		name       string
		sourceType string
		wantCalls  int
	}{
		{name: "hook source skips extraction", sourceType: "hook", wantCalls: 0},
		{name: "non-hook source triggers extraction", sourceType: "chat", wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := enq.calls()
			mem := &model.Memory{ID: "m-" + tt.sourceType, SourceType: tt.sourceType}
			mgr.handleAutoExtract(context.Background(), mem, true /* autoExtract */)
			assert.Equal(t, tt.wantCalls, enq.calls()-before,
				"enqueue calls for source_type=%q", tt.sourceType)
		})
	}
}

// TestExtractor_SkipsOverwriteIfSummaryExists 校验已有摘要不被覆盖（RetainTool 预填充场景）。
// Verify a pre-populated summary is not overwritten by extraction.
func TestExtractor_SkipsOverwriteIfSummaryExists(t *testing.T) {
	mock := &enrichMockLLM{responses: []*llm.ChatResponse{
		{Content: `{"summary":"A different generated summary.","entities":[],"relations":[]}`},
	}}
	ext, memStore := newEnrichExtractor(t, mock)
	ctx := context.Background()

	mem := &model.Memory{Content: "Alice joined Acme Corp.", Summary: "Pre-existing.", Scope: "test"}
	require.NoError(t, memStore.Create(ctx, mem))

	_, err := ext.Extract(ctx, &model.ExtractRequest{
		MemoryID: mem.ID,
		Content:  mem.Content,
		Scope:    mem.Scope,
	})
	require.NoError(t, err)

	got, err := memStore.Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, "Pre-existing.", got.Summary, "existing summary must not be overwritten")
}
