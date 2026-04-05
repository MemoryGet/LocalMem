// Package eval 提供评测运行器 / provides the evaluation test runner.
package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
)

// Runner 评测运行器 / Evaluation runner
type Runner struct {
	memStore  store.MemoryStore
	manager   *memory.Manager
	retriever *search.Retriever
	dbPath    string
}

// NewTestRunner 创建测试用临时运行器（FTS 模式，preprocess=false）/
// Creates a temporary runner for testing using FTS mode (preprocess=false).
// Returns the runner and a cleanup function.
func NewTestRunner(t *testing.T) (*Runner, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "eval_test.db")

	r, cleanup, err := NewRunner(dbPath, "fts")
	if err != nil {
		t.Fatalf("NewTestRunner: failed to create runner: %v", err)
	}
	return r, cleanup
}

// RunnerOption 运行器配置选项 / Runner configuration option
type RunnerOption func(*runnerOpts)

type runnerOpts struct {
	tokenizerName string // simple | gse | jieba
	jiebaURL      string
}

// WithTokenizer 指定分词器 / Specify tokenizer
func WithTokenizer(name string) RunnerOption {
	return func(o *runnerOpts) { o.tokenizerName = name }
}

// WithJiebaURL 指定 jieba 服务地址 / Specify jieba service URL
func WithJiebaURL(url string) RunnerOption {
	return func(o *runnerOpts) { o.jiebaURL = url }
}

// NewRunner 创建指定模式的评测运行器 / Creates an evaluation runner for the specified mode.
// Modes: "fts" (FTS-only), "hybrid" (FTS + preprocess + LLM), "hybrid+rerank" (hybrid + overlap rerank).
func NewRunner(dbPath string, mode string, opts ...RunnerOption) (*Runner, func(), error) {
	ctx := context.Background()

	o := &runnerOpts{tokenizerName: "simple", jiebaURL: "http://localhost:8866"}
	for _, opt := range opts {
		opt(o)
	}

	tok, err := buildTokenizer(o)
	if err != nil {
		return nil, nil, fmt.Errorf("NewRunner: create tokenizer: %w", err)
	}
	bm25Weights := [3]float64{config.DefaultBM25Content, config.DefaultBM25Excerpt, config.DefaultBM25Summary}

	memStore, err := store.NewSQLiteMemoryStore(dbPath, bm25Weights, tok)
	if err != nil {
		return nil, nil, fmt.Errorf("NewRunner: create sqlite store: %w", err)
	}

	if err := memStore.Init(ctx); err != nil {
		return nil, nil, fmt.Errorf("NewRunner: init sqlite store: %w", err)
	}

	// LLM Provider：hybrid 模式从环境变量加载 / Load LLM from env for hybrid modes
	var llmProvider llm.Provider
	if mode != "fts" {
		llmProvider = resolveLLMProvider()
	}

	// Manager：hybrid 模式注入 LLM（丰富摘要生成）/ Inject LLM for rich abstract generation
	mgr := memory.NewManager(memory.ManagerDeps{MemStore: memStore, LLMProvider: llmProvider})

	cfg := buildRetrievalConfig(mode)
	var retriever *search.Retriever
	if mode == "fts" {
		retriever = search.NewRetriever(memStore, nil, nil, nil, nil, cfg, nil, nil)
	} else {
		// hybrid：Preprocessor 注入 tokenizer + LLM（语义改写 + HyDE）/ Inject tokenizer + LLM for semantic rewrite + HyDE
		preprocessor := search.NewPreprocessor(tok, nil, llmProvider, cfg)
		retriever = search.NewRetriever(memStore, nil, nil, nil, llmProvider, cfg, preprocessor, nil)
	}

	r := &Runner{
		memStore:  memStore,
		manager:   mgr,
		retriever: retriever,
		dbPath:    dbPath,
	}
	return r, func() { _ = memStore.Close() }, nil
}

// RetrieveRaw 返回原始检索结果（用于调试排序）/ Return raw retrieval results for debugging
func (r *Runner) RetrieveRaw(ctx context.Context, query string, limit int) ([]*model.SearchResult, error) {
	return r.retriever.Retrieve(ctx, &model.RetrieveRequest{Query: query, Limit: limit})
}

