package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/embed"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/pipeline/builtin"
	"iclude/internal/search/strategy"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
)

// LongMemEvalEntry 单个 LongMemEval 问题（独立 seed + case）/ Single LongMemEval question with its own seeds
type LongMemEvalEntry struct {
	SeedMemories []SeedMemory       `json:"seed_memories"`
	Case         LongMemEvalCase    `json:"case"`
}

// LongMemEvalCase LongMemEval 用例 / LongMemEval case with extra metadata
type LongMemEvalCase struct {
	Query        string   `json:"query"`
	Expected     []string `json:"expected"`
	Category     string   `json:"category"`
	Difficulty   string   `json:"difficulty"`
	QuestionID   string   `json:"question_id"`
	GoldAnswer   string   `json:"gold_answer"`
	IsAbstention bool     `json:"is_abstention"`
}

// LoadLongMemEval 加载 LongMemEval 适配后的数据集 / Load adapted LongMemEval dataset
func LoadLongMemEval(path string) ([]LongMemEvalEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read longmemeval file: %w", err)
	}
	var entries []LongMemEvalEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse longmemeval JSON: %w", err)
	}
	return entries, nil
}

// RunLongMemEval 逐问题独立建库评测 / Run per-question isolated evaluation
func RunLongMemEval(ctx context.Context, entries []LongMemEvalEntry, tmpDir string) (*EvalReport, error) {
	start := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%50 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [%d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		cr, err := runSingleQuestion(ctx, entry, tmpDir, i)
		if err != nil {
			// 记录失败但继续 / Log failure but continue
			cases = append(cases, CaseResult{
				Query:      entry.Case.Query,
				Expected:   entry.Case.GoldAnswer,
				Category:   entry.Case.Category,
				Difficulty: entry.Case.Difficulty,
				Hit:        false,
				Rank:       -1,
			})
			continue
		}
		cases = append(cases, cr)
	}

	metrics := Aggregate(cases)
	report := &EvalReport{
		Mode:         "fts (simple) — LongMemEval oracle",
		Dataset:      "longmemeval-oracle",
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

// RunLongMemEvalPipeline 管线模式逐问题评测 / Pipeline-mode per-question evaluation
func RunLongMemEvalPipeline(ctx context.Context, entries []LongMemEvalEntry, tmpDir string) (*EvalReport, error) {
	start := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%50 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [pipeline %d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		cr, err := runSingleQuestionPipeline(ctx, entry, tmpDir, i)
		if err != nil {
			cases = append(cases, CaseResult{
				Query:      entry.Case.Query,
				Expected:   entry.Case.GoldAnswer,
				Category:   entry.Case.Category,
				Difficulty: entry.Case.Difficulty,
				Hit:        false,
				Rank:       -1,
			})
			continue
		}
		cases = append(cases, cr)
	}

	metrics := Aggregate(cases)
	report := &EvalReport{
		Mode:         "pipeline (rule classifier) — LongMemEval oracle",
		Dataset:      "longmemeval-oracle",
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

// runSingleQuestionPipeline 管线模式单问题评测 / Pipeline-mode single question evaluation
func runSingleQuestionPipeline(ctx context.Context, entry LongMemEvalEntry, tmpDir string, idx int) (CaseResult, error) {
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("pq%d.db", idx))

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return CaseResult{}, fmt.Errorf("create store: %w", err)
	}
	defer func() {
		_ = memStore.Close()
		_ = os.Remove(dbPath)
	}()

	if err := memStore.Init(ctx); err != nil {
		return CaseResult{}, fmt.Errorf("init store: %w", err)
	}

	mgr := memory.NewManager(memory.ManagerDeps{MemStore: memStore})

	for _, sm := range entry.SeedMemories {
		kind := sm.Kind
		if kind == "" {
			kind = "conversation"
		}
		_, err := mgr.Create(ctx, &model.CreateMemoryRequest{
			Content: sm.Content,
			Kind:    kind,
			SubKind: sm.SubKind,
			Scope:   "eval/longmemeval",
		})
		if err != nil {
			return CaseResult{}, fmt.Errorf("seed: %w", err)
		}
	}

	cfg := buildRetrievalConfig("fts")
	retriever := search.NewRetriever(memStore, nil, nil, nil, nil, cfg, nil, nil)
	retriever.InitPipeline() // 关键区别：启用管线模式 / Key difference: enable pipeline mode

	results, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
		Query: entry.Case.Query,
		Limit: 10,
	})
	if err != nil {
		return CaseResult{}, fmt.Errorf("retrieve: %w", err)
	}

	hit, rank, score := checkHit(results, entry.Case.Expected)
	if !hit && entry.Case.GoldAnswer != "" {
		hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
	}

	return CaseResult{
		Query:       entry.Case.Query,
		Expected:    entry.Case.GoldAnswer,
		Category:    entry.Case.Category,
		Difficulty:  entry.Case.Difficulty,
		Hit:         hit,
		Rank:        rank,
		Score:       score,
		ResultCount: len(results),
	}, nil
}

// runSingleQuestion 为单个问题创建独立 DB，seed 后查询 / Create isolated DB for one question
func runSingleQuestion(ctx context.Context, entry LongMemEvalEntry, tmpDir string, idx int) (CaseResult, error) {
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("q%d.db", idx))

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return CaseResult{}, fmt.Errorf("create store: %w", err)
	}
	defer func() {
		_ = memStore.Close()
		_ = os.Remove(dbPath)
	}()

	if err := memStore.Init(ctx); err != nil {
		return CaseResult{}, fmt.Errorf("init store: %w", err)
	}

	mgr := memory.NewManager(memory.ManagerDeps{MemStore: memStore})

	// Seed
	for _, sm := range entry.SeedMemories {
		kind := sm.Kind
		if kind == "" {
			kind = "conversation"
		}
		_, err := mgr.Create(ctx, &model.CreateMemoryRequest{
			Content: sm.Content,
			Kind:    kind,
			SubKind: sm.SubKind,
			Scope:   "eval/longmemeval",
		})
		if err != nil {
			return CaseResult{}, fmt.Errorf("seed: %w", err)
		}
	}

	// Retrieve
	cfg := buildRetrievalConfig("fts")
	retriever := search.NewRetriever(memStore, nil, nil, nil, nil, cfg, nil, nil)

	results, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
		Query: entry.Case.Query,
		Limit: 10,
	})
	if err != nil {
		return CaseResult{}, fmt.Errorf("retrieve: %w", err)
	}

	hit, rank, score := checkHit(results, entry.Case.Expected)

	// 如果关键词匹配失败，尝试宽松匹配（answer 中的核心词出现在任一结果中）
	if !hit && entry.Case.GoldAnswer != "" {
		hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
	}

	return CaseResult{
		Query:       entry.Case.Query,
		Expected:    entry.Case.GoldAnswer,
		Category:    entry.Case.Category,
		Difficulty:  entry.Case.Difficulty,
		Hit:         hit,
		Rank:        rank,
		Score:       score,
		ResultCount: len(results),
	}, nil
}

