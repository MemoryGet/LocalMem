// Package memory_test Extractor 单元测试 / Extractor unit tests
package memory_test

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
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLMProvider mock LLM提供者 / Mock LLM provider for extractor tests
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

// entitiesJSON 构建实体抽取的 JSON 响应 / Build entity extraction JSON response
func entitiesJSON(entities []map[string]string, relations []map[string]string) string {
	result := map[string]any{
		"entities":  entities,
		"relations": relations,
	}
	b, _ := json.Marshal(result)
	return string(b)
}

// normalizeMatchJSON 构建规范化匹配 JSON / Build normalization match JSON
func normalizeMatchJSON(match bool, matchedEntity string) string {
	b, _ := json.Marshal(map[string]any{"match": match, "matched_entity": matchedEntity})
	return string(b)
}

// setupExtractor 创建测试用 Extractor / Set up test Extractor with real SQLite
func setupExtractor(t *testing.T, mockLLM *mockLLMProvider, cfg *config.ExtractConfig) (*memory.Extractor, *memory.GraphManager, store.MemoryStore, *store.Stores) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	storeCfg := config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled: true,
				Path:    dbPath,
				Search: config.SearchConfig{
					BM25Weights: config.BM25WeightsConfig{
						Content:  10.0,
						Excerpt: 5.0,
						Summary:  3.0,
					},
				},
				Tokenizer: config.TokenizerConfig{
					Provider: "simple",
				},
			},
		},
	}

	stores, err := store.InitStores(context.Background(), storeCfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		stores.Close()
		os.RemoveAll(dir)
	})

	graphManager := memory.NewGraphManager(stores.GraphStore)

	extractCfg := config.ExtractConfig{
		MaxEntities:         20,
		MaxRelations:        30,
		NormalizeEnabled:    true,
		NormalizeCandidates: 20,
		Timeout:             30 * time.Second,
	}
	if cfg != nil {
		extractCfg = *cfg
	}

	ext := memory.NewExtractor(mockLLM, graphManager, stores.MemoryStore, extractCfg)
	return ext, graphManager, stores.MemoryStore, stores
}

func TestExtract_BasicEntitiesAndRelations(t *testing.T) {
	llmResp := entitiesJSON(
		[]map[string]string{
			{"name": "Alice", "entity_type": "person", "description": "Engineer"},
			{"name": "Go", "entity_type": "tool", "description": "Programming language"},
		},
		[]map[string]string{
			{"source": "Alice", "target": "Go", "relation_type": "uses"},
		},
	)

	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{{Content: llmResp}},
	}
	ext, _, _, _ := setupExtractor(t, mock, nil)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		MemoryID: "mem-1",
		Content:  "Alice uses Go for backend development",
		Scope:    "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 2)
	assert.Len(t, resp.Relations, 1)
	assert.Equal(t, "Alice", resp.Entities[0].Name)
	assert.Equal(t, "person", resp.Entities[0].EntityType)
	assert.False(t, resp.Entities[0].Reused)
	assert.Equal(t, "uses", resp.Relations[0].RelationType)
	assert.False(t, resp.Relations[0].Skipped)
}

func TestExtract_EntityReuse_ExactMatch(t *testing.T) {
	// 先种一个实体 / Seed an entity first
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: entitiesJSON(
				[]map[string]string{{"name": "Go", "entity_type": "tool", "description": "Lang"}},
				nil,
			)},
		},
	}
	ext, graphManager, _, _ := setupExtractor(t, mock, nil)

	// 种实体 / Seed entity
	_, err := graphManager.CreateEntity(context.Background(), &model.CreateEntityRequest{
		Name: "Go", EntityType: "tool", Scope: "test",
	})
	require.NoError(t, err)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "Go is great",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1)
	assert.True(t, resp.Entities[0].Reused)
	assert.Equal(t, "Go", resp.Entities[0].Name)
}

func TestExtract_EntityNormalize_LLMMatch(t *testing.T) {
	// LLM 第1次调用: 抽取实体（返回 "阿里巴巴"）
	// LLM 第2次调用: 规范化（匹配 "Alibaba"）
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: entitiesJSON(
				[]map[string]string{{"name": "阿里巴巴", "entity_type": "org", "description": "Company"}},
				nil,
			)},
			{Content: normalizeMatchJSON(true, "Alibaba")},
		},
	}
	ext, graphManager, _, _ := setupExtractor(t, mock, nil)

	// 种 Alibaba 实体 / Seed Alibaba entity
	_, err := graphManager.CreateEntity(context.Background(), &model.CreateEntityRequest{
		Name: "Alibaba", EntityType: "org", Scope: "test",
	})
	require.NoError(t, err)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "阿里巴巴是一家公司",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1)
	assert.True(t, resp.Entities[0].Reused)
	assert.Equal(t, "Alibaba", resp.Entities[0].Name)
	assert.Equal(t, "阿里巴巴", resp.Entities[0].NormalizedFrom)
	assert.Equal(t, 1, resp.Normalized)
}

func TestExtract_EntityNormalize_LLMNoMatch(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: entitiesJSON(
				[]map[string]string{{"name": "TechCo", "entity_type": "org", "description": "New company"}},
				nil,
			)},
			{Content: normalizeMatchJSON(false, "")},
		},
	}
	ext, graphManager, _, _ := setupExtractor(t, mock, nil)

	// 种不同实体 / Seed different entity
	_, err := graphManager.CreateEntity(context.Background(), &model.CreateEntityRequest{
		Name: "Alibaba", EntityType: "org", Scope: "test",
	})
	require.NoError(t, err)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "TechCo is a startup",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1)
	assert.False(t, resp.Entities[0].Reused)
	assert.Equal(t, "TechCo", resp.Entities[0].Name)
}

