package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"path/filepath"
)

// EvalCollection 评测专用 Qdrant collection（与生产 collection 隔离）
// Eval-only Qdrant collection — intentionally separate from the production collection.
const EvalCollection = "memories_eval"

// evalQdrantURL 从 config.yaml 读取 Qdrant 地址，兜底 localhost / Qdrant URL from config, fallback to localhost.
func evalQdrantURL() string {
	loadTestConfig()
	if u := config.AppConfig.Storage.Qdrant.URL; u != "" {
		return u
	}
	return "http://localhost:6333"
}

// EvalQdrantURL 对外暴露，供测试文件使用 / Exported for use from *_test.go files.
func EvalQdrantURL() string { return evalQdrantURL() }

// evalQdrantDim 从 config.yaml 读取向量维度，兜底 4096（Qwen3-Embedding-8B）
// Vector dimension from config, fallback to 4096 (Qwen3-Embedding-8B).
func evalQdrantDim() int {
	loadTestConfig()
	if d := config.AppConfig.Storage.Qdrant.Dimension; d > 0 {
		return d
	}
	return 4096
}

// EvalQdrantDim 对外暴露 / Exported for use from *_test.go files.
func EvalQdrantDim() int { return evalQdrantDim() }

// LongMemEvalEntry 单个 LongMemEval 问题（独立 seed + case）/ Single LongMemEval question with its own seeds
type LongMemEvalEntry struct {
	SeedMemories []SeedMemory    `json:"seed_memories"`
	Case         LongMemEvalCase `json:"case"`
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

// fuzzyCheckHit 宽松匹配：将 gold answer 拆词后检查是否多数词出现在结果中
func fuzzyCheckHit(results []*model.SearchResult, goldAnswer string) (bool, int, float64) {
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

	threshold := max(len(meaningful)/2, 1)

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

// Tier 描述一个评测层级的检索能力配置 / Describes retrieval capabilities for one eval tier
type Tier struct {
	Name        string  // 用于报告和基线命名 / Used for report and baseline naming
	Pipeline    bool    // 启用 Cascade 意图分类器 / Enable cascade intent classifier
	Graph       bool    // 启用实体抽取 + 图谱检索 / Enable entity extraction + graph stage
	Vector      bool    // 启用 Qdrant 向量检索 / Enable Qdrant vector search
	Rerank      bool    // 启用 LLM 精排 / Enable LLM reranking
	GraphWeight float64 // 图谱检索权重（0 时使用默认值 0.8）/ Graph weight (0 = default 0.8)
}

// 五个预定义层级 / Five predefined tiers
var (
	TierFTS      = Tier{Name: "fts"}
	TierPipeline = Tier{Name: "fts+pipeline", Pipeline: true}
	TierGraph    = Tier{Name: "fts+pipeline+graph", Pipeline: true, Graph: true, GraphWeight: 0.5}
	TierVector   = Tier{Name: "fts+pipeline+graph+vector", Pipeline: true, Graph: true, Vector: true}
	TierFull     = Tier{Name: "full", Pipeline: true, Graph: true, Vector: true, Rerank: true}
)

// SeedLongMemEvalDB 将所有 entry 的 seed 记忆写入共享库 / Seed all entry memories into shared DB
// withExtraction=true 时触发实体抽取（Graph/Vector/Full 层需要）
func SeedLongMemEvalDB(ctx context.Context, entries []LongMemEvalEntry, dbPath string, withExtraction bool) error {
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return fmt.Errorf("create store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	db, ok := memStore.DB().(*sql.DB)
	if !ok {
		return fmt.Errorf("store does not expose *sql.DB")
	}
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)

	var extractor *memory.Extractor
	if withExtraction {
		llmProvider := resolveLLMProvider()
		if llmProvider == nil {
			return fmt.Errorf("LLM provider required for extraction (set OPENAI_API_KEY)")
		}
		extractor = memory.NewExtractor(llmProvider, graphMgr, memStore, nil, config.ExtractConfig{})
	}

	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore:  memStore,
		Extractor: extractor,
	})

	total := 0
	for _, entry := range entries {
		for _, sm := range entry.SeedMemories {
			kind := sm.Kind
			if kind == "" {
				kind = "conversation"
			}
			_, err := mgr.Create(ctx, &model.CreateMemoryRequest{
				Content:       sm.Content,
				Kind:          kind,
				SubKind:       sm.SubKind,
				Scope:         "eval/longmemeval",
				RetentionTier: model.TierPermanent,
				AutoExtract:   withExtraction,
			})
			if err != nil {
				return fmt.Errorf("seed memory: %w", err)
			}
			total++
		}
	}
	fmt.Printf("  seeded %d memories into %s\n", total, dbPath)
	return nil
}

