// Package eval 评测工具共享帮助函数 / Shared eval helpers
package eval

import (
	"context"
	"testing"

	"iclude/internal/memory"
	"iclude/internal/search"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
)

// SetupTestMgrAndRetriever 创建测试用 Manager 和 Retriever（无 LLM 依赖）。
// Create a test Manager and Retriever backed by a temp SQLite store (no LLM).
func SetupTestMgrAndRetriever(t *testing.T, dbPath string) (*memory.Manager, *search.Retriever) {
	t.Helper()
	loadTestConfig()
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		t.Fatalf("SetupTestMgrAndRetriever: open store: %v", err)
	}
	ctx := context.Background()
	if err := memStore.Init(ctx); err != nil {
		t.Fatalf("SetupTestMgrAndRetriever: init store: %v", err)
	}
	t.Cleanup(func() { memStore.Close() })

	mgr := memory.NewManager(memory.ManagerDeps{MemStore: memStore})

	cfg := buildRetrievalConfig("fts")
	preprocessor := search.NewPreprocessor(tok, nil, nil, cfg)
	retriever := search.NewRetriever(memStore, nil, nil, nil, nil, cfg, preprocessor, nil)
	retriever.InitPipeline()

	return mgr, retriever
}

// LoadLoCoMoSessions 从 testdata 加载 LoCoMo 会话（返回前 limit 个）。
// Load LoCoMo sessions from testdata (returns first limit sessions).
func LoadLoCoMoSessions(t *testing.T, limit int) ([]ConvSession, error) {
	t.Helper()
	// Stub: implement based on existing LoCoMo data patterns when needed.
	return nil, nil
}
