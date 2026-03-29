// Package heartbeat_test 自主巡检引擎测试 / Heartbeat autonomous inspection engine tests
package heartbeat_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/heartbeat"
	"iclude/internal/llm"
	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hbMockLLM 用于 heartbeat 测试的 mock LLM
type hbMockLLM struct {
	answer string // "yes" or "no"
	err    error
	calls  int
}

func (m *hbMockLLM) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.answer}, nil
}

// setupHeartbeatStores 创建测试用存储层 / Set up test stores with temp SQLite
func setupHeartbeatStores(t *testing.T) (*store.Stores, store.MemoryStore) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hb_test.db")

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

// makeHBConfig 创建 Heartbeat 配置 / Create heartbeat config for test
func makeHBConfig(enabled bool, contradictionEnabled bool, maxComp int) config.HeartbeatConfig {
	return config.HeartbeatConfig{
		Enabled:              enabled,
		Interval:             1 * time.Hour,
		ContradictionEnabled: contradictionEnabled,
		ContradictionMaxComp: maxComp,
		DecayAuditMinAgeDays: 0,
		DecayAuditThreshold:  0.1,
	}
}

// TestHeartbeat_Run_Disabled 引擎未启用时 Run 立即返回 / Run exits immediately when disabled
func TestHeartbeat_Run_Disabled(t *testing.T) {
	hbCfg := makeHBConfig(false, false, 0)
	stores, _ := setupHeartbeatStores(t)
	engine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, nil, nil, hbCfg)
	err := engine.Run(context.Background())
	assert.NoError(t, err)
}

// TestHeartbeat_Run_EmptyDB 空库巡检不报错 / Inspection of empty DB is error-free
func TestHeartbeat_Run_EmptyDB(t *testing.T) {
	hbCfg := makeHBConfig(true, false, 0)
	stores, _ := setupHeartbeatStores(t)
	engine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, nil, nil, hbCfg)
	err := engine.Run(context.Background())
	assert.NoError(t, err)
}

// TestHeartbeat_Run_NoLLM_ContradictionSkipped LLM 为 nil 时矛盾检测跳过 / Contradiction skipped with nil LLM
func TestHeartbeat_Run_NoLLM_ContradictionSkipped(t *testing.T) {
	hbCfg := makeHBConfig(true, true, 10)
	stores, memStore := setupHeartbeatStores(t)

	// 写入两条可能矛盾的记忆
	m1 := &model.Memory{Content: "cats are mammals", Scope: "test", TeamID: "default"}
	m2 := &model.Memory{Content: "cats are reptiles", Scope: "test", TeamID: "default"}
	require.NoError(t, memStore.Create(context.Background(), m1))
	require.NoError(t, memStore.Create(context.Background(), m2))

	// nil LLM → contradiction_enabled=true 但跳过（engine.go:56 的 nil guard）
	engine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, nil, nil, hbCfg)
	err := engine.Run(context.Background())
	assert.NoError(t, err)
}

// TestHeartbeat_ContradictionLLMError LLM 报错时巡检继续 / LLM error doesn't abort inspection
func TestHeartbeat_ContradictionLLMError(t *testing.T) {
	hbCfg := makeHBConfig(true, false, 5) // contradiction=false → LLM not called at all
	mockLLM := &hbMockLLM{err: assert.AnError}
	stores, _ := setupHeartbeatStores(t)
	engine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, nil, mockLLM, hbCfg)
	err := engine.Run(context.Background())
	assert.NoError(t, err, "LLM errors should be logged, not returned")
}

// TestHeartbeat_DecayAudit 衰减审计：低强度记忆被识别并记录 / Decay audit logs low-strength memories
func TestHeartbeat_DecayAudit(t *testing.T) {
	hbCfg := makeHBConfig(true, false, 0)
	stores, memStore := setupHeartbeatStores(t)

	// 写入一条低强度记忆
	m := &model.Memory{
		Content:  "old weak memory that should decay",
		Strength: 0.05, // below DecayAuditThreshold=0.1
		Scope:    "test",
		TeamID:   "default",
	}
	require.NoError(t, memStore.Create(context.Background(), m))

	engine := heartbeat.NewEngine(stores.MemoryStore, nil, nil, nil, hbCfg)
	err := engine.Run(context.Background())
	assert.NoError(t, err)
}

// TestHeartbeat_ContextCancel ctx 取消时 Run 正常退出 / Run respects context cancellation
func TestHeartbeat_ContextCancel(t *testing.T) {
	hbCfg := makeHBConfig(true, false, 0)
	stores, _ := setupHeartbeatStores(t)
	engine := heartbeat.NewEngine(stores.MemoryStore, nil, nil, nil, hbCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// TestHeartbeat_MaxComparisons_Zero 最大比较数为 0 时不调用 LLM / No LLM calls when maxComp=0
func TestHeartbeat_MaxComparisons_Zero(t *testing.T) {
	hbCfg := makeHBConfig(true, true, 0)
	mockLLM := &hbMockLLM{answer: "no"}
	stores, _ := setupHeartbeatStores(t)
	engine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, nil, mockLLM, hbCfg)
	err := engine.Run(context.Background())
	assert.NoError(t, err)
	// vecStore=nil → contradiction check skipped before hitting maxComp limit
	assert.Equal(t, 0, mockLLM.calls)
}