// RunLongMemEvalGraphPipeline 图谱增强管线评测（实体抽取 + graph stage）/ Graph-enhanced pipeline evaluation
// maxQuestions 限制评测问题数（0=全部），避免大量 LLM 调用 / Limit questions to avoid excessive LLM calls
func RunLongMemEvalGraphPipeline(ctx context.Context, entries []LongMemEvalEntry, tmpDir string, maxQuestions int) (*EvalReport, error) {
	llmProvider := resolveLLMProvider()
	if llmProvider == nil {
		return nil, fmt.Errorf("LLM provider required for graph pipeline (set OPENAI_API_KEY)")
	}

	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	start := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%10 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [graph-pipeline %d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		cr, err := runSingleQuestionGraphPipeline(ctx, entry, tmpDir, i, llmProvider)
		if err != nil {
			cases = append(cases, CaseResult{
				Query:      entry.Case.Query,
				Expected:   entry.Case.GoldAnswer,
				Category:   entry.Case.Category,
				Difficulty: entry.Case.Difficulty,
				Hit:        false,
				Rank:       -1,
			})
			continue
		}
		cases = append(cases, cr)

		// 避免 API 限流 / Avoid API rate limits
		time.Sleep(100 * time.Millisecond)
	}

	metrics := Aggregate(cases)
	report := &EvalReport{
		Mode:         "pipeline (graph) — LongMemEval oracle",
		Dataset:      "longmemeval-oracle",
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

// RunLongMemEvalFullPipeline 完整管线评测（Graph + LLM rerank）/ Full pipeline evaluation with Graph + LLM reranker
// maxQuestions 限制评测问题数（0=全部）/ Limit questions (0=all)
func RunLongMemEvalFullPipeline(ctx context.Context, entries []LongMemEvalEntry, tmpDir string, maxQuestions int) (*EvalReport, error) {
	llmProvider := resolveLLMProvider()
	if llmProvider == nil {
		return nil, fmt.Errorf("LLM provider required for full pipeline (set OPENAI_API_KEY)")
	}

	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	start := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%10 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [full-pipeline %d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		cr, err := runSingleQuestionFullPipeline(ctx, entry, tmpDir, i, llmProvider)
		if err != nil {
			cases = append(cases, CaseResult{
				Query:      entry.Case.Query,
				Expected:   entry.Case.GoldAnswer,
				Category:   entry.Case.Category,
				Difficulty: entry.Case.Difficulty,
				Hit:        false,
				Rank:       -1,
			})
			continue
		}
		cases = append(cases, cr)

		// 避免 API 限流 / Avoid API rate limits
		time.Sleep(100 * time.Millisecond)
	}

	metrics := Aggregate(cases)
	report := &EvalReport{
		Mode:         "pipeline (full) — LongMemEval oracle",
		Dataset:      "longmemeval-oracle",
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

// runSingleQuestionGraphPipeline 图谱增强管线单问题评测 / Graph-enhanced pipeline single question evaluation
// 与 runSingleQuestionPipeline 区别：创建 graphStore + extractor，seed 时自动抽取实体
func runSingleQuestionGraphPipeline(ctx context.Context, entry LongMemEvalEntry, tmpDir string, idx int, llmProvider llm.Provider) (CaseResult, error) {
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("gq%d.db", idx))

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return CaseResult{}, fmt.Errorf("create store: %w", err)
	}
	defer func() {
		_ = memStore.Close()
		_ = os.Remove(dbPath)
	}()

	if err := memStore.Init(ctx); err != nil {
		return CaseResult{}, fmt.Errorf("init store: %w", err)
	}

	// 创建图谱存储和管理器 / Create graph store and manager
	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)
	extractor := memory.NewExtractor(llmProvider, graphMgr, memStore, nil, config.ExtractConfig{})

	// Manager 不传 LLMProvider，避免 seed 时逐条 LLM 生成 excerpt / No LLMProvider to skip per-seed excerpt generation
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:  memStore,
		Extractor: extractor,
	})

	for _, sm := range entry.SeedMemories {
		kind := sm.Kind
		if kind == "" {
			kind = "conversation"
		}
		_, err := mgr.Create(ctx, &model.CreateMemoryRequest{
			Content: sm.Content,
			Kind:    kind,
			SubKind: sm.SubKind,
			Scope:   "eval/longmemeval",
		})
		if err != nil {
			return CaseResult{}, fmt.Errorf("seed: %w", err)
		}
	}

	// 图谱增强检索配置 / Graph-enhanced retrieval config
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	retriever := search.NewRetriever(memStore, nil, nil, graphStore, llmProvider, cfg, nil, nil)
	retriever.InitPipeline()

	results, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
		Query: entry.Case.Query,
		Limit: 10,
	})
	if err != nil {
		return CaseResult{}, fmt.Errorf("retrieve: %w", err)
	}

	hit, rank, score := checkHit(results, entry.Case.Expected)
	if !hit && entry.Case.GoldAnswer != "" {
		hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
	}

	return CaseResult{
		Query:       entry.Case.Query,
		Expected:    entry.Case.GoldAnswer,
		Category:    entry.Case.Category,
		Difficulty:  entry.Case.Difficulty,
		Hit:         hit,
		Rank:        rank,
		Score:       score,
		ResultCount: len(results),
	}, nil
}

// runSingleQuestionFullPipeline 完整管线单问题评测（Graph + LLM rerank）/ Full pipeline single question evaluation
func runSingleQuestionFullPipeline(ctx context.Context, entry LongMemEvalEntry, tmpDir string, idx int, llmProvider llm.Provider) (CaseResult, error) {
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("fq%d.db", idx))

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return CaseResult{}, fmt.Errorf("create store: %w", err)
	}
	defer func() {
		_ = memStore.Close()
		_ = os.Remove(dbPath)
	}()

	if err := memStore.Init(ctx); err != nil {
		return CaseResult{}, fmt.Errorf("init store: %w", err)
	}

	// 创建图谱存储和管理器 / Create graph store and manager
	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)
	extractor := memory.NewExtractor(llmProvider, graphMgr, memStore, nil, config.ExtractConfig{})

	// Manager 不传 LLMProvider，避免 seed 时逐条 LLM 生成 excerpt / No LLMProvider to skip per-seed excerpt generation
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:  memStore,
		Extractor: extractor,
	})

	for _, sm := range entry.SeedMemories {
		kind := sm.Kind
		if kind == "" {
			kind = "conversation"
		}
		_, err := mgr.Create(ctx, &model.CreateMemoryRequest{
			Content: sm.Content,
			Kind:    kind,
			SubKind: sm.SubKind,
			Scope:   "eval/longmemeval",
		})
		if err != nil {
			return CaseResult{}, fmt.Errorf("seed: %w", err)
		}
	}

	// 完整管线配置：Graph + LLM rerank / Full pipeline config: Graph + LLM rerank
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	retriever := search.NewRetriever(memStore, nil, nil, graphStore, llmProvider, cfg, nil, nil)
	retriever.InitPipeline()

	// 强制使用 full 管线（Graph + LLM rerank）/ Force full pipeline (Graph + LLM rerank)
	results, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
		Query:    entry.Case.Query,
		Limit:    10,
		Pipeline: "full",
	})
	if err != nil {
		return CaseResult{}, fmt.Errorf("retrieve: %w", err)
	}

	hit, rank, score := checkHit(results, entry.Case.Expected)
	if !hit && entry.Case.GoldAnswer != "" {
		hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
	}

	return CaseResult{
		Query:       entry.Case.Query,
		Expected:    entry.Case.GoldAnswer,
		Category:    entry.Case.Category,
		Difficulty:  entry.Case.Difficulty,
		Hit:         hit,
		Rank:        rank,
		Score:       score,
		ResultCount: len(results),
	}, nil
}

