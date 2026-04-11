package store

import (
	"database/sql"
	"fmt"

	"iclude/internal/logger"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// 当前最新 schema 版本
const latestVersion = 27

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

	// 新数据库：直接创建终态 schema / Fresh database: create final schema directly
	if version == 0 {
		if err := createFreshSchema(db, tok); err != nil {
			return fmt.Errorf("fresh schema creation failed: %w", err)
		}
		logger.Info("fresh schema created", zap.Int("version", latestVersion))
		return nil
	}

	// 已有数据库：增量迁移 / Existing database: incremental migrations

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
		version = 10
	}

	// V10→V11: memory_tags 反向索引 + ConnMaxIdleTime 提示 / memory_tags reverse index
	if version < 11 {
		if err := migrateV10ToV11(db); err != nil {
			return fmt.Errorf("V10→V11 migration failed: %w", err)
		}
		version = 11
	}

	// V11→V12: 记忆演化层级 / Memory evolution layer (memory_class + derived_from)
	if version < 12 {
		if err := migrateV11ToV12(db); err != nil {
			return fmt.Errorf("V11→V12 migration failed: %w", err)
		}
		version = 12
	}

	// V12→V13: 上下文行为约束字段 / Context behavioral constraint fields
	if version < 13 {
		if err := migrateV12ToV13(db); err != nil {
			return fmt.Errorf("V12→V13 migration failed: %w", err)
		}
		version = 13
	}

	// V13→V14: 列重命名 + 删除死列 / Column renames (abstract→excerpt, contexts.kind→context_type) + drop embedding_id
	if version < 14 {
		if err := migrateV13ToV14(db); err != nil {
			return fmt.Errorf("V13→V14 migration failed: %w", err)
		}
		version = 14
	}

	// V14→V15: FK CASCADE on junction tables + CHECK constraints / 关联表外键级联 + CHECK 约束
	if version < 15 {
		if err := migrateV14ToV15(db); err != nil {
			return fmt.Errorf("V14→V15 migration failed: %w", err)
		}
		version = 15
	}

	// V15→V16: derived_from JSON → memory_derivations junction table / 溯源 JSON 列迁移至关联表
	if version < 16 {
		if err := migrateV15ToV16(db); err != nil {
			return fmt.Errorf("V15→V16 migration failed: %w", err)
		}
		version = 16
	}

	// V16→V17: source_ref + consolidated_into indexes / B6/B7 高频查询索引
	if version < 17 {
		if err := migrateV16ToV17(db); err != nil {
			return fmt.Errorf("V16→V17 migration failed: %w", err)
		}
		version = 17
	}

	// V17→V18: sessions 表 / Sessions table
	if version < 18 {
		if err := migrateV17ToV18(db); err != nil {
			return fmt.Errorf("V17→V18 migration failed: %w", err)
		}
		version = 18
	}

	// V18→V19: session_finalize_state 表 / Session finalize state table
	if version < 19 {
		if err := migrateV18ToV19(db); err != nil {
			return fmt.Errorf("V18→V19 migration failed: %w", err)
		}
		version = 19
	}

	// V19→V20: transcript_cursors 表 / Transcript cursors table
	if version < 20 {
		if err := migrateV19ToV20(db); err != nil {
			return fmt.Errorf("V19→V20 migration failed: %w", err)
		}
		version = 20
	}

	// V20→V21: idempotency_keys 表 / Idempotency keys table
	if version < 21 {
		if err := migrateV20ToV21(db); err != nil {
			return fmt.Errorf("V20→V21 migration failed: %w", err)
		}
		version = 21
	}

	// V21→V22: FK constraints on session tables + entities composite index
	if version < 22 {
		if err := migrateV21ToV22(db); err != nil {
			return fmt.Errorf("V21→V22 migration failed: %w", err)
		}
		version = 22
	}

	if version < 23 {
		if err := migrateV22ToV23(db); err != nil {
			return fmt.Errorf("V22→V23 migration failed: %w", err)
		}
		version = 23
	}

	if version < 24 {
		if err := migrateV23ToV24(db); err != nil {
			return fmt.Errorf("V23→V24 migration failed: %w", err)
		}
		version = 24
	}

	// V24→V25: missing excerpt index for heartbeat ListMissingExcerpt
	if version < 25 {
		if err := migrateV24ToV25(db); err != nil {
			return fmt.Errorf("V24→V25 migration failed: %w", err)
		}
		version = 25
	}

	// V25→V26: entity lifecycle fields + entity soft delete + source_ref prefix index
	if version < 26 {
		if err := migrateV25ToV26(db); err != nil {
			return fmt.Errorf("V25→V26 migration failed: %w", err)
		}
		version = 26
	}

	// V26→V27: memory_entity confidence + entity_candidates
	if version < 27 {
		if err := migrateV26ToV27(db); err != nil {
			return fmt.Errorf("V26→V27 migration failed: %w", err)
		}
		version = 27
	}

	return nil
}