// SeedOne 播种单条记忆 / Seed a single memory
func (r *Runner) SeedOne(ctx context.Context, seed SeedMemory) error {
	_, err := r.manager.Create(ctx, &model.CreateMemoryRequest{
		Content:     seed.Content,
		Kind:        seed.Kind,
		SubKind:     seed.SubKind,
		MemoryClass: seed.MemoryClass,
		Scope:       "eval/test",
	})
	return err
}

// buildTokenizer 根据名称创建分词器 / Create tokenizer by name
func buildTokenizer(o *runnerOpts) (tokenizer.Tokenizer, error) {
	switch o.tokenizerName {
	case "gse":
		return tokenizer.NewGseTokenizer("", nil)
	case "jieba":
		return tokenizer.NewJiebaTokenizer(o.jiebaURL), nil
	default:
		return tokenizer.NewSimpleTokenizer(), nil
	}
}

// resolveLLMProvider 从环境变量创建 LLM Provider / Create LLM provider from env vars
func resolveLLMProvider() llm.Provider {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return llm.NewOpenAIProvider(baseURL, apiKey, model)
}

// buildRetrievalConfig 根据模式构建检索配置 / Build retrieval config for the given mode
func buildRetrievalConfig(mode string) config.RetrievalConfig {
	cfg := config.RetrievalConfig{
		FTSWeight:   1.0,
		GraphWeight: 0.8,
		AccessAlpha: 0.15,
		Preprocess: config.PreprocessConfig{
			Enabled: false,
		},
		Rerank: config.RerankConfig{
			Enabled: false,
		},
	}

	switch mode {
	case "hybrid":
		cfg.Preprocess.Enabled = true
		cfg.Preprocess.UseLLM = true
		cfg.Preprocess.LLMTimeout = 10 * time.Second
		cfg.Preprocess.SynonymFiles = []string{"config/synonym_zh.txt"}
	case "hybrid+rerank":
		cfg.Preprocess.Enabled = true
		cfg.Preprocess.UseLLM = true
		cfg.Preprocess.LLMTimeout = 10 * time.Second
		cfg.Preprocess.SynonymFiles = []string{"config/synonym_zh.txt"}
		cfg.Rerank = config.RerankConfig{
			Enabled:     true,
			Provider:    "overlap",
			TopK:        20,
			ScoreWeight: 0.7,
		}
	}
	return cfg
}

// Run 播种记忆并运行所有评测用例 / Seeds memories and runs all evaluation cases.
// Returns an EvalReport with aggregated metrics.
func (r *Runner) Run(ctx context.Context, ds *EvalDataset, mode string) (*EvalReport, error) {
	start := time.Now()

	// 播种记忆 / Seed memories
	for i, seed := range ds.SeedMemories {
		req := &model.CreateMemoryRequest{
			Content:     seed.Content,
			Kind:        seed.Kind,
			SubKind:     seed.SubKind,
			MemoryClass: seed.MemoryClass,
			Scope:       "eval/test",
		}
		if _, err := r.manager.Create(ctx, req); err != nil {
			return nil, fmt.Errorf("Run: seed memory %d: %w", i, err)
		}
	}

	// 运行查询用例 / Run query cases
	cases := make([]CaseResult, 0, len(ds.Cases))
	for _, ec := range ds.Cases {
		req := &model.RetrieveRequest{
			Query: ec.Query,
			Limit: 10,
		}
		results, err := r.retriever.Retrieve(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("Run: retrieve for query %q: %w", ec.Query, err)
		}

		hit, rank, score := checkHit(results, ec.Expected)
		cr := CaseResult{
			Query:       ec.Query,
			Expected:    strings.Join(ec.Expected, "|"),
			Category:    ec.Category,
			Difficulty:  ec.Difficulty,
			Hit:         hit,
			Rank:        rank,
			Score:       score,
			ResultCount: len(results),
		}
		cases = append(cases, cr)
	}

	metrics := Aggregate(cases)

	report := &EvalReport{
		Mode:         mode,
		Dataset:      ds.Name,
		Timestamp:    time.Now(),
		Metrics:      metrics,
		ByCategory:   groupAggregate(cases, func(c CaseResult) string { return c.Category }),
		ByDifficulty: groupAggregate(cases, func(c CaseResult) string { return c.Difficulty }),
		Cases:        cases,
		Duration:     time.Since(start),
		GitCommit:    resolveGitCommit(),
	}
	return report, nil
}

