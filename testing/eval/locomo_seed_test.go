//go:build ignore

package eval_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/require"
)

// defaultLoCoMoDBPath 持久化共享库路径（可通过 LOCOMO_DB_PATH 覆盖）
// Persistent LoCoMo DB path (override with LOCOMO_DB_PATH env var).
func defaultLoCoMoDBPath() string {
	if p := os.Getenv("LOCOMO_DB_PATH"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "data", "eval_locomo.db")
}

// TestLoCoMoSeedFTS 建立 LoCoMo FTS/Graph 共享库（不含向量）
// Build LoCoMo shared DB for FTS/Graph tiers. Skips if DB already exists.
func TestLoCoMoSeedFTS(t *testing.T) {
	eval.LoadTestConfig()

	dbPath := defaultLoCoMoDBPath()
	if _, err := os.Stat(dbPath); err == nil {
		t.Logf("LoCoMo DB already exists at %s, skipping seed (delete to re-seed)", dbPath)
		return
	}

	datasetPath := filepath.Join("testdata", "locomo", "locomo10.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/locomo/locomo10.json not found")
	}

	convs, err := eval.LoadLoCoMo(datasetPath)
	require.NoError(t, err)
	t.Logf("Loaded %d conversations from %s", len(convs), datasetPath)

	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0755))

	cfg := eval.LoCoMoSeedConfig{
		WindowSize:  3, // 每个 memory 3 条轮次，精细粒度 / 3 turns per memory window
		Stride:      1, // 步长 1，最大重叠覆盖 / stride 1 for maximum overlap coverage
		ContextSize: 2, // 前缀补 2 条上下文，保留对话背景 / 2 preceding turns as context prefix
	}
	err = eval.SeedLoCoMoDB(context.Background(), convs, dbPath, cfg)
	require.NoError(t, err)
	t.Logf("Seed complete: %s (window=%d stride=%d context=%d)", dbPath, cfg.WindowSize, cfg.Stride, cfg.ContextSize)
}

// TestLoCoMoExtractEntities 对 LoCoMo DB 补跑批量实体抽取（幂等，可断点续跑）
// Backfill entity extraction on LoCoMo DB. Safe to re-run.
// Uses prompt hints to skip speaker names (ubiquitous in every session → super-nodes in graph).
func TestLoCoMoExtractEntities(t *testing.T) {
	eval.LoadTestConfig()
	if !eval.HasLLMConfig() {
		t.Skip("skip: LLM API key not configured")
	}
	dbPath := defaultLoCoMoDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: LoCoMo DB not found at %s (run TestLoCoMoSeedFTS first)", dbPath)
	}

	datasetPath := filepath.Join("testdata", "locomo", "locomo10.json")
	convs, err := eval.LoadLoCoMo(datasetPath)
	require.NoError(t, err)

	// 收集所有说话人名字，作为实体抽取黑名单
	// Collect all speaker names to exclude from entity extraction (they appear in every session → graph super-nodes)
	speakerHint := eval.LoCoMoSpeakerExcludeHint(convs)
	t.Logf("Prompt hint: %s", speakerHint)

	t.Logf("Running batch entity extraction on %s", dbPath)
	created, err := eval.ExtractEntitiesFromDBWithHints(context.Background(), dbPath, 0, speakerHint)
	require.NoError(t, err)
	t.Logf("Batch extraction complete: %d new entities created", created)
}

// TestLoCoMoSeedVector 将 LoCoMo DB 中的记忆批量写入 Qdrant（幂等）
// Seed LoCoMo memories into Qdrant. Idempotent — safe to re-run.
func TestLoCoMoSeedVector(t *testing.T) {
	dbPath := defaultLoCoMoDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: LoCoMo DB not found at %s (run TestLoCoMoSeedFTS first)", dbPath)
	}

	eval.LoadTestConfig()

	ctx := context.Background()
	qdrantURL := eval.LoCoMoQdrantURL()
	dim := eval.LoCoMoQdrantDim()
	t.Logf("Seeding vectors → Qdrant %s collection %q dim=%d", qdrantURL, eval.LoCoMoCollection, dim)

	n, err := eval.SeedLoCoMoVectors(ctx, dbPath, qdrantURL, dim, 0)
	require.NoError(t, err)
	t.Logf("Seeded %d vectors into Qdrant collection %q", n, eval.LoCoMoCollection)
	require.Greater(t, n, 0, "expected at least 1 vector seeded")
}

// ─── 1-turn granularity seed ─────────────────────────────────────────────────

// default1TurnDBPath 1-turn 粒度 DB 路径（可通过 LOCOMO_1TURN_DB_PATH 覆盖）
func default1TurnDBPath() string {
	if p := os.Getenv("LOCOMO_1TURN_DB_PATH"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "data", "eval_locomo_1turn.db")
}

// TestLoCoMoSeedFTS1Turn 建立 LoCoMo 1-turn 粒度 SQLite 共享库
// Build LoCoMo shared DB with 1-turn granularity (each memory = one dialogue turn).
// Creates data/eval_locomo_1turn.db. Skips if already exists.
func TestLoCoMoSeedFTS1Turn(t *testing.T) {
	eval.LoadTestConfig()

	dbPath := default1TurnDBPath()
	if _, err := os.Stat(dbPath); err == nil {
		t.Logf("1-turn DB already exists at %s, skipping (delete to re-seed)", dbPath)
		return
	}

	datasetPath := filepath.Join("testdata", "locomo", "locomo10.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/locomo/locomo10.json not found")
	}

	convs, err := eval.LoadLoCoMo(datasetPath)
	require.NoError(t, err)
	t.Logf("Loaded %d conversations", len(convs))

	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0755))

	cfg := eval.LoCoMoSeedConfig{
		WindowSize:  1, // 1 turn per memory — no window noise
		Stride:      1,
		ContextSize: 0, // no context prefix; single turn is self-contained
	}
	err = eval.SeedLoCoMoDB(context.Background(), convs, dbPath, cfg)
	require.NoError(t, err)
	t.Logf("1-turn seed complete: %s", dbPath)
}

// TestLoCoMoSeedVector1Turn 将 1-turn DB 中的记忆批量写入 memories_locomo_1turn Qdrant collection（幂等）
// Seed 1-turn LoCoMo memories into the dedicated Qdrant collection. Idempotent.
func TestLoCoMoSeedVector1Turn(t *testing.T) {
	dbPath := default1TurnDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: 1-turn DB not found at %s (run TestLoCoMoSeedFTS1Turn first)", dbPath)
	}

	eval.LoadTestConfig()

	ctx := context.Background()
	qdrantURL := eval.LoCoMoQdrantURL()
	dim := eval.LoCoMoQdrantDim()
	t.Logf("Seeding 1-turn vectors → Qdrant %s collection %q dim=%d", qdrantURL, eval.LoCoMoCollection1Turn, dim)

	n, err := eval.SeedLoCoMoVectorsToCollection(ctx, dbPath, qdrantURL, eval.LoCoMoCollection1Turn, dim, 0)
	require.NoError(t, err)
	t.Logf("Seeded %d vectors into collection %q", n, eval.LoCoMoCollection1Turn)
	require.Greater(t, n, 0, "expected at least 1 vector seeded")
}
