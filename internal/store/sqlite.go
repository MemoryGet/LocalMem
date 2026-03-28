package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/pkg/sqlbuilder"
	"iclude/pkg/tokenizer"

	"github.com/google/uuid"
	"go.uber.org/zap"
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

	// 将每个词包裹为短语防止被解释为操作符 / Wrap each token as phrase to prevent operator interpretation
	words := strings.Fields(cleaned)
	for i, w := range words {
		upper := strings.ToUpper(w)
		if upper == "AND" || upper == "OR" || upper == "NOT" || upper == "NEAR" {
			words[i] = `"` + w + `"`
		}
	}
	return strings.Join(words, " ")
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

// Create 创建记忆 / Create a new memory record
func (s *SQLiteMemoryStore) Create(ctx context.Context, mem *model.Memory) error {
	if mem.Content == "" {
		return fmt.Errorf("content is required: %w", model.ErrInvalidInput)
	}

	now := time.Now().UTC()
	mem.ID = uuid.New().String()
	mem.CreatedAt = now
	mem.UpdatedAt = now
	if !mem.IsLatest {
		mem.IsLatest = true
	}

	// 设置默认值
	if mem.Strength == 0 {
		mem.Strength = 1.0
	}
	if mem.DecayRate == 0 {
		mem.DecayRate = 0.01
	}
	if mem.Scope == "" {
		mem.Scope = "default"
	}
	if mem.LastAccessedAt == nil {
		mem.LastAccessedAt = &now
	}
	if mem.RetentionTier == "" {
		mem.RetentionTier = "standard"
	}
	if mem.Visibility == "" {
		mem.Visibility = model.VisibilityPrivate
	}

	metadataJSON, err := marshalMetadata(mem.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO memories (` + memoryColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = tx.ExecContext(ctx, query,
		mem.ID, mem.Content, metadataJSON, mem.TeamID,
		mem.EmbeddingID, mem.ParentID, boolToInt(mem.IsLatest), mem.AccessCount,
		mem.CreatedAt, mem.UpdatedAt,
		mem.URI, mem.ContextID, mem.Kind, mem.SubKind, mem.Scope, mem.Abstract, mem.Summary,
		timeToNull(mem.HappenedAt), mem.SourceType, mem.SourceRef, mem.DocumentID, mem.ChunkIndex,
		timeToNull(mem.DeletedAt), mem.Strength, mem.DecayRate, timeToNull(mem.LastAccessedAt),
		mem.ReinforcedCount, timeToNull(mem.ExpiresAt),
		mem.RetentionTier, mem.MessageRole, mem.TurnNumber, mem.ContentHash, mem.ConsolidatedInto,
		mem.OwnerID, mem.Visibility,
	)
	if err != nil {
		return fmt.Errorf("failed to insert memory: %w", err)
	}

	// 同步 FTS5（external content 模式）
	if err := s.syncFTS5Tx(ctx, tx, mem); err != nil {
		return fmt.Errorf("failed to sync FTS5 after insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create tx: %w", err)
	}

	return nil
}

// CreateBatch 批量创建记忆（单事务）/ Batch create memories in a single transaction
func (s *SQLiteMemoryStore) CreateBatch(ctx context.Context, memories []*model.Memory) error {
	if len(memories) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	insertQuery := `INSERT INTO memories (` + memoryColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	insertStmt, err := tx.PrepareContext(ctx, insertQuery)
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	defer insertStmt.Close()

	for _, mem := range memories {
		now := time.Now().UTC()
		mem.ID = uuid.New().String()
		mem.CreatedAt = now
		mem.UpdatedAt = now
		if !mem.IsLatest {
			mem.IsLatest = true
		}
		if mem.Strength == 0 {
			mem.Strength = 1.0
		}
		if mem.DecayRate == 0 {
			mem.DecayRate = 0.01
		}
		if mem.Scope == "" {
			mem.Scope = "default"
		}
		if mem.LastAccessedAt == nil {
			mem.LastAccessedAt = &now
		}
		if mem.RetentionTier == "" {
			mem.RetentionTier = "standard"
		}
		if mem.Visibility == "" {
			mem.Visibility = model.VisibilityPrivate
		}

		metadataJSON, err := marshalMetadata(mem.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}

		_, err = insertStmt.ExecContext(ctx,
			mem.ID, mem.Content, metadataJSON, mem.TeamID,
			mem.EmbeddingID, mem.ParentID, boolToInt(mem.IsLatest), mem.AccessCount,
			mem.CreatedAt, mem.UpdatedAt,
			mem.URI, mem.ContextID, mem.Kind, mem.SubKind, mem.Scope, mem.Abstract, mem.Summary,
			timeToNull(mem.HappenedAt), mem.SourceType, mem.SourceRef, mem.DocumentID, mem.ChunkIndex,
			timeToNull(mem.DeletedAt), mem.Strength, mem.DecayRate, timeToNull(mem.LastAccessedAt),
			mem.ReinforcedCount, timeToNull(mem.ExpiresAt),
			mem.RetentionTier, mem.MessageRole, mem.TurnNumber, mem.ContentHash, mem.ConsolidatedInto,
			mem.OwnerID, mem.Visibility,
		)
		if err != nil {
			return fmt.Errorf("failed to insert memory %s: %w", mem.ID, err)
		}
	}

	// FTS5 同步在事务内，保证原子性 / Sync FTS5 inside transaction for atomicity
	for _, mem := range memories {
		if err := s.syncFTS5Tx(ctx, tx, mem); err != nil {
			logger.Warn("CreateBatch: FTS5 sync failed",
				zap.String("id", mem.ID),
				zap.Error(err),
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit batch insert: %w", err)
	}

	return nil
}

// Get 获取单条记忆（纯读，不修改访问计数）/ Get a memory by ID (read-only, does not update access count)
// 调用方如需记录访问，请显式调用 IncrementAccessCount / Callers should use IncrementAccessCount explicitly
func (s *SQLiteMemoryStore) Get(ctx context.Context, id string) (*model.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE id = ? AND deleted_at IS NULL`

	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	return mem, nil
}

// GetVisible 带可见性校验获取记忆 / Get memory with visibility check
func (s *SQLiteMemoryStore) GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error) {
	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE id = ? AND deleted_at IS NULL AND ` + visCond

	args := append([]interface{}{id}, visArgs...)
	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get visible memory: %w", err)
	}
	return mem, nil
}

// Update 更新记忆 / Update an existing memory
func (s *SQLiteMemoryStore) Update(ctx context.Context, mem *model.Memory) error {
	if mem.ID == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	metadataJSON, err := marshalMetadata(mem.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	mem.UpdatedAt = time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 先读取旧行的 rowid 用于 FTS5 删除
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, mem.ID).Scan(&rowid); err != nil {
		if err == sql.ErrNoRows {
			return model.ErrMemoryNotFound
		}
		return fmt.Errorf("failed to get rowid: %w", err)
	}

	// 删除旧 FTS5 行
	if err := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); err != nil {
		return fmt.Errorf("failed to delete old FTS5 entry: %w", err)
	}

	query := `UPDATE memories SET content = ?, metadata = ?, team_id = ?, embedding_id = ?, parent_id = ?,
		is_latest = ?, updated_at = ?,
		uri = ?, context_id = ?, kind = ?, sub_kind = ?, scope = ?, abstract = ?, summary = ?,
		happened_at = ?, source_type = ?, source_ref = ?, document_id = ?, chunk_index = ?,
		strength = ?, decay_rate = ?, last_accessed_at = ?, reinforced_count = ?, expires_at = ?,
		retention_tier = ?, message_role = ?, turn_number = ?, owner_id = ?, visibility = ?
		WHERE id = ?`

	result, err := tx.ExecContext(ctx, query,
		mem.Content, metadataJSON, mem.TeamID, mem.EmbeddingID, mem.ParentID,
		boolToInt(mem.IsLatest), mem.UpdatedAt,
		mem.URI, mem.ContextID, mem.Kind, mem.SubKind, mem.Scope, mem.Abstract, mem.Summary,
		timeToNull(mem.HappenedAt), mem.SourceType, mem.SourceRef, mem.DocumentID, mem.ChunkIndex,
		mem.Strength, mem.DecayRate, timeToNull(mem.LastAccessedAt), mem.ReinforcedCount, timeToNull(mem.ExpiresAt),
		mem.RetentionTier, mem.MessageRole, mem.TurnNumber, mem.OwnerID, mem.Visibility,
		mem.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	// 插入新 FTS5 行
	if err := s.syncFTS5Tx(ctx, tx, mem); err != nil {
		return fmt.Errorf("failed to sync FTS5 after update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update tx: %w", err)
	}

	return nil
}

// Delete 删除记忆（硬删除）/ Delete a memory by ID (hard delete)
func (s *SQLiteMemoryStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete tx: %w", err)
	}
	defer tx.Rollback()

	// 获取 rowid 用于 FTS5 清理
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, id).Scan(&rowid); err != nil {
		if err == sql.ErrNoRows {
			return model.ErrMemoryNotFound
		}
		return fmt.Errorf("failed to get rowid for delete: %w", err)
	}

	// 在同一事务内删除 FTS5 条目
	if err := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); err != nil {
		return fmt.Errorf("failed to delete FTS5 entry: %w", err)
	}

	// 删除主表记录
	result, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete tx: %w", err)
	}

	return nil
}

// List 分页列表（排除软删除）/ List memories with pagination (exclude soft-deleted)
func (s *SQLiteMemoryStore) List(ctx context.Context, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE deleted_at IS NULL AND ` + visCond + `
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?`

	args := append(visArgs, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// SearchText 全文检索 / Full-text search using FTS5
func (s *SQLiteMemoryStore) SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required: %w", model.ErrInvalidInput)
	}
	if limit <= 0 {
		limit = 10
	}

	// 查询预分词
	tokenizedQuery, err := s.tokenizer.Tokenize(ctx, query)
	if err != nil {
		tokenizedQuery = query // 分词失败回退原文
	}
	// FTS5 语法净化：包裹为短语查询防止操作符注入 / Sanitize for FTS5 syntax injection
	tokenizedQuery = sanitizeFTS5Query(tokenizedQuery)

	visCond, visArgs := visibilityCondition("m.", identity)
	w := s.bm25Weights
	// 用 CTE 消除 bm25() 重复计算 / Use CTE to avoid computing bm25() twice
	sqlQuery := `WITH ranked AS (
		SELECT ` + memoryColumnsAliased + `,
			bm25(memories_fts, ?, ?, ?) AS rank
		FROM memories m
		JOIN memories_fts f ON m.rowid = f.rowid
		WHERE memories_fts MATCH ? AND m.deleted_at IS NULL AND ` + visCond + `
	)
	SELECT * FROM ranked ORDER BY rank LIMIT ?`

	args := []interface{}{w[0], w[1], w[2], tokenizedQuery}
	args = append(args, visArgs...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %w", err)
	}
	defer rows.Close()

	var results []*model.SearchResult
	for rows.Next() {
		mem, rank, err := s.scanMemoryWithRank(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  -rank,
			Source: "sqlite",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate search results: %w", err)
	}

	return results, nil
}

// ListByContext 按上下文列出记忆 / List memories by context ID
func (s *SQLiteMemoryStore) ListByContext(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE context_id = ? AND deleted_at IS NULL AND ` + visCond + `
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?`

	args := append([]interface{}{contextID}, visArgs...)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories by context: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// GetByURI 通过 URI 获取记忆 / Get memory by URI
func (s *SQLiteMemoryStore) GetByURI(ctx context.Context, uri string) (*model.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE uri = ? AND deleted_at IS NULL`
	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, uri))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get memory by URI: %w", err)
	}
	return mem, nil
}

