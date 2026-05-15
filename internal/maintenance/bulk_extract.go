// Package maintenance — entity extraction backfill / 实体抽取回填.
package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"

	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"

	"go.uber.org/zap"
)

const (
	backfillExtractQueueFile     = "backfill_extract_queue.md"
	defaultExtractChunkSize      = 300
	defaultExtractTokenThreshold = 8000
	preloadChunkSize             = 500
)

// BulkExtractor 为缺少实体关联的记忆批量回填实体抽取 / Backfills entity extraction for memories without entity associations.
// 队列文件 {dataDir}/backfill_extract_queue.md 在服务重启后自动恢复 / Queue file auto-resumes after restart.
type BulkExtractor struct {
	db        *sql.DB
	extractor *memory.Extractor
	queue     *QueueFile
	chunkSize int
	threshold int
}

// NewBulkExtractor 创建实体抽取回填器 / Create a bulk extractor backfiller.
func NewBulkExtractor(db *sql.DB, extractor *memory.Extractor, dataDir string) *BulkExtractor {
	return &BulkExtractor{
		db:        db,
		extractor: extractor,
		queue:     NewQueueFile(dataDir, backfillExtractQueueFile),
		chunkSize: defaultExtractChunkSize,
		threshold: defaultExtractTokenThreshold,
	}
}

// Run 执行一次回填批次 / Execute one backfill pass.
func (b *BulkExtractor) Run(ctx context.Context) error {
	if b.extractor == nil || b.db == nil {
		return nil
	}

	pending, err := b.queue.Load()
	if err != nil {
		return fmt.Errorf("load extract queue: %w", err)
	}

	if pending == nil {
		pending, err = b.scanMissing(ctx)
		if err != nil {
			return fmt.Errorf("scan missing entities: %w", err)
		}
		if len(pending) == 0 {
			logger.Info("backfill-extract: nothing to do")
			return nil
		}
		if err := b.queue.Save(pending); err != nil {
			return fmt.Errorf("save initial queue: %w", err)
		}
		logger.Info("backfill-extract: queue initialized", zap.Int("pending", len(pending)))
	}

	if len(pending) == 0 {
		_ = b.queue.Save(nil)
		return nil
	}

	contents, err := b.preloadContents(ctx, pending)
	if err != nil {
		return fmt.Errorf("preload contents: %w", err)
	}

	threshold := b.threshold
	if v := os.Getenv("EXTRACT_BATCH_THRESHOLD"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			threshold = n
		}
	}

	total := len(pending)
	processed := 0
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			_ = b.queue.Save(pending)
			return err
		}

		take := b.chunkSize
		if take > len(pending) {
			take = len(pending)
		}
		chunk := pending[:take]

		items := make([]model.BatchExtractItem, 0, len(chunk))
		for _, id := range chunk {
			content, ok := contents[id]
			if !ok || strings.TrimSpace(content) == "" {
				continue
			}
			items = append(items, model.BatchExtractItem{MemoryID: id, Content: content})
		}

		if len(items) > 0 {
			if _, extractErr := b.extractor.ExtractBatch(ctx, &model.BatchExtractRequest{Items: items}); extractErr != nil {
				logger.Warn("backfill-extract: ExtractBatch failed, will retry on next run",
					zap.Int("chunk_size", len(items)),
					zap.Int("token_threshold", threshold),
					zap.Error(extractErr))
				_ = b.queue.Save(pending)
				return extractErr
			}
		}

		pending = pending[take:]
		processed += take
		if err := b.queue.Save(pending); err != nil {
			logger.Warn("backfill-extract: save queue failed", zap.Error(err))
		}
		logger.Info("backfill-extract: chunk processed",
			zap.Int("processed", processed),
			zap.Int("total", total),
			zap.Int("remaining", len(pending)))
	}

	logger.Info("backfill-extract: completed", zap.Int("total", total))
	return nil
}

// scanMissing 扫描缺少实体关联的记忆 ID / Scan memories without any entity association.
func (b *BulkExtractor) scanMissing(ctx context.Context) ([]string, error) {
	const q = `
SELECT m.id FROM memories m
WHERE m.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM memory_entities me WHERE me.memory_id = m.id)
ORDER BY m.created_at`
	return queryIDs(ctx, b.db, q)
}

// preloadContents 通过 IN 子句批量加载内容 / Preload memory content via IN-clause batches.
func (b *BulkExtractor) preloadContents(ctx context.Context, ids []string) (map[string]string, error) {
	contents := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += preloadChunkSize {
		end := start + preloadChunkSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := loadContents(ctx, b.db, ids[start:end], contents); err != nil {
			return nil, err
		}
	}
	return contents, nil
}
