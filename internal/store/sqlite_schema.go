package store

import (
	"database/sql"
	"fmt"

	"iclude/internal/logger"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// createFreshSchema 为新数据库一步创建 V16 终态 schema / Create final V16 schema for new databases in one step
// 等效于 V0→V16 全部迁移的最终结果，但跳过中间步骤
// Equivalent to running all V0→V16 migrations, but skips intermediate steps
func createFreshSchema(db *sql.DB, tok tokenizer.Tokenizer) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin fresh schema: %w", err)
	}
	defer tx.Rollback()

	// --- schema_version 表 / schema_version table ---
	if _, err := tx.Exec(`CREATE TABLE schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("fresh schema: create schema_version: %w", err)
	}

	// --- memories 表 (35 列, V16 终态) / memories table (35 columns, V16 final state) ---
	if _, err := tx.Exec(`CREATE TABLE memories (
		id               TEXT PRIMARY KEY,
		content          TEXT NOT NULL,
		metadata         TEXT,
		team_id          TEXT NOT NULL DEFAULT '',
		parent_id        TEXT DEFAULT '',
		is_latest        INTEGER DEFAULT 1,
		access_count     INTEGER DEFAULT 0,
		created_at       DATETIME NOT NULL,
		updated_at       DATETIME NOT NULL,
		uri              TEXT DEFAULT '',
		context_id       TEXT DEFAULT '',
		kind             TEXT DEFAULT '',
		sub_kind         TEXT DEFAULT '',
		scope            TEXT DEFAULT 'default',
		excerpt          TEXT DEFAULT '',
		summary          TEXT DEFAULT '',
		happened_at      DATETIME,
		source_type      TEXT DEFAULT '',
		source_ref       TEXT DEFAULT '',
		document_id      TEXT DEFAULT '',
		chunk_index      INTEGER DEFAULT 0,
		deleted_at       DATETIME,
		strength         REAL DEFAULT 1.0,
		decay_rate       REAL DEFAULT 0.01,
		last_accessed_at DATETIME,
		reinforced_count INTEGER DEFAULT 0,
		expires_at       DATETIME,
		retention_tier   TEXT DEFAULT 'standard',
		message_role     TEXT DEFAULT '',
		turn_number      INTEGER DEFAULT 0,
		content_hash     TEXT DEFAULT '',
		consolidated_into TEXT DEFAULT '',
		owner_id         TEXT DEFAULT '',
		visibility       TEXT DEFAULT 'private',
		memory_class     TEXT NOT NULL DEFAULT 'episodic'
	)`); err != nil {
		return fmt.Errorf("fresh schema: create memories: %w", err)
	}

	// --- memories_fts 虚拟表 (V14 使用 excerpt 列) / FTS5 virtual table (V14+ uses excerpt column) ---
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(
		content, excerpt, summary,
		content='memories', content_rowid='rowid'
	)`); err != nil {
		return fmt.Errorf("fresh schema: create memories_fts: %w", err)
	}

	// --- contexts 表 (16 列, V13 行为字段 + V14 列重命名) / contexts table (16 columns) ---
	if _, err := tx.Exec(`CREATE TABLE contexts (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		path         TEXT NOT NULL UNIQUE,
		parent_id    TEXT DEFAULT '',
		scope        TEXT DEFAULT '',
		context_type TEXT DEFAULT '',
		description  TEXT DEFAULT '',
		metadata     TEXT,
		depth        INTEGER DEFAULT 0,
		sort_order   INTEGER DEFAULT 0,
		memory_count INTEGER DEFAULT 0,
		created_at   DATETIME NOT NULL,
		updated_at   DATETIME NOT NULL,
		mission      TEXT NOT NULL DEFAULT '',
		directives   TEXT NOT NULL DEFAULT '',
		disposition  TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("fresh schema: create contexts: %w", err)
	}

	// --- tags 表 / tags table ---
	if _, err := tx.Exec(`CREATE TABLE tags (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		scope      TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		UNIQUE(name, scope)
	)`); err != nil {
		return fmt.Errorf("fresh schema: create tags: %w", err)
	}

	// --- memory_tags 关联表 (V15 FK CASCADE) / memory_tags junction table ---
	if _, err := tx.Exec(`CREATE TABLE memory_tags (
		memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		tag_id     TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (memory_id, tag_id)
	)`); err != nil {
		return fmt.Errorf("fresh schema: create memory_tags: %w", err)
	}

	// --- entities 表 / entities table ---
	if _, err := tx.Exec(`CREATE TABLE entities (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		scope       TEXT DEFAULT '',
		description TEXT DEFAULT '',
		metadata    TEXT,
		created_at  DATETIME NOT NULL,
		updated_at  DATETIME NOT NULL,
		UNIQUE(name, entity_type, scope)
	)`); err != nil {
		return fmt.Errorf("fresh schema: create entities: %w", err)
	}

	// --- entity_relations 表 (V15 FK CASCADE + CHECK) / entity_relations table ---
	if _, err := tx.Exec(`CREATE TABLE entity_relations (
		id            TEXT PRIMARY KEY,
		source_id     TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		target_id     TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		relation_type TEXT NOT NULL,
		weight        REAL DEFAULT 1.0 CHECK (weight >= 0),
		metadata      TEXT,
		created_at    DATETIME NOT NULL,
		CHECK (source_id != target_id),
		UNIQUE(source_id, target_id, relation_type)
	)`); err != nil {
		return fmt.Errorf("fresh schema: create entity_relations: %w", err)
	}

	// --- memory_entities 关联表 (V15 FK CASCADE + CHECK) / memory_entities junction table ---
	if _, err := tx.Exec(`CREATE TABLE memory_entities (
		memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		entity_id  TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		role       TEXT DEFAULT '' CHECK (role IN ('', 'subject', 'object', 'mentioned')),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (memory_id, entity_id)
	)`); err != nil {
		return fmt.Errorf("fresh schema: create memory_entities: %w", err)
	}

	// --- documents 表 (14 列, V10 扩展字段) / documents table (14 columns) ---
	if _, err := tx.Exec(`CREATE TABLE documents (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		doc_type     TEXT NOT NULL,
		scope        TEXT DEFAULT '',
		context_id   TEXT DEFAULT '',
		file_path    TEXT DEFAULT '',
		file_size    INTEGER DEFAULT 0,
		content_hash TEXT DEFAULT '',
		status       TEXT DEFAULT 'pending',
		chunk_count  INTEGER DEFAULT 0,
		metadata     TEXT,
		created_at   DATETIME NOT NULL,
		updated_at   DATETIME NOT NULL,
		error_msg    TEXT DEFAULT '',
		stage        TEXT DEFAULT '',
		parser       TEXT DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("fresh schema: create documents: %w", err)
	}

	// --- async_tasks 表 (V8) / async_tasks table ---
	if _, err := tx.Exec(`CREATE TABLE async_tasks (
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
	)`); err != nil {
		return fmt.Errorf("fresh schema: create async_tasks: %w", err)
	}

	// --- memory_derivations 表 (V16) / memory_derivations junction table ---
	if _, err := tx.Exec(`CREATE TABLE memory_derivations (
		source_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		target_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (source_id, target_id)
	)`); err != nil {
		return fmt.Errorf("fresh schema: create memory_derivations: %w", err)
	}

	// --- meta 表 + tokenizer 记录 / meta table + tokenizer record ---
	if _, err := tx.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		return fmt.Errorf("fresh schema: create meta: %w", err)
	}
	tokName := "simple"
	if tok != nil {
		tokName = tok.Name()
	}
	if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('tokenizer', ?)`, tokName); err != nil {
		return fmt.Errorf("fresh schema: insert tokenizer meta: %w", err)
	}

	// ==================== 索引 / Indexes ====================

	// memories 表索引 / memories table indexes
	memIndexes := []string{
		// V1: team_id
		`CREATE INDEX idx_memories_team_id ON memories(team_id)`,
		// V2: scope, context_id, kind, deleted_at, happened_at, expires_at
		`CREATE INDEX idx_memories_scope ON memories(scope)`,
		`CREATE INDEX idx_memories_context_id ON memories(context_id) WHERE context_id != ''`,
		`CREATE INDEX idx_memories_kind ON memories(kind) WHERE kind != ''`,
		`CREATE INDEX idx_memories_deleted_at ON memories(deleted_at) WHERE deleted_at IS NOT NULL`,
		`CREATE INDEX idx_memories_happened_at ON memories(happened_at) WHERE happened_at IS NOT NULL`,
		`CREATE INDEX idx_memories_expires_at ON memories(expires_at) WHERE expires_at IS NOT NULL`,
		// V3: retention_tier, message_role, context+turn
		`CREATE INDEX idx_memories_retention_tier ON memories(retention_tier)`,
		`CREATE INDEX idx_memories_message_role ON memories(message_role) WHERE message_role != ''`,
		`CREATE INDEX idx_memories_context_turn ON memories(context_id, turn_number) WHERE context_id != '' AND turn_number > 0`,
		// V4: content_hash unique
		`CREATE UNIQUE INDEX idx_memories_content_hash_unique ON memories(content_hash) WHERE content_hash != '' AND deleted_at IS NULL`,
		// V6: owner_id, visibility
		`CREATE INDEX idx_memories_owner_id ON memories(owner_id)`,
		`CREATE INDEX idx_memories_visibility ON memories(visibility)`,
		// V9: performance indexes
		`CREATE INDEX idx_memories_strength ON memories(strength) WHERE deleted_at IS NULL`,
		`CREATE INDEX idx_memories_updated_at ON memories(updated_at DESC) WHERE deleted_at IS NULL`,
		`CREATE INDEX idx_memories_scope_kind ON memories(scope, kind) WHERE deleted_at IS NULL`,
		`CREATE INDEX idx_memories_owner_team ON memories(owner_id, team_id) WHERE deleted_at IS NULL`,
		`CREATE INDEX idx_memories_uri ON memories(uri) WHERE uri != '' AND deleted_at IS NULL`,
		`CREATE INDEX idx_memories_document_id ON memories(document_id) WHERE document_id != '' AND deleted_at IS NULL`,
		`CREATE INDEX idx_memories_parent_id ON memories(parent_id) WHERE parent_id != '' AND deleted_at IS NULL`,
		`CREATE INDEX idx_memories_team_vis_owner ON memories(team_id, visibility, owner_id) WHERE deleted_at IS NULL`,
		// V12: memory_class
		`CREATE INDEX idx_memories_memory_class ON memories(memory_class)`,
	}
	for _, idx := range memIndexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("fresh schema: index %q: %w", idx, err)
		}
	}

	// contexts 表索引 / contexts table indexes
	ctxIndexes := []string{
		`CREATE INDEX idx_contexts_path ON contexts(path)`,
		`CREATE INDEX idx_contexts_parent_id ON contexts(parent_id)`,
	}
	for _, idx := range ctxIndexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("fresh schema: index %q: %w", idx, err)
		}
	}

	// entities 表索引 / entities table indexes
	if _, err := tx.Exec(`CREATE INDEX idx_entities_lower_name ON entities(name COLLATE NOCASE)`); err != nil {
		return fmt.Errorf("fresh schema: entities name index: %w", err)
	}

	// entity_relations 表索引 / entity_relations table indexes
	erIndexes := []string{
		`CREATE INDEX idx_entity_relations_source ON entity_relations(source_id)`,
		`CREATE INDEX idx_entity_relations_target ON entity_relations(target_id)`,
	}
	for _, idx := range erIndexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("fresh schema: index %q: %w", idx, err)
		}
	}

	// memory_entities 表索引 / memory_entities table indexes
	meIndexes := []string{
		`CREATE INDEX idx_memory_entities_entity_id ON memory_entities(entity_id)`,
		`CREATE INDEX idx_memory_entities_memory_id ON memory_entities(memory_id)`,
	}
	for _, idx := range meIndexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("fresh schema: index %q: %w", idx, err)
		}
	}

	// memory_tags 表索引 / memory_tags table indexes
	if _, err := tx.Exec(`CREATE INDEX idx_memory_tags_tag_id ON memory_tags(tag_id)`); err != nil {
		return fmt.Errorf("fresh schema: memory_tags tag_id index: %w", err)
	}

	// documents 表索引 / documents table indexes
	docIndexes := []string{
		`CREATE INDEX idx_documents_status ON documents(status) WHERE status IN ('pending', 'processing')`,
		`CREATE UNIQUE INDEX idx_documents_content_hash_unique ON documents(content_hash) WHERE content_hash != ''`,
		`CREATE INDEX idx_documents_scope ON documents(scope) WHERE scope != ''`,
		`CREATE INDEX idx_documents_status_created ON documents(status, created_at)`,
	}
	for _, idx := range docIndexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("fresh schema: index %q: %w", idx, err)
		}
	}

	// async_tasks 表索引 / async_tasks table indexes
	taskIndexes := []string{
		`CREATE INDEX idx_async_tasks_status_created ON async_tasks(status, created_at)`,
		`CREATE INDEX idx_async_tasks_scheduled_at ON async_tasks(scheduled_at) WHERE scheduled_at IS NOT NULL`,
	}
	for _, idx := range taskIndexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("fresh schema: index %q: %w", idx, err)
		}
	}

	// memory_derivations 表索引 / memory_derivations table indexes
	if _, err := tx.Exec(`CREATE INDEX idx_memory_derivations_target ON memory_derivations(target_id)`); err != nil {
		return fmt.Errorf("fresh schema: memory_derivations target index: %w", err)
	}

	// --- 记录 schema 版本 = 16 / Record schema version = 16 ---
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (16)`); err != nil {
		return fmt.Errorf("fresh schema: record version: %w", err)
	}

	logger.Info("fresh schema V16 created successfully",
		zap.String("tokenizer", tokName),
	)

	return tx.Commit()
}
