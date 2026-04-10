package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

// 编译期接口检查 / Compile-time interface compliance check
var _ MemoryStore = (*SQLiteMemoryStore)(nil)

// 全量列名（36列）/ Full column list (36 columns)
// derived_from 已迁移至 memory_derivations junction 表（V16）/ derived_from moved to junction table in V16
const memoryColumns = `id, content, metadata, team_id, parent_id, is_latest, access_count, created_at, updated_at,
	uri, context_id, kind, sub_kind, scope, excerpt, summary,
	happened_at, source_type, source_ref, document_id, chunk_index,
	deleted_at, strength, decay_rate, last_accessed_at, reinforced_count, expires_at,
	retention_tier, message_role, turn_number, content_hash, consolidated_into, owner_id, visibility,
	memory_class, candidate_for`

// 带 m. 前缀的全量列名，用于 JOIN 查询 / Full aliased column list for JOIN queries
const memoryColumnsAliased = `m.id, m.content, m.metadata, m.team_id, m.parent_id, m.is_latest, m.access_count, m.created_at, m.updated_at,
	m.uri, m.context_id, m.kind, m.sub_kind, m.scope, m.excerpt, m.summary,
	m.happened_at, m.source_type, m.source_ref, m.document_id, m.chunk_index,
	m.deleted_at, m.strength, m.decay_rate, m.last_accessed_at, m.reinforced_count, m.expires_at,
	m.retention_tier, m.message_role, m.turn_number, m.content_hash, m.consolidated_into, m.owner_id, m.visibility,
	m.memory_class, m.candidate_for`

// SQLiteMemoryStore 基于 SQLite 的结构化存储 / SQLite-backed structured memory store
type SQLiteMemoryStore struct {
	db          *sql.DB
	bm25Weights [3]float64          // content, excerpt, summary
	tokenizer   tokenizer.Tokenizer // 可拔插分词器 / pluggable tokenizer
}

// NewSQLiteMemoryStore 创建 SQLite 存储实例 / Create a new SQLite memory store
// tok 可为 nil，此时使用 NoopTokenizer（不分词）
func NewSQLiteMemoryStore(dbPath string, bm25Weights [3]float64, tok tokenizer.Tokenizer) (*SQLiteMemoryStore, error) {
	// PRAGMAs are set via DSN _pragma parameters so every connection in the pool
	// inherits them automatically. db.Exec PRAGMAs only apply to the current
	// connection — new pool connections would miss critical settings like
	// foreign_keys=ON, which breaks FK CASCADE behavior.
	dsn := dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-32000)" +
		"&_pragma=temp_store(MEMORY)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// mmap_size is a global setting that persists per database file, so it only
	// needs to be set once rather than per-connection via DSN.
	if _, err := db.Exec("PRAGMA mmap_size=268435456"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set pragma mmap_size: %w", err)
	}

	// 连接池配置 / Connection pool configuration
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	weights := bm25Weights
	if weights[0] == 0 && weights[1] == 0 && weights[2] == 0 {
		weights = [3]float64{config.DefaultBM25Content, config.DefaultBM25Excerpt, config.DefaultBM25Summary}
	}

	if tok == nil {
		tok = tokenizer.NewNoopTokenizer()
	}

	return &SQLiteMemoryStore{db: db, bm25Weights: weights, tokenizer: tok}, nil
}

// DB 获取底层数据库连接 / Get underlying database connection for sharing
func (s *SQLiteMemoryStore) DB() interface{} {
	return s.db
}

// Init 初始化存储（执行迁移）/ Initialize storage (run migrations)
func (s *SQLiteMemoryStore) Init(ctx context.Context) error {
	if err := Migrate(s.db, s.tokenizer); err != nil {
		return err
	}

	// 检测分词器是否变更，变更则自动重建 FTS / Detect tokenizer change and rebuild FTS
	if err := s.checkTokenizerChange(ctx); err != nil {
		return fmt.Errorf("tokenizer change check failed: %w", err)
	}

	return nil
}

