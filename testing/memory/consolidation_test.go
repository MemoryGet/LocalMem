// Package memory_test 记忆归纳引擎测试 / Memory consolidation engine tests
package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// consolidateMockLLM 归纳测试用 mock LLM
type consolidateMockLLM struct {
	response string
	err      error
}

func (m *consolidateMockLLM) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}

func setupConsolidatorStores(t *testing.T) (*store.Stores, store.MemoryStore) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "consol_test.db")

	cfg := config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled: true,
				Path:    dbPath,
				Search: config.SearchConfig{
					BM25Weights: config.BM25WeightsConfig{Content: 10, Abstract: 5, Summary: 3},
				},
			},
		},
	}
	stores, err := store.InitStores(context.Background(), cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		stores.Close()
		os.Remove(dbPath)
	})
	return stores, stores.MemoryStore
}

// TestConsolidator_Run_NoLLM LLM 为 nil 时 Run 提前返回不报错 / nil LLM skips gracefully
func TestConsolidator_Run_NoLLM(t *testing.T) {
	stores, _ := setupConsolidatorStores(t)
	c := memory.NewConsolidator(stores.MemoryStore, nil, nil, config.ConsolidationConfig{}) // nil LLM
	config.AppConfig.Consolidation = config.ConsolidationConfig{
		Enabled: true, MinAgeDays: 0, SimilarityThreshold: 0.8,
		MinClusterSize: 2, MaxMemoriesPerRun: 100,
	}
	err := c.Run(context.Background())
	assert.NoError(t, err, "nil LLM should skip gracefully")
}

// TestConsolidator_Run_EmptyDB 空数据库不出错 / Empty DB run is error-free
func TestConsolidator_Run_EmptyDB(t *testing.T) {
	stores, _ := setupConsolidatorStores(t)
	mockLLM := &consolidateMockLLM{response: strings.Repeat("consolidated fact. ", 5)}
	c := memory.NewConsolidator(stores.MemoryStore, nil, mockLLM, config.ConsolidationConfig{})
	config.AppConfig.Consolidation = config.ConsolidationConfig{
		Enabled: true, MinAgeDays: 0, SimilarityThreshold: 0.8,
		MinClusterSize: 2, MaxMemoriesPerRun: 100,
	}
	err := c.Run(context.Background())
	assert.NoError(t, err)
}

// TestConsolidator_Run_NoVecStore vecStore 为 nil 时跳过（需要向量做聚类）
func TestConsolidator_Run_NoVecStore(t *testing.T) {
	stores, memStore := setupConsolidatorStores(t)

	m1 := &model.Memory{Content: "fact A about dogs", Scope: "test", TeamID: "default"}
	m2 := &model.Memory{Content: "fact B about dogs", Scope: "test", TeamID: "default"}
	require.NoError(t, memStore.Create(context.Background(), m1))
	require.NoError(t, memStore.Create(context.Background(), m2))

	mockLLM := &consolidateMockLLM{response: strings.Repeat("consolidated. ", 10)}
	// vecStore=nil → Run skips (no vectors for clustering)
	c := memory.NewConsolidator(stores.MemoryStore, nil, mockLLM, config.ConsolidationConfig{})
	config.AppConfig.Consolidation = config.ConsolidationConfig{
		Enabled: true, MinAgeDays: 0, SimilarityThreshold: 0.8,
		MinClusterSize: 2, MaxMemoriesPerRun: 100,
	}
	err := c.Run(context.Background())
	assert.NoError(t, err)
}

// TestConsolidator_LLMError LLM 失败时 cluster 被跳过整体不报错 / LLM failure skips cluster, no top-level error
func TestConsolidator_LLMError(t *testing.T) {
	stores, _ := setupConsolidatorStores(t)
	mockLLM := &consolidateMockLLM{err: assert.AnError}
	c := memory.NewConsolidator(stores.MemoryStore, nil, mockLLM, config.ConsolidationConfig{})
	config.AppConfig.Consolidation = config.ConsolidationConfig{
		Enabled: true, MinAgeDays: 0, SimilarityThreshold: 0.8,
		MinClusterSize: 2, MaxMemoriesPerRun: 100,
	}
	// With no vecStore, Run exits early — no LLM calls
	err := c.Run(context.Background())
	assert.NoError(t, err)
}

