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
	extractor := memory.NewExtractor(llmProvider, graphMgr, memStore, config.ExtractConfig{})

	// Manager 带 extractor，seed 时自动抽取实体 / Manager with extractor for auto entity extraction
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:    memStore,
		Extractor:   extractor,
		LLMProvider: llmProvider,
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
	extractor := memory.NewExtractor(llmProvider, graphMgr, memStore, config.ExtractConfig{})

	// Manager 带 extractor / Manager with extractor
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:    memStore,
		Extractor:   extractor,
		LLMProvider: llmProvider,
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

// RunLongMemEvalSharedDB 共享单库评测：Phase1 一次性建库 + 图谱，Phase2 逐题查询
// Shared-DB evaluation: seed once, query many — correctly separates write/query costs
func RunLongMemEvalSharedDB(ctx context.Context, entries []LongMemEvalEntry, tmpDir string, maxQuestions int, useLLMExtract bool) (*EvalReport, *LLMTracker, error) {
	tracker := NewLLMTracker()

	var rawProvider llm.Provider
	if useLLMExtract {
		rawProvider = resolveLLMProvider()
		if rawProvider == nil {
			return nil, nil, fmt.Errorf("LLM provider required when useLLMExtract=true (set OPENAI_API_KEY)")
		}
	}

	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	// ===== Phase 1: 一次性建库 + seed 全部记忆 / Phase 1: Build shared DB, seed all memories =====
	fmt.Printf("[shared-db] Phase 1: seeding %d questions...\n", len(entries))
	seedStart := time.Now()

	dbPath := filepath.Join(tmpDir, "shared_longmemeval.db")
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return nil, nil, fmt.Errorf("create store: %w", err)
	}
	defer memStore.Close()

	if err := memStore.Init(ctx); err != nil {
		return nil, nil, fmt.Errorf("init store: %w", err)
	}

	// 图谱存储 / Graph store
	db := memStore.DB().(*sql.DB)
	graphStore := store.NewSQLiteGraphStore(db)

	// Manager 配置 / Configure manager
	var mgr *memory.Manager
	if useLLMExtract && rawProvider != nil {
		extractProvider := &stageProvider{inner: rawProvider, tracker: tracker, stage: "entity_extraction"}
		graphMgr := memory.NewGraphManager(graphStore)
		extractor := memory.NewExtractor(extractProvider, graphMgr, memStore, config.ExtractConfig{})
		mgr = memory.NewManager(memory.ManagerDeps{
			MemStore:    memStore,
			Extractor:   extractor,
			LLMProvider: extractProvider,
		})
	} else {
		mgr = memory.NewManager(memory.ManagerDeps{MemStore: memStore})
	}

	// Seed 所有记忆（去重）/ Seed all memories (deduplicate)
	seenContent := make(map[string]bool)
	seedCount := 0
	for i, entry := range entries {
		for _, sm := range entry.SeedMemories {
			if seenContent[sm.Content] {
				continue
			}
			seenContent[sm.Content] = true
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
				fmt.Printf("  [seed] question %d seed failed: %v\n", i, err)
				continue
			}
			seedCount++
		}
		if (i+1)%50 == 0 {
			fmt.Printf("  [seed %d/%d] seeded %d unique memories\n", i+1, len(entries), seedCount)
		}
	}

	seedDuration := time.Since(seedStart)
	seedUsage := tracker.Total()
	fmt.Printf("[shared-db] Phase 1 done: %d unique memories, %s, LLM calls: %d, tokens: %d\n",
		seedCount, seedDuration.Round(time.Second), seedUsage.Calls, seedUsage.TotalTokens)

	// ===== Phase 2: 逐题查询 / Phase 2: Query per question =====
	fmt.Printf("[shared-db] Phase 2: querying %d questions...\n", len(entries))
	queryStart := time.Now()

	// 构建检索器 / Build retriever
	cfg := buildRetrievalConfig("fts")
	cfg.GraphEnabled = true
	cfg.GraphDepth = 2
	cfg.GraphWeight = 0.8
	cfg.Strategy.UseLLM = rawProvider != nil
	cfg.Strategy.FallbackPipeline = "exploration"

	var retrieverLLM llm.Provider
	if rawProvider != nil {
		retrieverLLM = &stageProvider{inner: rawProvider, tracker: tracker, stage: "query_llm"}
	}

	retriever := search.NewRetriever(memStore, nil, nil, graphStore, retrieverLLM, cfg, nil, nil)
	retriever.InitPipeline()

	cases := make([]CaseResult, 0, len(entries))
	for i, entry := range entries {
		if i > 0 && i%20 == 0 {
			hits := 0
			for _, c := range cases {
				if c.Hit {
					hits++
				}
			}
			queryUsage := tracker.Total()
			fmt.Printf("  [query %d/%d] hit %d/%d (%.1f%%) | LLM calls: %d, tokens: %d\n",
				i, len(entries), hits, i, float64(hits)*100/float64(i),
				queryUsage.Calls-seedUsage.Calls, queryUsage.TotalTokens-seedUsage.TotalTokens)
		}

		results, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
			Query: entry.Case.Query,
			Limit: 10,
		})
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

		hit, rank, score := checkHit(results, entry.Case.Expected)
		if !hit && entry.Case.GoldAnswer != "" {
			hit, rank, score = fuzzyCheckHit(results, entry.Case.GoldAnswer)
		}

		cases = append(cases, CaseResult{
			Query:       entry.Case.Query,
			Expected:    entry.Case.GoldAnswer,
			Category:    entry.Case.Category,
			Difficulty:  entry.Case.Difficulty,
			Hit:         hit,
			Rank:        rank,
			Score:       score,
			ResultCount: len(results),
		})
	}

	queryDuration := time.Since(queryStart)
	queryUsage := tracker.Total()

	// ===== Phase 3: 分离报告 / Phase 3: Separated report =====
	metrics := Aggregate(cases)

	mode := "pipeline (shared-db)"
	if useLLMExtract {
		mode += " + LLM extract"
	}

	report := &EvalReport{
		Mode:         mode + " — LongMemEval oracle",
		Dataset:      "longmemeval-oracle",
		Timestamp:    time.Now(),
		Metrics:      metrics,
		ByCategory:   groupAggregate(cases, func(c CaseResult) string { return c.Category }),
		ByDifficulty: groupAggregate(cases, func(c CaseResult) string { return c.Difficulty }),
		Cases:        cases,
		Duration:     time.Since(seedStart),
		GitCommit:    resolveGitCommit(),
	}

	// 打印分离成本 / Print separated costs
	fmt.Printf("\n=== Cost Breakdown ===\n")
	fmt.Printf("Phase 1 (Seed + Extract): %s, LLM calls: %d, tokens: %d\n",
		seedDuration.Round(time.Second), seedUsage.Calls, seedUsage.TotalTokens)
	fmt.Printf("Phase 2 (Query):          %s, LLM calls: %d, tokens: %d\n",
		queryDuration.Round(time.Second),
		queryUsage.Calls-seedUsage.Calls,
		queryUsage.TotalTokens-seedUsage.TotalTokens)
	fmt.Printf("Avg query latency:        %s\n", (queryDuration / time.Duration(len(entries))).Round(time.Millisecond))

	return report, tracker, nil
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
	extractor := memory.NewExtractor(extractProvider, graphMgr, memStore, config.ExtractConfig{})

	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:    memStore,
		Extractor:   extractor,
		LLMProvider: extractProvider,
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
