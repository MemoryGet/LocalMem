package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
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

	mgr := memory.NewManager(memStore, nil, nil, nil, nil, nil, nil, memory.ManagerConfig{})

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
