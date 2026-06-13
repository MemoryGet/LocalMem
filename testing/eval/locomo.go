package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"
)

// LoCoMoCollection Qdrant collection 名称（与 LongMemEval 完全隔离）
// Qdrant collection name — fully isolated from LongMemEval.
const LoCoMoCollection = "memories_locomo"

// LoCoMoCollection1Turn 1-turn 粒度评测专用 collection（与默认 3-turn collection 隔离）
// Qdrant collection for 1-turn window granularity eval — isolated from the default 3-turn collection.
const LoCoMoCollection1Turn = "memories_locomo_1turn"

// ─── Data model ─────────────────────────────────────────────────────────────

// LoCoMoTurn 单条对话轮次 / Single dialogue turn
type LoCoMoTurn struct {
	Speaker string `json:"speaker"`
	DiaID   string `json:"dia_id"`
	Text    string `json:"text"`
}

// LoCoMoQA 问答对 / QA pair
type LoCoMoQA struct {
	Question string          `json:"question"`
	Answer   json.RawMessage `json:"answer"` // string, number, or null
	Evidence []string        `json:"evidence"`
	Category int             `json:"category"` // 1=single-hop 2=multi-hop 3=temporal 4=adversarial 5=open
}

// AnswerString returns the answer as a string regardless of the underlying JSON type.
func (q LoCoMoQA) AnswerString() string {
	if len(q.Answer) == 0 || string(q.Answer) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(q.Answer, &s); err == nil {
		return s
	}
	return string(q.Answer)
}

// LoCoMoConversation 一段完整长期对话 / One complete long-term conversation
type LoCoMoConversation struct {
	SampleID     string                 `json:"sample_id"`
	Conversation map[string]interface{} `json:"conversation"`
	QA           []LoCoMoQA             `json:"qa"`
}

// LoCoMoSession 解析后的 session / Parsed session
type LoCoMoSession struct {
	SessionNum int
	DateTime   string
	Turns      []LoCoMoTurn
}

// ─── Loader ──────────────────────────────────────────────────────────────────

// LoadLoCoMo 加载 LoCoMo JSON 文件 / Load LoCoMo JSON file
func LoadLoCoMo(path string) ([]LoCoMoConversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open locomo: %w", err)
	}
	defer f.Close()
	var data []LoCoMoConversation
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode locomo: %w", err)
	}
	return data, nil
}

// parseSessions 从 conversation map 中解析所有 session / Parse all sessions from conversation map
func parseSessions(conv map[string]interface{}) []LoCoMoSession {
	sessionRe := regexp.MustCompile(`^session_(\d+)$`)
	var sessions []LoCoMoSession

	for key, val := range conv {
		m := sessionRe.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])

		turnsRaw, ok := val.([]interface{})
		if !ok {
			continue
		}
		var turns []LoCoMoTurn
		for _, t := range turnsRaw {
			tm, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			turns = append(turns, LoCoMoTurn{
				Speaker: locoStrOf(tm["speaker"]),
				DiaID:   locoStrOf(tm["dia_id"]),
				Text:    locoStrOf(tm["text"]),
			})
		}
		if len(turns) == 0 {
			continue
		}

		dateKey := fmt.Sprintf("session_%d_date_time", n)
		sessions = append(sessions, LoCoMoSession{
			SessionNum: n,
			DateTime:   locoStrOf(conv[dateKey]),
			Turns:      turns,
		})
	}

	// Sort by session number
	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			if sessions[i].SessionNum > sessions[j].SessionNum {
				sessions[i], sessions[j] = sessions[j], sessions[i]
			}
		}
	}
	return sessions
}

// locoParseTime 解析 LoCoMo 时间格式 "1:56 pm on 8 May, 2023"
func locoParseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, f := range []string{
		"3:04 pm on 2 January, 2006",
		"3:04 am on 2 January, 2006",
		"3:04 pm on 2 Jan, 2006",
		"3:04 am on 2 Jan, 2006",
	} {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	// Fallback: extract date portion only
	re := regexp.MustCompile(`(\d+)\s+(\w+),?\s+(\d{4})`)
	if m := re.FindStringSubmatch(s); len(m) == 4 {
		for _, f := range []string{"2 January 2006", "2 Jan 2006"} {
			if t, err := time.Parse(f, m[1]+" "+m[2]+" "+m[3]); err == nil {
				return t
			}
		}
	}
	return time.Now()
}

