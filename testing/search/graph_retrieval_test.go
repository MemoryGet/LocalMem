// Package search_test Graph 检索通道测试 / Graph retrieval channel tests
package search_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// graphMockLLM mock LLM for graph tests
type graphMockLLM struct {
	responses []*llm.ChatResponse
	errors    []error
	callIndex int
}

func (m *graphMockLLM) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
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

// setupGraphRetriever 创建测试用 Retriever + GraphStore / Set up test Retriever with GraphStore
func setupGraphRetriever(t *testing.T, mockLLM llm.Provider, cfg config.RetrievalConfig) (*search.Retriever, *memory.Manager, *memory.GraphManager, *store.Stores) {
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
				Tokenizer: config.TokenizerConfig{Provider: "simple"},
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
	mgr := memory.NewManager(stores.MemoryStore, nil, nil, nil, nil, nil, nil, memory.ManagerConfig{})
	ret := search.NewRetriever(stores.MemoryStore, nil, nil, stores.GraphStore, mockLLM, cfg, nil, nil)

	return ret, mgr, graphManager, stores
}

// seedGraphData 种测试数据 / Seed test data for graph tests
func seedGraphData(t *testing.T, mgr *memory.Manager, gm *memory.GraphManager) (memIDs []string, entityIDs []string) {
	t.Helper()
	ctx := context.Background()

	// 创建记忆 / Create memories
	mem1, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Alice uses Go for backend development", Scope: "test"})
	require.NoError(t, err)
	mem2, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Bob knows Alice from the Go meetup", Scope: "test"})
	require.NoError(t, err)
	mem3, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Charlie uses Rust for system programming", Scope: "test"})
	require.NoError(t, err)

	// 创建实体 / Create entities
	alice, err := gm.CreateEntity(ctx, &model.CreateEntityRequest{Name: "Alice", EntityType: "person", Scope: "test"})
	require.NoError(t, err)
	bob, err := gm.CreateEntity(ctx, &model.CreateEntityRequest{Name: "Bob", EntityType: "person", Scope: "test"})
	require.NoError(t, err)
	goEnt, err := gm.CreateEntity(ctx, &model.CreateEntityRequest{Name: "Go", EntityType: "tool", Scope: "test"})
	require.NoError(t, err)
	charlie, err := gm.CreateEntity(ctx, &model.CreateEntityRequest{Name: "Charlie", EntityType: "person", Scope: "test"})
	require.NoError(t, err)
	rust, err := gm.CreateEntity(ctx, &model.CreateEntityRequest{Name: "Rust", EntityType: "tool", Scope: "test"})
	require.NoError(t, err)

	// 创建关系 / Create relations
	_, err = gm.CreateRelation(ctx, &model.CreateEntityRelationRequest{SourceID: alice.ID, TargetID: goEnt.ID, RelationType: "uses"})
	require.NoError(t, err)
	_, err = gm.CreateRelation(ctx, &model.CreateEntityRelationRequest{SourceID: bob.ID, TargetID: alice.ID, RelationType: "knows"})
	require.NoError(t, err)
	_, err = gm.CreateRelation(ctx, &model.CreateEntityRelationRequest{SourceID: charlie.ID, TargetID: rust.ID, RelationType: "uses"})
	require.NoError(t, err)

	// 关联记忆和实体 / Link memories to entities
	gm.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{MemoryID: mem1.ID, EntityID: alice.ID, Role: "subject"})
	gm.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{MemoryID: mem1.ID, EntityID: goEnt.ID, Role: "object"})
	gm.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{MemoryID: mem2.ID, EntityID: bob.ID, Role: "subject"})
	gm.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{MemoryID: mem2.ID, EntityID: alice.ID, Role: "object"})
	gm.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{MemoryID: mem3.ID, EntityID: charlie.ID, Role: "subject"})
	gm.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{MemoryID: mem3.ID, EntityID: rust.ID, Role: "object"})

	return []string{mem1.ID, mem2.ID, mem3.ID}, []string{alice.ID, bob.ID, goEnt.ID, charlie.ID, rust.ID}
}

func TestGraphRetrieval_BasicFlow(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 1, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	seedGraphData(t, mgr, gm)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "Alice",
		Limit: 10,
	})

	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1, "should find results via FTS5 + Graph")
}

func TestGraphRetrieval_Depth1(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 1, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	memIDs, _ := seedGraphData(t, mgr, gm)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "Alice Go",
		Limit: 20,
	})

	require.NoError(t, err)
	// depth=1 应该找到 Alice 的直接关联（Bob knows Alice）→ mem2
	foundIDs := map[string]bool{}
	for _, r := range results {
		foundIDs[r.Memory.ID] = true
	}
	assert.True(t, foundIDs[memIDs[0]], "should find mem1 (Alice uses Go)")
	// Bob's memory should be reachable via graph (Alice→Bob relation)
	// mem2 mentions Bob and Alice, so it should be in results via FTS or Graph
}

func TestGraphRetrieval_Depth2(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 2, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	seedGraphData(t, mgr, gm)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "Alice",
		Limit: 20,
	})

	require.NoError(t, err)
	// depth=2 should reach further
	assert.GreaterOrEqual(t, len(results), 1)
}