// GetByContentHash 通过内容哈希获取记忆 / Get memory by content hash
func (s *SQLiteMemoryStore) GetByContentHash(ctx context.Context, contentHash string) (*model.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE content_hash = ? AND deleted_at IS NULL`
	mem, err := s.scanMemory(s.db.QueryRowContext(ctx, query, contentHash))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrMemoryNotFound
		}
		return nil, fmt.Errorf("failed to get memory by content hash: %w", err)
	}
	return mem, nil
}

// SearchTextFiltered 带过滤条件的全文检索 / Full-text search with filters
func (s *SQLiteMemoryStore) SearchTextFiltered(ctx context.Context, query string, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required: %w", model.ErrInvalidInput)
	}
	if limit <= 0 {
		limit = 10
	}

	// 查询预分词
	tokenizedQuery, err := s.tokenizer.Tokenize(ctx, query)
	if err != nil {
		tokenizedQuery = query
	}
	// FTS5 语法净化 / Sanitize for FTS5 syntax injection
	tokenizedQuery = sanitizeFTS5Query(tokenizedQuery)

	// 使用 sqlbuilder 构建 WHERE 子句
	wb := sqlbuilder.NewWhere()
	wb.And("memories_fts MATCH ?", tokenizedQuery)
	wb.And("m.deleted_at IS NULL")

	if filters != nil {
		wb.AndIf(filters.Scope != "", "m.scope = ?", filters.Scope)
		wb.AndIf(filters.ContextID != "", "m.context_id = ?", filters.ContextID)
		wb.AndIf(filters.Kind != "", "m.kind = ?", filters.Kind)
		wb.AndIf(filters.SourceType != "", "m.source_type = ?", filters.SourceType)
		wb.AndIf(filters.HappenedAfter != nil, "m.happened_at >= ?", filters.HappenedAfter)
		wb.AndIf(filters.HappenedBefore != nil, "m.happened_at <= ?", filters.HappenedBefore)
		wb.AndIf(filters.MinStrength > 0, "m.strength >= ?", filters.MinStrength)
		if !filters.IncludeExpired {
			wb.And("(m.expires_at IS NULL OR m.expires_at > ?)", time.Now().UTC())
		}
		wb.AndIf(filters.RetentionTier != "", "m.retention_tier = ?", filters.RetentionTier)
		wb.AndIf(filters.MessageRole != "", "m.message_role = ?", filters.MessageRole)

		// 可见性过滤（TeamID/OwnerID 由 API 层注入）/ Visibility filtering (injected by API layer)
		if filters.TeamID != "" || filters.OwnerID != "" {
			identity := &model.Identity{TeamID: filters.TeamID, OwnerID: filters.OwnerID}
			visCond, visArgs := visibilityCondition("m.", identity)
			wb.And(visCond, visArgs...)
		}
	}

	whereClause, whereArgs := wb.Build()
	w := s.bm25Weights

	// 用 CTE 消除 bm25() 重复计算 / Use CTE to avoid computing bm25() twice
	sqlQuery := fmt.Sprintf(`WITH ranked AS (
		SELECT %s,
			bm25(memories_fts, ?, ?, ?) AS rank
		FROM memories m
		JOIN memories_fts f ON m.rowid = f.rowid
		WHERE %s
	)
	SELECT * FROM ranked ORDER BY rank LIMIT ?`, memoryColumnsAliased, whereClause)

	finalArgs := make([]interface{}, 0, len(whereArgs)+4)
	finalArgs = append(finalArgs, w[0], w[1], w[2])
	finalArgs = append(finalArgs, whereArgs...)
	finalArgs = append(finalArgs, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, finalArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories with filters: %w", err)
	}
	defer rows.Close()

	var results []*model.SearchResult
	for rows.Next() {
		mem, rank, err := s.scanMemoryWithRank(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan filtered search result: %w", err)
		}
		results = append(results, &model.SearchResult{
			Memory: mem,
			Score:  -rank,
			Source: "sqlite",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate filtered search results: %w", err)
	}

	return results, nil
}

// ListTimeline 时间线查询 / List memories by timeline
func (s *SQLiteMemoryStore) ListTimeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	qb := sqlbuilder.Select(memoryColumns).
		From("memories").
		OrderBy("COALESCE(happened_at, created_at) DESC").
		Limit(limit)

	qb.Where().And("deleted_at IS NULL")
	qb.Where().AndIf(req.Scope != "", "scope = ?", req.Scope)
	qb.Where().AndIf(req.After != nil, "COALESCE(happened_at, created_at) >= ?", req.After)
	qb.Where().AndIf(req.Before != nil, "COALESCE(happened_at, created_at) <= ?", req.Before)

	// 可见性过滤：使用请求中携带的身份信息 / Apply visibility filter using identity from request
	if req.TeamID != "" || req.OwnerID != "" {
		identity := &model.Identity{TeamID: req.TeamID, OwnerID: req.OwnerID}
		visCond, visArgs := visibilityCondition("", identity)
		qb.Where().And(visCond, visArgs...)
	}

	sqlQuery, args := qb.Build()

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list timeline: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// SoftDelete 软删除记忆 / Soft delete a memory
func (s *SQLiteMemoryStore) SoftDelete(ctx context.Context, id string) error {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin soft delete tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE memories SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return fmt.Errorf("failed to soft delete memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	// 在同一事务内清理 FTS5 索引 / Remove FTS5 index within the same transaction
	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM memories WHERE id = ?`, id).Scan(&rowid); err == nil {
		if ftsErr := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); ftsErr != nil {
			logger.Warn("soft delete: FTS5 cleanup failed", zap.String("id", id), zap.Error(ftsErr))
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit soft delete tx: %w", err)
	}

	return nil
}

