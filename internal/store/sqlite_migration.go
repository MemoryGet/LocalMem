package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"iclude/internal/logger"
	"iclude/pkg/hashutil"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// 当前最新 schema 版本
const latestVersion = 12

// getCurrentVersion 获取当前 schema 版本 / Get current schema version
func getCurrentVersion(db *sql.DB) (int, error) {
	// 检查 schema_version 表是否存在
	var count int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to check schema_version table: %w", err)
	}
	if count == 0 {
		return 0, nil
	}

	var version int
	err = db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}
	return version, nil
}

// Migrate 执行数据库迁移 / Execute database migrations
// tok 用于 V4+ FTS5 重建时的分词；V3 以下迁移不需要
func Migrate(db *sql.DB, tok tokenizer.Tokenizer) error {
	version, err := getCurrentVersion(db)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	logger.Info("database migration check",
		zap.Int("current_version", version),
		zap.Int("latest_version", latestVersion),
	)

	if version >= latestVersion {
		return nil
	}

	// V0→V1: 初始建表
	if version < 1 {
		if err := migrateV0ToV1(db); err != nil {
			return fmt.Errorf("migration V0→V1 failed: %w", err)
		}
		version = 1
	}

	// V1→V2: 分层扩展
	if version < 2 {
		if err := migrateV1ToV2(db); err != nil {
			return fmt.Errorf("migration V1→V2 failed: %w", err)
		}
		version = 2
	}

	// V2→V3: 知识分级 + LLM Agent 兼容
	if version < 3 {
		if err := migrateV2ToV3(db); err != nil {
			return fmt.Errorf("migration V2→V3 failed: %w", err)
		}
		version = 3
	}

	// V3→V4: 内容哈希去重 + meta 表 + FTS5 重建（gse 分词）
	if version < 4 {
		if err := migrateV3ToV4(db, tok); err != nil {
			return fmt.Errorf("migration V3→V4 failed: %w", err)
		}
		version = 4
	}

	// V4→V5: 记忆归纳审计字段
	if version < 5 {
		if err := migrateV4ToV5(db); err != nil {
			return fmt.Errorf("migration V4→V5 failed: %w", err)
		}
		version = 5
	}

	// V5→V6: 身份与归属 / Identity & Ownership
	if version < 6 {
		if err := migrateV5ToV6(db); err != nil {
			return fmt.Errorf("migration V5→V6 failed: %w", err)
		}
		version = 6
	}

	// V6→V7: 图谱关联表索引 / Graph and memory-entity association table indexes
	if version < 7 {
		if err := migrateV6ToV7(db); err != nil {
			return fmt.Errorf("migrate V6→V7: %w", err)
		}
		version = 7
	}

	// V7→V8: 异步任务队列表 / Async task queue table
	if version < 8 {
		if err := migrateV7ToV8(db); err != nil {
			return fmt.Errorf("migrate V7→V8: %w", err)
		}
		version = 8
	}

	// V8→V9: 性能索引 + 实体名索引 + 缺失列索引 + 复合索引 / Performance, entity name, missing column, and composite indexes
	if version < 9 {
		if err := migrateV8ToV9(db); err != nil {
			return fmt.Errorf("migrate V8→V9: %w", err)
		}
		version = 9
	}

	// V9→V10: 文档扩展字段 / Document extension fields (error_msg, stage, parser)
	if version < 10 {
		if err := migrateV9ToV10(db); err != nil {
			return fmt.Errorf("V9→V10 migration failed: %w", err)
		}
	}

	// V10→V11: memory_tags 反向索引 + ConnMaxIdleTime 提示 / memory_tags reverse index
	if version < 11 {
		if err := migrateV10ToV11(db); err != nil {
			return fmt.Errorf("V10→V11 migration failed: %w", err)
		}
	}

	// V11→V12: 记忆演化层级 / Memory evolution layer (memory_class + derived_from)
	if version < 12 {
		if err := migrateV11ToV12(db); err != nil {
			return fmt.Errorf("V11→V12 migration failed: %w", err)
		}
	}

	return nil
}

