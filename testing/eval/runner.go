// Package eval 提供评测运行器 / provides the evaluation test runner.
package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"iclude/internal/config"
	"iclude/internal/embed"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
)

var configOnce sync.Once

// loadTestConfig 加载 config.yaml 和 .env（向上查找项目根目录）
// Load config.yaml and .env by walking up to project root.
// Safe to call multiple times; only runs once per process.
// Explicitly loads .env from the project root so ${ENV_VAR} references in config.yaml
// expand correctly even when tests run with CWD = package directory.
func loadTestConfig() {
	configOnce.Do(func() {
		projectRoot := ""
		if os.Getenv("ICLUDE_CONFIG_PATH") == "" {
			dir, _ := os.Getwd()
			for {
				candidate := filepath.Join(dir, "config.yaml")
				if _, err := os.Stat(candidate); err == nil {
					_ = os.Setenv("ICLUDE_CONFIG_PATH", candidate)
					projectRoot = dir
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		} else {
			projectRoot = filepath.Dir(os.Getenv("ICLUDE_CONFIG_PATH"))
		}
		// 显式加载项目根目录的 .env，godotenv.autoload 只找 CWD，测试时 CWD=package 目录会找不到
		// Explicitly load .env from project root — godotenv.autoload only checks CWD (= package dir during tests)
		if projectRoot != "" {
			_ = godotenv.Load(filepath.Join(projectRoot, ".env"))
		}
		if err := config.LoadConfig(); err != nil {
			fmt.Printf("  loadTestConfig: warn: %v\n", err)
		}
	})
}

// LoadTestConfig 对外暴露配置加载，供测试文件调用 / Exported for use from *_test.go files.
func LoadTestConfig() { loadTestConfig() }

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

// Retriever 暴露底层检索器（用于 RetrieveWithDebug 等高级接口）/ Expose retriever for advanced use
func (r *Runner) Retriever() *search.Retriever {
	return r.retriever
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

// resolveEmbedder 创建 embedder，config.yaml 优先、env var 兜底
// Create embedder: config.yaml values first, env var fallback for backward compat.
func resolveEmbedder() (store.Embedder, error) {
	loadTestConfig()
	embCfg := config.AppConfig.LLM.Embedding

	// config.yaml 优先（字段已由 LoadConfig 展开）/ config.yaml first (fields expanded by LoadConfig)
	if embCfg.BaseURL != "" && embCfg.APIKey != "" {
		return embed.NewOpenAICompatibleEmbedder(embCfg.BaseURL, embCfg.APIKey, embCfg.Model), nil
	}
	if embCfg.Provider == "openai" && embCfg.APIKey != "" {
		return embed.NewOpenAIEmbedder(embCfg.APIKey, embCfg.Model), nil
	}

	// env var 兜底（向后兼容）/ Env var fallback (backward compat)
	embModel := firstNonEmpty(os.Getenv("EMBEDDING_MODEL"), "Qwen3-Embedding-8B")
	apiBase := firstNonEmpty(os.Getenv("EMBEDDING_API_BASE"), os.Getenv("EMBEDDING_BASE_URL"))
	apiKey := firstNonEmpty(os.Getenv("EMBEDDING_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	provider := os.Getenv("EMBEDDING_PROVIDER")

	if apiBase != "" {
		if apiKey == "" {
			return nil, fmt.Errorf("EMBEDDING_API_KEY (or OPENAI_API_KEY) required when EMBEDDING_API_BASE is set")
		}
		return embed.NewOpenAICompatibleEmbedder(apiBase, apiKey, embModel), nil
	}
	if provider == "openai" {
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for EMBEDDING_PROVIDER=openai")
		}
		return embed.NewOpenAIEmbedder(apiKey, embModel), nil
	}

	// Ollama — 探测可用性，不通则 fallback 到 OpenAI / Probe Ollama; fallback to OpenAI if unreachable
	ollamaBase := firstNonEmpty(os.Getenv("EMBEDDING_BASE_URL"), "http://localhost:11434")
	ollamaModel := firstNonEmpty(os.Getenv("EMBEDDING_MODEL"), "bge-m3")
	ollamaEmb := embed.NewOllamaEmbedder(ollamaBase, ollamaModel)
	if _, probeErr := ollamaEmb.Embed(context.Background(), "ping"); probeErr != nil {
		if apiKey == "" {
			return nil, fmt.Errorf("Ollama unreachable (%v) and no EMBEDDING_API_KEY/OPENAI_API_KEY set", probeErr)
		}
		fmt.Printf("  resolveEmbedder: Ollama unreachable, falling back to OpenAI (%s)\n", embModel)
		return embed.NewOpenAIEmbedder(apiKey, embModel), nil
	}
	return ollamaEmb, nil
}

// resolveLLMProvider 创建 LLM Provider，config.yaml 优先、env var 兜底
// Create LLM provider: config.yaml first, env var fallback for backward compat.
func resolveLLMProvider() llm.Provider {
	loadTestConfig()
	llmCfg := config.AppConfig.LLM

	// config.yaml 优先（字段已由 LoadConfig 展开）/ config.yaml first (fields expanded by LoadConfig)
	if llmCfg.OpenAI.BaseURL != "" && llmCfg.OpenAI.APIKey != "" {
		return llm.NewOpenAIProvider(llmCfg.OpenAI.BaseURL, llmCfg.OpenAI.APIKey, llmCfg.OpenAI.Model)
	}

	// env var 兜底（向后兼容）/ Env var fallback (backward compat)
	if localURL := firstNonEmpty(os.Getenv("LOCAL_API_BASE"), os.Getenv("LLM_BASE_URL")); localURL != "" {
		apiKey := firstNonEmpty(os.Getenv("LOCAL_API_KEY"), os.Getenv("LLM_API_KEY"), os.Getenv("OPENAI_API_KEY"), "local")
		llmModel := firstNonEmpty(os.Getenv("LOCAL_MODEL"), os.Getenv("LLM_MODEL"), os.Getenv("OPENAI_MODEL"), "default")
		return llm.NewOpenAIProvider(localURL, apiKey, llmModel)
	}
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		llmModel := firstNonEmpty(os.Getenv("OPENAI_MODEL"), os.Getenv("LLM_MODEL"), "gpt-4o-mini")
		return llm.NewOpenAIProvider("https://api.openai.com/v1", apiKey, llmModel)
	}
	return nil
}

// HasLLMConfig 检查是否有可用的 LLM 配置 / Check if an LLM provider can be resolved.
func HasLLMConfig() bool {
	loadTestConfig()
	return config.AppConfig.LLM.OpenAI.APIKey != "" || os.Getenv("OPENAI_API_KEY") != ""
}

// firstNonEmpty 返回第一个非空字符串 / Return first non-empty string
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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
	case "fts+hyde":
		// FTS + 意图感知 HyDE（仅 Semantic 意图触发，需要 Qdrant + LLM）
		cfg.Preprocess.Enabled = true
		cfg.Preprocess.UseLLM = true
		cfg.Preprocess.LLMTimeout = 10 * time.Second
		cfg.Preprocess.HyDEEnabled = true
		cfg.Preprocess.HyDEWeight = 0.8
		cfg.Preprocess.HyDEMinRunes = 25
		cfg.Rerank = config.RerankConfig{
			Enabled:     true,
			Provider:    "remote",
			BaseURL:     os.Getenv("RERANK_BASE_URL"),
			APIKey:      os.Getenv("RERANK_API_KEY"),
			Model:       os.Getenv("RERANK_MODEL"),
			TopK:        20,
			ScoreWeight: 0.7,
			Timeout:     10 * time.Second,
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
			Content:       seed.Content,
			Kind:          seed.Kind,
			SubKind:       seed.SubKind,
			MemoryClass:   seed.MemoryClass,
			Scope:         "eval/test",
			RetentionTier: model.TierPermanent,
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