// checkHit 检查检索结果中是否命中期望关键词 / Check if any expected keyword appears in results.
// Returns (hit, rank 1-based, score). Returns (false, -1, 0) on miss.
func checkHit(results []*model.SearchResult, expected []string) (bool, int, float64) {
	for i, res := range results {
		content := strings.ToLower(res.Memory.Content)
		excerpt := strings.ToLower(res.Memory.Excerpt)
		for _, kw := range expected {
			kw = strings.ToLower(kw)
			if strings.Contains(content, kw) || strings.Contains(excerpt, kw) {
				return true, i + 1, res.Score
			}
		}
	}
	return false, -1, 0
}

// groupAggregate 按键函数分组并计算聚合指标 / Group cases by key function and aggregate each group.
func groupAggregate(cases []CaseResult, keyFn func(CaseResult) string) map[string]AggregateMetrics {
	groups := make(map[string][]CaseResult)
	for _, c := range cases {
		key := keyFn(c)
		groups[key] = append(groups[key], c)
	}

	result := make(map[string]AggregateMetrics, len(groups))
	for key, group := range groups {
		result[key] = Aggregate(group)
	}
	return result
}

// resolveGitCommit 获取当前 git commit hash / Get current git commit hash
func resolveGitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PrintReport 终端输出评测报告 / Print evaluation report to terminal
func PrintReport(report *EvalReport) {
	fmt.Printf("\n=== Eval: %s | %s ===\n", report.Mode, report.Dataset)
	m := report.Metrics
	fmt.Printf("  HitRate:  %5.1f%%  MRR: %.3f  NDCG@10: %.3f\n", m.HitRate, m.MRR, m.NDCG10)
	fmt.Printf("  Recall@1: %5.1f%%  @3: %.1f%%  @5: %.1f%%  @10: %.1f%%\n",
		m.RecallAt1*100, m.RecallAt3*100, m.RecallAt5*100, m.RecallAt10*100)
	fmt.Printf("  Duration: %s | Cases: %d\n", report.Duration.Round(time.Millisecond), m.Total)

	if len(report.ByCategory) > 0 {
		fmt.Printf("\n  By Category:\n")
		for cat, cm := range report.ByCategory {
			fmt.Printf("    %-12s %5.1f%% (MRR %.3f, %d cases)\n", cat+":", cm.HitRate, cm.MRR, cm.Total)
		}
	}
	if len(report.ByDifficulty) > 0 {
		fmt.Printf("\n  By Difficulty:\n")
		for diff, dm := range report.ByDifficulty {
			fmt.Printf("    %-8s %5.1f%% (MRR %.3f, %d cases)\n", diff+":", dm.HitRate, dm.MRR, dm.Total)
		}
	}
	fmt.Println()
}

// PrintComparison 输出基线对比 / Print baseline comparison
func PrintComparison(current, baseline *EvalReport, regressions []Regression) {
	fmt.Printf("  vs baseline %s:\n", baseline.Mode)
	printDelta := func(name string, cur, base float64, pct bool) {
		symbol := "✓"
		for _, r := range regressions {
			if r.Metric == name {
				symbol = "✗ REGRESSION"
				break
			}
		}
		delta := cur - base
		if pct {
			fmt.Printf("    %-10s %5.1f%% → %5.1f%% (%+.1f) %s\n", name+":", base, cur, delta, symbol)
		} else {
			fmt.Printf("    %-10s %.3f → %.3f (%+.3f) %s\n", name+":", base, cur, delta, symbol)
		}
	}
	printDelta("HitRate", current.Metrics.HitRate, baseline.Metrics.HitRate, true)
	printDelta("MRR", current.Metrics.MRR, baseline.Metrics.MRR, false)
	printDelta("NDCG@10", current.Metrics.NDCG10, baseline.Metrics.NDCG10, false)
	fmt.Println()
}