// migrateV0ToV1 初始建表（全新数据库）
func migrateV0ToV1(db *sql.DB) error {
	logger.Info("executing migration V0→V1")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			metadata TEXT,
			team_id TEXT DEFAULT '',
			embedding_id TEXT DEFAULT '',
			parent_id TEXT DEFAULT '',
			is_latest INTEGER DEFAULT 1,
			access_count INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_team_id ON memories(team_id)`,
		// V1 使用触发器模式的 FTS5
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(content, content=memories, content_rowid=rowid)`,
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, content) VALUES (new.rowid, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.rowid, old.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.rowid, old.content);
			INSERT INTO memories_fts(rowid, content) VALUES (new.rowid, new.content);
		END`,
		// 创建 schema_version 表
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT OR IGNORE INTO schema_version (version) VALUES (1)`,
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to execute statement: %w", err)
		}
	}

	return tx.Commit()
}

// migrateV1ToV2 分层扩展迁移
func migrateV1ToV2(db *sql.DB) error {
	logger.Info("executing migration V1→V2")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 确保 schema_version 表存在（从旧版升级时可能没有）
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("failed to create schema_version table: %w", err)
	}

	// 逐列 ALTER TABLE memories ADD COLUMN（SQLite 不支持多列 ALTER）
	alterColumns := []string{
		`ALTER TABLE memories ADD COLUMN uri TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN context_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN kind TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN sub_kind TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN scope TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN abstract TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN summary TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN happened_at DATETIME`,
		`ALTER TABLE memories ADD COLUMN source_type TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN source_ref TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN document_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN chunk_index INTEGER DEFAULT 0`,
		`ALTER TABLE memories ADD COLUMN deleted_at DATETIME`,
		`ALTER TABLE memories ADD COLUMN strength REAL DEFAULT 1.0`,
		`ALTER TABLE memories ADD COLUMN decay_rate REAL DEFAULT 0.01`,
		`ALTER TABLE memories ADD COLUMN last_accessed_at DATETIME`,
		`ALTER TABLE memories ADD COLUMN reinforced_count INTEGER DEFAULT 0`,
		`ALTER TABLE memories ADD COLUMN expires_at DATETIME`,
	}

	for _, stmt := range alterColumns {
		// 忽略 "duplicate column" 错误（幂等）
		if _, err := tx.Exec(stmt); err != nil {
			// SQLite 错误信息包含 "duplicate column name"
			if isColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to alter table: %w", err)
		}
	}

	// 创建 contexts 表
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS contexts (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		path TEXT NOT NULL UNIQUE,
		parent_id TEXT DEFAULT '',
		scope TEXT DEFAULT '',
		kind TEXT DEFAULT '',
		description TEXT DEFAULT '',
		metadata TEXT,
		depth INTEGER DEFAULT 0,
		sort_order INTEGER DEFAULT 0,
		memory_count INTEGER DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("failed to create contexts table: %w", err)
	}

	// 创建 tags 表
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		scope TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		UNIQUE(name, scope)
	)`); err != nil {
		return fmt.Errorf("failed to create tags table: %w", err)
	}

	// 创建 memory_tags 关联表
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS memory_tags (
		memory_id TEXT NOT NULL,
		tag_id TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (memory_id, tag_id)
	)`); err != nil {
		return fmt.Errorf("failed to create memory_tags table: %w", err)
	}

	// 创建 entities 表
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS entities (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		scope TEXT DEFAULT '',
		description TEXT DEFAULT '',
		metadata TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		UNIQUE(name, entity_type, scope)
	)`); err != nil {
		return fmt.Errorf("failed to create entities table: %w", err)
	}

	// 创建 entity_relations 表
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS entity_relations (
		id TEXT PRIMARY KEY,
		source_id TEXT NOT NULL,
		target_id TEXT NOT NULL,
		relation_type TEXT NOT NULL,
		weight REAL DEFAULT 1.0,
		metadata TEXT,
		created_at DATETIME NOT NULL,
		UNIQUE(source_id, target_id, relation_type)
	)`); err != nil {
		return fmt.Errorf("failed to create entity_relations table: %w", err)
	}

	// 创建 memory_entities 关联表
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS memory_entities (
		memory_id TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		role TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (memory_id, entity_id)
	)`); err != nil {
		return fmt.Errorf("failed to create memory_entities table: %w", err)
	}

	// 创建 documents 表
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		doc_type TEXT NOT NULL,
		scope TEXT DEFAULT '',
		context_id TEXT DEFAULT '',
		file_path TEXT DEFAULT '',
		file_size INTEGER DEFAULT 0,
		content_hash TEXT DEFAULT '',
		status TEXT DEFAULT 'pending',
		chunk_count INTEGER DEFAULT 0,
		metadata TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("failed to create documents table: %w", err)
	}

	// 删除旧 FTS5 触发器
	for _, trigger := range []string{"memories_ai", "memories_ad", "memories_au"} {
		if _, err := tx.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s`, trigger)); err != nil {
			return fmt.Errorf("failed to drop trigger %s: %w", trigger, err)
		}
	}

	// 删除旧 FTS5 虚拟表
	if _, err := tx.Exec(`DROP TABLE IF EXISTS memories_fts`); err != nil {
		return fmt.Errorf("failed to drop old FTS5 table: %w", err)
	}

	// 重建 FTS5（external content 模式，3列，无触发器）
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(
		content, abstract, summary,
		content=memories, content_rowid=rowid
	)`); err != nil {
		return fmt.Errorf("failed to create new FTS5 table: %w", err)
	}

	// 重新填充 FTS5
	if _, err := tx.Exec(`INSERT INTO memories_fts(rowid, content, abstract, summary)
		SELECT rowid, content, COALESCE(abstract, ''), COALESCE(summary, '') FROM memories`); err != nil {
		return fmt.Errorf("failed to populate FTS5: %w", err)
	}

	// 创建新索引
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_scope ON memories(scope)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_context_id ON memories(context_id) WHERE context_id != ''`,
		`CREATE INDEX IF NOT EXISTS idx_memories_kind ON memories(kind) WHERE kind != ''`,
		`CREATE INDEX IF NOT EXISTS idx_memories_deleted_at ON memories(deleted_at) WHERE deleted_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_happened_at ON memories(happened_at) WHERE happened_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_expires_at ON memories(expires_at) WHERE expires_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_contexts_path ON contexts(path)`,
		`CREATE INDEX IF NOT EXISTS idx_contexts_parent_id ON contexts(parent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(status) WHERE status IN ('pending', 'processing')`,
		`CREATE INDEX IF NOT EXISTS idx_documents_content_hash ON documents(content_hash) WHERE content_hash != ''`,
	}
	for _, stmt := range indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// 回填默认值
	if _, err := tx.Exec(`UPDATE memories SET scope='default', strength=1.0, decay_rate=0.01 WHERE scope IS NULL OR scope=''`); err != nil {
		return fmt.Errorf("failed to backfill defaults: %w", err)
	}

	// 记录 schema_version
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version) VALUES (2)`); err != nil {
		return fmt.Errorf("failed to record schema version: %w", err)
	}

	logger.Info("migration V1→V2 completed successfully")
	return tx.Commit()
}

// migrateV2ToV3 知识分级 + LLM Agent 兼容
func migrateV2ToV3(db *sql.DB) error {
	logger.Info("executing migration V2→V3")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 新增 3 列
	alterColumns := []string{
		`ALTER TABLE memories ADD COLUMN retention_tier TEXT DEFAULT 'standard'`,
		`ALTER TABLE memories ADD COLUMN message_role TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN turn_number INTEGER DEFAULT 0`,
	}
	for _, stmt := range alterColumns {
		if _, err := tx.Exec(stmt); err != nil {
			if isColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to alter table: %w", err)
		}
	}

	// 新增索引
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_retention_tier ON memories(retention_tier)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_message_role ON memories(message_role) WHERE message_role != ''`,
		`CREATE INDEX IF NOT EXISTS idx_memories_context_turn ON memories(context_id, turn_number) WHERE context_id != '' AND turn_number > 0`,
	}
	for _, stmt := range indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// 回填：decay_rate=0 的设为 permanent，其余确保为 standard
	if _, err := tx.Exec(`UPDATE memories SET retention_tier = 'permanent' WHERE decay_rate = 0`); err != nil {
		return fmt.Errorf("failed to backfill permanent tier: %w", err)
	}
	if _, err := tx.Exec(`UPDATE memories SET retention_tier = 'standard' WHERE retention_tier IS NULL OR retention_tier = ''`); err != nil {
		return fmt.Errorf("failed to backfill standard tier: %w", err)
	}

	// 记录 schema_version
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version) VALUES (3)`); err != nil {
		return fmt.Errorf("failed to record schema version: %w", err)
	}

	logger.Info("migration V2→V3 completed successfully")
	return tx.Commit()
}

// migrateV3ToV4 内容哈希去重 + meta 表 + FTS5 重建
func migrateV3ToV4(db *sql.DB, tok tokenizer.Tokenizer) error {
	logger.Info("executing migration V3→V4")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. 新增 content_hash 列
	if _, err := tx.Exec(`ALTER TABLE memories ADD COLUMN content_hash TEXT DEFAULT ''`); err != nil {
		if !isColumnExistsError(err) {
			return fmt.Errorf("failed to add content_hash column: %w", err)
		}
	}

	// 2. content_hash 部分唯一索引
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_content_hash_unique ON memories(content_hash) WHERE content_hash != '' AND deleted_at IS NULL`); err != nil {
		return fmt.Errorf("failed to create content_hash unique index: %w", err)
	}

	// 3. meta 表，记录 tokenizer 名称
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		return fmt.Errorf("failed to create meta table: %w", err)
	}
	tokName := "simple"
	if tok != nil {
		tokName = tok.Name()
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('tokenizer', ?)`, tokName); err != nil {
		return fmt.Errorf("failed to record tokenizer: %w", err)
	}

	// 4. 重建 FTS5（用新 tokenizer 全量重新分词）
	if _, err := tx.Exec(`DROP TABLE IF EXISTS memories_fts`); err != nil {
		return fmt.Errorf("failed to drop old FTS5 table: %w", err)
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(
		content, abstract, summary,
		content=memories, content_rowid=rowid
	)`); err != nil {
		return fmt.Errorf("failed to create new FTS5 table: %w", err)
	}

	// 全量重新分词插入
	ctx := context.Background()
	rows, err := tx.Query(`SELECT rowid, content, COALESCE(abstract,''), COALESCE(summary,'') FROM memories WHERE deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("failed to query memories for FTS5 rebuild: %w", err)
	}
	defer rows.Close()

	ftsCount := 0
	for rows.Next() {
		var rowid int64
		var content, abstract, summary string
		if err := rows.Scan(&rowid, &content, &abstract, &summary); err != nil {
			return fmt.Errorf("failed to scan memory row: %w", err)
		}
		tc, ta, ts := content, abstract, summary
		if tok != nil {
			tc, _ = tok.Tokenize(ctx, content)
			ta, _ = tok.Tokenize(ctx, abstract)
			ts, _ = tok.Tokenize(ctx, summary)
		}
		if _, err := tx.Exec(`INSERT INTO memories_fts(rowid, content, abstract, summary) VALUES(?,?,?,?)`,
			rowid, tc, ta, ts); err != nil {
			return fmt.Errorf("failed to insert FTS5 row (rowid=%d): %w", rowid, err)
		}
		ftsCount++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("FTS5 rebuild rows iteration error: %w", err)
	}
	logger.Info("FTS5 rebuild completed", zap.Int("memories", ftsCount))

	// 5. 回填 content_hash
	hashRows, err := tx.Query(`SELECT id, content FROM memories WHERE (content_hash IS NULL OR content_hash = '') AND deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("failed to query for hash backfill: %w", err)
	}
	defer hashRows.Close()

	hashStmt, err := tx.Prepare(`UPDATE memories SET content_hash = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("failed to prepare hash update: %w", err)
	}
	defer hashStmt.Close()

	hashCount := 0
	for hashRows.Next() {
		var id, content string
		if err := hashRows.Scan(&id, &content); err != nil {
			return fmt.Errorf("failed to scan for hash backfill: %w", err)
		}
		hash := hashutil.ContentHash(content)
		if _, err := hashStmt.Exec(hash, id); err != nil {
			// 哈希冲突（重复内容）跳过，不中断迁移
			if strings.Contains(err.Error(), "UNIQUE constraint") {
				logger.Warn("hash backfill skipped duplicate", zap.String("id", id))
				continue
			}
			return fmt.Errorf("failed to update hash for %s: %w", id, err)
		}
		hashCount++
	}
	if err := hashRows.Err(); err != nil {
		return fmt.Errorf("hash backfill rows iteration error: %w", err)
	}
	logger.Info("content_hash backfill completed", zap.Int("memories", hashCount))

	// 6. 记录 schema_version
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version) VALUES (4)`); err != nil {
		return fmt.Errorf("failed to record schema version: %w", err)
	}

	logger.Info("migration V3→V4 completed successfully")
	return tx.Commit()
}