// Restore 恢复软删除的记忆 / Restore a soft-deleted memory
func (s *SQLiteMemoryStore) Restore(ctx context.Context, id string) error {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restore tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE memories SET deleted_at = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`, now, id)
	if err != nil {
		return fmt.Errorf("failed to restore memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	// 重建 FTS5 索引（SoftDelete 时已清除）/ Rebuild FTS5 index (cleared during SoftDelete)
	var mem model.Memory
	if err := tx.QueryRowContext(ctx, `SELECT id, content, COALESCE(abstract, ''), COALESCE(summary, '') FROM memories WHERE id = ?`, id).Scan(&mem.ID, &mem.Content, &mem.Abstract, &mem.Summary); err == nil {
		if syncErr := s.syncFTS5Tx(ctx, tx, &mem); syncErr != nil {
			logger.Warn("failed to rebuild FTS5 on restore", zap.String("id", id), zap.Error(syncErr))
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore tx: %w", err)
	}

	return nil
}

// Reinforce 强化记忆 / Reinforce a memory (increase strength)
func (s *SQLiteMemoryStore) Reinforce(ctx context.Context, id string) error {
	now := time.Now().UTC()
	// strength += 0.1 * (1 - strength)
	result, err := s.db.ExecContext(ctx,
		`UPDATE memories SET strength = strength + 0.1 * (1.0 - strength),
		reinforced_count = reinforced_count + 1,
		last_accessed_at = ?,
		updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return fmt.Errorf("failed to reinforce memory: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrMemoryNotFound
	}

	return nil
}