// RunLongMemEval 对已 seed 的共享库按指定层级运行评测 / Run eval against seeded shared DB with given tier
func RunLongMemEval(ctx context.Context, entries []LongMemEvalEntry, dbPath string, tier Tier, maxQuestions int) (*EvalReport, error) {
	if maxQuestions > 0 && len(entries) > maxQuestions {
		entries = entries[:maxQuestions]
	}

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	db, ok := memStore.DB().(*sql.DB)
	if !ok {
		return nil, fmt.Errorf("store does not expose *sql.DB")
	}
	graphStore := store.NewSQLiteGraphStore(db)

	cfg := buildRetrievalConfig("fts")
	if tier.Pipeline {
		// buildRetrievalConfig 无 "pipeline" case，手动启用 preprocess（激活管线意图分类）
		// buildRetrievalConfig has no "pipeline" case; enable preprocess to activate pipeline intent classification
		cfg.Preprocess.Enabled = true
	}
	cfg.GraphEnabled = tier.Graph
	if tier.Graph {
		cfg.GraphDepth = 2
		cfg.GraphWeight = tier.GraphWeight
		if cfg.GraphWeight <= 0 {
			cfg.GraphWeight = 0.8
		}
	}

	llmProvider := resolveLLMProvider()

	// Vector store (optional, requires Qdrant) / 向量存储（可选，需要 Qdrant）
	var vecStore store.VectorStore
	var embedder store.Embedder
	if tier.Vector {
		emb, embErr := resolveEmbedder()
		if embErr != nil {
			return nil, fmt.Errorf("resolve embedder for eval: %w", embErr)
		}
		vs := store.NewQdrantVectorStore(evalQdrantURL(), EvalCollection, evalQdrantDim())
		if initErr := vs.Init(ctx); initErr != nil {
			return nil, fmt.Errorf("init qdrant for eval: %w", initErr)
		}
		vecStore = vs
		embedder = emb
	}

	// LLM rerank (optional) / LLM 精排（可选）
	var extraPostStages []pipeline.Stage
	if tier.Rerank && llmProvider != nil {
		// 精排参数：topK=20, scoreWeight=0.7, minRelevance=0.3, timeout=8s
		// Rerank knobs: topK=20, scoreWeight=0.7, minRelevance=0.3, timeout=8s
		extraPostStages = append(extraPostStages,
			stage.NewRerankLLMStage(llmProvider, 20, 0.7, 0.3, 8*time.Second))
	}

	retriever := search.NewRetriever(memStore, vecStore, embedder, graphStore, llmProvider, cfg, nil, nil)
	retriever.InitPipeline(extraPostStages...)

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
			fmt.Printf("  [%s %d/%d] hit %d/%d (%.1f%%)\n",
				tier.Name, i, len(entries), hits, i, float64(hits)*100/float64(i))
		}

		results, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
			Query: entry.Case.Query,
			Limit: 10,
		})
		if err != nil {
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
		time.Sleep(50 * time.Millisecond)
	}

	metrics := Aggregate(cases)
	return &EvalReport{
		Mode:         "longmemeval — " + tier.Name,
		Dataset:      "longmemeval-oracle",
		Timestamp:    time.Now(),
		Metrics:      metrics,
		ByCategory:   groupAggregate(cases, func(c CaseResult) string { return c.Category }),
		ByDifficulty: groupAggregate(cases, func(c CaseResult) string { return c.Difficulty }),
		Cases:        cases,
		Duration:     time.Since(start),
		GitCommit:    resolveGitCommit(),
	}, nil
}

// extractQueuePath 队列文件路径（与 DB 同目录）/ Queue file path alongside the DB
func extractQueuePath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "extract_queue.md")
}

// loadExtractQueue 读取队列文件，返回待处理 ID 列表。文件不存在返回 nil。
// Load queue file and return pending memory IDs. Returns nil if file absent.
func loadExtractQueue(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read queue file: %w", err)
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	return ids, nil
}

