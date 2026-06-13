package eval_test

import (
	"context"
	"fmt"
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

func runLoCoMoQuery(t *testing.T, tier eval.Tier, maxQ int) {
	t.Helper()
	eval.LoadTestConfig()

	dbPath := defaultLoCoMoDBPath()
	if tier.DBPath != "" {
		dbPath = tier.DBPath
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: LoCoMo DB not found at %s", dbPath)
	}

	datasetPath := filepath.Join("testdata", "locomo", "locomo10.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/locomo/locomo10.json not found")
	}

	convs, err := eval.LoadLoCoMo(datasetPath)
	require.NoError(t, err)
	t.Logf("Loaded %d conversations, running up to %d QA pairs with tier [%s]", len(convs), maxQ, tier.Name)

	report, err := eval.RunLoCoMoEval(context.Background(), dbPath, convs, tier, maxQ)
	require.NoError(t, err)

	t.Logf("[%s] HitRate %.1f%%  MRR %.3f  Total %d  Duration %s",
		report.Tier, report.HitRate, report.MRR, report.Total, report.Duration)

	// Per-category breakdown
	for cat, m := range report.ByCategory {
		t.Logf("  %-12s  HitRate %5.1f%%  MRR %.3f  N=%d", cat, m.HitRate, m.MRR, m.Total)
	}

	// Print formatted summary
	fmt.Printf("\n=== LoCoMo Eval [%s] ===\n", report.Tier)
	fmt.Printf("  HitRate: %.1f%%\n", report.HitRate)
	fmt.Printf("  MRR:     %.3f\n", report.MRR)
	fmt.Printf("  Total:   %d QA pairs\n", report.Total)
	fmt.Printf("  Duration: %s\n\n", report.Duration)

	// 保存 baseline / Save baseline snapshot
	name := "locomo-" + tier.Name + "-v1"
	evalReport := eval.LoCoMoReportToEvalReport(report)
	if err := eval.SaveBaseline(evalReport, name, "baselines"); err != nil {
		t.Logf("warn: failed to save baseline %s: %v", name, err)
	} else {
		t.Logf("baseline saved → baselines/%s.json", name)
	}
}

// TestLoCoMoFTS 层级 1：纯 FTS 检索基线 / Tier 1: FTS-only baseline
func TestLoCoMoFTS(t *testing.T) {
	runLoCoMoQuery(t, eval.TierFTS, 200)
}

// TestLoCoMoPipeline 层级 2：FTS + 意图分类器 / Tier 2: FTS + intent classifier
func TestLoCoMoPipeline(t *testing.T) {
	runLoCoMoQuery(t, eval.TierPipeline, 200)
}

// TestLoCoMoGraph 层级 3：FTS + 图谱检索（默认权重 0.5）/ Tier 3: FTS + graph (default weight 0.5)
func TestLoCoMoGraph(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for graph stage")
	}
	runLoCoMoQuery(t, eval.TierGraph, 200)
}

// TestLoCoMoGraphSalience 层级 3c：FTS + 图谱检索 + hub 实体过滤（Option A）
// Tier 3c: FTS + graph with hub entity salience filtering (Option A).
// hub 节点（出现在 >= 1% 记忆中）跳过 BFS 扩张，消除超级节点噪声 / Hub nodes skip BFS expansion.
func TestLoCoMoGraphSalience(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for graph stage")
	}
	runLoCoMoQuery(t, eval.TierGraphSalience, 200)
}

// TestLoCoMoGraphVecEvidence 层级 3b：FTS + 图谱检索 + 向量边证据过滤（Phase 2）
// Tier 3b: FTS + graph with vector-based edge evidence filtering (Phase 2).
// Requires Qdrant with seeded vectors (run TestLoCoMoSeedVector first).
func TestLoCoMoGraphVecEvidence(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for graph stage + embedding")
	}
	runLoCoMoQuery(t, eval.TierGraphVecEvidence, 200)
}

// TestLoCoMoHyDE 层级 5：FTS + 向量 + HyDE 假设文档嵌入
// Tier 5: FTS + vector + HyDE (Hypothetical Document Embedding).
// LLM 根据 query 生成假设答案文档，embed 后与原始 query embed 混合做向量搜索。
// 对词汇不匹配类查询效果最好：query 用"sport"，记忆说"tennis"。
// Requires LLM API key + seeded Qdrant vectors (run TestLoCoMoSeedVector first).
// 注：每个 query 需要 1 次 LLM 调用，建议先用 maxQ=50 快速验证。
func TestLoCoMoHyDE(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for HyDE generation")
	}
	runLoCoMoQuery(t, eval.TierHyDE, 200)
}