// fuzzyCheckHit 宽松匹配：将 gold answer 拆词后检查是否多数词出现在结果中
func fuzzyCheckHit(results []*model.SearchResult, goldAnswer string) (bool, int, float64) {
	// 提取 gold answer 中的有意义词（长度>=2）
	words := strings.Fields(strings.ToLower(goldAnswer))
	var meaningful []string
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'()[]")
		if len(w) >= 2 {
			meaningful = append(meaningful, w)
		}
	}
	if len(meaningful) == 0 {
		return false, -1, 0
	}

	// 至少 50% 的有意义词出现在某个结果中
	threshold := len(meaningful) / 2
	if threshold < 1 {
		threshold = 1
	}

	for i, r := range results {
		if r == nil || r.Memory == nil {
			continue
		}
		content := strings.ToLower(r.Memory.Content)
		matched := 0
		for _, w := range meaningful {
			if strings.Contains(content, w) {
				matched++
			}
		}
		if matched >= threshold {
			return true, i + 1, r.Score
		}
	}
	return false, -1, 0
}

// RunLongMemEvalAllLLM 全链路 LLM 评测：实体抽取 + strategy agent + graph + LLM rerank + preprocess
// Full LLM pipeline evaluation with per-stage token tracking
func RunLongMemEvalAllLLM(ctx context.Context, entries []LongMemEvalEntry, tmpDir string, maxQuestions int) (*EvalReport, *LLMTracker, error) {
	rawProvider := resolveLLMProvider()
	if rawProvider == nil {
		return nil, nil, fmt.Errorf("LLM provider required (set OPENAI_API_KEY)")
	}

	tracker := NewLLMTracker()

	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	start := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%10 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			total := tracker.Total()
			fmt.Printf("  [all-llm %d/%d] hit %d/%d (%.1f%%) | LLM calls: %d, tokens: %d\n",
				i, len(entries), hits, i, float64(hits)*100/float64(i),
				total.Calls, total.TotalTokens)
		}

		cr, err := runSingleQuestionAllLLM(ctx, entry, tmpDir, i, rawProvider, tracker)
		if err != nil {
			cases = append(cases, CaseResult{
				Query:      entry.Case.Query,
				Expected:   entry.Case.GoldAnswer,
				Category:   entry.Case.Category,
				Difficulty: entry.Case.Difficulty,
				Hit:        false,
				Rank:       -1,
			})
			continue
		}
		cases = append(cases, cr)

		time.Sleep(100 * time.Millisecond)
	}

	metrics := Aggregate(cases)
	report := &EvalReport{
		Mode:         "pipeline (all-llm) — LongMemEval oracle",
		Dataset:      "longmemeval-oracle",
		Timestamp:    time.Now(),
		Metrics:      metrics,
		ByCategory:   groupAggregate(cases, func(c CaseResult) string { return c.Category }),
		ByDifficulty: groupAggregate(cases, func(c CaseResult) string { return c.Difficulty }),
		Cases:        cases,
		Duration:     time.Since(start),
		GitCommit:    resolveGitCommit(),
	}
	return report, tracker, nil
}

// runSingleQuestionAllLLM 全链路 LLM 单问题评测 / All-LLM single question evaluation
// LLM 使用点:
//   1. entity_extraction — 实体抽取（seed 时 Extractor 调 LLM）
//   2. strategy_agent — 管线选择（Agent.Select 调 LLM）
//   3. rerank_llm — LLM 精排（full 管线的 rerank_llm stage）
//   4. preprocess — 查询预处理（HyDE/语义改写，如果开启）
//   5. graph_llm_fallback — 图谱 LLM 实体抽取 fallback
func runSingleQuestionAllLLM(ctx context.Context, entry LongMemEvalEntry, tmpDir string, idx int, rawProvider llm.Provider, tracker *LLMTracker) (CaseResult, error) {
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("allllm%d.db", idx))

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return CaseResult{}, fmt.Errorf("create store: %w", err)
	}
	defer func() {
		_ = memStore.Close()
		_ = os.Remove(dbPath)
	}()

	if err := memStore.Init(ctx); err != nil {
		return CaseResult{}, fmt.Errorf("init store: %w", err)
	}

	// --- 为每个阶段创建独立追踪的 Provider / Create per-stage tracked providers ---
	extractProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "entity_extraction"}
	strategyProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "strategy_agent"}
	rerankProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "rerank_llm"}
	preprocessProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "preprocess"}

	// --- 1. 图谱存储 + 实体抽取（用 extractProvider 追踪）/ Graph store + entity extraction ---
	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)
	extractor := memory.NewExtractor(extractProvider, graphMgr, memStore, nil, config.ExtractConfig{})

	// Manager 不传 LLMProvider，避免 seed 时逐条 LLM 生成 excerpt / No LLMProvider to skip per-seed excerpt generation
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:  memStore,
		Extractor: extractor,
	})

	// Seed memories（触发实体抽取）/ Seed memories (triggers entity extraction)
	for _, sm := range entry.SeedMemories {
		kind := sm.Kind
		if kind == "" {
			kind = "conversation"
		}
		_, err := mgr.Create(ctx, &model.CreateMemoryRequest{
			Content: sm.Content,
			Kind:    kind,
			SubKind: sm.SubKind,
			Scope:   "eval/longmemeval",
		})
		if err != nil {
			return CaseResult{}, fmt.Errorf("seed: %w", err)
		}
	}

	// --- 2. 配置检索管线（开启所有 LLM 功能）/ Configure retrieval with all LLM features ---
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	// 开启 strategy agent LLM 选择 / Enable LLM strategy selection
	cfg.Strategy.UseLLM = true
	cfg.Strategy.FallbackPipeline = "exploration"
	// 开启预处理 LLM（HyDE + 语义改写）/ Enable preprocessing LLM (HyDE + semantic rewrite)
	cfg.Preprocess.Enabled = true
	cfg.Preprocess.UseLLM = true
	cfg.Preprocess.LLMTimeout = 10 * time.Second

	// Preprocessor 用 preprocessProvider / Preprocessor uses preprocessProvider
	preprocessor := search.NewPreprocessor(tok, graphStore, preprocessProvider, cfg)

	// Retriever: graphFallbackProvider 用于图谱 LLM fallback，rerankProvider 用于 LLM rerank
	// 注意：builtin.Deps.LLM 会被注入到 rerank_llm stage 和 graph stage 的 LLM fallback
	// 这里需要用一个统一 Provider，但通过 stageProvider 分别追踪
	// 解决方案：用 rerankProvider（rerank_llm stage 会用它），graph LLM fallback 用 graphFallbackProvider
	retriever := search.NewRetriever(memStore, nil, nil, graphStore, rerankProvider, cfg, preprocessor, nil)

	// 手动初始化管线，覆盖 strategy agent 的 LLM / Manually init pipeline with overridden strategy LLM
	registry := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher: memStore,
		GraphStore:  graphStore,
		LLM:         rerankProvider, // rerank_llm stage 用这个 / rerank_llm stage uses this
		Cfg:         cfg,
	}
	postStages := builtin.RegisterBuiltins(registry, deps)
	executor := pipeline.NewExecutor(registry, pipeline.WithPostStages(postStages...))

	rc := strategy.NewRuleClassifier(pipeline.PipelineExploration)
	agent := strategy.NewAgent(strategyProvider, rc, 5*time.Second)

	retriever.SetPipelineComponents(executor, agent, rc)

	// --- 3. 执行检索（不强制管线，让 strategy agent 选择）/ Execute retrieval (let strategy agent decide) ---
	results, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
		Query: entry.Case.Query,
		Limit: 10,
	})
	if err != nil {
		return CaseResult{}, fmt.Errorf("retrieve: %w", err)
	}

	hit, rank, score := checkHit(results, entry.Case.Expected)
	if !hit && entry.Case.GoldAnswer != "" {
		hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
	}

	return CaseResult{
		Query:       entry.Case.Query,
		Expected:    entry.Case.GoldAnswer,
		Category:    entry.Case.Category,
		Difficulty:  entry.Case.Difficulty,
		Hit:         hit,
		Rank:        rank,
		Score:       score,
		ResultCount: len(results),
	}, nil
}

