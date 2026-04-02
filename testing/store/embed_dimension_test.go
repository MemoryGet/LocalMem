package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockEmbedder 测试用 embedder / Mock embedder for testing
type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	vec := make([]float32, m.dim)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		v, _ := m.Embed(context.Background(), texts[i])
		result[i] = v
	}
	return result, nil
}

// TestEmbedderDimensionMatch 维度匹配时不应报错 / Dimension match should pass
func TestEmbedderDimensionMatch(t *testing.T) {
	emb := &mockEmbedder{dim: 384}
	vec, err := emb.Embed(context.Background(), "dimension probe")
	assert.NoError(t, err)
	assert.Equal(t, 384, len(vec), "dimension should match config")
}

// TestEmbedderDimensionMismatch 维度不匹配时应检测到 / Dimension mismatch should be detected
func TestEmbedderDimensionMismatch(t *testing.T) {
	emb := &mockEmbedder{dim: 1536} // 模型返回 1536，但配置期望 384
	expectedDim := 384

	vec, err := emb.Embed(context.Background(), "dimension probe")
	assert.NoError(t, err)
	assert.NotEqual(t, expectedDim, len(vec), "should detect dimension mismatch: expected %d, got %d", expectedDim, len(vec))
}
