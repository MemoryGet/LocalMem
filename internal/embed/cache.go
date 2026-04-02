package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"iclude/internal/logger"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// CachedEmbedder LRU 缓存装饰器 / LRU cache decorator for Embedder
// 对 Embed 做缓存，EmbedBatch 逐条走缓存
type CachedEmbedder struct {
	inner    store.Embedder
	cache    map[string][]float32
	order    []string // LRU 顺序 / LRU order (oldest first)
	maxSize  int
	mu       sync.RWMutex
	hits     int64
	misses   int64
}

// NewCachedEmbedder 创建带 LRU 缓存的 embedder / Create an LRU-cached embedder
// maxSize: 最大缓存条目数 / Maximum number of cached entries
func NewCachedEmbedder(inner store.Embedder, maxSize int) store.Embedder {
	if maxSize <= 0 {
		return inner
	}
	return &CachedEmbedder{
		inner:   inner,
		cache:   make(map[string][]float32, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

// Embed 带缓存的向量化 / Embed with LRU cache
func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := hashKey(text)

	// 读缓存 / Check cache
	c.mu.RLock()
	if vec, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		c.mu.Lock()
		c.hits++
		c.touchLocked(key)
		c.mu.Unlock()
		return copyVec(vec), nil
	}
	c.mu.RUnlock()

	// 缓存未命中，调用底层 / Cache miss, call inner embedder
	vec, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	// 写入缓存 / Write to cache
	c.mu.Lock()
	c.misses++
	c.putLocked(key, vec)
	c.mu.Unlock()

	return vec, nil
}

// EmbedBatch 批量向量化（逐条走缓存）/ Batch embed with per-item cache
func (c *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	var missIndices []int
	var missTexts []string

	// 先查缓存 / Check cache first
	c.mu.RLock()
	for i, text := range texts {
		key := hashKey(text)
		if vec, ok := c.cache[key]; ok {
			results[i] = copyVec(vec)
		} else {
			missIndices = append(missIndices, i)
			missTexts = append(missTexts, text)
		}
	}
	c.mu.RUnlock()

	if len(missTexts) == 0 {
		c.mu.Lock()
		c.hits += int64(len(texts))
		c.mu.Unlock()
		return results, nil
	}

	// 批量调用底层 / Batch call inner for misses
	missVecs, err := c.inner.EmbedBatch(ctx, missTexts)
	if err != nil {
		return nil, err
	}

	// 填充结果 + 写入缓存 / Fill results and cache
	c.mu.Lock()
	c.hits += int64(len(texts) - len(missTexts))
	c.misses += int64(len(missTexts))
	for j, idx := range missIndices {
		if j < len(missVecs) {
			results[idx] = missVecs[j]
			c.putLocked(hashKey(missTexts[j]), missVecs[j])
		}
	}
	c.mu.Unlock()

	return results, nil
}

// Stats 返回缓存命中统计 / Return cache hit statistics
func (c *CachedEmbedder) Stats() (hits, misses int64, size int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses, len(c.cache)
}

// LogStats 打印缓存统计日志 / Log cache statistics
func (c *CachedEmbedder) LogStats() {
	hits, misses, size := c.Stats()
	total := hits + misses
	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}
	logger.Info("embedding cache stats",
		zap.Int64("hits", hits),
		zap.Int64("misses", misses),
		zap.Float64("hit_rate_pct", hitRate),
		zap.Int("cache_size", size),
		zap.Int("max_size", c.maxSize),
	)
}

// putLocked 写入缓存（需持有写锁）/ Put into cache (must hold write lock)
func (c *CachedEmbedder) putLocked(key string, vec []float32) {
	if _, exists := c.cache[key]; exists {
		c.touchLocked(key)
		return
	}
	// 淘汰最旧条目 / Evict oldest entry
	for len(c.cache) >= c.maxSize && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.cache, oldest)
	}
	c.cache[key] = copyVec(vec)
	c.order = append(c.order, key)
}

// touchLocked 将 key 移到末尾（最近使用）/ Move key to end (most recently used)
func (c *CachedEmbedder) touchLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

func hashKey(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:16]) // 128-bit，足够避免碰撞 / 128-bit, sufficient for collision avoidance
}

func copyVec(v []float32) []float32 {
	c := make([]float32, len(v))
	copy(c, v)
	return c
}
