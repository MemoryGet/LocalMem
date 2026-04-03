package eval_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"iclude/testing/eval"
)

// TestEvolutionAwareRetrieval 验证 memory_class 权重对检索质量的影响
// Uses English content so simple tokenizer produces valid BM25 scores for multiple memories.
// A/B comparison: flat (all episodic) vs evolved (mixed classes).
func TestEvolutionAwareRetrieval(t *testing.T) {
	ds := buildEvolutionDataset()

	// Run A: flat baseline
	flatDS := cloneDatasetFlat(ds)
	flatRunner, flatCleanup := createRunner(t, "flat")
	defer flatCleanup()
	flatReport, err := flatRunner.Run(context.Background(), flatDS, "fts-flat")
	if err != nil {
		t.Fatalf("flat run: %v", err)
	}

	// Run B: evolved
	evolvedRunner, evolvedCleanup := createRunner(t, "evolved")
	defer evolvedCleanup()
	evolvedReport, err := evolvedRunner.Run(context.Background(), ds, "fts-evolved")
	if err != nil {
		t.Fatalf("evolved run: %v", err)
	}

	eval.PrintReport(flatReport)
	eval.PrintReport(evolvedReport)

	fmt.Printf("\n=== Evolution Impact ===\n")
	fmt.Printf("  Flat    HitRate: %5.1f%%  MRR: %.3f  NDCG@10: %.3f\n",
		flatReport.Metrics.HitRate, flatReport.Metrics.MRR, flatReport.Metrics.NDCG10)
	fmt.Printf("  Evolved HitRate: %5.1f%%  MRR: %.3f  NDCG@10: %.3f\n",
		evolvedReport.Metrics.HitRate, evolvedReport.Metrics.MRR, evolvedReport.Metrics.NDCG10)
	fmt.Printf("  Delta   HitRate: %+5.1f%%  MRR: %+.3f  NDCG@10: %+.3f\n",
		evolvedReport.Metrics.HitRate-flatReport.Metrics.HitRate,
		evolvedReport.Metrics.MRR-flatReport.Metrics.MRR,
		evolvedReport.Metrics.NDCG10-flatReport.Metrics.NDCG10)

	// Per-case rank comparison
	fmt.Printf("\n  Per-case rank changes:\n")
	rankFlips := 0
	for i := range flatReport.Cases {
		fc := flatReport.Cases[i]
		ec := evolvedReport.Cases[i]
		if fc.Rank != ec.Rank {
			rankFlips++
			fmt.Printf("    [%d] %q: rank %d→%d (score %.4f→%.4f)\n",
				i, fc.Query[:min(40, len(fc.Query))], fc.Rank, ec.Rank, fc.Score, ec.Score)
		}
	}
	fmt.Printf("  Rank flips: %d/%d\n", rankFlips, len(flatReport.Cases))

	// Evolved should be >= flat
	if evolvedReport.Metrics.MRR < flatReport.Metrics.MRR-0.01 {
		t.Errorf("evolved MRR (%.3f) worse than flat (%.3f)", evolvedReport.Metrics.MRR, flatReport.Metrics.MRR)
	}
}

// TestEvolutionDetailedScoring 输出每条查询的完整排名对比（调试用）
func TestEvolutionDetailedScoring(t *testing.T) {
	ds := buildEvolutionDataset()
	ctx := context.Background()

	for _, tc := range []struct {
		label    string
		useClass bool
	}{
		{"flat", false},
		{"evolved", true},
	} {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, tc.label+".db")
		runner, cleanup, err := eval.NewRunner(dbPath, "fts")
		if err != nil {
			t.Fatal(err)
		}

		for _, s := range ds.SeedMemories {
			seed := s
			if !tc.useClass {
				seed.MemoryClass = ""
			}
			if err := runner.SeedOne(ctx, seed); err != nil {
				t.Fatal(err)
			}
		}

		fmt.Printf("\n========== %s mode ==========\n", tc.label)
		for _, c := range ds.Cases[:3] { // Print first 3 queries in detail
			results, err := runner.RetrieveRaw(ctx, c.Query, 5)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("\n  Query: %q\n", c.Query)
			for i, r := range results {
				fmt.Printf("    #%d score=%8.4f class=%-11s %.80s\n",
					i+1, r.Score, r.Memory.MemoryClass, r.Memory.Content)
			}
		}
		cleanup()
	}
}