// checkTokenizerChange 检测分词器变更并重建 FTS 索引 / Detect tokenizer change and rebuild FTS index
func (s *SQLiteMemoryStore) checkTokenizerChange(ctx context.Context) error {
	var stored string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='tokenizer'`).Scan(&stored)
	if err != nil {
		// meta 表不存在或无记录，跳过 / meta table missing or no record, skip
		return nil
	}

	current := "simple"
	if s.tokenizer != nil {
		current = s.tokenizer.Name()
	}

	if stored == current {
		return nil
	}

	logger.Info("tokenizer changed, rebuilding FTS index",
		zap.String("from", stored),
		zap.String("to", current),
	)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin FTS rebuild transaction: %w", err)
	}
	defer tx.Rollback()

	// 重建 FTS5 / Rebuild FTS5
	if _, err := tx.Exec(`DROP TABLE IF EXISTS memories_fts`); err != nil {
		return fmt.Errorf("failed to drop FTS5 table: %w", err)
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(
		content, excerpt, summary,
		content=memories, content_rowid=rowid
	)`); err != nil {
		return fmt.Errorf("failed to create FTS5 table: %w", err)
	}

	rows, err := tx.Query(`SELECT rowid, content, COALESCE(excerpt,''), COALESCE(summary,'') FROM memories WHERE deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("failed to query memories for FTS rebuild: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var rowid int64
		var content, excerpt, summary string
		if err := rows.Scan(&rowid, &content, &excerpt, &summary); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		tc, _ := s.tokenizer.Tokenize(ctx, content)
		ta, _ := s.tokenizer.Tokenize(ctx, excerpt)
		ts, _ := s.tokenizer.Tokenize(ctx, summary)
		if _, err := tx.Exec(`INSERT INTO memories_fts(rowid, content, excerpt, summary) VALUES(?,?,?,?)`,
			rowid, tc, ta, ts); err != nil {
			return fmt.Errorf("failed to insert FTS row (rowid=%d): %w", rowid, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("FTS rebuild iteration error: %w", err)
	}

	// 更新 meta / Update meta
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('tokenizer', ?)`, current); err != nil {
		return fmt.Errorf("failed to update tokenizer meta: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit FTS rebuild: %w", err)
	}

	logger.Info("FTS index rebuilt for new tokenizer", zap.String("tokenizer", current), zap.Int("memories", count))
	return nil
}

// Close 关闭数据库连接 / Close the database connection
func (s *SQLiteMemoryStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// sanitizeFTS5Query 清除 FTS5 操作符，每个词独立包裹为短语 / Strip FTS5 operators, wrap each token as phrase
// maxFTS5Terms FTS5 OR 查询最大项数，CTE 隔离后 FTS5 纯索引操作对 term 数不敏感，保持合理上限即可
// Max OR terms in FTS5 query; with CTE isolation FTS5 runs index-only, moderate cap suffices
const maxFTS5Terms = 12

// fts5StopWords 英文停用词（FTS5 查询级过滤，减少无意义 term）/ English stop words for FTS5 query filtering
var fts5StopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "shall": true, "can": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "you": true, "your": true,
	"he": true, "she": true, "it": true, "its": true, "they": true, "them": true, "their": true,
	"this": true, "that": true, "these": true, "those": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true, "with": true,
	"by": true, "from": true, "as": true, "into": true, "about": true, "after": true,
	"what": true, "which": true, "who": true, "whom": true, "how": true, "when": true, "where": true,
	"if": true, "then": true, "so": true, "no": true, "not": true, "but": true, "or": true, "and": true,
}