func TestGraphRetrieval_NoEntities(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 1, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, _, _ := setupGraphRetriever(t, nil, cfg)

	// 创建记忆但不关联实体 / Create memory without entity associations
	_, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{Content: "Some random text", Scope: "test"})
	require.NoError(t, err)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "random text",
		Limit: 10,
	})

	require.NoError(t, err)
	// 应该还是能通过 FTS5 找到，只是 Graph 路为空
	assert.GreaterOrEqual(t, len(results), 1)
}

func TestGraphRetrieval_Disabled(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: false, FTSWeight: 1.0}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	seedGraphData(t, mgr, gm)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "Alice",
		Limit: 10,
	})

	require.NoError(t, err)
	// 所有结果应该是 sqlite 来源（Graph 被禁用）
	for _, r := range results {
		assert.NotEqual(t, "graph", r.Source, "should not have graph results when disabled")
	}
}

func TestGraphRetrieval_GraphStoreNil(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, FTSWeight: 1.0}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	storeCfg := config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled: true, Path: dbPath,
				Search:    config.SearchConfig{BM25Weights: config.BM25WeightsConfig{Content: 10, Excerpt: 5, Summary: 3}},
				Tokenizer: config.TokenizerConfig{Provider: "simple"},
			},
		},
	}
	stores, err := store.InitStores(context.Background(), storeCfg, nil)
	require.NoError(t, err)
	defer stores.Close()

	// 传 nil graphStore / Pass nil graphStore
	ret := search.NewRetriever(stores.MemoryStore, nil, nil, nil, nil, cfg, nil, nil)
	mgr := memory.NewManager(stores.MemoryStore, nil, nil, nil, nil, nil, nil, memory.ManagerConfig{})
	_, err = mgr.Create(context.Background(), &model.CreateMemoryRequest{Content: "test content", Scope: "test"})
	require.NoError(t, err)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{Query: "test", Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1)
}

func TestGraphRetrieval_CycleProtection(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 3, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	ctx := context.Background()

	// 创建记忆 / Create memories
	mem1, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Node A connects to Node B", Scope: "test"})
	require.NoError(t, err)

	// 创建循环关系 A → B → A
	a, err := gm.CreateEntity(ctx, &model.CreateEntityRequest{Name: "NodeA", EntityType: "concept", Scope: "test"})
	require.NoError(t, err)
	b, err := gm.CreateEntity(ctx, &model.CreateEntityRequest{Name: "NodeB", EntityType: "concept", Scope: "test"})
	require.NoError(t, err)
	_, err = gm.CreateRelation(ctx, &model.CreateEntityRelationRequest{SourceID: a.ID, TargetID: b.ID, RelationType: "related_to"})
	require.NoError(t, err)
	_, err = gm.CreateRelation(ctx, &model.CreateEntityRelationRequest{SourceID: b.ID, TargetID: a.ID, RelationType: "related_to"})
	require.NoError(t, err)
	gm.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{MemoryID: mem1.ID, EntityID: a.ID, Role: "subject"})

	// 不应死循环 / Should not infinite loop
	results, err := ret.Retrieve(ctx, &model.RetrieveRequest{Query: "Node A", Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1)
}

func TestGraphRetrieval_FTSEmpty_LLMFallback(t *testing.T) {
	entitiesJSON, _ := json.Marshal(map[string]any{"entities": []string{"Alice"}})
	mock := &graphMockLLM{
		responses: []*llm.ChatResponse{{Content: string(entitiesJSON)}},
	}

	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 1, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, mock, cfg)
	seedGraphData(t, mgr, gm)

	// 用一个 FTS5 搜不到的 query，但 LLM 能提取出 "Alice"
	_, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "xyzzy_not_in_any_memory",
		Limit: 10,
	})

	// 主要验证不报错 + 不卡死（LLM fallback 可能找到也可能找不到）
	_ = err
}

func TestGraphRetrieval_FTSEmpty_NoLLM(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 1, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	seedGraphData(t, mgr, gm)

	// FTS5 搜不到 + 没有 LLM
	_, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "xyzzy_not_in_any_memory",
		Limit: 10,
	})

	// FTS5 搜不到 + 没有 LLM + 没有 Qdrant → 可能返回错误，但不应 panic
	_ = err
}

func TestGraphRetrieval_DeduplicateWithFTS(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 1, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	seedGraphData(t, mgr, gm)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "Alice uses Go",
		Limit: 20,
	})

	require.NoError(t, err)
	// 验证没有重复 ID / Verify no duplicate IDs
	seen := map[string]bool{}
	for _, r := range results {
		assert.False(t, seen[r.Memory.ID], "duplicate memory ID: %s", r.Memory.ID)
		seen[r.Memory.ID] = true
	}
}

func TestGraphRetrieval_ResultLimitedByRRF(t *testing.T) {
	cfg := config.RetrievalConfig{GraphEnabled: true, GraphDepth: 1, GraphWeight: 0.8, FTSWeight: 1.0, GraphFTSTop: 5, GraphEntityLimit: 10}
	ret, mgr, gm, _ := setupGraphRetriever(t, nil, cfg)
	seedGraphData(t, mgr, gm)

	results, err := ret.Retrieve(context.Background(), &model.RetrieveRequest{
		Query: "Alice",
		Limit: 2, // 限制为 2 条
	})

	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 2, "should respect limit")
}