// GetOwnerID 获取记忆的 owner_id（含 soft-deleted）/ Get owner_id including soft-deleted memories
func (s *SQLiteMemoryStore) GetOwnerID(ctx context.Context, id string) (string, error) {
	var ownerID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM memories WHERE id = ?`, id).Scan(&ownerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", model.ErrMemoryNotFound
		}
		return "", fmt.Errorf("failed to get owner_id: %w", err)
	}
	if ownerID.Valid {
		return ownerID.String, nil
	}
	return "", nil
}

// IncrementAccessCount 递增访问计数 / Increment access count by delta
func (s *SQLiteMemoryStore) IncrementAccessCount(ctx context.Context, id string, delta int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE memories SET access_count = access_count + ? WHERE id = ? AND deleted_at IS NULL`,
		delta, id)
	if err != nil {
		return fmt.Errorf("failed to increment access count: %w", err)
	}
	return nil
}

// ListExpired 列出已过期记忆 / List expired memories
func (s *SQLiteMemoryStore) ListExpired(ctx context.Context, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 100
	}

	now := time.Now().UTC()
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL
		ORDER BY expires_at ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list expired memories: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// ListWeak 列出弱记忆 / List weak memories below threshold
func (s *SQLiteMemoryStore) ListWeak(ctx context.Context, threshold float64, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE strength < ? AND deleted_at IS NULL
		ORDER BY strength ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list weak memories: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
}

