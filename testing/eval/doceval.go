// Package eval 文档知识库检索评测 / Document knowledge base retrieval evaluation
package eval

import (
	"context"
	"fmt"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
)

// DocInput 一份待评测文档 / A document to be chunked and seeded
type DocInput struct {
	Name       string // 文档名（作为 source_ref 前缀）/ Document name (used as source_ref prefix)
	Content    string // 文档内容 / Document content
	IsMarkdown bool   // true = MarkdownChunker, false = TextChunker
}

// DocEvalCase 单个评测用例 / Single evaluation case
type DocEvalCase struct {
	Query    string   // 查询语句 / Query
	Keywords []string // 期望出现在 top-K 结果中的关键词（任一匹配即命中）/ Expected keywords in top-K (any match = hit)
	Limit    int      // top-K，0 = 默认 10 / top-K limit, 0 defaults to 10
}

// DocCaseResult 单用例结果 / Per-case result
type DocCaseResult struct {
	Query          string
	Keywords       []string
	Hit            bool
	Rank           int    // 1-based; -1 = miss
	MatchedKeyword string // 命中的关键词 / Matched keyword
}

// DocEvalReport 整体评测报告 / Overall evaluation report
type DocEvalReport struct {
	HitRate  float64
	MRR      float64
	Total    int
	Hits     int
	Cases    []DocCaseResult
	Duration time.Duration
}

// RunDocKBEval 将文档切块写入临时库，运行检索评测，返回报告
// Seeds document chunks into a temp DB, runs retrieval evaluation, returns report.
// dbPath: SQLite path for this eval run (caller manages cleanup).
func RunDocKBEval(ctx context.Context, docs []DocInput, cases []DocEvalCase, dbPath string) (*DocEvalReport, error) {
	loadTestConfig()
	cfg := buildDocRetrievalConfig()

	// SeedVectorsToQdrant 内部使用 SimpleTokenizer 打开 SQLite，会触发 FTS 索引重建。
	// 使用相同的 SimpleTokenizer 确保 seed 前后 FTS 索引 tokenizer 一致，避免查询时 tokenizer 不匹配。
	// SeedVectorsToQdrant internally opens SQLite with SimpleTokenizer, rebuilding the FTS index.
	// Use the same SimpleTokenizer here so the FTS index stays consistent throughout the eval.
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	mgr := memory.NewManager(memory.ManagerDeps{MemStore: memStore})

	// ── 1. 切块并写入 SQLite ──────────────────────────────────────────────────
	if err := seedDocChunks(ctx, docs, mgr); err != nil {
		return nil, fmt.Errorf("seed chunks: %w", err)
	}

	// ── 2. 初始化向量存储并 seed（可选）/ Init vector store and seed embeddings (optional) ────
	var vecStore store.VectorStore
	var embedder store.Embedder
	if emb, embErr := resolveEmbedder(); embErr == nil {
		collection := docEvalCollection()
		vs := store.NewQdrantVectorStore(evalQdrantURL(), collection, evalQdrantDim())
		// 每次 eval 用干净的集合：先删再建，保证无历史脏数据
		// Clean collection each run: drop then recreate to avoid stale vectors from previous runs.
		_ = vs.DropCollection(ctx)
		if initErr := vs.Init(ctx); initErr == nil {
			fmt.Printf("  [doceval] seeding vectors → %s ...\n", collection)
			n, seedErr := SeedVectorsToQdrant(ctx, dbPath, evalQdrantURL(), collection, evalQdrantDim(), 0)
			if seedErr != nil {
				fmt.Printf("  [doceval] vector seed failed (falling back to FTS-only): %v\n", seedErr)
			} else {
				fmt.Printf("  [doceval] seeded %d vectors\n", n)
				vecStore = vs
				embedder = emb
			}
		}
	}

	// ── 3. 初始化检索器（semantic pipeline + instruction embedding）────────────
	// Use the same tok (SimpleTokenizer) for the preprocessor to match the FTS index tokenizer.
	preprocessor := search.NewPreprocessor(tok, nil, nil, cfg)
	retriever := search.NewRetriever(memStore, vecStore, embedder, nil, nil, cfg, preprocessor, nil)
	retriever.InitPipeline()

	// ── 4. 运行评测用例 / Run evaluation cases ────────────────────────────────
	start := time.Now()
	var results []DocCaseResult

	for _, ec := range cases {
		limit := ec.Limit
		if limit <= 0 {
			limit = 10
		}
		retrieved, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
			Query: ec.Query,
			Limit: limit,
		})
		cr := DocCaseResult{Query: ec.Query, Keywords: ec.Keywords, Hit: false, Rank: -1}
		if err == nil {
			for i, r := range retrieved {
				if i >= limit {
					break // 超出 limit 的结果不算命中 / Results beyond limit do not count as hits
				}
				if kw, ok := containsAny(r.Memory.Content, ec.Keywords); ok {
					cr.Hit = true
					cr.Rank = i + 1
					cr.MatchedKeyword = kw
					break
				}
			}
		}
		results = append(results, cr)
	}

	// ── 5. 汇总 / Aggregate ───────────────────────────────────────────────────
	hits, rr := 0, 0.0
	for _, r := range results {
		if r.Hit {
			hits++
			rr += 1.0 / float64(r.Rank)
		}
	}
	total := len(results)
	hr, mrr := 0.0, 0.0
	if total > 0 {
		hr = float64(hits) / float64(total) * 100
		mrr = rr / float64(total)
	}

	return &DocEvalReport{
		HitRate:  hr,
		MRR:      mrr,
		Total:    total,
		Hits:     hits,
		Cases:    results,
		Duration: time.Since(start),
	}, nil
}