// migrateV4ToV5 记忆归纳审计字段
func migrateV4ToV5(db *sql.DB) error {
	logger.Info("executing migration V4→V5")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 新增 consolidated_into 列
	if _, err := tx.Exec(`ALTER TABLE memories ADD COLUMN consolidated_into TEXT DEFAULT ''`); err != nil {
		if !isColumnExistsError(err) {
			return fmt.Errorf("failed to add consolidated_into column: %w", err)
		}
	}

	// 记录 schema_version
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version) VALUES (5)`); err != nil {
		return fmt.Errorf("failed to record schema version: %w", err)
	}

	logger.Info("migration V4→V5 completed successfully")
	return tx.Commit()
}

// migrateV5ToV6 身份与归属字段 / Identity & Ownership fields
func migrateV5ToV6(db *sql.DB) error {
	logger.Info("executing migration V5→V6")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 新增 owner_id 和 visibility 列 / Add owner_id and visibility columns
	alterColumns := []string{
		`ALTER TABLE memories ADD COLUMN owner_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN visibility TEXT DEFAULT 'private'`,
	}
	for _, stmt := range alterColumns {
		if _, err := tx.Exec(stmt); err != nil {
			if isColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to alter table: %w", err)
		}
	}

	// 索引 / Indexes
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_owner_id ON memories(owner_id)`); err != nil {
		return fmt.Errorf("failed to create owner_id index: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_visibility ON memories(visibility)`); err != nil {
		return fmt.Errorf("failed to create visibility index: %w", err)
	}

	// 老数据迁移：visibility 设为 team，空 team_id 回填 default
	// Backfill old data: set visibility to team, set empty team_id to default
	if _, err := tx.Exec(`UPDATE memories SET visibility = 'team' WHERE owner_id = ''`); err != nil {
		return fmt.Errorf("failed to backfill visibility: %w", err)
	}
	if _, err := tx.Exec(`UPDATE memories SET team_id = 'default' WHERE team_id = ''`); err != nil {
		return fmt.Errorf("failed to backfill team_id: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_version (version) VALUES (6)`); err != nil {
		return fmt.Errorf("failed to update schema version: %w", err)
	}

	logger.Info("migration V5→V6 completed successfully")
	return tx.Commit()
}

// migrateV6ToV7 图谱关联表索引 / Graph and memory-entity association table indexes
func migrateV6ToV7(db *sql.DB) error {
	logger.Info("executing migration V6→V7")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin V6→V7: %w", err)
	}
	defer tx.Rollback()

	// 按表分组，仅当表存在时才创建对应索引 / Only create indexes when the table exists
	tableIndexes := []struct {
		table string
		stmts []string
	}{
		{
			table: "entity_relations",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_entity_relations_source ON entity_relations(source_id)`,
				`CREATE INDEX IF NOT EXISTS idx_entity_relations_target ON entity_relations(target_id)`,
			},
		},
		{
			table: "memory_entities",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_memory_entities_entity_id ON memory_entities(entity_id)`,
				`CREATE INDEX IF NOT EXISTS idx_memory_entities_memory_id ON memory_entities(memory_id)`,
			},
		},
	}

	for _, ti := range tableIndexes {
		var cnt int
		if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", ti.table).Scan(&cnt); err != nil {
			return fmt.Errorf("V6→V7 check table %s: %w", ti.table, err)
		}
		if cnt == 0 {
			logger.Info("migration V6→V7: skip indexes for missing table", zap.String("table", ti.table))
			continue
		}
		for _, stmt := range ti.stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("V6→V7 exec %q: %w", stmt, err)
			}
		}
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_version(version) VALUES(7)`); err != nil {
		return fmt.Errorf("V6→V7 schema_version: %w", err)
	}

	logger.Info("migration V6→V7 completed successfully")
	return tx.Commit()
}

// migrateV7ToV8 创建异步任务队列表 / Create async task queue table
func migrateV7ToV8(db *sql.DB) error {
	logger.Info("executing migration V7→V8")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin V7→V8: %w", err)
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS async_tasks (
			id           TEXT PRIMARY KEY,
			type         TEXT NOT NULL,
			payload      TEXT NOT NULL DEFAULT '{}',
			status       TEXT NOT NULL DEFAULT 'pending',
			retry_count  INTEGER NOT NULL DEFAULT 0,
			max_retries  INTEGER NOT NULL DEFAULT 3,
			error_msg    TEXT NOT NULL DEFAULT '',
			created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at   DATETIME NOT NULL DEFAULT (datetime('now')),
			scheduled_at DATETIME,
			completed_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_async_tasks_status_created ON async_tasks(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_async_tasks_scheduled_at ON async_tasks(scheduled_at) WHERE scheduled_at IS NOT NULL`,
		`INSERT OR IGNORE INTO schema_version(version) VALUES(8)`,
	}

	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("V7→V8 exec %q: %w", stmt, err)
		}
	}

	logger.Info("migration V7→V8 completed successfully")
	return tx.Commit()
}