// TestLoCoMoHyDEQuick 快速 HyDE 验证（50 QA）/ Quick HyDE validation (50 QA pairs)
func TestLoCoMoHyDEQuick(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for HyDE generation")
	}
	runLoCoMoQuery(t, eval.TierHyDE, 50)
}

// TestLoCoMoGraphSalienceFTS 层级 3d：Option A + Option B 组合
// Tier 3d: Salience filter (Option A) + FTS-first seeds (Option B).
// Hub 实体在种子层面（Option B）和 BFS 扩张层面（Option A）均被过滤，双重防护超级节点爆炸。
// Hub entities are filtered at seed level (Option B) AND during BFS expansion (Option A).
func TestLoCoMoGraphSalienceFTS(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for graph stage")
	}
	runLoCoMoQuery(t, eval.TierGraphSalienceFTS, 200)
}

// TestLoCoMoGraphW02 图谱权重 0.2 对比测试 / Graph weight 0.2 comparison
func TestLoCoMoGraphW02(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for graph stage")
	}
	tier := eval.TierGraph
	tier.Name = "fts+pipeline+graph-w0.2"
	tier.GraphWeight = 0.2
	runLoCoMoQuery(t, tier, 200)
}

// TestLoCoMoGraphW01 图谱权重 0.1 对比测试 / Graph weight 0.1 comparison
func TestLoCoMoGraphW01(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for graph stage")
	}
	tier := eval.TierGraph
	tier.Name = "fts+pipeline+graph-w0.1"
	tier.GraphWeight = 0.1
	runLoCoMoQuery(t, tier, 200)
}

// TestLoCoMoVector 层级 4：FTS + 图谱 + 向量 / Tier 4: FTS + graph + vector
func TestLoCoMoVector(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required for graph stage")
	}
	runLoCoMoQuery(t, eval.TierVector, 200)
}

// TestLoCoMoPipelineVector FTS + pipeline + vector（无图谱）/ FTS + pipeline + vector (no graph)
func TestLoCoMoPipelineVector(t *testing.T) {
	tier := eval.Tier{Name: "fts+pipeline+vector", Pipeline: true, Vector: true}
	runLoCoMoQuery(t, tier, 200)
}

// TestLoCoMoVectorInstruction FTS + pipeline + vector + 非对称检索指令前缀
// Instruction-tuned embedding: query side uses asymmetric retrieval instruction,
// document embeddings in Qdrant are unchanged — no re-indexing required.
// Expected improvement: semantic-gap single-hop queries (vocabulary mismatch between
// question and first-person memory passage).
func TestLoCoMoVectorInstruction(t *testing.T) {
	runLoCoMoQuery(t, eval.TierVectorInstruction, 200)
}

// TestLoCoMo1Turn 1-turn 粒度评测：每条记忆 = 1 句对话，消除窗口噪音
// 1-turn granularity eval: each memory is a single dialogue turn, no window dilution.
// Hypothesis: answer turn "I am a trans woman" gets its own embedding → high similarity
// with "What is Caroline's identity?" → single-hop hit rate improves.
// Requires:
//   data/eval_locomo_1turn.db  (run TestLoCoMoSeedFTS1Turn in locomo_seed_test.go)
//   memories_locomo_1turn      (run TestLoCoMoSeedVector1Turn in locomo_seed_test.go)
func TestLoCoMo1Turn(t *testing.T) {
	dbPath := filepath.Join("..", "..", "data", "eval_locomo_1turn.db")
	if p := os.Getenv("LOCOMO_1TURN_DB_PATH"); p != "" {
		dbPath = p
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: 1-turn DB not found at %s — run TestLoCoMoSeedFTS1Turn + TestLoCoMoSeedVector1Turn first", dbPath)
	}
	tier := eval.TierVector1Turn
	tier.DBPath = dbPath
	runLoCoMoQuery(t, tier, 200)
}

