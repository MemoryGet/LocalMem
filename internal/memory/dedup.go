package memory

import (
	"context"
	"errors"
	"fmt"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/hashutil"

	"go.uber.org/zap"
)

// DedupResult 去重检查结果 / Dedup check result
type DedupResult struct {
	IsDuplicate    bool          // 是否重复 / Whether content is duplicate
	ExistingMemory *model.Memory // 已存在的记忆（重复时非 nil）/ Existing memory (non-nil when duplicate)
}

// checkHashDedup 哈希去重检查 / Check for duplicate content using hash
// 返回 DedupResult 和错误。ErrMemoryNotFound 表示无重复（正常路径）
func (m *Manager) checkHashDedup(ctx context.Context, contentHash string) (*DedupResult, error) {
	existing, err := m.memStore.GetByContentHash(ctx, contentHash)
	if err != nil {
		if errors.Is(err, model.ErrMemoryNotFound) {
			return &DedupResult{IsDuplicate: false}, nil
		}
		return nil, fmt.Errorf("hash dedup check failed: %w", err)
	}
	return &DedupResult{IsDuplicate: true, ExistingMemory: existing}, nil
}

// ContentHash 计算内容哈希（委托给 pkg/hashutil）/ Compute content hash (delegates to pkg/hashutil)
func ContentHash(content string) string {
	return hashutil.ContentHash(content)
}

// checkVectorDedup 余弦相似度去重 / Check for semantic duplicate using vector similarity
// 双阈值：>=skipThreshold 直接跳过，>=mergeThreshold 视为候选
// 需要 vecStore 和 embedder 非 nil，否则跳过
func checkVectorDedup(ctx context.Context, embedding []float32, vecStore store.VectorStore, cfg config.DedupConfig) (*DedupResult, error) {
	if !cfg.VectorEnabled || vecStore == nil || len(embedding) == 0 {
		return &DedupResult{IsDuplicate: false}, nil
	}

	// 搜索最相似的 1 条（系统内部操作，使用 nil identity 跳过可见性过滤）
	// System-internal search: nil identity bypasses visibility filtering
	results, err := vecStore.Search(ctx, embedding, nil, 1)
	if err != nil {
		return nil, fmt.Errorf("vector dedup search failed: %w", err)
	}
	if len(results) == 0 {
		return &DedupResult{IsDuplicate: false}, nil
	}

	topResult := results[0]
	sim := topResult.Score

	if sim >= cfg.SkipThreshold {
		// 近似重复，直接跳过
		logger.Info("vector dedup: near-duplicate detected",
			zap.String("existing_id", topResult.Memory.ID),
			zap.Float64("similarity", sim),
		)
		return &DedupResult{IsDuplicate: true, ExistingMemory: topResult.Memory}, nil
	}

	if sim >= cfg.MergeThreshold {
		// 中间区间：暂时视为不同（后续可加 LLM 判断）
		logger.Info("vector dedup: merge candidate (allowing write)",
			zap.String("existing_id", topResult.Memory.ID),
			zap.Float64("similarity", sim),
		)
	}

	return &DedupResult{IsDuplicate: false}, nil
}