func sanitizeFTS5Query(query string) string {
	if query == "" {
		return query
	}
	replacer := strings.NewReplacer(
		`"`, ``, `*`, ``, `(`, ``, `)`, ``, `^`, ``, `-`, ` `, `+`, ` `,
	)
	cleaned := replacer.Replace(query)

	words := strings.Fields(cleaned)
	var filtered []string
	for _, w := range words {
		if w == "" {
			continue
		}
		lower := strings.ToLower(w)
		// 跳过英文停用词和单字符英文词 / Skip stop words and single-char ASCII words
		if fts5StopWords[lower] {
			continue
		}
		if len(w) == 1 && w[0] < 0x80 {
			continue
		}
		upper := strings.ToUpper(w)
		if upper == "AND" || upper == "OR" || upper == "NOT" || upper == "NEAR" {
			filtered = append(filtered, `"`+w+`"`)
		} else {
			filtered = append(filtered, w)
		}
	}
	if len(filtered) == 0 {
		return cleaned
	}

	parts := make([]string, len(filtered))
	copy(parts, filtered)

	// 二元组增强：3+ 关键词时追加相邻词对短语提升精确匹配权重 / Bigram boost for 3+ word queries
	if len(filtered) >= 3 {
		for i := 0; i < len(filtered)-1; i++ {
			parts = append(parts, `"`+filtered[i]+" "+filtered[i+1]+`"`)
		}
	}

	// 截断到上限 / Truncate to cap
	if len(parts) > maxFTS5Terms {
		parts = parts[:maxFTS5Terms]
	}

	return strings.Join(parts, " OR ")
}

// extractQueryWords 提取查询中的有效词（去重、小写化）/ Extract unique lowercased words from query
func extractQueryWords(query string) []string {
	seen := make(map[string]bool)
	var words []string
	for _, w := range strings.Fields(query) {
		lower := strings.ToLower(w)
		if lower != "" && !seen[lower] {
			seen[lower] = true
			words = append(words, lower)
		}
	}
	return words
}

// wordCoverage 计算文档对查询词的覆盖率 / Calculate query word coverage ratio in document
func wordCoverage(doc string, queryWords []string) float64 {
	if len(queryWords) == 0 {
		return 0
	}
	docLower := strings.ToLower(doc)
	matched := 0
	for _, w := range queryWords {
		if strings.Contains(docLower, w) {
			matched++
		}
	}
	return float64(matched) / float64(len(queryWords))
}

// visibilityCondition 返回可见性 WHERE 子句和参数 / Return visibility WHERE clause and args
// 兼容旧数据: owner_id 为空时视为无主记忆，同 team 即可见 / Legacy compat: empty owner_id is visible to same team
// TeamID 为空时跳过 team 匹配（向后兼容）/ Empty TeamID bypasses team matching for backward compatibility
func visibilityCondition(prefix string, identity *model.Identity) (string, []interface{}) {
	if identity.TeamID == "" {
		// 无 team 限制: 显示所有公开 + 所有 team 可见 + owner 匹配或无主的 private
		return fmt.Sprintf(`(%[1]svisibility = 'public'
			OR %[1]svisibility = 'team'
			OR (%[1]svisibility = 'private' AND (%[1]sowner_id = ? OR %[1]sowner_id = '')))`,
			prefix), []interface{}{identity.OwnerID}
	}
	return fmt.Sprintf(`(%[1]svisibility = 'public'
		OR (%[1]steam_id = ? AND %[1]svisibility = 'team')
		OR (%[1]steam_id = ? AND %[1]svisibility = 'private' AND (%[1]sowner_id = ? OR %[1]sowner_id = '')))`,
		prefix), []interface{}{identity.TeamID, identity.TeamID, identity.OwnerID}
}

// ---- 扫描辅助函数 ----

// memScanDest 记忆扫描目标，用于消除重复扫描逻辑 / Memory scan destination for reducing code duplication
type memScanDest struct {
	mem              model.Memory
	metaStr          sql.NullString
	isLatestInt      int
	happenedAt       sql.NullTime
	deletedAt        sql.NullTime
	lastAccessedAt   sql.NullTime
	expiresAt        sql.NullTime
	strength         sql.NullFloat64
	decayRate        sql.NullFloat64
	reinforcedCount  sql.NullInt64
	chunkIndex       sql.NullInt64
	retentionTier    sql.NullString
	messageRole      sql.NullString
	turnNumber       sql.NullInt64
	contentHash      sql.NullString
	consolidatedInto sql.NullString
	ownerID          sql.NullString
	visibility       sql.NullString
	memoryClass      sql.NullString
	candidateFor     sql.NullString
}