// locoSessionContent 将 session 所有轮次格式化为 memory content
func locoSessionContent(session LoCoMoSession) string {
	var sb strings.Builder
	for _, t := range session.Turns {
		fmt.Fprintf(&sb, "%s: %s\n", t.Speaker, t.Text)
	}
	return strings.TrimSpace(sb.String())
}

// locoWindowContent 将 turns[start:end] 格式化为 window memory content。
// contextSize > 0 时，在窗口正文前追加最多 contextSize 条前驱轮次作为上下文前缀。
// Formats turns[start:end] as window memory content.
// When contextSize > 0, prepends up to contextSize preceding turns as a context prefix.
func locoWindowContent(sess LoCoMoSession, start, end, contextSize int) string {
	turns := sess.Turns
	var sb strings.Builder

	if contextSize > 0 && start > 0 {
		ctxStart := start - contextSize
		if ctxStart < 0 {
			ctxStart = 0
		}
		sb.WriteString("[Context]\n")
		for _, t := range turns[ctxStart:start] {
			fmt.Fprintf(&sb, "%s: %s\n", t.Speaker, t.Text)
		}
		sb.WriteString("---\n")
	}

	if sess.DateTime != "" {
		fmt.Fprintf(&sb, "[Session %d | %s | T%d–T%d]\n", sess.SessionNum, sess.DateTime, start+1, end)
	} else {
		fmt.Fprintf(&sb, "[Session %d | T%d–T%d]\n", sess.SessionNum, start+1, end)
	}

	for _, t := range turns[start:end] {
		fmt.Fprintf(&sb, "%s: %s\n", t.Speaker, t.Text)
	}
	return strings.TrimSpace(sb.String())
}

// ─── Seeding ─────────────────────────────────────────────────────────────────

// LoCoMoSeedConfig 控制 session 写入时的分块粒度 / Controls chunking granularity when seeding.
type LoCoMoSeedConfig struct {
	// WindowSize 每个 memory 包含的轮次数；≤0 表示整 session 不分块（原始行为）。
	// Turns per memory window; ≤0 = whole session (original behavior).
	WindowSize int
	// Stride 相邻窗口起始间距；≤0 时默认等于 WindowSize（无重叠）。
	// Step between window starts; ≤0 defaults to WindowSize (non-overlapping).
	Stride int
	// ContextSize 窗口前额外追加的上下文轮次数，用于保留对话背景。
	// Preceding turns prepended as context prefix, preserving conversational context.
	ContextSize int
}

// SeedLoCoMoDB 将 LoCoMo 对话写入专用 SQLite DB（幂等）。
// cfg.WindowSize ≤ 0 → 整 session 一条记忆（原始行为）。
// cfg.WindowSize > 0 → 滑动窗口切分，同一 session 所有窗口共用 source_ref，命中判定不受影响。
// Seeds LoCoMo conversations into a dedicated SQLite DB. Idempotent.
// WindowSize ≤ 0 → one memory per session. WindowSize > 0 → sliding-window chunking;
// all windows within a session share the same source_ref so hit-rate eval is unaffected.
func SeedLoCoMoDB(ctx context.Context, convs []LoCoMoConversation, dbPath string, cfg LoCoMoSeedConfig) error {
	tok := tokenizer.NewSimpleTokenizer()
	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	if err != nil {
		return fmt.Errorf("open locomo store: %w", err)
	}
	defer memStore.Close()
	if err := memStore.Init(ctx); err != nil {
		return fmt.Errorf("init locomo store: %w", err)
	}

	db, ok := memStore.DB().(*sql.DB)
	if !ok {
		return fmt.Errorf("store does not expose *sql.DB")
	}
	graphStore := store.NewSQLiteGraphStore(db)
	graphMgr := memory.NewGraphManager(graphStore)
	_ = graphMgr
	mgr := memory.NewManager(memory.ManagerDeps{
		MemStore: memStore,
	})

	stride := cfg.Stride
	if stride <= 0 {
		stride = cfg.WindowSize
	}

	seeded := 0
	for _, conv := range convs {
		sessions := parseSessions(conv.Conversation)
		for _, sess := range sessions {
			ts := locoParseTime(sess.DateTime)
			sourceRef := fmt.Sprintf("%s/D%d", conv.SampleID, sess.SessionNum)

			var windows []string
			if cfg.WindowSize <= 0 {
				if c := locoSessionContent(sess); c != "" {
					windows = append(windows, c)
				}
			} else {
				turns := sess.Turns
				for start := 0; start < len(turns); start += stride {
					end := start + cfg.WindowSize
					if end > len(turns) {
						end = len(turns)
					}
					if c := locoWindowContent(sess, start, end, cfg.ContextSize); c != "" {
						windows = append(windows, c)
					}
					if end >= len(turns) {
						break
					}
				}
			}

			for _, content := range windows {
				_, err := mgr.Create(ctx, &model.CreateMemoryRequest{
					Content:       content,
					Kind:          "episodic",
					Scope:         "eval/locomo",
					SourceType:    "conversation",
					SourceRef:     sourceRef,
					RetentionTier: model.TierPermanent,
					HappenedAt:    &ts,
				})
				if err != nil {
					if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "UNIQUE") {
						continue
					}
					return fmt.Errorf("seed %s: %w", sourceRef, err)
				}
				seeded++
			}
		}
	}

	if cfg.WindowSize > 0 {
		fmt.Printf("  SeedLoCoMoDB: %d windows seeded (window=%d stride=%d context=%d, %d conversations)\n",
			seeded, cfg.WindowSize, stride, cfg.ContextSize, len(convs))
	} else {
		fmt.Printf("  SeedLoCoMoDB: %d sessions seeded (%d conversations)\n", seeded, len(convs))
	}
	return nil
}