func TestExtract_NormalizeDisabled(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: entitiesJSON(
				[]map[string]string{{"name": "阿里", "entity_type": "org", "description": "Ali"}},
				nil,
			)},
		},
	}
	cfg := &config.ExtractConfig{
		MaxEntities:      20,
		MaxRelations:     30,
		NormalizeEnabled: false, // 关闭规范化
		Timeout:          30 * time.Second,
	}
	ext, graphManager, _, _ := setupExtractor(t, mock, cfg)

	// 种 Alibaba / Seed Alibaba
	_, err := graphManager.CreateEntity(context.Background(), &model.CreateEntityRequest{
		Name: "Alibaba", EntityType: "org", Scope: "test",
	})
	require.NoError(t, err)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "阿里很大",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1)
	assert.False(t, resp.Entities[0].Reused) // 精确匹配不到，也不走 LLM
	assert.Equal(t, "阿里", resp.Entities[0].Name)
}

func TestExtract_NormalizeLLMFailed_CreatesNew(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: entitiesJSON(
				[]map[string]string{{"name": "阿里巴巴", "entity_type": "org", "description": "Company"}},
				nil,
			)},
		},
		errors: []error{nil, fmt.Errorf("LLM timeout")}, // 第2次（规范化）失败
	}
	ext, graphManager, _, _ := setupExtractor(t, mock, nil)

	_, err := graphManager.CreateEntity(context.Background(), &model.CreateEntityRequest{
		Name: "Alibaba", EntityType: "org", Scope: "test",
	})
	require.NoError(t, err)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "阿里巴巴是一家公司",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1)
	assert.False(t, resp.Entities[0].Reused) // 规范化失败，创建新实体
	assert.Equal(t, "阿里巴巴", resp.Entities[0].Name)
}

func TestExtract_RelationDedup(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: entitiesJSON(
				[]map[string]string{
					{"name": "Alice", "entity_type": "person", "description": "Dev"},
					{"name": "Go", "entity_type": "tool", "description": "Lang"},
				},
				[]map[string]string{
					{"source": "Alice", "target": "Go", "relation_type": "uses"},
				},
			)},
		},
	}
	ext, graphManager, _, _ := setupExtractor(t, mock, nil)

	// 预创建实体和关系 / Pre-create entities and relation
	alice, err := graphManager.CreateEntity(context.Background(), &model.CreateEntityRequest{
		Name: "Alice", EntityType: "person", Scope: "test",
	})
	require.NoError(t, err)
	goEnt, err := graphManager.CreateEntity(context.Background(), &model.CreateEntityRequest{
		Name: "Go", EntityType: "tool", Scope: "test",
	})
	require.NoError(t, err)
	_, err = graphManager.CreateRelation(context.Background(), &model.CreateEntityRelationRequest{
		SourceID: alice.ID, TargetID: goEnt.ID, RelationType: "uses",
	})
	require.NoError(t, err)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "Alice uses Go",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Relations, 1)
	assert.True(t, resp.Relations[0].Skipped)
}

func TestExtract_EmptyContent(t *testing.T) {
	mock := &mockLLMProvider{}
	ext, _, _, _ := setupExtractor(t, mock, nil)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 0)
	assert.Len(t, resp.Relations, 0)
}

func TestExtract_LLMFailed(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{nil},
		errors:    []error{fmt.Errorf("api error")},
	}
	ext, _, _, _ := setupExtractor(t, mock, nil)

	_, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "some text",
		Scope:   "test",
	})

	assert.Error(t, err)
	assert.ErrorIs(t, err, model.ErrExtractLLMFailed)
}

func TestExtract_ParseFallback(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: "this is not json at all"},
			{Content: "still not json"}, // retry also fails
		},
	}
	ext, _, _, _ := setupExtractor(t, mock, nil)

	_, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "some text",
		Scope:   "test",
	})

	assert.Error(t, err)
	assert.ErrorIs(t, err, model.ErrExtractParseFailed)
}

func TestParseExtractOutput_ValidJSON(t *testing.T) {
	input := entitiesJSON(
		[]map[string]string{{"name": "Alice", "entity_type": "person", "description": "Dev"}},
		nil,
	)

	// 直接测试 exported helper（如果有）或通过 Extract 间接测试
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{{Content: input}},
	}
	ext, _, _, _ := setupExtractor(t, mock, nil)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "Alice is a developer",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1)
	assert.Equal(t, "Alice", resp.Entities[0].Name)
}

func TestParseExtractOutput_InvalidEntityType(t *testing.T) {
	input := entitiesJSON(
		[]map[string]string{
			{"name": "Alice", "entity_type": "person", "description": "Dev"},
			{"name": "Unknown", "entity_type": "invalid_type", "description": "Bad"},
		},
		nil,
	)

	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{{Content: input}},
	}
	ext, _, _, _ := setupExtractor(t, mock, nil)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "Alice and Unknown entity",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1) // invalid_type 被过滤
	assert.Equal(t, "Alice", resp.Entities[0].Name)
}

func TestParseExtractOutput_ExtractFromText(t *testing.T) {
	// LLM 返回 JSON 包裹在文本中 / JSON embedded in text
	input := `Here are the entities: {"entities": [{"name": "Bob", "entity_type": "person", "description": "PM"}], "relations": []}`

	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{{Content: input}},
	}
	ext, _, _, _ := setupExtractor(t, mock, nil)

	resp, err := ext.Extract(context.Background(), &model.ExtractRequest{
		Content: "Bob is a project manager",
		Scope:   "test",
	})

	require.NoError(t, err)
	assert.Len(t, resp.Entities, 1)
	assert.Equal(t, "Bob", resp.Entities[0].Name)
}