// ---- 扫描辅助函数 ----

// scanMemory 从单行扫描 Memory 对象（35 列）/ Scan a Memory from a single row (35 columns)
func (s *SQLiteMemoryStore) scanMemory(row *sql.Row) (*model.Memory, error) {
	var (
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
	)

	err := row.Scan(
		&mem.ID, &mem.Content, &metaStr, &mem.TeamID,
		&mem.EmbeddingID, &mem.ParentID, &isLatestInt, &mem.AccessCount,
		&mem.CreatedAt, &mem.UpdatedAt,
		&mem.URI, &mem.ContextID, &mem.Kind, &mem.SubKind, &mem.Scope, &mem.Abstract, &mem.Summary,
		&happenedAt, &mem.SourceType, &mem.SourceRef, &mem.DocumentID, &chunkIndex,
		&deletedAt, &strength, &decayRate, &lastAccessedAt, &reinforcedCount, &expiresAt,
		&retentionTier, &messageRole, &turnNumber, &contentHash, &consolidatedInto,
		&ownerID, &visibility,
	)
	if err != nil {
		return nil, err
	}

	mem.IsLatest = isLatestInt != 0
	if metaStr.Valid {
		if err := json.Unmarshal([]byte(metaStr.String), &mem.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	applyNullables(&mem, happenedAt, deletedAt, lastAccessedAt, expiresAt, strength, decayRate, reinforcedCount, chunkIndex)
	applyV3Nullables(&mem, retentionTier, messageRole, turnNumber)
	if contentHash.Valid {
		mem.ContentHash = contentHash.String
	}
	if consolidatedInto.Valid {
		mem.ConsolidatedInto = consolidatedInto.String
	}
	if ownerID.Valid {
		mem.OwnerID = ownerID.String
	}
	if visibility.Valid {
		mem.Visibility = visibility.String
	}

	return &mem, nil
}

// scanMemoryFromRows 从结果集行扫描 Memory 对象（35 列）/ Scan a Memory from a rows cursor (35 columns)
func (s *SQLiteMemoryStore) scanMemoryFromRows(rows *sql.Rows) (*model.Memory, error) {
	var (
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
	)

	err := rows.Scan(
		&mem.ID, &mem.Content, &metaStr, &mem.TeamID,
		&mem.EmbeddingID, &mem.ParentID, &isLatestInt, &mem.AccessCount,
		&mem.CreatedAt, &mem.UpdatedAt,
		&mem.URI, &mem.ContextID, &mem.Kind, &mem.SubKind, &mem.Scope, &mem.Abstract, &mem.Summary,
		&happenedAt, &mem.SourceType, &mem.SourceRef, &mem.DocumentID, &chunkIndex,
		&deletedAt, &strength, &decayRate, &lastAccessedAt, &reinforcedCount, &expiresAt,
		&retentionTier, &messageRole, &turnNumber, &contentHash, &consolidatedInto,
		&ownerID, &visibility,
	)
	if err != nil {
		return nil, err
	}

	mem.IsLatest = isLatestInt != 0
	if metaStr.Valid {
		if err := json.Unmarshal([]byte(metaStr.String), &mem.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	applyNullables(&mem, happenedAt, deletedAt, lastAccessedAt, expiresAt, strength, decayRate, reinforcedCount, chunkIndex)
	applyV3Nullables(&mem, retentionTier, messageRole, turnNumber)
	if contentHash.Valid {
		mem.ContentHash = contentHash.String
	}
	if consolidatedInto.Valid {
		mem.ConsolidatedInto = consolidatedInto.String
	}
	if ownerID.Valid {
		mem.OwnerID = ownerID.String
	}
	if visibility.Valid {
		mem.Visibility = visibility.String
	}

	return &mem, nil
}

// scanMemoryWithRank 扫描带 rank 列的行（36 列 = 35 + rank）/ Scan a Memory + BM25 rank (36 columns)
func (s *SQLiteMemoryStore) scanMemoryWithRank(rows *sql.Rows) (*model.Memory, float64, error) {
	var (
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
		rank             float64
	)

	err := rows.Scan(
		&mem.ID, &mem.Content, &metaStr, &mem.TeamID,
		&mem.EmbeddingID, &mem.ParentID, &isLatestInt, &mem.AccessCount,
		&mem.CreatedAt, &mem.UpdatedAt,
		&mem.URI, &mem.ContextID, &mem.Kind, &mem.SubKind, &mem.Scope, &mem.Abstract, &mem.Summary,
		&happenedAt, &mem.SourceType, &mem.SourceRef, &mem.DocumentID, &chunkIndex,
		&deletedAt, &strength, &decayRate, &lastAccessedAt, &reinforcedCount, &expiresAt,
		&retentionTier, &messageRole, &turnNumber, &contentHash, &consolidatedInto,
		&ownerID, &visibility,
		&rank,
	)
	if err != nil {
		return nil, 0, err
	}

	mem.IsLatest = isLatestInt != 0
	if metaStr.Valid {
		if err := json.Unmarshal([]byte(metaStr.String), &mem.Metadata); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	applyNullables(&mem, happenedAt, deletedAt, lastAccessedAt, expiresAt, strength, decayRate, reinforcedCount, chunkIndex)
	applyV3Nullables(&mem, retentionTier, messageRole, turnNumber)
	if contentHash.Valid {
		mem.ContentHash = contentHash.String
	}
	if consolidatedInto.Valid {
		mem.ConsolidatedInto = consolidatedInto.String
	}
	if ownerID.Valid {
		mem.OwnerID = ownerID.String
	}
	if visibility.Valid {
		mem.Visibility = visibility.String
	}

	return &mem, rank, nil
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

// CleanupExpired 软删除已过期记忆（单事务，消除 TOCTOU）/ Soft delete expired memories in a single transaction
func (s *SQLiteMemoryStore) CleanupExpired(ctx context.Context) (int, error) {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin cleanup expired tx: %w", err)
	}
	defer tx.Rollback()

	// 在事务内查出即将软删除的行的 rowid / Collect rowids within transaction
	expiredRows, err := tx.QueryContext(ctx,
		`SELECT rowid FROM memories WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`, now)
	if err != nil {
		return 0, fmt.Errorf("failed to query expired rowids: %w", err)
	}
	var rowids []int64
	for expiredRows.Next() {
		var rowid int64
		if err := expiredRows.Scan(&rowid); err != nil {
			expiredRows.Close()
			return 0, fmt.Errorf("failed to scan expired rowid: %w", err)
		}
		rowids = append(rowids, rowid)
	}
	expiredRows.Close()

	// 在事务内清理 FTS5 索引 / Clean FTS5 within transaction
	for _, rowid := range rowids {
		_ = s.deleteFTS5ByRowIDTx(ctx, tx, rowid)
	}

	// 在事务内执行软删除 / Soft delete within transaction
	result, err := tx.ExecContext(ctx,
		`UPDATE memories SET deleted_at = ?, updated_at = ?
		WHERE expires_at IS NOT NULL AND expires_at <= ? AND deleted_at IS NULL`,
		now, now, now)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired memories: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit cleanup expired tx: %w", err)
	}

	return int(rows), nil
}

// PurgeDeleted 硬删除已软删除超过指定时间的记忆 / Hard delete old soft-deleted memories
func (s *SQLiteMemoryStore) PurgeDeleted(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan)

	// 合并两次相同 SELECT：一次查询同时收集 rowid 和 id / Merge two identical SELECTs into one scan
	rows, err := s.db.QueryContext(ctx, `SELECT rowid, id FROM memories WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to query purge candidates: %w", err)
	}
	var rowids []int64
	var memoryIDs []string
	for rows.Next() {
		var rowid int64
		var id string
		if err := rows.Scan(&rowid, &id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("failed to scan purge candidate: %w", err)
		}
		rowids = append(rowids, rowid)
		memoryIDs = append(memoryIDs, id)
	}
	rows.Close()

	// 原子删除：FTS5 + 关联表 + 主记录全在事务内 / Atomic purge: FTS5 + associations + main records in one tx
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin purge tx: %w", err)
	}
	defer tx.Rollback()

	// 在事务内清理 FTS5 条目 / Clean FTS5 within transaction
	for _, rowid := range rowids {
		if ftsErr := s.deleteFTS5ByRowIDTx(ctx, tx, rowid); ftsErr != nil {
			logger.Warn("purge: FTS5 cleanup failed", zap.Int64("rowid", rowid), zap.Error(ftsErr))
		}
	}

	if len(memoryIDs) > 0 {
		placeholders := strings.Repeat("?,", len(memoryIDs))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]interface{}, len(memoryIDs))
		for i, id := range memoryIDs {
			args[i] = id
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_tags WHERE memory_id IN (`+placeholders+`)`, args...); err != nil {
			return 0, fmt.Errorf("failed to delete memory_tags during purge: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_entities WHERE memory_id IN (`+placeholders+`)`, args...); err != nil {
			return 0, fmt.Errorf("failed to delete memory_entities during purge: %w", err)
		}
	}

	// 硬删除
	result, err := tx.ExecContext(ctx,
		`DELETE FROM memories WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to purge deleted memories: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit purge tx: %w", err)
	}

	return int(affected), nil
}

// ListByContextOrdered 按轮次顺序列出上下文记忆 / List memories by context ordered by turn number
func (s *SQLiteMemoryStore) ListByContextOrdered(ctx context.Context, contextID string, identity *model.Identity, offset, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 100
	}

	visCond, visArgs := visibilityCondition("", identity)
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE context_id = ? AND deleted_at IS NULL AND ` + visCond + `
		ORDER BY turn_number ASC, created_at ASC
		LIMIT ? OFFSET ?`

	args := append([]interface{}{contextID}, visArgs...)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories by context ordered: %w", err)
	}
	defer rows.Close()

	return s.scanMemories(rows)
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

// ListMissingAbstract 列出缺少摘要的记忆 / List memories missing abstract
func (s *SQLiteMemoryStore) ListMissingAbstract(ctx context.Context, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE (abstract = '' OR abstract IS NULL) AND deleted_at IS NULL ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list missing abstract: %w", err)
	}
	defer rows.Close()

	var memories []*model.Memory
	for rows.Next() {
		mem, err := s.scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		memories = append(memories, mem)
	}
	return memories, rows.Err()
}