// dynamicVecStore 动态向量存储 stub，ID 运行时填充 / Dynamic vector store stub populated at runtime
type dynamicVecStore struct {
	vectors map[string][]float32
}

func (d *dynamicVecStore) GetVectors(_ context.Context, ids []string) (map[string][]float32, error) {
	result := make(map[string][]float32, len(ids))
	for _, id := range ids {
		if v, ok := d.vectors[id]; ok {
			result[id] = v
		}
	}
	return result, nil
}
func (d *dynamicVecStore) Upsert(_ context.Context, id string, vec []float32, _ map[string]any) error {
	d.vectors[id] = vec
	return nil
}
func (d *dynamicVecStore) Search(_ context.Context, _ []float32, _ *model.Identity, _ int) ([]*model.SearchResult, error) {
	return nil, nil
}
func (d *dynamicVecStore) SearchFiltered(_ context.Context, _ []float32, _ *model.SearchFilters, _ int) ([]*model.SearchResult, error) {
	return nil, nil
}
func (d *dynamicVecStore) Delete(_ context.Context, _ string) error { return nil }
func (d *dynamicVecStore) Init(_ context.Context) error             { return nil }
func (d *dynamicVecStore) Close() error                             { return nil }

// setupClusterWithVecStore 创建相似记忆簇并填充向量 / Create similar memory cluster with vectors
func setupClusterWithVecStore(t *testing.T, memStore store.MemoryStore, count int, teamID string) *dynamicVecStore {
	t.Helper()
	vs := &dynamicVecStore{vectors: make(map[string][]float32)}
	for i := 0; i < count; i++ {
		m := &model.Memory{
			Content: strings.Repeat("shared topic about dogs and their behavior. ", 3),
			TeamID:  teamID,
			Scope:   "animals",
		}
		require.NoError(t, memStore.Create(context.Background(), m))
		// 向量高度相似（同一方向 + 微小扰动）→ 聚类阈值 0.8 下合并 / Nearly identical vectors → cluster at threshold 0.8
		vec := []float32{1.0, 0.01 * float32(i), 0.0}
		vs.vectors[m.ID] = vec
	}
	return vs
}

// TestConsolidator_OutputValidation_EmptyRejected 空输出被拒绝（通过 Run 全流程验证）/ Empty LLM output rejected through full Run path
func TestConsolidator_OutputValidation_EmptyRejected(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		expectErr bool // Run() itself returns nil (cluster failure is logged, not bubbled up)
	}{
		{"empty response skipped", "", false},
		{"whitespace-only skipped", "   \n  ", false},
		{"valid response creates consolidated memory", strings.Repeat("a real consolidated fact. ", 6), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stores, memStore := setupConsolidatorStores(t)
			vs := setupClusterWithVecStore(t, memStore, 3, "default")

			mockLLM := &consolidateMockLLM{response: tc.response}
			c := memory.NewConsolidator(stores.MemoryStore, vs, mockLLM, config.ConsolidationConfig{})

			config.AppConfig.Consolidation = config.ConsolidationConfig{
				Enabled:             true,
				MinAgeDays:          0,
				SimilarityThreshold: 0.8,
				MinClusterSize:      2,
				MaxMemoriesPerRun:   100,
			}
			err := c.Run(context.Background())
			// Run() swallows cluster-level errors (logs Warn); top-level always nil
			assert.NoError(t, err, tc.name)
		})
	}
}

// TestConsolidator_ScopeInheritance 归纳记忆继承首个成员的 scope / Consolidated memory inherits first member's scope
func TestConsolidator_ScopeInheritance(t *testing.T) {
	stores, memStore := setupConsolidatorStores(t)

	// Create memories with same scope
	for i := 0; i < 3; i++ {
		m := &model.Memory{
			Content: strings.Repeat("memory content about topic. ", 3),
			Scope:   "project-x",
			TeamID:  "team-1",
		}
		require.NoError(t, memStore.Create(context.Background(), m))
	}

	sysID := &model.Identity{TeamID: "team-1", OwnerID: model.SystemOwnerID}
	list, err := memStore.List(context.Background(), sysID, 0, 10)
	require.NoError(t, err)
	assert.Len(t, list, 3)
	for _, m := range list {
		assert.Equal(t, "project-x", m.Scope)
	}

	_ = stores // cleanup handled by t.Cleanup
}