// RunLongMemEvalSingleVerbose 单问题详细调试评测，每一步输出日志
// Single-question verbose debug evaluation with step-by-step logging
func RunLongMemEvalSingleVerbose(ctx context.Context, entry LongMemEvalEntry, tmpDir string) error {
	rawProvider := resolveLLMProvider()
	if rawProvider == nil {
		return fmt.Errorf("LLM provider required (set OPENAI_API_KEY)")
	}

	tracker := NewLLMTracker()
	start := time.Now()
	qCase := entry.Case

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Printf("VERBOSE SINGLE-QUESTION DEBUG\n")
	fmt.Printf("Question ID: %s\n", qCase.QuestionID)
	fmt.Printf("Query:       %s\n", qCase.Query)
	fmt.Printf("Expected:    %v\n", qCase.Expected)
	fmt.Printf("Gold Answer: %s\n", qCase.GoldAnswer)
	fmt.Printf("Category:    %s | Difficulty: %s\n", qCase.Category, qCase.Difficulty)
	fmt.Printf("Seed memories: %d\n", len(entry.SeedMemories))
	fmt.Println(strings.Repeat("=", 70))

	// --- Step 1: 建库 / Create DB ---
	stepStart := time.Now()
	dbPath := filepath.Join(tmpDir, "verbose_debug.db")
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return fmt.Errorf("create store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	fmt.Printf("\n[Step 1] DB created (%s)\n", time.Since(stepStart).Round(time.Millisecond))

	// --- Step 2: 图谱 + 实体抽取器 / Graph + Extractor ---
	stepStart = time.Now()
	extractProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "entity_extraction"}
	strategyProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "strategy_agent"}
	rerankProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "rerank_llm"}
	preprocessProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "preprocess"}

	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)
	ext := memory.NewExtractor(extractProvider, graphMgr, memStore, nil, config.ExtractConfig{})

	// Manager 不传 LLMProvider，避免 seed 时逐条 LLM 生成 excerpt / No LLMProvider to skip per-seed excerpt generation
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:  memStore,
		Extractor: ext,
	})
	fmt.Printf("[Step 2] Graph store + Extractor initialized (%s)\n", time.Since(stepStart).Round(time.Millisecond))

	// --- Step 3: Seed memories + 收集 ID / Seed memories and collect IDs ---
	stepStart = time.Now()
	fmt.Printf("\n[Step 3] Seeding %d memories...\n", len(entry.SeedMemories))
	type seededMem struct{ ID, Content string }
	var allSeeded []seededMem
	for i, sm := range entry.SeedMemories {
		kind := sm.Kind
		if kind == "" {
			kind = "conversation"
		}
		seedStart := time.Now()
		mem, createErr := mgr.Create(ctx, &model.CreateMemoryRequest{
			Content: sm.Content,
			Kind:    kind,
			SubKind: sm.SubKind,
			Scope:   "eval/longmemeval",
		})
		elapsed := time.Since(seedStart)
		contentPreview := sm.Content
		if len([]rune(contentPreview)) > 80 {
			contentPreview = string([]rune(contentPreview)[:80]) + "..."
		}
		status := "OK"
		if createErr != nil {
			status = fmt.Sprintf("ERR: %v", createErr)
		} else {
			allSeeded = append(allSeeded, seededMem{ID: mem.ID, Content: sm.Content})
		}
		fmt.Printf("  [3.%d] %s kind=%s (%s) %s\n", i+1, status, kind, elapsed.Round(time.Millisecond), contentPreview)
	}
	fmt.Printf("[Step 3] Seeding done: %d memories (%s)\n", len(allSeeded), time.Since(stepStart).Round(time.Millisecond))

	// --- Step 3b: 批量实体抽取 / Batch entity extraction ---
	stepStart = time.Now()
	fmt.Printf("\n[Step 3b] Batch entity extraction (%d items)...\n", len(allSeeded))
	batchItems := make([]model.BatchExtractItem, len(allSeeded))
	for i, s := range allSeeded {
		batchItems[i] = model.BatchExtractItem{MemoryID: s.ID, Content: s.Content}
	}
	batchResp, batchErr := ext.ExtractBatch(ctx, &model.BatchExtractRequest{
		Items: batchItems, Scope: "eval/longmemeval",
	})
	if batchErr != nil {
		fmt.Printf("[Step 3b] Batch extraction FAILED: %v\n", batchErr)
	} else {
		totalEntities := 0
		for _, r := range batchResp.Results {
			totalEntities += len(r.Entities)
		}
		fmt.Printf("[Step 3b] Done: %d batches, %d tokens, %d memory-entity associations (%s)\n",
			batchResp.BatchCount, batchResp.TotalTokens, totalEntities,
			time.Since(stepStart).Round(time.Millisecond))
	}

	// 输出抽取的实体 / Print extracted entities
	entities, _ := graphStore.ListEntities(ctx, "", "", 1000)
	fmt.Printf("  Entities in graph: %d\n", len(entities))
	for i, e := range entities {
		fmt.Printf("  [E%d] %s (type=%s)\n", i+1, e.Name, e.EntityType)
	}

	// --- Step 4: 配置管线 / Configure pipeline ---
	stepStart = time.Now()
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	cfg.Strategy.UseLLM = true
	cfg.Strategy.FallbackPipeline = "exploration"
	cfg.Preprocess.Enabled = true
	cfg.Preprocess.UseLLM = true
	cfg.Preprocess.LLMTimeout = 10 * time.Second

	preprocessor := search.NewPreprocessor(tok, graphStore, preprocessProvider, cfg)
	retriever := search.NewRetriever(memStore, nil, nil, graphStore, rerankProvider, cfg, preprocessor, nil)

	// 手动初始化管线 / Manually init pipeline
	registry := pipeline.NewRegistry()
	deps := builtin.Deps{
		FTSSearcher: memStore,
		GraphStore:  graphStore,
		LLM:         rerankProvider,
		Cfg:         cfg,
	}
	postStages := builtin.RegisterBuiltins(registry, deps)
	executor := pipeline.NewExecutor(registry, pipeline.WithPostStages(postStages...))

	rc := strategy.NewRuleClassifier(pipeline.PipelineExploration)
	agent := strategy.NewAgent(strategyProvider, rc, 5*time.Second)
	retriever.SetPipelineComponents(executor, agent, rc)
	fmt.Printf("[Step 4] Pipeline configured (%s)\n", time.Since(stepStart).Round(time.Millisecond))

	// --- Step 5: 执行检索（带 Debug trace）/ Execute retrieval with debug trace ---
	stepStart = time.Now()
	fmt.Printf("\n[Step 5] Retrieving: %q\n", qCase.Query)
	result, err := retriever.RetrieveWithDebug(ctx, &model.RetrieveRequest{
		Query: qCase.Query,
		Limit: 10,
		Debug: true,
	})
	if err != nil {
		return fmt.Errorf("retrieve: %w", err)
	}
	fmt.Printf("[Step 5] Retrieval done (%s)\n", time.Since(stepStart).Round(time.Millisecond))

	// --- Step 6: 输出 Pipeline Debug 信息 / Print pipeline debug info ---
	if result.PipelineInfo != nil {
		fmt.Printf("\n[Step 6] Pipeline: %s\n", result.PipelineInfo.PipelineName)
		fmt.Printf("  Traces (%d stages):\n", len(result.PipelineInfo.Traces))
		for i, tr := range result.PipelineInfo.Traces {
			skipMark := ""
			if tr.Skipped {
				skipMark = " [SKIPPED]"
			}
			note := ""
			if tr.Note != "" {
				note = fmt.Sprintf(" (%s)", tr.Note)
			}
			fmt.Printf("  [T%d] %-20s in=%d out=%d %s%s%s\n",
				i+1, tr.Name, tr.InputCount, tr.OutputCount, tr.Duration.Round(time.Millisecond), skipMark, note)
		}
	}

	// --- Step 7: 输出检索结果 / Print retrieval results ---
	results := result.Results
	fmt.Printf("\n[Step 7] Results: %d items\n", len(results))
	for i, r := range results {
		if r == nil || r.Memory == nil {
			fmt.Printf("  [R%d] <nil>\n", i+1)
			continue
		}
		contentPreview := r.Memory.Content
		if len([]rune(contentPreview)) > 100 {
			contentPreview = string([]rune(contentPreview)[:100]) + "..."
		}
		fmt.Printf("  [R%d] score=%.4f source=%s kind=%s\n       %s\n",
			i+1, r.Score, r.Source, r.Memory.Kind, contentPreview)
	}

	// --- Step 8: 匹配检查 / Hit check ---
	hit, rank, score := checkHit(results, qCase.Expected)
	if !hit && qCase.GoldAnswer != "" {
		hit, rank, score = fuzzyCheckHit(results, qCase.GoldAnswer)
		if hit {
			fmt.Printf("\n[Step 8] HIT (fuzzy) rank=%d score=%.4f\n", rank, score)
		}
	}
	if hit && rank > 0 {
		fmt.Printf("\n[Step 8] HIT rank=%d score=%.4f\n", rank, score)
	} else {
		fmt.Printf("\n[Step 8] MISS — expected keywords not found in results\n")
	}

	// --- Step 9: LLM 用量 / LLM usage ---
	tracker.PrintUsage()

	totalDuration := time.Since(start)
	fmt.Printf("Total duration: %s\n", totalDuration.Round(time.Millisecond))
	fmt.Println(strings.Repeat("=", 70))

	return nil
}

