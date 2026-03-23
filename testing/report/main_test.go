package report_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"
)

// 全局测试 store
var (
	testStoreSimple store.MemoryStore // 带 SimpleTokenizer
	testStoreNoop   store.MemoryStore // 带 NoopTokenizer
	tmpDirs         []string
)

func TestMain(m *testing.M) {
	testreport.Init("IClude")

	code := m.Run()

	// 清理
	for _, d := range tmpDirs {
		os.RemoveAll(d)
	}

	os.Exit(code)
}

// newTestStore 创建测试用 SQLite store
func newTestStore(t *testing.T, tok tokenizer.Tokenizer) store.MemoryStore {
	t.Helper()
	dir := t.TempDir()
	tmpDirs = append(tmpDirs, dir)
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	return s
}

// seedMemories 批量插入测试记忆
func seedMemories(t *testing.T, s store.MemoryStore, memories []*model.Memory) {
	t.Helper()
	ctx := context.Background()
	for _, mem := range memories {
		if err := s.Create(ctx, mem); err != nil {
			t.Fatalf("failed to seed memory: %v", err)
		}
	}
}
