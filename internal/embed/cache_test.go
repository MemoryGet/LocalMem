package embed

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingEmbedder struct {
	calls atomic.Int64
	dim   int
}

func (e *countingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.calls.Add(1)
	vec := make([]float32, e.dim)
	for i := range vec {
		vec[i] = float32(len(text)) * 0.01
	}
	return vec, nil
}

func (e *countingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, t := range texts {
		v, _ := e.Embed(context.Background(), t)
		result[i] = v
	}
	return result, nil
}

func TestCachedEmbedder_HitOnRepeat(t *testing.T) {
	inner := &countingEmbedder{dim: 4}
	cached := NewCachedEmbedder(inner, 100).(*CachedEmbedder)

	ctx := context.Background()
	v1, err := cached.Embed(ctx, "hello")
	require.NoError(t, err)

	v2, err := cached.Embed(ctx, "hello")
	require.NoError(t, err)

	assert.Equal(t, v1, v2)
	assert.Equal(t, int64(1), inner.calls.Load(), "inner should be called only once")

	hits, misses, size := cached.Stats()
	assert.Equal(t, int64(1), hits)
	assert.Equal(t, int64(1), misses)
	assert.Equal(t, 1, size)
}

func TestCachedEmbedder_Eviction(t *testing.T) {
	inner := &countingEmbedder{dim: 4}
	cached := NewCachedEmbedder(inner, 3).(*CachedEmbedder)

	ctx := context.Background()
	_, _ = cached.Embed(ctx, "a")
	_, _ = cached.Embed(ctx, "b")
	_, _ = cached.Embed(ctx, "c")
	_, _ = cached.Embed(ctx, "d") // evicts "a"

	assert.Equal(t, int64(4), inner.calls.Load())

	_, _ = cached.Embed(ctx, "a") // should miss (evicted)
	assert.Equal(t, int64(5), inner.calls.Load())

	_, _ = cached.Embed(ctx, "d") // should hit
	assert.Equal(t, int64(5), inner.calls.Load())
}

func TestCachedEmbedder_BatchUseCache(t *testing.T) {
	inner := &countingEmbedder{dim: 4}
	cached := NewCachedEmbedder(inner, 100).(*CachedEmbedder)

	ctx := context.Background()
	_, _ = cached.Embed(ctx, "x")
	_, _ = cached.Embed(ctx, "y")

	// Batch: "x" cached, "z" miss
	results, err := cached.EmbedBatch(ctx, []string{"x", "z"})
	require.NoError(t, err)
	assert.Len(t, results, 2)
	// inner called: x(1) + y(1) + z(1) = 3
	assert.Equal(t, int64(3), inner.calls.Load())
}

func TestCachedEmbedder_ZeroSize_Passthrough(t *testing.T) {
	inner := &countingEmbedder{dim: 4}
	embedder := NewCachedEmbedder(inner, 0)

	_, err := embedder.Embed(context.Background(), "test")
	require.NoError(t, err)
	_, err = embedder.Embed(context.Background(), "test")
	require.NoError(t, err)

	assert.Equal(t, int64(2), inner.calls.Load(), "zero-size cache should passthrough")
}

func TestCachedEmbedder_CopyIsolation(t *testing.T) {
	inner := &countingEmbedder{dim: 4}
	cached := NewCachedEmbedder(inner, 100)

	ctx := context.Background()
	v1, _ := cached.Embed(ctx, "test")
	v1[0] = 999.0 // mutate returned vector

	v2, _ := cached.Embed(ctx, "test")
	assert.NotEqual(t, float32(999.0), v2[0], "cached vector should not be mutated by caller")
}