// RunLongMemEvalSharedDB 共享单库评测：全部记忆写入同一 DB + LLM 实体抽取 + 全局图谱
// Shared-DB eval: all memories in one DB with LLM entity extraction and global graph
func RunLongMemEvalSharedDB(ctx context.Context, entries []LongMemEvalEntry, tmpDir string, maxQuestions int) (*EvalReport, error) {
	llmProvider := resolveLLMProvider()
	if llmProvider == nil {
		return nil, fmt.Errorf("LLM provider required for shared-DB eval (set OPENAI_API_KEY)")
	}

	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	start := time.Now()
	dbPath := filepath.Join(tmpDir, "shared_eval.db")

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return nil, fmt.Errorf("create shared store: %w", err)
	}
	defer memStore.Close()

	if err := memStore.Init(ctx); err != nil {
		return nil, fmt.Errorf("init shared store: %w", err)
	}

	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)
	ext := memory.NewExtractor(llmProvider, graphMgr, memStore, nil, config.ExtractConfig{})

	// Manager 不传 LLMProvider，避免 seed 时逐条 LLM 生成 excerpt / No LLMProvider to skip per-seed excerpt generation
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:  memStore,
		Extractor: ext,
	})

	// --- Phase 1a: seed（收集 memoryID）/ Seed and collect memory IDs ---
	type seedKey struct{ content, kind string }
	seeded := make(map[seedKey]bool)
	type seededMem struct{ ID, Content string }
	var allSeeded []seededMem

	for _, entry := range entries {
		for _, sm := range entry.SeedMemories {
			kind := sm.Kind
			if kind == "" {
				kind = "conversation"
			}
			key := seedKey{content: sm.Content, kind: kind}
			if seeded[key] {
				continue
			}
			seeded[key] = true
			mem, createErr := mgr.Create(ctx, &model.CreateMemoryRequest{
				Content: sm.Content,
				Kind:    kind,
				SubKind: sm.SubKind,
				Scope:   "eval/longmemeval",
			})
			if createErr != nil {
				continue
			}
			allSeeded = append(allSeeded, seededMem{ID: mem.ID, Content: sm.Content})
			if len(allSeeded)%50 == 0 {
				fmt.Printf("  [seed] %d memories seeded...\n", len(allSeeded))
			}
		}
	}
	seedDuration := time.Since(start)
	fmt.Printf("  [seed] Phase 1a done: %d memories (%s)\n", len(allSeeded), seedDuration.Round(time.Second))

	// --- Phase 1b: 批量实体抽取 / Batch entity extraction ---
	extractStart := time.Now()
	batchItems := make([]model.BatchExtractItem, len(allSeeded))
	for i, s := range allSeeded {
		batchItems[i] = model.BatchExtractItem{MemoryID: s.ID, Content: s.Content}
	}
	batchResp, batchErr := ext.ExtractBatch(ctx, &model.BatchExtractRequest{
		Items: batchItems, Scope: "eval/longmemeval",
	})
	if batchErr != nil {
		fmt.Printf("  [extract] batch extraction failed: %v\n", batchErr)
	} else {
		fmt.Printf("  [extract] Phase 1b done: %d batches, %d tokens (%s)\n",
			batchResp.BatchCount, batchResp.TotalTokens, time.Since(extractStart).Round(time.Second))
	}

	entityCount := 0
	if ents, listErr := graphStore.ListEntities(ctx, "", "", 100000); listErr == nil {
		entityCount = len(ents)
	}
	fmt.Printf("  [seed] Phase 1 total: %d memories, %d entities, %s\n", len(allSeeded), entityCount, time.Since(start).Round(time.Second))

	// --- Phase 2: query ---
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	retriever := search.NewRetriever(memStore, nil, nil, graphStore, llmProvider, cfg, nil, nil)
	retriever.InitPipeline()

	queryStart := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%5 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [query %d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		results, qErr := retriever.Retrieve(ctx, &model.RetrieveRequest{
			Query: entry.Case.Query,
			Limit: 10,
		})
		if qErr != nil {
			cases = append(cases, CaseResult{
				Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
				Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
				Hit: false, Rank: -1,
			})
			continue
		}

		hit, rank, score := checkHit(results, entry.Case.Expected)
		if !hit && entry.Case.GoldAnswer != "" {
			hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
		}
		cases = append(cases, CaseResult{
			Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
			Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
			Hit: hit, Rank: rank, Score: score, ResultCount: len(results),
		})
	}

	queryDuration := time.Since(queryStart)
	metrics := Aggregate(cases)

	report := &EvalReport{
		Mode:         fmt.Sprintf("shared-DB (graph+llm) — %d memories, %d entities", len(allSeeded), entityCount),
		Dataset:      "longmemeval-oracle",
		Timestamp:    time.Now(),
		Metrics:      metrics,
		ByCategory:   groupAggregate(cases, func(c CaseResult) string { return c.Category }),
		ByDifficulty: groupAggregate(cases, func(c CaseResult) string { return c.Difficulty }),
		Cases:        cases,
		Duration:     time.Since(start),
		GitCommit:    resolveGitCommit(),
	}

	fmt.Printf("\n  Phase 1 (seed+extract): %s | Phase 2 (query): %s\n", seedDuration.Round(time.Second), queryDuration.Round(time.Second))
	return report, nil
}