// SeedLoCoMoVectors 将 LoCoMo DB 中的记忆批量写入 Qdrant（幂等 upsert）
// Seeds LoCoMo memories from DB into the dedicated Qdrant collection. Idempotent.
func SeedLoCoMoVectors(ctx context.Context, dbPath, qdrantURL string, dim, maxItems int) (int, error) {
	return SeedVectorsToQdrant(ctx, dbPath, qdrantURL, LoCoMoCollection, dim, maxItems)
}

// SeedLoCoMoVectorsToCollection 将 LoCoMo DB 中的记忆批量写入指定 Qdrant collection（幂等）
// Seeds LoCoMo memories into a specified Qdrant collection. Use for alternative chunking strategies.
func SeedLoCoMoVectorsToCollection(ctx context.Context, dbPath, qdrantURL, collection string, dim, maxItems int) (int, error) {
	return SeedVectorsToQdrant(ctx, dbPath, qdrantURL, collection, dim, maxItems)
}

// ─── Evaluation ──────────────────────────────────────────────────────────────

// LoCoMoCategoryName 返回 category 人类可读名称 / Human-readable category name
func LoCoMoCategoryName(cat int) string {
	switch cat {
	case 1:
		return "single-hop"
	case 2:
		return "multi-hop"
	case 3:
		return "temporal"
	case 4:
		return "adversarial"
	case 5:
		return "open-domain"
	default:
		return fmt.Sprintf("cat%d", cat)
	}
}

// LoCoMoCaseResult 单条 QA 评测结果 / Single QA evaluation result
type LoCoMoCaseResult struct {
	SampleID string
	Question string
	Expected string
	Category int
	Hit      bool
	Rank     int // 1-based; 0 = not found
}

// LoCoMoReport 整体评测报告 / Overall evaluation report
type LoCoMoReport struct {
	Tier       string
	Total      int
	HitRate    float64
	MRR        float64
	ByCategory map[string]LoCoMoCatMetrics
	Duration   time.Duration
}

// LoCoMoCatMetrics per-category metrics / 分类指标
type LoCoMoCatMetrics struct {
	Total   int
	HitRate float64
	MRR     float64
}