// scanFields 返回扫描目标字段列表（与 memoryColumns 顺序一致，36 列）
// Returns scan destination fields matching memoryColumns order (36 columns)
// DerivedFrom 通过 memory_derivations junction 表单独加载（V16）
func (d *memScanDest) scanFields() []any {
	return []any{
		&d.mem.ID, &d.mem.Content, &d.metaStr, &d.mem.TeamID,
		&d.mem.ParentID, &d.isLatestInt, &d.mem.AccessCount,
		&d.mem.CreatedAt, &d.mem.UpdatedAt,
		&d.mem.URI, &d.mem.ContextID, &d.mem.Kind, &d.mem.SubKind, &d.mem.Scope, &d.mem.Excerpt, &d.mem.Summary,
		&d.happenedAt, &d.mem.SourceType, &d.mem.SourceRef, &d.mem.DocumentID, &d.chunkIndex,
		&d.deletedAt, &d.strength, &d.decayRate, &d.lastAccessedAt, &d.reinforcedCount, &d.expiresAt,
		&d.retentionTier, &d.messageRole, &d.turnNumber, &d.contentHash, &d.consolidatedInto,
		&d.ownerID, &d.visibility,
		&d.memoryClass, &d.candidateFor,
	}
}

// toMemory 将扫描结果转为 Memory / Convert scan result to Memory
func (d *memScanDest) toMemory() (*model.Memory, error) {
	d.mem.IsLatest = d.isLatestInt != 0
	if d.metaStr.Valid && d.metaStr.String != "" {
		if err := json.Unmarshal([]byte(d.metaStr.String), &d.mem.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	applyNullables(&d.mem, d.happenedAt, d.deletedAt, d.lastAccessedAt, d.expiresAt, d.strength, d.decayRate, d.reinforcedCount, d.chunkIndex)
	applyV3Nullables(&d.mem, d.retentionTier, d.messageRole, d.turnNumber)
	if d.contentHash.Valid {
		d.mem.ContentHash = d.contentHash.String
	}
	if d.consolidatedInto.Valid {
		d.mem.ConsolidatedInto = d.consolidatedInto.String
	}
	if d.ownerID.Valid {
		d.mem.OwnerID = d.ownerID.String
	}
	if d.visibility.Valid {
		d.mem.Visibility = d.visibility.String
	}
	if d.memoryClass.Valid && d.memoryClass.String != "" {
		d.mem.MemoryClass = d.memoryClass.String
	}
	if d.candidateFor.Valid && d.candidateFor.String != "" {
		d.mem.CandidateFor = d.candidateFor.String
	}
	// DerivedFrom 通过 memory_derivations junction 表单独加载（V16）/ Loaded separately from junction table
	return &d.mem, nil
}

// scanMemory 从单行扫描 Memory 对象（35 列）/ Scan a Memory from a single row (35 columns)
func (s *SQLiteMemoryStore) scanMemory(row *sql.Row) (*model.Memory, error) {
	var d memScanDest
	if err := row.Scan(d.scanFields()...); err != nil {
		return nil, err
	}
	return d.toMemory()
}

// scanMemoryFromRows 从结果集行扫描 Memory 对象（35 列）/ Scan a Memory from a rows cursor (35 columns)
func (s *SQLiteMemoryStore) scanMemoryFromRows(rows *sql.Rows) (*model.Memory, error) {
	var d memScanDest
	if err := rows.Scan(d.scanFields()...); err != nil {
		return nil, err
	}
	return d.toMemory()
}

// scanMemoryWithRank 扫描带 rank 列的行（36 列 = 35 + rank）/ Scan a Memory + BM25 rank (36 columns)
func (s *SQLiteMemoryStore) scanMemoryWithRank(rows *sql.Rows) (*model.Memory, float64, error) {
	var d memScanDest
	var rank float64
	fields := append(d.scanFields(), &rank)
	if err := rows.Scan(fields...); err != nil {
		return nil, 0, err
	}
	mem, err := d.toMemory()
	if err != nil {
		return nil, 0, err
	}
	return mem, rank, nil
}

// scanMemories 扫描多行
func (s *SQLiteMemoryStore) scanMemories(rows *sql.Rows) ([]*model.Memory, error) {
	var memories []*model.Memory
	for rows.Next() {
		mem, err := s.scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		memories = append(memories, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate memory rows: %w", err)
	}
	return memories, nil
}

// applyNullables 将 Null* 值赋给 Memory 字段
func applyNullables(mem *model.Memory, happenedAt, deletedAt, lastAccessedAt, expiresAt sql.NullTime, strength, decayRate sql.NullFloat64, reinforcedCount, chunkIndex sql.NullInt64) {
	if happenedAt.Valid {
		t := happenedAt.Time
		mem.HappenedAt = &t
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		mem.DeletedAt = &t
	}
	if lastAccessedAt.Valid {
		t := lastAccessedAt.Time
		mem.LastAccessedAt = &t
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		mem.ExpiresAt = &t
	}
	if strength.Valid {
		mem.Strength = strength.Float64
	}
	if decayRate.Valid {
		mem.DecayRate = decayRate.Float64
	}
	if reinforcedCount.Valid {
		mem.ReinforcedCount = int(reinforcedCount.Int64)
	}
	if chunkIndex.Valid {
		mem.ChunkIndex = int(chunkIndex.Int64)
	}
}

// applyV3Nullables 将 V3 新增的 Null* 值赋给 Memory 字段
func applyV3Nullables(mem *model.Memory, retentionTier, messageRole sql.NullString, turnNumber sql.NullInt64) {
	if retentionTier.Valid {
		mem.RetentionTier = retentionTier.String
	}
	if messageRole.Valid {
		mem.MessageRole = messageRole.String
	}
	if turnNumber.Valid {
		mem.TurnNumber = int(turnNumber.Int64)
	}
}

// ---- FTS5 同步辅助（external content 模式）----

// syncFTS5Tx FTS5 原子同步（在事务内执行）/ Atomic FTS5 sync within a transaction
func (s *SQLiteMemoryStore) syncFTS5Tx(ctx context.Context, tx *sql.Tx, mem *model.Memory) error {
	// 获取 rowid（事务内查询）
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, mem.ID).Scan(&rowid); err != nil {
		return fmt.Errorf("failed to get rowid for FTS5 sync: %w", err)
	}

	content, err := s.tokenizer.Tokenize(ctx, mem.Content)
	if err != nil {
		content = mem.Content
	}
	excerpt, err := s.tokenizer.Tokenize(ctx, mem.Excerpt)
	if err != nil {
		excerpt = mem.Excerpt
	}
	summary, err := s.tokenizer.Tokenize(ctx, mem.Summary)
	if err != nil {
		summary = mem.Summary
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories_fts(rowid, content, excerpt, summary) VALUES (?, ?, ?, ?)`,
		rowid, content, excerpt, summary,
	)
	return err
}

// deleteFTS5ByRowIDTx 在事务内通过 rowid 删除 FTS5 条目 / Delete FTS5 entry by rowid within a transaction
func (s *SQLiteMemoryStore) deleteFTS5ByRowIDTx(ctx context.Context, tx *sql.Tx, rowid int64) error {
	var content, excerpt, summary string
	err := tx.QueryRowContext(ctx, `SELECT content, COALESCE(excerpt, ''), COALESCE(summary, '') FROM memories WHERE rowid = ?`, rowid).Scan(&content, &excerpt, &summary)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("failed to get old content for FTS5 delete: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories_fts(memories_fts, rowid, content, excerpt, summary) VALUES('delete', ?, ?, ?, ?)`,
		rowid, content, excerpt, summary,
	)
	return err
}

// ---- 通用辅助函数 ----

// marshalMetadata 将 metadata map 序列化为 JSON 字符串
func marshalMetadata(metadata map[string]any) (sql.NullString, error) {
	if metadata == nil {
		return sql.NullString{Valid: false}, nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}

// boolToInt 将 bool 转为 SQLite INTEGER (0/1)
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// timeToNull 将 *time.Time 转为可 NULL 值
func timeToNull(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}
