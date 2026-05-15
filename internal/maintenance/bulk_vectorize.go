// Package maintenance — vector backfill / 向量化回填.
package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"iclude/internal/logger"
	"iclude/internal/store"

	"go.uber.org/zap"
)

const (
	backfillVectorQueueFile = "backfill_vector_queue.md"
	defaultVectorChunkSize  = 500
	vectorCheckBatchSize    = 200
)

// BulkVectorizer 为 Qdrant 中缺失的记忆批量回填向量 / Backfills Qdrant vectors for memories missing from the vector store.
// 队列文件 {dataDir}/backfill_vector_queue.md 在服务重启后自动恢复 / Queue file auto-resumes after restart.
type BulkVectorizer struct {
	memStore   store.MemoryStore
	vecStore   store.VectorStore
	embedder   store.Embedder
	collection string
	queue      *QueueFile
	chunkSize  int
}

// NewBulkVectorizer 创建向量化回填器 / Create a bulk vectorizer backfiller.
func NewBulkVectorizer(memStore store.MemoryStore, vecStore store.VectorStore, embedder store.Embedder, collection, dataDir string) *BulkVectorizer {
	return &BulkVectorizer{
		memStore:   memStore,
		vecStore:   vecStore,
		embedder:   embedder,
		collection: collection,
		queue:      NewQueueFile(dataDir, backfillVectorQueueFile),
		chunkSize:  defaultVectorChunkSize,
	}
}

// Run 执行一次向量化回填批次 / Execute one vectorization backfill pass.
func (b *BulkVectorizer) Run(ctx context.Context) error {
	if b.memStore == nil || b.vecStore == nil || b.embedder == nil {
		return nil
	}

	db, ok := b.memStore.DB().(*sql.DB)
	if !ok || db == nil {
		logger.Warn("backfill-vectorize: memory store does not expose *sql.DB, skipping")
		return nil
	}

	pending, err := b.queue.Load()
	if err != nil {
		return fmt.Errorf("load vector queue: %w", err)
	}

	if pending == nil {
		allIDs, scanErr := queryIDs(ctx, db, "SELECT id FROM memories WHERE deleted_at IS NULL ORDER BY created_at")
		if scanErr != nil {
			return fmt.Errorf("scan memory ids: %w", scanErr)
		}
		missing, diffErr := b.findMissing(ctx, allIDs)
		if diffErr != nil {
			return fmt.Errorf("find missing vectors: %w", diffErr)
		}
		if len(missing) == 0 {
			logger.Info("backfill-vectorize: nothing to do", zap.Int("scanned", len(allIDs)))
			return nil
		}
		if err := b.queue.Save(missing); err != nil {
			return fmt.Errorf("save initial queue: %w", err)
		}
		logger.Info("backfill-vectorize: queue initialized",
			zap.Int("missing", len(missing)),
			zap.Int("scanned", len(allIDs)))
		pending = missing
	}

	if len(pending) == 0 {
		_ = b.queue.Save(nil)
		return nil
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

		if err := b.vectorizeChunk(ctx, db, chunk); err != nil {
			logger.Warn("backfill-vectorize: chunk failed, will retry on next run",
				zap.Int("chunk_size", len(chunk)), zap.Error(err))
			_ = b.queue.Save(pending)
			return err
		}

		pending = pending[take:]
		processed += take
		if err := b.queue.Save(pending); err != nil {
			logger.Warn("backfill-vectorize: save queue failed", zap.Error(err))
		}
		logger.Info("backfill-vectorize: chunk processed",
			zap.Int("processed", processed),
			zap.Int("total", total),
			zap.Int("remaining", len(pending)))
	}

	logger.Info("backfill-vectorize: completed",
		zap.Int("total", total),
		zap.String("collection", b.collection))
	return nil
}

// findMissing 检查哪些 ID 在向量库中缺失（分批查询）/ Determine which IDs are missing from the vector store (batched).
func (b *BulkVectorizer) findMissing(ctx context.Context, ids []string) ([]string, error) {
	missing := make([]string, 0)
	for start := 0; start < len(ids); start += vectorCheckBatchSize {
		end := start + vectorCheckBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		existing, err := b.vecStore.GetVectors(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("get vectors batch [%d:%d]: %w", start, end, err)
		}
		for _, id := range batch {
			if _, ok := existing[id]; !ok {
				missing = append(missing, id)
			}
		}
	}
	return missing, nil
}

// vectorizeChunk 加载内容 → embed → upsert / Load content → embed → upsert for a chunk.
func (b *BulkVectorizer) vectorizeChunk(ctx context.Context, db *sql.DB, ids []string) error {
	contentByID := make(map[string]string, len(ids))
	if err := loadContents(ctx, db, ids, contentByID); err != nil {
		return err
	}

	orderedIDs := make([]string, 0, len(ids))
	contents := make([]string, 0, len(ids))
	for _, id := range ids {
		content, ok := contentByID[id]
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		orderedIDs = append(orderedIDs, id)
		contents = append(contents, content)
	}
	if len(contents) == 0 {
		return nil
	}

	vectors, err := b.embedder.EmbedBatch(ctx, contents)
	if err != nil {
		return fmt.Errorf("embed batch: %w", err)
	}
	if len(vectors) != len(orderedIDs) {
		return fmt.Errorf("embedding count mismatch: got %d, want %d", len(vectors), len(orderedIDs))
	}

	for i, id := range orderedIDs {
		payload := map[string]any{"memory_id": id}
		if err := b.vecStore.Upsert(ctx, id, vectors[i], payload); err != nil {
			return fmt.Errorf("upsert vector %s: %w", id, err)
		}
	}
	return nil
}