// RunLongMemEvalResolverDB 向量解析器评测：全部记忆写入同一 DB + EntityResolver 实体抽取（无 LLM）
// Resolver-DB eval: all memories in one DB with EntityResolver extraction (no LLM)
func RunLongMemEvalResolverDB(ctx context.Context, entries []LongMemEvalEntry, tmpDir string, maxQuestions int) (*EvalReport, error) {
	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	start := time.Now()
	dbPath := filepath.Join(tmpDir, "resolver_eval.db")

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return nil, fmt.Errorf("create resolver store: %w", err)
	}
	defer memStore.Close()

	if err := memStore.Init(ctx); err != nil {
		return nil, fmt.Errorf("init resolver store: %w", err)
	}

	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)
	candidateStore := store.NewSQLiteCandidateStore(db)

	// Manager 不传 LLM/Extractor / No LLM, no Extractor
	mgr := memory.NewManager(memory.ManagerDeps{MemStore: memStore})

	// --- Phase 1a: seed ---
	type seedKey struct{ content, kind string }
	seeded := make(map[seedKey]bool)
	type seededMem struct{ ID, Content string }
	var allSeeded []seededMem

	for _, entry := range entries {
		for _, sm := range entry.SeedMemories {
			kind := sm.Kind
			if kind == "" {
				kind = "conversation"
			}
			key := seedKey{content: sm.Content, kind: kind}
			if seeded[key] {
				continue
			}
			seeded[key] = true
			mem, createErr := mgr.Create(ctx, &model.CreateMemoryRequest{
				Content: sm.Content, Kind: kind, SubKind: sm.SubKind, Scope: "eval/longmemeval",
			})
			if createErr != nil {
				continue
			}
			allSeeded = append(allSeeded, seededMem{ID: mem.ID, Content: sm.Content})
			if len(allSeeded)%50 == 0 {
				fmt.Printf("  [seed] %d memories seeded...\n", len(allSeeded))
			}
		}
	}
	seedDuration := time.Since(start)
	fmt.Printf("  [seed] Phase 1a done: %d memories (%s)\n", len(allSeeded), seedDuration.Round(time.Second))

	// --- Phase 1b: EntityResolver 两轮解析（无 LLM）/ Two-pass resolver (no LLM) ---
	resolveStart := time.Now()
	resolverCfg := config.ResolverConfig{Enabled: true, CandidatePromoteMin: 2}
	resolver := memory.NewEntityResolver(tok, graphStore, candidateStore, nil, nil, resolverCfg)

	mems := make([]*model.Memory, len(allSeeded))
	for i, s := range allSeeded {
		mems[i] = &model.Memory{ID: s.ID, Content: s.Content, Scope: "eval/longmemeval"}
	}

	// Pass 1: 全部词成为候选（无已知实体）/ All terms become candidates
	resolver.Resolve(ctx, mems)

	// 晋升候选 → 正式实体 / Promote candidates to real entities
	candidates, _ := candidateStore.ListPromotable(ctx, 2)
	promoted := 0
	for _, c := range candidates {
		entity := &model.Entity{Name: c.Name, EntityType: "concept", Scope: c.Scope}
		if err := graphStore.CreateEntity(ctx, entity); err != nil {
			continue
		}
		for _, memID := range c.MemoryIDs {
			_ = graphStore.CreateMemoryEntity(ctx, &model.MemoryEntity{
				MemoryID: memID, EntityID: entity.ID, Role: "mentioned", Confidence: 0.9,
			})
		}
		_ = candidateStore.DeleteCandidate(ctx, c.Name, c.Scope)
		promoted++
	}
	fmt.Printf("  [resolver] promoted %d candidates to entities\n", promoted)

	// Pass 2: 匹配已有实体 + 建立共现关系 / Match promoted entities + build co-occurrence
	resolver.Resolve(ctx, mems)

	entityCount := 0
	if ents, listErr := graphStore.ListEntities(ctx, "", "", 100000); listErr == nil {
		entityCount = len(ents)
	}
	fmt.Printf("  [resolver] Phase 1b done: %d entities (%s)\n", entityCount, time.Since(resolveStart).Round(time.Second))

	// --- Phase 2: query ---
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	retriever := search.NewRetriever(memStore, nil, nil, graphStore, nil, cfg, nil, nil)
	retriever.InitPipeline()

	queryStart := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%5 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [query %d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		results, qErr := retriever.Retrieve(ctx, &model.RetrieveRequest{Query: entry.Case.Query, Limit: 10})
		if qErr != nil {
			cases = append(cases, CaseResult{
				Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
				Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
				Hit: false, Rank: -1,
			})
			continue
		}

		hit, rank, score := checkHit(results, entry.Case.Expected)
		if !hit && entry.Case.GoldAnswer != "" {
			hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
		}
		cases = append(cases, CaseResult{
			Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
			Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
			Hit: hit, Rank: rank, Score: score, ResultCount: len(results),
		})
	}

	queryDuration := time.Since(queryStart)
	metrics := Aggregate(cases)

	report := &EvalReport{
		Mode:         fmt.Sprintf("shared-DB (resolver, no LLM) — %d memories, %d entities", len(allSeeded), entityCount),
		Dataset:      "longmemeval-oracle",
		Timestamp:    time.Now(),
		Metrics:      metrics,
		ByCategory:   groupAggregate(cases, func(c CaseResult) string { return c.Category }),
		ByDifficulty: groupAggregate(cases, func(c CaseResult) string { return c.Difficulty }),
		Cases:        cases,
		Duration:     time.Since(start),
		GitCommit:    resolveGitCommit(),
	}

	fmt.Printf("\n  Phase 1 (seed+resolve): %s | Phase 2 (query): %s\n",
		(seedDuration + time.Since(resolveStart)).Round(time.Second), queryDuration.Round(time.Second))
	return report, nil
}

