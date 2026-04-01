package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/model"
	"iclude/pkg/tokenizer"

	_ "modernc.org/sqlite"
)

// 编译期接口检查 / Compile-time interface compliance check
var _ MemoryStore = (*SQLiteMemoryStore)(nil)

// 全量列名（35列）/ Full column list (35 columns)
const memoryColumns = `id, content, metadata, team_id, embedding_id, parent_id, is_latest, access_count, created_at, updated_at,
	uri, context_id, kind, sub_kind, scope, abstract, summary,
	happened_at, source_type, source_ref, document_id, chunk_index,
	deleted_at, strength, decay_rate, last_accessed_at, reinforced_count, expires_at,
	retention_tier, message_role, turn_number, content_hash, consolidated_into, owner_id, visibility`

// 带 m. 前缀的全量列名，用于 JOIN 查询 / Full aliased column list for JOIN queries
const memoryColumnsAliased = `m.id, m.content, m.metadata, m.team_id, m.embedding_id, m.parent_id, m.is_latest, m.access_count, m.created_at, m.updated_at,
	m.uri, m.context_id, m.kind, m.sub_kind, m.scope, m.abstract, m.summary,
	m.happened_at, m.source_type, m.source_ref, m.document_id, m.chunk_index,
	m.deleted_at, m.strength, m.decay_rate, m.last_accessed_at, m.reinforced_count, m.expires_at,
	m.retention_tier, m.message_role, m.turn_number, m.content_hash, m.consolidated_into, m.owner_id, m.visibility`

// SQLiteMemoryStore 基于 SQLite 的结构化存储 / SQLite-backed structured memory store
type SQLiteMemoryStore struct {
	db          *sql.DB
	bm25Weights [3]float64          // content, abstract, summary
	tokenizer   tokenizer.Tokenizer // 可拔插分词器 / pluggable tokenizer
}

// NewSQLiteMemoryStore 创建 SQLite 存储实例 / Create a new SQLite memory store
// tok 可为 nil，此时使用 NoopTokenizer（不分词）
func NewSQLiteMemoryStore(dbPath string, bm25Weights [3]float64, tok tokenizer.Tokenizer) (*SQLiteMemoryStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// 性能优化 PRAGMAs
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA mmap_size=268435456",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = -32000",
		"PRAGMA temp_store = MEMORY",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma %q: %w", pragma, err)
		}
	}

	// 连接池配置 / Connection pool configuration
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	weights := bm25Weights
	if weights[0] == 0 && weights[1] == 0 && weights[2] == 0 {
		weights = [3]float64{10.0, 5.0, 3.0}
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
	return Migrate(s.db, s.tokenizer)
}

// Close 关闭数据库连接 / Close the database connection
func (s *SQLiteMemoryStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// sanitizeFTS5Query 清除 FTS5 操作符，每个词独立包裹为短语 / Strip FTS5 operators, wrap each token as phrase
func sanitizeFTS5Query(query string) string {
	if query == "" {
		return query
	}
	// 移除 FTS5 特殊字符和操作符 / Remove FTS5 special chars
	replacer := strings.NewReplacer(
		`"`, ``, `*`, ``, `(`, ``, `)`, ``, `^`, ``, `-`, ` `, `+`, ` `,
	)
	cleaned := replacer.Replace(query)

	// 过滤 FTS5 保留字并用 OR 连接（提高召回率）/ Filter reserved words and join with OR for better recall
	words := strings.Fields(cleaned)
	var filtered []string
	for _, w := range words {
		if w == "" {
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
	return strings.Join(filtered, " OR ")
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
}

// scanFields 返回扫描目标字段列表（与 memoryColumns 顺序一致，35 列）
// Returns scan destination fields matching memoryColumns order (35 columns)
func (d *memScanDest) scanFields() []any {
	return []any{
		&d.mem.ID, &d.mem.Content, &d.metaStr, &d.mem.TeamID,
		&d.mem.EmbeddingID, &d.mem.ParentID, &d.isLatestInt, &d.mem.AccessCount,
		&d.mem.CreatedAt, &d.mem.UpdatedAt,
		&d.mem.URI, &d.mem.ContextID, &d.mem.Kind, &d.mem.SubKind, &d.mem.Scope, &d.mem.Abstract, &d.mem.Summary,
		&d.happenedAt, &d.mem.SourceType, &d.mem.SourceRef, &d.mem.DocumentID, &d.chunkIndex,
		&d.deletedAt, &d.strength, &d.decayRate, &d.lastAccessedAt, &d.reinforcedCount, &d.expiresAt,
		&d.retentionTier, &d.messageRole, &d.turnNumber, &d.contentHash, &d.consolidatedInto,
		&d.ownerID, &d.visibility,
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

// syncFTS5 同步 FTS5 索引（INSERT），写入前预分词 / Sync FTS5 index with pre-tokenization
func (s *SQLiteMemoryStore) syncFTS5(ctx context.Context, mem *model.Memory) error {
	// 获取 rowid
	var rowid int64
	if err := s.db.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, mem.ID).Scan(&rowid); err != nil {
		return fmt.Errorf("failed to get rowid for FTS5 sync: %w", err)
	}

	// 预分词：对 content/abstract/summary 分词后写入 FTS5
	content, err := s.tokenizer.Tokenize(ctx, mem.Content)
	if err != nil {
		content = mem.Content // 分词失败回退原文
	}
	abstract, err := s.tokenizer.Tokenize(ctx, mem.Abstract)
	if err != nil {
		abstract = mem.Abstract
	}
	summary, err := s.tokenizer.Tokenize(ctx, mem.Summary)
	if err != nil {
		summary = mem.Summary
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO memories_fts(rowid, content, abstract, summary) VALUES (?, ?, ?, ?)`,
		rowid, content, abstract, summary,
	)
	return err
}

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
	abstract, err := s.tokenizer.Tokenize(ctx, mem.Abstract)
	if err != nil {
		abstract = mem.Abstract
	}
	summary, err := s.tokenizer.Tokenize(ctx, mem.Summary)
	if err != nil {
		summary = mem.Summary
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories_fts(rowid, content, abstract, summary) VALUES (?, ?, ?, ?)`,
		rowid, content, abstract, summary,
	)
	return err
}

// deleteFTS5ByRowID 通过 rowid 删除 FTS5 条目
func (s *SQLiteMemoryStore) deleteFTS5ByRowID(ctx context.Context, rowid int64) error {
	// 先获取旧内容
	var content, abstract, summary string
	err := s.db.QueryRowContext(ctx, `SELECT content, COALESCE(abstract, ''), COALESCE(summary, '') FROM memories WHERE rowid = ?`, rowid).Scan(&content, &abstract, &summary)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("failed to get old content for FTS5 delete: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO memories_fts(memories_fts, rowid, content, abstract, summary) VALUES('delete', ?, ?, ?, ?)`,
		rowid, content, abstract, summary,
	)
	return err
}

// deleteFTS5ByRowIDTx 在事务内通过 rowid 删除 FTS5 条目 / Delete FTS5 entry by rowid within a transaction
func (s *SQLiteMemoryStore) deleteFTS5ByRowIDTx(ctx context.Context, tx *sql.Tx, rowid int64) error {
	var content, abstract, summary string
	err := tx.QueryRowContext(ctx, `SELECT content, COALESCE(abstract, ''), COALESCE(summary, '') FROM memories WHERE rowid = ?`, rowid).Scan(&content, &abstract, &summary)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("failed to get old content for FTS5 delete: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories_fts(memories_fts, rowid, content, abstract, summary) VALUES('delete', ?, ?, ?, ?)`,
		rowid, content, abstract, summary,
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
