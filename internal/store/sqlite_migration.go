package store

import (
	"database/sql"
	"fmt"

	"iclude/internal/logger"
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

	return nil
}

