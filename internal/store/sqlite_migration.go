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
const latestVersion = 7

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
	}

	// V4→V5: 记忆归纳审计字段
	if version < 5 {
		if err := migrateV4ToV5(db); err != nil {
			return fmt.Errorf("migration V4→V5 failed: %w", err)
		}
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

	// 性能索引（幂等，CREATE IF NOT EXISTS）
	if err := migrateAddPerformanceIndexes(db); err != nil {
		return fmt.Errorf("performance indexes migration failed: %w", err)
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

// migrateAddPerformanceIndexes 添加性能索引 / Add performance indexes for common query patterns
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

// isColumnExistsError 检查是否为列已存在错误
func isColumnExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists")
}