// saveExtractQueue 将剩余 ID 写回队列文件；IDs 为空时删除文件。
// Write remaining IDs back to queue file; deletes file when empty.
func saveExtractQueue(path string, ids []string) error {
	if len(ids) == 0 {
		_ = os.Remove(path)
		return nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Entity Extraction Queue — %d remaining\n", len(ids))
	sb.WriteString("# Each line is a memory ID pending extraction.\n")
	sb.WriteString("# Completed IDs are removed after each chunk. Delete file to skip.\n\n")
	for _, id := range ids {
		sb.WriteString(id)
		sb.WriteString("\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// queryModelContextLimit 返回批量抽取的 token 阈值，委托给 llm.DetectContextWindow。
// Delegates to llm.DetectContextWindow — 5-layer detection: env → API → pattern table → ask model → default.
func queryModelContextLimit(ctx context.Context, provider llm.Provider) int {
	return llm.DetectContextWindow(ctx, provider)
}

// ExtractEntitiesFromDB 对共享库中所有记忆补跑批量实体抽取
// Batch entity extraction for all memories already in the shared DB.
// Safe to re-run: existing entities are reused via exact-match.
// maxItems=0 means no limit; pass a positive value to cap the number of memories processed.
func ExtractEntitiesFromDB(ctx context.Context, dbPath string, maxItems int) (int, error) {
	llmProvider := resolveLLMProvider()
	if llmProvider == nil {
		return 0, fmt.Errorf("LLM provider required (set OPENAI_API_KEY)")
	}

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return 0, fmt.Errorf("open store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return 0, fmt.Errorf("init store: %w", err)
	}

	db, ok := memStore.DB().(*sql.DB)
	if !ok {
		return 0, fmt.Errorf("store does not expose *sql.DB")
	}
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)

	queueFile := extractQueuePath(dbPath)

	// 尝试从队列文件恢复；文件不存在则从 DB 查询并初始化队列
	// Try to resume from queue file; if absent, query DB and initialize queue.
	pendingIDs, err := loadExtractQueue(queueFile)
	if err != nil {
		return 0, fmt.Errorf("load queue: %w", err)
	}

	if pendingIDs == nil {
		// 队列文件不存在：从 DB 查询未抽取的记忆 / Queue absent: query DB for unextracted memories
		q := `SELECT m.id FROM memories m
			WHERE m.deleted_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM memory_entities me WHERE me.memory_id = m.id)
			ORDER BY m.created_at`
		if maxItems > 0 {
			q += fmt.Sprintf(" LIMIT %d", maxItems)
		}
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return 0, fmt.Errorf("query memories: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, fmt.Errorf("scan id: %w", err)
			}
			pendingIDs = append(pendingIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("iterate ids: %w", err)
		}
		if len(pendingIDs) == 0 {
			fmt.Println("  ExtractEntitiesFromDB: all memories already have entity associations, nothing to do")
			return 0, nil
		}
		if err := saveExtractQueue(queueFile, pendingIDs); err != nil {
			return 0, fmt.Errorf("save queue: %w", err)
		}
		fmt.Printf("  ExtractEntitiesFromDB: initialized queue → %s (%d memories)\n", queueFile, len(pendingIDs))
	} else if len(pendingIDs) == 0 {
		fmt.Println("  ExtractEntitiesFromDB: queue file is empty, nothing to do")
		return 0, nil
	} else {
		fmt.Printf("  ExtractEntitiesFromDB: resuming from queue → %s (%d remaining)\n", queueFile, len(pendingIDs))
	}

	// 预加载所有待处理 ID 的 content（单次查询）/ Preload content for all pending IDs (single query)
	contentMap := make(map[string]string, len(pendingIDs))
	{
		// 用 IN 查询，分批避免 SQLite 参数上限 / Batch IN query to avoid SQLite parameter limit
		batchSize := 500
		for i := 0; i < len(pendingIDs); i += batchSize {
			end := i + batchSize
			if end > len(pendingIDs) {
				end = len(pendingIDs)
			}
			chunk := pendingIDs[i:end]
			placeholders := make([]string, len(chunk))
			args := make([]any, len(chunk))
			for j, id := range chunk {
				placeholders[j] = "?"
				args[j] = id
			}
			q := fmt.Sprintf("SELECT id, content FROM memories WHERE id IN (%s)",
				strings.Join(placeholders, ","))
			rows, err := db.QueryContext(ctx, q, args...)
			if err != nil {
				return 0, fmt.Errorf("preload content: %w", err)
			}
			for rows.Next() {
				var id, content string
				if err := rows.Scan(&id, &content); err != nil {
					rows.Close()
					return 0, fmt.Errorf("scan content: %w", err)
				}
				contentMap[id] = content
			}
			rows.Close()
		}
	}

	threshold := queryModelContextLimit(ctx, llmProvider)
	extractor := memory.NewExtractor(llmProvider, graphMgr, memStore, nil, config.ExtractConfig{
		BatchTokenThreshold: threshold,
		BatchConcurrency:    8,
	})

	// 分块处理：每块完成后立即更新队列文件 / Process in chunks, updating queue file after each chunk
	const chunkSize = 300
	created := 0
	total := len(pendingIDs)

	for len(pendingIDs) > 0 {
		end := chunkSize
		if end > len(pendingIDs) {
			end = len(pendingIDs)
		}
		chunk := pendingIDs[:end]

		items := make([]model.BatchExtractItem, 0, len(chunk))
		for _, id := range chunk {
			if content, ok := contentMap[id]; ok {
				items = append(items, model.BatchExtractItem{MemoryID: id, Content: content})
			}
		}

		done := total - len(pendingIDs)
		fmt.Printf("  ExtractEntitiesFromDB: chunk %d-%d/%d (threshold=%d)\n",
			done+1, done+len(chunk), total, threshold)

		resp, err := extractor.ExtractBatch(ctx, &model.BatchExtractRequest{
			Items: items,
			Scope: "eval/longmemeval",
		})
		if err != nil {
			// 保存当前进度后返回错误，下次从断点续跑 / Save progress before returning error — resume next run
			_ = saveExtractQueue(queueFile, pendingIDs)
			return created, fmt.Errorf("batch extract at offset %d: %w", done, err)
		}

		for _, r := range resp.Results {
			if r == nil {
				continue
			}
			for _, e := range r.Entities {
				if !e.Reused {
					created++
				}
			}
		}
		fmt.Printf("  ExtractEntitiesFromDB: chunk done — %d batches, %d tokens, %d new entities (total created: %d)\n",
			resp.BatchCount, resp.TotalTokens, func() int {
				n := 0
				for _, r := range resp.Results {
					if r != nil {
						for _, e := range r.Entities {
							if !e.Reused {
								n++
							}
						}
					}
				}
				return n
			}(), created)

		// 从队列移除已完成的 chunk / Remove completed chunk from queue
		pendingIDs = pendingIDs[end:]
		if err := saveExtractQueue(queueFile, pendingIDs); err != nil {
			return created, fmt.Errorf("update queue: %w", err)
		}
	}

	fmt.Printf("  ExtractEntitiesFromDB: all done — %d new entities created\n", created)
	return created, nil
}

// seedItem 单条待嵌入记忆 / Single memory item pending embedding
type seedItem struct {
	id, content, scope, kind, ownerID string
}

// SeedVectorsToQdrant 将 SQLite eval DB 中的记忆批量嵌入并写入 Qdrant
// Batch-embed memories from SQLite eval DB and upsert into Qdrant.
// Embedder is resolved from env vars via resolveEmbedder() (EMBEDDING_PROVIDER / EMBEDDING_MODEL).
// Uses collection "memories_eval" to avoid colliding with production collection.
// maxItems=0 means no limit. Safe to re-run: Qdrant upsert is idempotent.
// Concurrent: uses 8 workers to parallelize embed+upsert.
func SeedVectorsToQdrant(ctx context.Context, dbPath, qdrantURL, collection string, dim int, maxItems int) (int, error) {
	embedder, err := resolveEmbedder()
	if err != nil {
		return 0, fmt.Errorf("resolve embedder: %w", err)
	}

	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return 0, fmt.Errorf("open store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return 0, fmt.Errorf("init store: %w", err)
	}

	db, ok := memStore.DB().(*sql.DB)
	if !ok {
		return 0, fmt.Errorf("store does not expose *sql.DB")
	}

	vecStore := store.NewQdrantVectorStore(qdrantURL, collection, dim)
	if err := vecStore.Init(ctx); err != nil {
		return 0, fmt.Errorf("init qdrant: %w", err)
	}

	// 1. 读取全部记忆到内存，避免边遍历边发网络请求 / Load all rows first to avoid holding cursor during parallel embed
	q := `SELECT id, content, scope, kind, owner_id FROM memories
          WHERE deleted_at IS NULL ORDER BY created_at`
	if maxItems > 0 {
		q += fmt.Sprintf(" LIMIT %d", maxItems)
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("query memories: %w", err)
	}
	var items []seedItem
	for rows.Next() {
		var it seedItem
		if err := rows.Scan(&it.id, &it.content, &it.scope, &it.kind, &it.ownerID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan row: %w", err)
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rows error: %w", err)
	}
	// 2. 切分批次，全部并发提交 EmbedBatch，每批完成后报告一次进度
	// Split into batches; submit all concurrently via EmbedBatch; report progress once per completed batch.
	const batchSize = 50 // 每批文本数 / texts per batch

	// 取前 batchSize 条真实内容作探针，确保 token 量与实际一致
	// Use real content as probe sample so token load matches the actual workload.
	probeN := batchSize
	if probeN > len(items) {
		probeN = len(items)
	}
	probeSample := make([]string, probeN)
	for i := range probeSample {
		probeSample[i] = items[i].content
	}
	concurrency := probeSafeConcurrency(ctx, embedder, probeSample)

	total := len(items)
	numBatches := (total + batchSize - 1) / batchSize
	fmt.Printf("  SeedVectorsToQdrant: %d memories → %d batches (size=%d conc=%d) → %q\n",
		total, numBatches, batchSize, concurrency, collection)

	// 预切批次 / Pre-slice batches
	batches := make([][]seedItem, 0, numBatches)
	for i := 0; i < total; i += batchSize {
		batches = append(batches, items[i:min(i+batchSize, total)])
	}

	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var seeded int
	var wg sync.WaitGroup

	for batchIdx, batch := range batches {
		wg.Add(1)
		sem <- struct{}{}
		go func(batchIdx int, batch []seedItem) {
			defer func() { <-sem; wg.Done() }()

			texts := make([]string, len(batch))
			for i, it := range batch {
				texts[i] = it.content
			}

			vecs, err := embedder.EmbedBatch(ctx, texts)
			if err != nil {
				fmt.Printf("  SeedVectorsToQdrant: batch %d/%d embed failed: %v (skipping %d items)\n",
					batchIdx+1, numBatches, err, len(batch))
				return
			}

			localSeeded := 0
			for i, it := range batch {
				payload := map[string]any{
					"scope":      it.scope,
					"kind":       it.kind,
					"owner_id":   it.ownerID,
					"visibility": "private",
					"team_id":    "",
				}
				if err := vecStore.Upsert(ctx, it.id, vecs[i], payload); err != nil {
					fmt.Printf("  SeedVectorsToQdrant: upsert failed for %s: %v (skipping)\n", it.id, err)
					continue
				}
				localSeeded++
			}

			// 每批一次锁，减少争用，完成时打印进度 / One lock per batch to reduce contention; print progress on completion
			mu.Lock()
			seeded += localSeeded
			current := seeded
			mu.Unlock()
			fmt.Printf("  SeedVectorsToQdrant: batch %d/%d done (+%d) total=%d/%d\n",
				batchIdx+1, numBatches, localSeeded, current, total)
		}(batchIdx, batch)
	}
	wg.Wait()

	fmt.Printf("  SeedVectorsToQdrant: done — %d/%d vectors into %q\n", seeded, total, collection)
	return seeded, nil
}

// probeSafeConcurrency 指数探测安全并发数 / Exponential probe to find safe embed concurrency.
// 从 startConc=4 开始，不报错就加倍，遇到首个错误退一档即为安全并发数。
// Starts at 4, doubles each step; backs off one step on first error.
// batchSize 决定探针文本数（取 min(batchSize,8)），使探针负载与真实批次匹配。
// sampleTexts 应为真实内容（而非占位符），以确保 token 量与实际批次一致。
// sampleTexts should be real content (not placeholders) so token load matches the actual workload.
// EMBED_CONCURRENCY env var skips probing and uses the specified value directly.
func probeSafeConcurrency(ctx context.Context, embedder store.Embedder, sampleTexts []string) int {
	if v := os.Getenv("EMBED_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			fmt.Printf("  probeSafeConcurrency: EMBED_CONCURRENCY=%d (override)\n", n)
			return n
		}
	}

	const (
		startConc     = 1
		maxConc       = 10
		// 25s: 允许低延迟服务探出 conc=2+；仍能识别 429（10s+20s backoff > 25s）
		// 25s: lets low-latency backends probe conc=2+; still catches 429 (10s+20s backoff > 25s)
		probeDeadline = 25 * time.Second
	)

	probe := startConc
	safe := 1 // 仅在探测通过后才提升 / only raised after a successful probe round

	for probe <= maxConc {
		probeCtx, cancel := context.WithTimeout(ctx, probeDeadline)

		errc := make(chan error, probe)
		var wg sync.WaitGroup
		for i := 0; i < probe; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := embedder.EmbedBatch(probeCtx, sampleTexts)
				errc <- err
			}()
		}
		wg.Wait()
		cancel()
		close(errc)

		var hitLimit bool
		for err := range errc {
			if err != nil {
				hitLimit = true
				break
			}
		}

		if hitLimit {
			fmt.Printf("  probeSafeConcurrency: conc=%d → error, safe=%d\n", probe, safe)
			return safe
		}
		fmt.Printf("  probeSafeConcurrency: conc=%d ok\n", probe)
		safe = probe
		probe *= 2
	}

	fmt.Printf("  probeSafeConcurrency: reached cap, safe=%d\n", safe)
	return safe
}