func createRunner(t *testing.T, suffix string) (*eval.Runner, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("eval_%s.db", suffix))
	r, cleanup, err := eval.NewRunner(dbPath, "fts")
	if err != nil {
		t.Fatalf("create runner %s: %v", suffix, err)
	}
	return r, cleanup
}

func cloneDatasetFlat(ds *eval.EvalDataset) *eval.EvalDataset {
	flat := &eval.EvalDataset{
		Name:        ds.Name + " (flat)",
		Description: ds.Description,
		Cases:       ds.Cases,
	}
	flat.SeedMemories = make([]eval.SeedMemory, len(ds.SeedMemories))
	for i, s := range ds.SeedMemories {
		flat.SeedMemories[i] = eval.SeedMemory{
			Content: s.Content,
			Kind:    s.Kind,
			SubKind: s.SubKind,
		}
	}
	return flat
}

// buildEvolutionDataset constructs a three-layer memory dataset using English content.
// English ensures simple tokenizer (whitespace-based) produces valid BM25 scores
// for multiple memories per query, allowing class weights to influence ranking.
//
// Each topic has: 4 episodic (raw events) + 1 semantic (observation) + 1 procedural (strategy)
// Queries target the semantic/procedural content — class weights should boost those.
func buildEvolutionDataset() *eval.EvalDataset {
	seeds := []eval.SeedMemory{
		// === Topic 1: Frontend technology stack ===
		{Content: "In March 2024 the team discussed using React framework for new frontend projects", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In April 2024 the product detail page was rewritten from Vue to React and deployed", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In May 2024 new frontend engineers reported React was easier to learn than Vue", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In June 2024 the frontend team fully adopted React hooks replacing class components", Kind: "fact", MemoryClass: "episodic"},
		{Content: "The team gradually migrated from Vue to React for frontend development with improved efficiency and satisfaction", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "New frontend projects must use React with TypeScript by default and Vue should only be used for maintaining legacy projects", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 2: Database selection ===
		{Content: "In January 2024 PostgreSQL experienced lock wait timeouts during high concurrency writes", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In February 2024 testing showed ClickHouse was 20 times faster than PostgreSQL for log analysis queries", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In March 2024 monitoring log storage was migrated from PostgreSQL to ClickHouse", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In April 2024 the ClickHouse cluster processed 1 billion logs per day with stable performance", Kind: "fact", MemoryClass: "episodic"},
		{Content: "Analytical queries should use ClickHouse instead of PostgreSQL while transactional business logic should stay on PostgreSQL", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "Database selection strategy requires OLTP on PostgreSQL and OLAP on ClickHouse and caching on Redis with no mixing allowed", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 3: Deployment process ===
		{Content: "In May 2024 a manual deployment caused production configuration errors and 2 hours of downtime", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In June 2024 GitHub Actions CI CD pipeline was introduced for automated deployments", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In July 2024 all services were migrated to containerized deployment using Docker and Kubernetes", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In August 2024 deployment frequency increased from monthly to multiple times daily with zero rollbacks", Kind: "fact", MemoryClass: "episodic"},
		{Content: "Automated CI CD and containerization significantly improved deployment frequency and reliability", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "All services must be deployed through the CI CD pipeline and manual deployment to production is strictly prohibited", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 4: Team communication ===
		{Content: "In March 2024 daily standup meetings frequently ran overtime and went off topic", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In April 2024 the team switched to async text standups via Slack messages reporting three items", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In May 2024 the team reported that async standups saved time and improved efficiency", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In June 2024 the Friday all hands meeting was replaced with a biweekly format", Kind: "fact", MemoryClass: "episodic"},
		{Content: "Asynchronous communication works better than synchronous meetings for distributed teams saving time without losing information", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "Daily communication defaults to async Slack messages and video meetings should only be scheduled when real time discussion is needed", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 5: Security practices ===
		{Content: "In February 2024 an API key was accidentally committed to a public GitHub repository causing a security incident", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In March 2024 git secrets pre commit hooks were added to scan for sensitive information", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In April 2024 all secrets were migrated to HashiCorp Vault for centralized management", Kind: "fact", MemoryClass: "episodic"},
		{Content: "In May 2024 the security audit found zero secret leakage incidents", Kind: "fact", MemoryClass: "episodic"},
		{Content: "Migrating secret management from code repositories to Vault significantly improved security posture", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "All secrets must be stored in Vault and hardcoding secrets in source code or environment variable files is prohibited", Kind: "mental_model", MemoryClass: "procedural"},
	}

	cases := []eval.EvalCase{
		// Topic 1: frontend strategy
		{Query: "frontend technology stack strategy", Expected: []string{"React", "TypeScript"}, Category: "strategy", Difficulty: "medium"},
		{Query: "React or Vue for new projects", Expected: []string{"React", "Vue"}, Category: "preference", Difficulty: "easy"},
		{Query: "frontend framework selection criteria", Expected: []string{"React"}, Category: "strategy", Difficulty: "medium"},

		// Topic 2: database selection
		{Query: "database selection strategy", Expected: []string{"PostgreSQL", "ClickHouse"}, Category: "strategy", Difficulty: "medium"},
		{Query: "which database for analytical queries", Expected: []string{"ClickHouse"}, Category: "strategy", Difficulty: "easy"},
		{Query: "OLTP and OLAP database choice", Expected: []string{"PostgreSQL", "ClickHouse"}, Category: "strategy", Difficulty: "medium"},

		// Topic 3: deployment
		{Query: "deployment process rules", Expected: []string{"CI CD", "pipeline", "prohibited"}, Category: "strategy", Difficulty: "medium"},
		{Query: "can we manually deploy to production", Expected: []string{"prohibited", "CI CD"}, Category: "strategy", Difficulty: "easy"},
		{Query: "deployment method and containerization", Expected: []string{"Docker", "Kubernetes", "CI CD"}, Category: "strategy", Difficulty: "medium"},

		// Topic 4: communication
		{Query: "team communication policy", Expected: []string{"async", "Slack"}, Category: "strategy", Difficulty: "medium"},
		{Query: "when to use meetings vs messages", Expected: []string{"async", "video"}, Category: "strategy", Difficulty: "hard"},
		{Query: "daily standup format", Expected: []string{"async", "Slack"}, Category: "preference", Difficulty: "easy"},

		// Topic 5: security
		{Query: "secret management rules", Expected: []string{"Vault", "prohibited"}, Category: "strategy", Difficulty: "medium"},
		{Query: "where to store API keys", Expected: []string{"Vault"}, Category: "strategy", Difficulty: "easy"},
		{Query: "how to prevent secret leakage", Expected: []string{"Vault", "git secrets"}, Category: "strategy", Difficulty: "medium"},

		// Cross-topic
		{Query: "team best practices and strategies", Expected: []string{"CI CD", "async", "Vault", "React"}, Category: "cross-topic", Difficulty: "hard"},
		{Query: "overall technical decision principles", Expected: []string{"PostgreSQL", "ClickHouse", "React"}, Category: "cross-topic", Difficulty: "hard"},
	}

	return &eval.EvalDataset{
		Name:         "evolution-aware",
		Description:  "Three-layer memory evolution test with English content for valid BM25 scoring",
		SeedMemories: seeds,
		Cases:        cases,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
