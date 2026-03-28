// Package memory_test Manager.Create auto_extract 集成测试 / Manager.Create auto_extract integration tests
package memory_test

import (
	"context"
	"encoding/json"
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

func setupManagerWithExtractor(t *testing.T, mockLLM *mockLLMProvider) (*memory.Manager, *store.Stores) {
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
	}

	stores, err := store.InitStores(context.Background(), cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		stores.Close()
		os.RemoveAll(dir)
	})

	graphManager := memory.NewGraphManager(stores.GraphStore)
	extractCfg := config.ExtractConfig{
		MaxEntities:      20,
		MaxRelations:     30,
		NormalizeEnabled: false,
		Timeout:          30 * time.Second,
	}

	var extractor *memory.Extractor
	if mockLLM != nil {
		extractor = memory.NewExtractor(mockLLM, graphManager, stores.MemoryStore, extractCfg)
	}

	mgr := memory.NewManager(stores.MemoryStore, nil, nil, nil, nil, extractor, nil, memory.ManagerConfig{})
	return mgr, stores
}

func TestManagerCreate_AutoExtractTrue(t *testing.T) {
	entJSON, _ := json.Marshal(map[string]any{
		"entities":  []map[string]string{{"name": "Alice", "entity_type": "person", "description": "Dev"}},
		"relations": []any{},
	})
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{{Content: string(entJSON)}},
	}

	mgr, stores := setupManagerWithExtractor(t, mock)

	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content:     "Alice is a developer",
		Scope:       "test",
		AutoExtract: true,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, mem.ID)

	// 自动提取在后台 goroutine 中异步执行，轮询等待结果
	// Auto-extract runs asynchronously in a goroutine; poll until the entity appears
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		entities, err := stores.GraphStore.ListEntities(context.Background(), "test", "person", 10)
		assert.NoError(c, err)
		if assert.Len(c, entities, 1) {
			assert.Equal(c, "Alice", entities[0].Name)
		}
	}, 5*time.Second, 20*time.Millisecond)
}

func TestManagerCreate_AutoExtractFalse(t *testing.T) {
	mock := &mockLLMProvider{} // 不应被调用 / Should not be called
	mgr, stores := setupManagerWithExtractor(t, mock)

	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content:     "Bob is a designer",
		Scope:       "test",
		AutoExtract: false,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, mem.ID)

	// 不应有实体 / Should have no entities
	entities, err := stores.GraphStore.ListEntities(context.Background(), "test", "", 10)
	require.NoError(t, err)
	assert.Len(t, entities, 0)
}

func TestManagerCreate_ExtractorNil(t *testing.T) {
	mgr, _ := setupManagerWithExtractor(t, nil) // extractor is nil

	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content:     "Charlie is a manager",
		AutoExtract: true, // 即使 true 也不触发
	})

	require.NoError(t, err)
	assert.NotEmpty(t, mem.ID)
}

func TestManagerCreate_ExtractFails_MemoryStillCreated(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{nil},
		errors:    []error{assert.AnError},
	}
	mgr, _ := setupManagerWithExtractor(t, mock)

	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content:     "Dave works at TechCo",
		Scope:       "test",
		AutoExtract: true,
	})

	// 记忆应该成功创建（最佳努力）/ Memory should be created (best-effort)
	require.NoError(t, err)
	assert.NotEmpty(t, mem.ID)
	assert.Equal(t, "Dave works at TechCo", mem.Content)
}