// TestLoCoMoFull 层级 5：全通道 + LLM 精排 / Tier 5: all channels + LLM rerank
func TestLoCoMoFull(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("LOCAL_API_KEY") == "" {
		t.Skip("skip: LLM API key required")
	}
	runLoCoMoQuery(t, eval.TierFull, 200)
}

// ============================================================
// REGRESSION GUARD — performance floor for single-hop retrieval
// Run with: go test ./testing/eval/ -run TestLoCoMo_RegressionGuard
// Requires: LoCoMo DB (data/eval_locomo.db) + Qdrant seeded (memories_locomo collection)
// ============================================================

// ============================================================
// REGRESSION GUARD — 1-turn production floor
// Baseline: 1-turn chunking (each memory = one dialogue turn)
// DB: data/eval_locomo_1turn.db  Collection: memories_locomo_1turn
//
// 1-turn FTS-only:        overall=90.5%  single-hop=81.2%
// 1-turn FTS+pipeline:    overall=91.5%  single-hop=81.2%  ← guard uses this
// ============================================================

// minSingleHopHitRate 1-turn 单跳检索命中率下限（实测 81.2%，留 1.5pp 缓冲）
// Single-hop hit rate floor with 1-turn chunking. Measured at 81.2% — 1.5pp buffer.
const minSingleHopHitRate = 79.5

// minOverallHitRate 1-turn 整体命中率下限（实测 91.5%，留 1.5pp 缓冲）
// Overall hit rate floor with 1-turn chunking. Measured at 91.5% — 1.5pp buffer.
const minOverallHitRate = 90.0

// TestLoCoMo_RegressionGuard_SingleHop 确保 1-turn 粒度下单跳命中率不低于历史水位
// Guards that single-hop hit rate does not drop below the measured floor after code changes.
// Uses 1-turn DB + pipeline tier (no vector required — FTS alone achieves 81.2% with 1-turn).
// Run in CI after any change to: FTSRewriteRetryStage, MergeStage, builtin pipelines, preprocess.
func TestLoCoMo_RegressionGuard_SingleHop(t *testing.T) {
	if testing.Short() {
		t.Skip("skip: regression guard requires live 1-turn DB (omit -short to run)")
	}

	dbPath := filepath.Join("..", "..", "data", "eval_locomo_1turn.db")
	if p := os.Getenv("LOCOMO_1TURN_DB_PATH"); p != "" {
		dbPath = p
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("skip: 1-turn DB not found at %s — run TestLoCoMoSetup1TurnFTS first", dbPath)
	}
	datasetPath := filepath.Join("testdata", "locomo", "locomo10.json")
	if _, err := os.Stat(datasetPath); os.IsNotExist(err) {
		t.Skip("skip: testdata/locomo/locomo10.json not found")
	}

	convs, err := eval.LoadLoCoMo(datasetPath)
	if err != nil {
		t.Fatalf("load locomo: %v", err)
	}

	// fts+pipeline: FTS alone achieves 81.2% single-hop with 1-turn chunking.
	// Vector adds 0pp on this dataset; pipeline adds temporal improvement (+7.1pp temporal).
	tier := eval.TierPipeline
	tier.DBPath = dbPath

	report, err := eval.RunLoCoMoEval(context.Background(), dbPath, convs, tier, 200)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}

	t.Logf("[RegressionGuard/1turn] Overall HR=%.1f%%  MRR=%.3f  Total=%d",
		report.HitRate, report.MRR, report.Total)
	for cat, m := range report.ByCategory {
		t.Logf("  %-12s  HR=%5.1f%%  MRR=%.3f  N=%d", cat, m.HitRate, m.MRR, m.Total)
	}

	overallHR := report.HitRate
	if overallHR < minOverallHitRate {
		t.Errorf("REGRESSION: overall hit rate %.1f%% dropped below floor %.1f%%",
			overallHR, minOverallHitRate)
	}

	shMetrics, ok := report.ByCategory["single-hop"]
	if !ok {
		t.Error("REGRESSION: 'single-hop' category missing from eval report")
		return
	}
	if shMetrics.HitRate < minSingleHopHitRate {
		t.Errorf("REGRESSION: single-hop hit rate %.1f%% dropped below floor %.1f%% — chunking granularity or FTS pipeline may have degraded",
			shMetrics.HitRate, minSingleHopHitRate)
	}
}