// migrateV8ToV9 性能索引版本化 + 实体名索引 + 缺失列索引 + 复合索引 / Versioned performance indexes + entity name + missing column + composite indexes
func migrateV8ToV9(db *sql.DB) error {
	logger.Info("executing migration V8→V9")
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin V8→V9 tx: %w", err)
	}
	defer tx.Rollback()

	// memories 表索引（必须成功）/ Memory table indexes (must succeed)
	memIndexes := []string{
		// 原有性能索引（从 migrateAddPerformanceIndexes 迁入）
		`CREATE INDEX IF NOT EXISTS idx_memories_strength ON memories(strength) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at DESC) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_scope_kind ON memories(scope, kind) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_owner_team ON memories(owner_id, team_id) WHERE deleted_at IS NULL`,
		// 缺失列索引 / Missing column indexes
		`CREATE INDEX IF NOT EXISTS idx_memories_uri ON memories(uri) WHERE uri != '' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_document_id ON memories(document_id) WHERE document_id != '' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_parent_id ON memories(parent_id) WHERE parent_id != '' AND deleted_at IS NULL`,
		// 多租户复合索引（优化 visibilityCondition OR 路径）/ Multi-tenant composite index
		`CREATE INDEX IF NOT EXISTS idx_memories_team_vis_owner ON memories(team_id, visibility, owner_id) WHERE deleted_at IS NULL`,
	}
	for _, idx := range memIndexes {
		if _, err := tx.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("V8→V9 index creation failed: %w", err)
		}
	}

	// entities 表索引（表可能不存在，best-effort）/ Entity table index (table may not exist)
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_entities_lower_name ON entities(name COLLATE NOCASE)`); err != nil {
		logger.Warn("V8→V9: entities index skipped (table may not exist)", zap.Error(err))
	}

	// 写入版本号
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (9, datetime('now'))`); err != nil {
		return fmt.Errorf("V8→V9 version write failed: %w", err)
	}

	logger.Info("migration V8→V9 completed successfully")
	return tx.Commit()
}

