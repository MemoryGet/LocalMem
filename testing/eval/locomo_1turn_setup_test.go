package eval_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/require"
)

// default1TurnDBPath 1-turn 粒度 DB 路径 / 1-turn DB path (override with LOCOMO_1TURN_DB_PATH)
func default1TurnDBPath() string {
	if p := os.Getenv("LOCOMO_1TURN_DB_PATH"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "data", "eval_locomo_1turn.db")
}

// TestLoCoMoSetup1TurnFTS 建立 LoCoMo 1-turn 粒度 SQLite 共享库
// Seeds data/eval_locomo_1turn.db with WindowSize=1 (one turn per memory).
// Skips if the DB already exists.
func TestLoCoMoSetup1TurnFTS(t *testing.T) {
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
		WindowSize:  1,
		Stride:      1,
		ContextSize: 0,
	}
	err = eval.SeedLoCoMoDB(context.Background(), convs, dbPath, cfg)
	require.NoError(t, err)
	t.Logf("1-turn seed complete: %s", dbPath)
}

// TestLoCoMoSetup1TurnVector 将 1-turn DB 写入 memories_locomo_1turn Qdrant collection（幂等）
// Seeds 1-turn embeddings into Qdrant. Idempotent — safe to re-run.
func TestLoCoMoSetup1TurnVector(t *testing.T) {
	dbPath := default1TurnDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: 1-turn DB not found at %s (run TestLoCoMoSetup1TurnFTS first)", dbPath)
	}

	eval.LoadTestConfig()

	ctx := context.Background()
	qdrantURL := eval.LoCoMoQdrantURL()
	dim := eval.LoCoMoQdrantDim()
	t.Logf("Seeding → Qdrant %s collection %q dim=%d", qdrantURL, eval.LoCoMoCollection1Turn, dim)

	n, err := eval.SeedLoCoMoVectorsToCollection(ctx, dbPath, qdrantURL, eval.LoCoMoCollection1Turn, dim, 0)
	require.NoError(t, err)
	t.Logf("Seeded %d vectors → %q", n, eval.LoCoMoCollection1Turn)
	require.Greater(t, n, 0, "expected at least 1 vector seeded")
}