// RunLongMemEvalResolverFull 完整三层向量解析器评测：SQLite + Qdrant + Embedding + EntityResolver（无 LLM）
// Full three-layer resolver eval: SQLite + Qdrant + Embedding + EntityResolver (no LLM)
// 需要: EMBEDDING_API_KEY + Qdrant (localhost:6333) / Requires: EMBEDDING_API_KEY + Qdrant
func RunLongMemEvalResolverFull(ctx context.Context, entries []LongMemEvalEntry, tmpDir string, maxQuestions int) (*EvalReport, error) {
	// 解析环境变量 / Parse environment variables
	embeddingAPIKey := os.Getenv("EMBEDDING_API_KEY")
	if embeddingAPIKey == "" {
		embeddingAPIKey = os.Getenv("OPENAI_API_KEY")
	}
	if embeddingAPIKey == "" {
		return nil, fmt.Errorf("EMBEDDING_API_KEY or OPENAI_API_KEY required for full resolver eval")
	}
	embeddingModel := os.Getenv("EMBEDDING_MODEL")
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}
	qdrantURL := os.Getenv("QDRANT_URL")
	if qdrantURL == "" {
		qdrantURL = "http://localhost:6333"
	}
	qdrantCollection := "eval_resolver_" + time.Now().Format("20060102_150405")
	qdrantDimension := 1536 // text-embedding-3-small default
	if d := os.Getenv("EMBEDDING_DIMENSION"); d != "" {
		if n, err := fmt.Sscanf(d, "%d", &qdrantDimension); n == 0 || err != nil {
			qdrantDimension = 1536
		}
	}

	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	start := time.Now()
	dbPath := filepath.Join(tmpDir, "resolver_full_eval.db")

	// --- 初始化存储 / Initialize stores ---
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)
	candidateStore := store.NewSQLiteCandidateStore(db)

	// Qdrant 向量存储 / Qdrant vector store
	vecStore := store.NewQdrantVectorStore(qdrantURL, qdrantCollection, qdrantDimension)
	if err := vecStore.Init(ctx); err != nil {
		return nil, fmt.Errorf("init qdrant: %w (is Qdrant running at %s?)", err, qdrantURL)
	}
	defer vecStore.Close()
	fmt.Printf("  [init] Qdrant collection: %s (dim=%d)\n", qdrantCollection, qdrantDimension)

	// Embedding 模型 / Embedding model
	embedder := embed.NewOpenAIEmbedder(embeddingAPIKey, embeddingModel)
	fmt.Printf("  [init] Embedding model: %s\n", embeddingModel)

	// CentroidManager / Centroid manager
	centroidCollection := qdrantCollection + "_centroids"
	centroidMgr, err := memory.NewCentroidManager(qdrantURL, centroidCollection, qdrantDimension)
	if err != nil {
		fmt.Printf("  [init] CentroidManager failed (Layer 2 disabled): %v\n", err)
		centroidMgr = nil
	} else {
		fmt.Printf("  [init] CentroidManager: %s\n", centroidCollection)
	}

	// Manager 带 vecStore + embedder / Manager with vector store + embedder
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore: memStore,
		VecStore: vecStore,
		Embedder: embedder,
	})

	// --- Phase 1a: seed（写入 SQLite + Qdrant）/ Seed into SQLite + Qdrant ---
	type seedKey struct{ content, kind string }
	seeded := make(map[seedKey]bool)
	type seededMem struct {
		ID, Content string
		Embedding   []float32
	}
	var allSeeded []seededMem

	for _, entry := range entries {
		for _, sm := range entry.SeedMemories {
			kind := sm.Kind
			if kind == "" {
				kind = "conversation"
			}
			key := seedKey{content: sm.Content, kind: kind}
			if seeded[key] {
				continue
			}
			seeded[key] = true
			mem, createErr := mgr.Create(ctx, &model.CreateMemoryRequest{
				Content: sm.Content, Kind: kind, SubKind: sm.SubKind, Scope: "eval/longmemeval",
			})
			if createErr != nil {
				continue
			}
			allSeeded = append(allSeeded, seededMem{ID: mem.ID, Content: sm.Content})
			if len(allSeeded)%50 == 0 {
				fmt.Printf("  [seed] %d memories seeded (SQLite + Qdrant)...\n", len(allSeeded))
			}
		}
	}
	seedDuration := time.Since(start)
	fmt.Printf("  [seed] Phase 1a done: %d memories (%s)\n", len(allSeeded), seedDuration.Round(time.Second))

	// --- Phase 1b: 批量获取 embedding + 三层解析 / Batch embedding + three-layer resolution ---
	resolveStart := time.Now()

	// 批量获取 embedding / Batch embed all seeded memories
	fmt.Printf("  [embed] Embedding %d memories...\n", len(allSeeded))
	texts := make([]string, len(allSeeded))
	for i, s := range allSeeded {
		texts[i] = s.Content
	}

	batchSize := 100
	var allEmbeddings [][]float32
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, embErr := embedder.EmbedBatch(ctx, texts[i:end])
		if embErr != nil {
			return nil, fmt.Errorf("embed batch %d-%d: %w", i, end, embErr)
		}
		allEmbeddings = append(allEmbeddings, batch...)
		fmt.Printf("  [embed] %d/%d embedded\n", len(allEmbeddings), len(texts))
	}
	fmt.Printf("  [embed] Done: %d embeddings (%s)\n", len(allEmbeddings), time.Since(resolveStart).Round(time.Second))

	// EntityResolver 三层 / Three-layer EntityResolver
	resolverCfg := config.ResolverConfig{
		Enabled:             true,
		CentroidThreshold:   0.6,
		NeighborK:           10,
		NeighborMinCount:    2,
		CandidatePromoteMin: 2,
	}
	resolver := memory.NewEntityResolver(tok, graphStore, candidateStore, centroidMgr, vecStore, resolverCfg)

	mems := make([]*model.Memory, len(allSeeded))
	for i, s := range allSeeded {
		mems[i] = &model.Memory{ID: s.ID, Content: s.Content, Scope: "eval/longmemeval"}
	}

	// Pass 1: 三层解析（Layer 1 分词 + Layer 2 质心 + Layer 3 近邻）/ Three-layer pass 1
	fmt.Printf("  [resolver] Pass 1: three-layer resolution...\n")
	resolver.ResolveWithEmbeddings(ctx, mems, allEmbeddings)

	// 晋升候选 / Promote candidates
	promCandidates, _ := candidateStore.ListPromotable(ctx, 2)
	promoted := 0
	for _, c := range promCandidates {
		entity := &model.Entity{Name: c.Name, EntityType: "concept", Scope: c.Scope}
		if err := graphStore.CreateEntity(ctx, entity); err != nil {
			continue
		}
		for _, memID := range c.MemoryIDs {
			_ = graphStore.CreateMemoryEntity(ctx, &model.MemoryEntity{
				MemoryID: memID, EntityID: entity.ID, Role: "mentioned", Confidence: 0.9,
			})
		}
		_ = candidateStore.DeleteCandidate(ctx, c.Name, c.Scope)
		promoted++

		// 计算晋升实体的质心 / Compute centroid for promoted entity
		if centroidMgr != nil {
			var centroidVecs [][]float32
			for _, memID := range c.MemoryIDs {
				for j, s := range allSeeded {
					if s.ID == memID && j < len(allEmbeddings) {
						centroidVecs = append(centroidVecs, allEmbeddings[j])
					}
				}
			}
			if len(centroidVecs) > 0 {
				centroid := averageVectors(centroidVecs)
				_ = centroidMgr.UpsertCentroid(ctx, entity.ID, entity.Name, entity.Scope, centroid, len(centroidVecs))
			}
		}
	}
	fmt.Printf("  [resolver] promoted %d candidates to entities\n", promoted)

	// Pass 2: 二次解析 / Second pass
	fmt.Printf("  [resolver] Pass 2: re-resolution with promoted entities...\n")
	resolver.ResolveWithEmbeddings(ctx, mems, allEmbeddings)

	entityCount := 0
	if ents, listErr := graphStore.ListEntities(ctx, "", "", 100000); listErr == nil {
		entityCount = len(ents)
	}
	relCount := 0
	for _, ent := range func() []*model.Entity {
		ents, _ := graphStore.ListEntities(ctx, "", "", 100000)
		return ents
	}() {
		rels, _ := graphStore.GetEntityRelations(ctx, ent.ID)
		relCount += len(rels)
	}
	relCount /= 2 // 双向计数 / Double-counted

	fmt.Printf("  [resolver] Phase 1b done: %d entities, ~%d relations (%s)\n",
		entityCount, relCount, time.Since(resolveStart).Round(time.Second))

	// --- Phase 2: query ---
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	cfg.QdrantWeight = 0.6
	retriever := search.NewRetriever(memStore, vecStore, embedder, graphStore, nil, cfg, nil, nil)
	retriever.InitPipeline()

	queryStart := time.Now()
	cases := make([]CaseResult, 0, len(entries))

	for i, entry := range entries {
		if i > 0 && i%5 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [query %d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		results, qErr := retriever.Retrieve(ctx, &model.RetrieveRequest{Query: entry.Case.Query, Limit: 10})
		if qErr != nil {
			cases = append(cases, CaseResult{
				Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
				Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
				Hit: false, Rank: -1,
			})
			continue
		}

		hit, rank, score := checkHit(results, entry.Case.Expected)
		if !hit && entry.Case.GoldAnswer != "" {
			hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
		}
		cases = append(cases, CaseResult{
			Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
			Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
			Hit: hit, Rank: rank, Score: score, ResultCount: len(results),
		})
	}

	queryDuration := time.Since(queryStart)
	metrics := Aggregate(cases)

	layerInfo := "L1"
	if centroidMgr != nil {
		layerInfo += "+L2"
	}
	if vecStore != nil {
		layerInfo += "+L3"
	}

	report := &EvalReport{
		Mode:         fmt.Sprintf("full-resolver (%s, no LLM) — %d memories, %d entities, ~%d relations", layerInfo, len(allSeeded), entityCount, relCount),
		Dataset:      "longmemeval-oracle",
		Timestamp:    time.Now(),
		Metrics:      metrics,
		ByCategory:   groupAggregate(cases, func(c CaseResult) string { return c.Category }),
		ByDifficulty: groupAggregate(cases, func(c CaseResult) string { return c.Difficulty }),
		Cases:        cases,
		Duration:     time.Since(start),
		GitCommit:    resolveGitCommit(),
	}

	fmt.Printf("\n  Phase 1 (seed+embed+resolve): %s | Phase 2 (query): %s\n",
		time.Since(start).Round(time.Second)-queryDuration.Round(time.Second), queryDuration.Round(time.Second))
	return report, nil
}

