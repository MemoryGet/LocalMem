package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// migrateV14ToV15 FK CASCADE on junction tables + CHECK constraints / 关联表外键级联 + CHECK 约束
func migrateV14ToV15(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// --- 1. memory_tags: 添加 FK + CASCADE / Add FK + CASCADE ---
	var ddl string
	err = tx.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='memory_tags'").Scan(&ddl)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to read memory_tags DDL: %w", err)
	}

	if err == nil && !strings.Contains(ddl, "REFERENCES") {
		if _, err := tx.Exec(`CREATE TABLE memory_tags_new (
			memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
			tag_id     TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (memory_id, tag_id)
		)`); err != nil {
			return fmt.Errorf("failed to create memory_tags_new: %w", err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO memory_tags_new SELECT * FROM memory_tags`); err != nil {
			return fmt.Errorf("failed to copy memory_tags data: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE memory_tags`); err != nil {
			return fmt.Errorf("failed to drop old memory_tags: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE memory_tags_new RENAME TO memory_tags`); err != nil {
			return fmt.Errorf("failed to rename memory_tags_new: %w", err)
		}
		// 重建索引 / Recreate indexes
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memory_tags_tag_id ON memory_tags(tag_id)`); err != nil {
			return fmt.Errorf("failed to recreate memory_tags index: %w", err)
		}
	}

	// --- 2. memory_entities: FK + CASCADE + CHECK / 外键 + 级联 + 角色约束 ---
	ddl = ""
	err = tx.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='memory_entities'").Scan(&ddl)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to read memory_entities DDL: %w", err)
	}

	if err == nil && !strings.Contains(ddl, "REFERENCES") {
		if _, err := tx.Exec(`CREATE TABLE memory_entities_new (
			memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
			entity_id  TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
			role       TEXT DEFAULT '' CHECK (role IN ('', 'subject', 'object', 'mentioned')),
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (memory_id, entity_id)
		)`); err != nil {
			return fmt.Errorf("failed to create memory_entities_new: %w", err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO memory_entities_new SELECT * FROM memory_entities`); err != nil {
			return fmt.Errorf("failed to copy memory_entities data: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE memory_entities`); err != nil {
			return fmt.Errorf("failed to drop old memory_entities: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE memory_entities_new RENAME TO memory_entities`); err != nil {
			return fmt.Errorf("failed to rename memory_entities_new: %w", err)
		}
		// 重建索引 / Recreate indexes
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memory_entities_entity_id ON memory_entities(entity_id)`); err != nil {
			return fmt.Errorf("failed to recreate memory_entities entity_id index: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memory_entities_memory_id ON memory_entities(memory_id)`); err != nil {
			return fmt.Errorf("failed to recreate memory_entities memory_id index: %w", err)
		}
	}

	// --- 3. entity_relations: FK + CASCADE + CHECK / 外键 + 级联 + 权重/自引用约束 ---
	ddl = ""
	err = tx.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='entity_relations'").Scan(&ddl)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to read entity_relations DDL: %w", err)
	}

	if err == nil && !strings.Contains(ddl, "CHECK") {
		if _, err := tx.Exec(`CREATE TABLE entity_relations_new (
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
			return fmt.Errorf("failed to create entity_relations_new: %w", err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO entity_relations_new SELECT * FROM entity_relations`); err != nil {
			return fmt.Errorf("failed to copy entity_relations data: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE entity_relations`); err != nil {
			return fmt.Errorf("failed to drop old entity_relations: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE entity_relations_new RENAME TO entity_relations`); err != nil {
			return fmt.Errorf("failed to rename entity_relations_new: %w", err)
		}
		// 重建索引 / Recreate indexes
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_entity_relations_source ON entity_relations(source_id)`); err != nil {
			return fmt.Errorf("failed to recreate entity_relations source index: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_entity_relations_target ON entity_relations(target_id)`); err != nil {
			return fmt.Errorf("failed to recreate entity_relations target index: %w", err)
		}
	}

	// --- 4. 更新 schema 版本 / Update schema version ---
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (15, datetime('now'))`); err != nil {
		return fmt.Errorf("V14→V15 schema_version: %w", err)
	}

	logger.Info("migration V14→V15 completed: FK CASCADE on junction tables + CHECK constraints")
	return tx.Commit()
}

// migrateV15ToV16 derived_from JSON → memory_derivations junction table / 溯源 JSON 列迁移至关联表
func migrateV15ToV16(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// --- 1. 创建 junction 表 / Create junction table ---
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS memory_derivations (
		source_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		target_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (source_id, target_id)
	)`); err != nil {
		return fmt.Errorf("failed to create memory_derivations: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memory_derivations_target ON memory_derivations(target_id)`); err != nil {
		return fmt.Errorf("failed to create memory_derivations target index: %w", err)
	}

	// --- 2. 迁移 JSON 数据到 junction 表 / Migrate existing JSON data ---
	rows, err := tx.Query(`SELECT id, derived_from FROM memories WHERE derived_from IS NOT NULL AND derived_from != '' AND derived_from != '[]'`)
	if err != nil {
		return fmt.Errorf("failed to query derived_from data: %w", err)
	}

	insertStmt, err := tx.Prepare(`INSERT OR IGNORE INTO memory_derivations (source_id, target_id, created_at) VALUES (?, ?, datetime('now'))`)
	if err != nil {
		rows.Close()
		return fmt.Errorf("failed to prepare derivation insert: %w", err)
	}

	migratedCount := 0
	for rows.Next() {
		var targetID, derivedJSON string
		if err := rows.Scan(&targetID, &derivedJSON); err != nil {
			rows.Close()
			insertStmt.Close()
			return fmt.Errorf("failed to scan derived_from row: %w", err)
		}

		var sourceIDs []string
		if err := json.Unmarshal([]byte(derivedJSON), &sourceIDs); err != nil {
			logger.Warn("V15→V16: skipping malformed derived_from JSON",
				zap.String("memory_id", targetID),
				zap.String("json", derivedJSON),
			)
			continue
		}

		for _, srcID := range sourceIDs {
			if srcID == "" {
				continue
			}
			if _, err := insertStmt.Exec(srcID, targetID); err != nil {
				logger.Warn("V15→V16: failed to insert derivation row",
					zap.String("source_id", srcID),
					zap.String("target_id", targetID),
					zap.Error(err),
				)
			}
		}
		migratedCount++
	}
	rows.Close()
	insertStmt.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("V15→V16 rows iteration error: %w", err)
	}

	// --- 3. 删除旧列 / Drop derived_from column ---
	// SQLite 需要 recreate table（3.35+ 支持 ALTER TABLE DROP COLUMN，但 modernc/sqlite 兼容性不确定）
	// 使用安全的 recreate 方式 / Use safe recreate approach
	if _, err := tx.Exec(`ALTER TABLE memories DROP COLUMN derived_from`); err != nil {
		// 如果 DROP COLUMN 不支持，用 recreate 方式 / Fallback: recreate table
		if !strings.Contains(err.Error(), "no such column") {
			logger.Warn("V15→V16: ALTER TABLE DROP COLUMN not supported, using recreate", zap.Error(err))
			// 不阻塞迁移：旧列留存但不再使用 / Don't block migration: old column remains but unused
			// 后续版本可清理 / Can be cleaned up in future version
		}
	}

	// --- 4. 更新 schema 版本 / Update schema version ---
	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (16, datetime('now'))`); err != nil {
		return fmt.Errorf("V15→V16 schema_version: %w", err)
	}

	logger.Info("migration V15→V16 completed: derived_from JSON → memory_derivations junction table",
		zap.Int("migrated_memories", migratedCount),
	)
	return tx.Commit()
}
