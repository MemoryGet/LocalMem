package eval_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/require"
)

// defaultEvalDBPath 持久化共享库路径（可通过 EVAL_DB_PATH 覆盖）
// Persistent shared DB path (override with EVAL_DB_PATH env var)
func defaultEvalDBPath() string {
	if p := os.Getenv("EVAL_DB_PATH"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "data", "eval_longmemeval.db")
}

// TestLongMemEvalSeedFTS 建立 FTS/Pipeline/Graph 层共享库（不含向量）
// 首次运行耗时较长（LLM 实体抽取）；再次运行若 DB 已存在则跳过。
// Build shared DB for FTS/Pipeline/Graph tiers (no vector).
// First run is slow (LLM entity extraction); skips if DB already exists.
func TestLongMemEvalSeedFTS(t *testing.T) {
	eval.LoadTestConfig()
	if !eval.HasLLMConfig() {
		t.Skip("skip: LLM API key not configured (set LOCAL_API_KEY or OPENAI_API_KEY in .env, or configure llm.openai in config.yaml)")
	}
	dbPath := defaultEvalDBPath()

	// 已存在则跳过，避免重复 seed / Skip if DB already seeded
	if _, err := os.Stat(dbPath); err == nil {
		t.Logf("DB already exists at %s, skipping seed (delete to re-seed)", dbPath)
		return
	}

	datasetPath := filepath.Join("testdata", "longmemeval-oracle.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/longmemeval-oracle.json not found")
	}

	entries, err := eval.LoadLongMemEval(datasetPath)
	require.NoError(t, err)
	t.Logf("Seeding %d entries into %s", len(entries), dbPath)

	// 确保目录存在 / Ensure directory exists
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0755))

	err = eval.SeedLongMemEvalDB(context.Background(), entries, dbPath, true)
	require.NoError(t, err)
	t.Logf("Seed complete: %s", dbPath)
}

// TestLongMemEvalSeedVector 将 eval DB 中的记忆批量嵌入并写入 Qdrant
// Safe to re-run (Qdrant upsert is idempotent).
func TestLongMemEvalSeedVector(t *testing.T) {
	dbPath := defaultEvalDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: eval DB not found at %s (run TestLongMemEvalSeedFTS first)", dbPath)
	}

	eval.LoadTestConfig()

	ctx := context.Background()
	qdrantURL := eval.EvalQdrantURL()
	dim := eval.EvalQdrantDim()
	t.Logf("Seeding vectors → Qdrant %s collection %q dim=%d", qdrantURL, eval.EvalCollection, dim)
	n, err := eval.SeedVectorsToQdrant(ctx, dbPath, qdrantURL, eval.EvalCollection, dim, 0) // 0 = no limit
	require.NoError(t, err)
	t.Logf("Seeded %d vectors into Qdrant collection %q", n, eval.EvalCollection)
	require.Greater(t, n, 0, "expected at least 1 vector seeded")
}

// TestLongMemEvalExtractEntities 对已有共享库补跑批量实体抽取（无需重新 seed）
// Backfill entity extraction on existing shared DB without re-seeding memories.
// Safe to re-run: existing entities are matched and reused.
func TestLongMemEvalExtractEntities(t *testing.T) {
	eval.LoadTestConfig()
	if !eval.HasLLMConfig() {
		t.Skip("skip: LLM API key not configured (set LOCAL_API_KEY or OPENAI_API_KEY in .env, or configure llm.openai in config.yaml)")
	}
	dbPath := defaultEvalDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: eval DB not found at %s (run TestLongMemEvalSeedFTS first)", dbPath)
	}

	t.Logf("Running batch entity extraction on %s", dbPath)
	// maxItems=0: no limit — resumes from extract_queue.md if it exists
	created, err := eval.ExtractEntitiesFromDB(context.Background(), dbPath, 0)
	require.NoError(t, err)
	t.Logf("Batch extraction complete: %d new entities created", created)
}