// migrateAddPerformanceIndexes 添加性能索引（已纳入 V9，保留向后兼容）/ Add performance indexes (now part of V9, kept for backward compat)
func migrateAddPerformanceIndexes(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin performance index tx: %w", err)
	}
	defer tx.Rollback()

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_strength ON memories(strength) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at DESC) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_scope_kind ON memories(scope, kind) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_memories_owner_team ON memories(owner_id, team_id) WHERE deleted_at IS NULL`,
	}
	for _, idx := range indexes {
		if _, err := tx.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit performance index tx: %w", err)
	}
	return nil
}

// migrateV9ToV10 文档扩展字段（documents 表可能不存在）/ Document extension fields (table may not exist)
func migrateV9ToV10(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// documents 表可能不存在（旧库未启用文档功能）/ Table may not exist in older databases
	alterStmts := []string{
		`ALTER TABLE documents ADD COLUMN error_msg TEXT DEFAULT ''`,
		`ALTER TABLE documents ADD COLUMN stage TEXT DEFAULT ''`,
		`ALTER TABLE documents ADD COLUMN parser TEXT DEFAULT ''`,
	}
	for _, stmt := range alterStmts {
		if _, err := tx.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "no such table") || isColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to execute %q: %w", stmt, err)
		}
	}

	// Add UNIQUE index on documents.content_hash (replaces non-unique index from V1) / 为 documents.content_hash 添加唯一索引
	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_documents_content_hash`); err != nil {
		// 索引可能不存在或文档表不存在，忽略错误 / Index may not exist or documents table may not exist, ignore error
		logger.Debug("V9→V10: drop non-unique index skipped", zap.Error(err))
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_content_hash_unique ON documents(content_hash) WHERE content_hash != ''`); err != nil {
		// documents 表可能不存在，忽略此错误 / Table may not exist in older databases, ignore
		logger.Debug("V9→V10: create unique index skipped (table may not exist)", zap.Error(err))
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (10, datetime('now'))`); err != nil {
		return fmt.Errorf("V9→V10 version write failed: %w", err)
	}

	logger.Info("migration V9→V10 completed: document extension fields + unique content_hash index")
	return tx.Commit()
}

