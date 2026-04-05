package embed

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"iclude/internal/logger"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// cacheEntry LRU 缓存条目 / LRU cache entry
type cacheEntry struct {
	key string
	vec []float32
}

// CachedEmbedder LRU 缓存装饰器 / LRU cache decorator for Embedder
// 对 Embed 做缓存，EmbedBatch 逐条走缓存
type CachedEmbedder struct {
	inner     store.Embedder
	modelName string // 模型名称，用于缓存 key 隔离 / Model name for cache key isolation
	cache     map[string]*list.Element
	ll        *list.List // 双向链表，Front=最近使用 / Doubly linked list, Front=most recently used
	maxSize   int
	mu        sync.RWMutex
	hits      int64
	misses    int64
}

// NewCachedEmbedder 创建带 LRU 缓存的 embedder / Create an LRU-cached embedder
// maxSize: 最大缓存条目数 / Maximum number of cached entries
// modelName: 模型名称，用于缓存 key 隔离不同模型的 embedding / Model name for cache key isolation
func NewCachedEmbedder(inner store.Embedder, maxSize int, modelName ...string) store.Embedder {
	if maxSize <= 0 {
		return inner
	}
	name := ""
	if len(modelName) > 0 {
		name = modelName[0]
	}
	return &CachedEmbedder{
		inner:     inner,
		modelName: name,
		cache:     make(map[string]*list.Element, maxSize),
		ll:        list.New(),
		maxSize:   maxSize,
	}
}

// Embed 带缓存的向量化 / Embed with LRU cache
func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := hashKeyWithModel(c.modelName, text)

	// 读缓存 / Check cache
	c.mu.RLock()
	if _, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		// Double-check: 升级写锁后重新检查 key 是否仍存在（RLock→Lock 间可能被驱逐）
		// Double-check: re-verify key after upgrading to write lock (may be evicted between RLock→Lock)
		c.mu.Lock()
		if elem2, ok2 := c.cache[key]; ok2 {
			c.hits++
			c.ll.MoveToFront(elem2)
			vec := copyVec(elem2.Value.(*cacheEntry).vec)
			c.mu.Unlock()
			return vec, nil
		}
		// Key 已被驱逐，按 miss 路径处理 / Key evicted, fall through to miss path
		c.mu.Unlock()
	} else {
		c.mu.RUnlock()
	}

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
		key := hashKeyWithModel(c.modelName, text)
		if elem, ok := c.cache[key]; ok {
			results[i] = copyVec(elem.Value.(*cacheEntry).vec)
		} else {
			missIndices = append(missIndices, i)
			missTexts = append(missTexts, text)
		}
	}
	c.mu.RUnlock()

	if len(missTexts) == 0 {
		// Double-check: 写锁内重新验证所有 key（RLock→Lock 间可能被驱逐）
		// Double-check: re-verify all keys under write lock (may be evicted between RLock→Lock)
		c.mu.Lock()
		var lateEvicted bool
		for i, text := range texts {
			key := hashKeyWithModel(c.modelName, text)
			if elem, ok := c.cache[key]; ok {
				c.ll.MoveToFront(elem)
				results[i] = copyVec(elem.Value.(*cacheEntry).vec)
			} else {
				// Key 被驱逐，需要重新获取 / Key evicted, need to re-fetch
				missIndices = append(missIndices, i)
				missTexts = append(missTexts, text)
				lateEvicted = true
			}
		}
		if !lateEvicted {
			c.hits += int64(len(texts))
			c.mu.Unlock()
			return results, nil
		}
		c.mu.Unlock()
		// 继续 miss 路径处理被驱逐的 key / Fall through to miss path for evicted keys
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
	// 更新命中项的 LRU 顺序 / Update LRU order for hit items
	for i, text := range texts {
		key := hashKeyWithModel(c.modelName, text)
		if elem, ok := c.cache[key]; ok {
			c.ll.MoveToFront(elem)
			_ = i // hit item, already in results
		}
	}
	for j, idx := range missIndices {
		if j < len(missVecs) {
			results[idx] = missVecs[j]
			c.putLocked(hashKeyWithModel(c.modelName, missTexts[j]), missVecs[j])
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
	// 已存在则更新并移到前端 / If exists, update and move to front
	if elem, ok := c.cache[key]; ok {
		c.ll.MoveToFront(elem)
		elem.Value.(*cacheEntry).vec = copyVec(vec)
		return
	}
	// 淘汰最旧条目 / Evict oldest entry
	for len(c.cache) >= c.maxSize {
		back := c.ll.Back()
		if back == nil {
			break
		}
		evicted := c.ll.Remove(back).(*cacheEntry)
		delete(c.cache, evicted.key)
	}
	// 插入新条目到前端 / Insert new entry at front
	entry := &cacheEntry{key: key, vec: copyVec(vec)}
	elem := c.ll.PushFront(entry)
	c.cache[key] = elem
}

// hashKeyWithModel 含模型名的缓存 key / Cache key including model name for isolation
func hashKeyWithModel(modelName, text string) string {
	h := sha256.Sum256([]byte(modelName + ":" + text))
	return hex.EncodeToString(h[:16]) // 128-bit，足够避免碰撞 / 128-bit, sufficient for collision avoidance
}

func copyVec(v []float32) []float32 {
	c := make([]float32, len(v))
	copy(c, v)
	return c
}