// averageVectors 计算向量平均值 / Compute average of vectors
func averageVectors(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	avg := make([]float32, dim)
	for _, v := range vecs {
		for i, val := range v {
			avg[i] += val
		}
	}
	n := float32(len(vecs))
	for i := range avg {
		avg[i] /= n
	}
	return avg
}

// RunLongMemEvalQueryOnly 纯查询评测：复用已有数据库，跳过 seed+extract 阶段
// Query-only eval: reuse existing DB, skip seed and extraction phases
func RunLongMemEvalQueryOnly(ctx context.Context, entries []LongMemEvalEntry, dbPath string, maxQuestions int) (*EvalReport, error) {
	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	start := time.Now()
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return nil, fmt.Errorf("open existing store: %w", err)
	}
	defer memStore.Close()

	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)

	// 统计已有数据 / Report existing data
	memCount := 0
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories").Scan(&memCount)
	entityCount := 0
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities").Scan(&entityCount)
	relCount := 0
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entity_relations").Scan(&relCount)
	fmt.Printf("  [reuse] DB: %s — %d memories, %d entities, %d relations\n", dbPath, memCount, entityCount, relCount)

	// FTS + 图谱检索，strategy agent 走规则分类器 / FTS + graph retrieval, rule-based strategy
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	retriever := search.NewRetriever(memStore, nil, nil, graphStore, nil, cfg, nil, nil)
	retriever.InitPipeline()

	cases := make([]CaseResult, 0, len(entries))
	for i, entry := range entries {
		if i > 0 && i%10 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			fmt.Printf("  [query %d/%d] hit %d/%d (%.1f%%)\n", i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		qStart := time.Now()
		debugResult, qErr := retriever.RetrieveWithDebug(ctx, &model.RetrieveRequest{
			Query: entry.Case.Query,
			Limit: 10,
			Debug: true,
		})
		if qErr != nil {
			fmt.Printf("  [query %d] ERROR: %v (%.1fs)\n", i, qErr, time.Since(qStart).Seconds())
			cases = append(cases, CaseResult{
				Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
				Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
				Hit: false, Rank: -1,
			})
			continue
		}
		results := debugResult.Results
		// 打印管线调试信息 / Print pipeline debug traces
		if debugResult.PipelineInfo != nil {
			fmt.Printf("  [query %d] pipeline=%s (%.1fs)\n", i, debugResult.PipelineInfo.PipelineName, time.Since(qStart).Seconds())
			for _, tr := range debugResult.PipelineInfo.Traces {
				fmt.Printf("    stage=%-20s duration=%-10s in=%d out=%d skipped=%v note=%s\n",
					tr.Name, tr.Duration.Round(time.Millisecond), tr.InputCount, tr.OutputCount, tr.Skipped, tr.Note)
			}
		}

		hit, rank, score := checkHit(results, entry.Case.Expected)
		if !hit && entry.Case.GoldAnswer != "" {
			hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
		}
		cases = append(cases, CaseResult{
			Query: entry.Case.Query, Expected: entry.Case.GoldAnswer,
			Category: entry.Case.Category, Difficulty: entry.Case.Difficulty,
			Hit: hit, Rank: rank, Score: score, ResultCount: len(results),
		})
	}

	metrics := Aggregate(cases)
	report := &EvalReport{
		Mode:         fmt.Sprintf("query-only (reuse DB) — %d memories, %d entities", memCount, entityCount),
		Dataset:      "longmemeval-oracle",
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