// seedDocChunks 将文档切块写入 memory store / Chunk documents and seed into memory store
func seedDocChunks(ctx context.Context, docs []DocInput, mgr *memory.Manager) error {
	opts := document.ChunkOptions{
		MaxTokens:       512,
		OverlapTokens:   50,
		ContextPrefix:   true,
		KeepTableIntact: true,
		KeepCodeIntact:  true,
	}
	for _, doc := range docs {
		opts.DocName = doc.Name
		var chunks []document.Chunk
		if doc.IsMarkdown {
			chunks = document.NewMarkdownChunker().Chunk(doc.Content, opts)
		} else {
			chunks = document.NewTextChunker().Chunk(doc.Content, opts)
		}
		for i, ch := range chunks {
			content := ch.Content
			if content == "" {
				content = ch.RawContent
			}
			if strings.TrimSpace(content) == "" {
				continue
			}
			sourceRef := fmt.Sprintf("%s#chunk%d", doc.Name, i)
			if _, err := mgr.Create(ctx, &model.CreateMemoryRequest{
				Content:       content,
				Kind:          "document",
				Scope:         "eval/doceval",
				SourceType:    "document",
				SourceRef:     sourceRef,
				RetentionTier: model.TierPermanent,
				Visibility:    model.VisibilityPublic, // public so Qdrant visibility filter passes without identity
			}); err != nil && !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("seed %s chunk %d: %w", doc.Name, i, err)
			}
		}
	}
	return nil
}

// buildDocRetrievalConfig 构建文档 KB 检索配置 / Build retrieval config for document KB eval
func buildDocRetrievalConfig() config.RetrievalConfig {
	cfg := buildRetrievalConfig("fts")
	cfg.Preprocess.Enabled = true
	// 文档 KB 默认 semantic pipeline（vector+FTS RRF）
	// Document KB defaults to semantic pipeline (vector+FTS RRF)
	cfg.Strategy.FallbackPipeline = "semantic"
	// 非对称检索指令 / Asymmetric retrieval instruction
	cfg.VectorQueryInstruction = "Instruct: Given a question, retrieve the most relevant document passage that answers it."
	return cfg
}

// docEvalCollection Qdrant collection for document eval (isolated from other evals)
func docEvalCollection() string {
	loadTestConfig()
	if c := config.AppConfig.Storage.Qdrant.Collection; c != "" {
		return c + "_doceval"
	}
	return "memories_doceval"
}

// containsAny 检查 text 是否包含 keywords 中任一关键词（忽略大小写）
// Check if text contains any keyword (case-insensitive). Returns matched keyword.
func containsAny(text string, keywords []string) (string, bool) {
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return kw, true
		}
	}
	return "", false
}

// PrintDocEvalReport 打印文档评测报告 / Print document eval report to stdout
func PrintDocEvalReport(r *DocEvalReport) {
	fmt.Printf("\n=== Document KB Eval ===\n")
	fmt.Printf("  HitRate: %.1f%%  MRR: %.3f  Total: %d  Duration: %s\n",
		r.HitRate, r.MRR, r.Total, r.Duration.Round(time.Millisecond))
	fmt.Println()
	for _, c := range r.Cases {
		mark := "✗"
		detail := "miss"
		if c.Hit {
			mark = "✓"
			detail = fmt.Sprintf("rank=%d kw=%q", c.Rank, c.MatchedKeyword)
		}
		fmt.Printf("  %s [%s] %s\n", mark, detail, c.Query)
	}
	fmt.Println()
}