// RunLoCoMoEval 在 LoCoMo 数据集上运行检索评测（跳过 category=4 对抗性问题）
// Runs retrieval evaluation on LoCoMo dataset. Skips adversarial (category=4) questions.
// maxQ=0 means all QA pairs.
func RunLoCoMoEval(ctx context.Context, dbPath string, convs []LoCoMoConversation, tier Tier, maxQ int) (*LoCoMoReport, error) {
	loadTestConfig()
	cfg := buildRetrievalConfig("fts")
	if tier.Pipeline {
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

	// tier.DBPath 覆盖传入的 dbPath（用于 1-turn 等替代粒度评测）
	// tier.DBPath overrides the passed dbPath — used for alternative chunking evaluations.
	if tier.DBPath != "" {
		dbPath = tier.DBPath
	}

	// 使用 config.yaml 配置的分词器（jieba/gse/simple），与生产路径一致
	// Use config-based tokenizer (jieba/gse/simple) to match production path.
	tok := resolveEvalTokenizer()
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
	llmProvider := resolveLLMProvider()

	var vecStore store.VectorStore
	var embedder store.Embedder
	// tier.Vector / tier.GraphVectorEvidence / tier.HyDE 都需要 Qdrant + Embedder
	// tier.Vector, tier.GraphVectorEvidence, and tier.HyDE all require Qdrant + Embedder
	if tier.Vector || tier.GraphVectorEvidence || tier.HyDE {
		emb, embErr := resolveEmbedder()
		if embErr != nil {
			return nil, fmt.Errorf("resolve embedder: %w", embErr)
		}
		collection := LoCoMoCollection
		if tier.QdrantCollection != "" {
			collection = tier.QdrantCollection
		}
		vs := store.NewQdrantVectorStore(evalQdrantURL(), collection, evalQdrantDim())
		if initErr := vs.Init(ctx); initErr != nil {
			return nil, fmt.Errorf("init qdrant: %w", initErr)
		}
		vecStore = vs
		embedder = emb
	}

	// 向量查询侧非对称检索指令 / Asymmetric retrieval instruction for vector query side
	cfg.VectorQueryInstruction = tier.VectorQueryInstruction

	// hub 节点过滤：按记忆总数比例计算阈值 / Hub entity filter: threshold = total memories × ratio
	if tier.GraphSalienceRatio > 0 {
		var totalMems int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL`).Scan(&totalMems); err == nil && totalMems > 0 {
			threshold := int(float64(totalMems) * tier.GraphSalienceRatio)
			if threshold < 10 {
				threshold = 10
			}
			cfg.GraphSalienceMaxCount = threshold
		}
	}
	// Option B: FTS 优先种子策略 / FTS-first seed strategy
	if tier.GraphFTSFirstSeeds {
		cfg.GraphFTSFirstSeeds = true
	}

	// HyDE: LLM 生成假设答案文档 + 向量搜索 / HyDE: LLM generates hypothetical doc + vector search
	var preprocessor *search.Preprocessor
	if tier.HyDE {
		cfg.Preprocess.Enabled = true
		cfg.Preprocess.UseLLM = true
		cfg.Preprocess.LLMTimeout = 15 * time.Second
		cfg.Preprocess.HyDEEnabled = true
		cfg.Preprocess.HyDEWeight = 0.8
		cfg.Preprocess.HyDEMinRunes = 10 // LoCoMo 问题较短，降低触发阈值 / Lower threshold for short LoCoMo questions
		if llmProvider != nil {
			preprocessor = search.NewPreprocessor(tok, graphStore, llmProvider, cfg)
		}
	}

	// Pipeline tier: 即使没有 LLM，也创建 rule-based preprocessor 做关键词提取
	// Pipeline tier: create rule-based preprocessor even without LLM for keyword extraction.
	// Benefits: stopword filtering strips noise from raw query (e.g. removes "does/did/is"),
	// temporal anchor detection enables TemporalFilterStage, entity matching works when graph is seeded.
	if tier.Pipeline && preprocessor == nil {
		preprocessor = search.NewPreprocessor(tok, graphStore, nil, cfg)
	}

	retriever := search.NewRetriever(memStore, vecStore, embedder, graphStore, llmProvider, cfg, preprocessor, nil)
	retriever.InitPipeline()

	// Collect non-adversarial QA pairs
	type qaItem struct {
		sampleID string
		qa       LoCoMoQA
		evidence map[string]bool // set of source_refs that answer this question
	}
	var items []qaItem
	for i := range convs {
		for _, qa := range convs[i].QA {
			if qa.Category == 4 {
				continue // adversarial: no expected answer
			}
			ev := make(map[string]bool)
			for _, e := range qa.Evidence {
				if n := locoExtractSessionNum(e); n > 0 {
					ev[fmt.Sprintf("%s/D%d", convs[i].SampleID, n)] = true
				}
			}
			items = append(items, qaItem{sampleID: convs[i].SampleID, qa: qa, evidence: ev})
		}
	}
	if maxQ > 0 && len(items) > maxQ {
		items = items[:maxQ]
	}

	start := time.Now()
	var results []LoCoMoCaseResult

	for i, item := range items {
		retrieved, err := retriever.Retrieve(ctx, &model.RetrieveRequest{
			Query: item.qa.Question,
			Limit: 10,
		})
		hit, rank := false, 0
		if err == nil {
			for j, r := range retrieved {
				if item.evidence[r.Memory.SourceRef] {
					hit = true
					rank = j + 1
					break
				}
			}
		}

		results = append(results, LoCoMoCaseResult{
			SampleID: item.sampleID,
			Question: item.qa.Question,
			Expected: item.qa.AnswerString(),
			Category: item.qa.Category,
			Hit:      hit,
			Rank:     rank,
		})

		if (i+1)%50 == 0 || i+1 == len(items) {
			hits := 0
			for _, r := range results {
				if r.Hit {
					hits++
				}
			}
			fmt.Printf("  [locomo/%s %d/%d] hit %d/%d (%.1f%%)\n",
				tier.Name, i+1, len(items), hits, i+1, float64(hits)/float64(i+1)*100)
		}

		time.Sleep(20 * time.Millisecond)
	}

	return locoComputeReport(tier.Name, results, time.Since(start)), nil
}

// locoComputeReport 计算汇总指标 / Compute aggregate metrics
func locoComputeReport(tierName string, results []LoCoMoCaseResult, dur time.Duration) *LoCoMoReport {
	type acc struct {
		hits, total int
		rr          float64
	}
	overall := &acc{}
	byCat := map[int]*acc{}

	for _, r := range results {
		overall.total++
		if r.Hit {
			overall.hits++
			overall.rr += 1.0 / float64(r.Rank)
		}
		if byCat[r.Category] == nil {
			byCat[r.Category] = &acc{}
		}
		byCat[r.Category].total++
		if r.Hit {
			byCat[r.Category].hits++
			byCat[r.Category].rr += 1.0 / float64(r.Rank)
		}
	}

	byCategory := map[string]LoCoMoCatMetrics{}
	for cat, a := range byCat {
		hr, mrr := 0.0, 0.0
		if a.total > 0 {
			hr = float64(a.hits) / float64(a.total) * 100
			mrr = a.rr / float64(a.total)
		}
		byCategory[LoCoMoCategoryName(cat)] = LoCoMoCatMetrics{Total: a.total, HitRate: hr, MRR: mrr}
	}

	hr, mrr := 0.0, 0.0
	if overall.total > 0 {
		hr = float64(overall.hits) / float64(overall.total) * 100
		mrr = overall.rr / float64(overall.total)
	}
	return &LoCoMoReport{
		Tier:       tierName,
		Total:      overall.total,
		HitRate:    hr,
		MRR:        mrr,
		ByCategory: byCategory,
		Duration:   dur,
	}
}

// LoCoMoReportToEvalReport 将 LoCoMoReport 转为通用 EvalReport 以便保存 baseline
// Converts a LoCoMoReport to EvalReport for baseline persistence.
func LoCoMoReportToEvalReport(r *LoCoMoReport) *EvalReport {
	byCategory := make(map[string]AggregateMetrics, len(r.ByCategory))
	for cat, m := range r.ByCategory {
		byCategory[cat] = AggregateMetrics{
			Total:   m.Total,
			HitRate: m.HitRate,
			MRR:     m.MRR,
		}
	}
	return &EvalReport{
		Mode:      "locomo — " + r.Tier,
		Dataset:   "locomo10",
		Timestamp: time.Now(),
		Metrics: AggregateMetrics{
			Total:   r.Total,
			HitRate: r.HitRate,
			MRR:     r.MRR,
		},
		ByCategory: byCategory,
		Duration:   r.Duration,
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// locoExtractSessionNum parses session number from evidence "D1:3" → 1
func locoExtractSessionNum(evidence string) int {
	m := regexp.MustCompile(`^D(\d+):`).FindStringSubmatch(evidence)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// locoStrOf safely casts interface{} to string
func locoStrOf(v interface{}) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// LoCoMoSpeakerExcludeHint 返回 LoCoMo 数据集的正向抽取提示词。
// 不排除说话人名字，依赖 base prompt 的相关性过滤决定是否抽取。
// Returns a positive extraction hint for LoCoMo. Does not exclude speaker names;
// relies on the base prompt relevance filter to determine extraction quality.
func LoCoMoSpeakerExcludeHint(_ []LoCoMoConversation) string {
	return "Focus on: health events, activities, places visited, organizations joined, " +
		"tools or hobbies mentioned, and third-party people discussed in the conversation."
}

// LoCoMoQdrantURL returns the Qdrant URL for LoCoMo eval (reuses global config)
func LoCoMoQdrantURL() string { return evalQdrantURL() }

// LoCoMoQdrantDim returns the vector dimension for LoCoMo eval (reuses global config)
func LoCoMoQdrantDim() int { return evalQdrantDim() }

// buildRetrievalConfig is referenced here but defined in longmemeval.go.
// Compile-time check: ensure config package is imported correctly.
var _ = config.RetrievalConfig{}