// migrateV10ToV11 memory_tags 反向索引 + 文档索引 / memory_tags reverse index + document indexes
func migrateV10ToV11(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 按表分组，仅当表存在时才创建对应索引 / Only create indexes when the table exists
	tableIndexes := []struct {
		table string
		stmts []string
	}{
		{
			table: "memory_tags",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_memory_tags_tag_id ON memory_tags(tag_id)`,
			},
		},
		{
			table: "documents",
			stmts: []string{
				`CREATE INDEX IF NOT EXISTS idx_documents_scope ON documents(scope) WHERE scope != ''`,
				`CREATE INDEX IF NOT EXISTS idx_documents_status_created ON documents(status, created_at)`,
			},
		},
	}

	for _, ti := range tableIndexes {
		var cnt int
		if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", ti.table).Scan(&cnt); err != nil {
			return fmt.Errorf("V10→V11 check table %s: %w", ti.table, err)
		}
		if cnt == 0 {
			logger.Info("migration V10→V11: skip indexes for missing table", zap.String("table", ti.table))
			continue
		}
		for _, stmt := range ti.stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("failed to execute %q: %w", stmt, err)
			}
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (11, datetime('now'))`); err != nil {
		return fmt.Errorf("V10→V11 schema_version: %w", err)
	}

	logger.Info("migration V10→V11 completed: memory_tags reverse index + document indexes")
	return tx.Commit()
}

// migrateV11ToV12 记忆演化层级 / Memory evolution layer (memory_class + derived_from)
func migrateV11ToV12(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 新增列（幂等）/ Add columns idempotently
	alterColumns := []string{
		`ALTER TABLE memories ADD COLUMN memory_class TEXT NOT NULL DEFAULT 'episodic'`,
		`ALTER TABLE memories ADD COLUMN derived_from TEXT`,
	}
	for _, stmt := range alterColumns {
		if _, err := tx.Exec(stmt); err != nil {
			if isColumnExistsError(err) {
				continue
			}
			return fmt.Errorf("failed to alter table: %w", err)
		}
	}

	// 根据 kind 回填 memory_class / Backfill memory_class based on kind
	if _, err := tx.Exec(`UPDATE memories SET memory_class = 'procedural' WHERE kind = 'mental_model' AND memory_class = 'episodic'`); err != nil {
		return fmt.Errorf("failed to backfill procedural: %w", err)
	}
	if _, err := tx.Exec(`UPDATE memories SET memory_class = 'semantic' WHERE kind = 'consolidated' AND memory_class = 'episodic'`); err != nil {
		return fmt.Errorf("failed to backfill semantic: %w", err)
	}

	// 索引 / Index
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_memory_class ON memories(memory_class)`); err != nil {
		return fmt.Errorf("failed to create memory_class index: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (12, datetime('now'))`); err != nil {
		return fmt.Errorf("V11→V12 schema_version: %w", err)
	}

	logger.Info("migration V11→V12 completed: memory_class + derived_from columns")
	return tx.Commit()
}

// isColumnExistsError 检查是否为列已存在错误
func isColumnExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists")
}
