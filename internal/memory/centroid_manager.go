package memory

import (
	"context"
	"fmt"

	"iclude/internal/logger"
	"iclude/pkg/qdrant"

	"go.uber.org/zap"
)

// CentroidManager 实体质心向量管理 / Entity centroid vector manager
type CentroidManager struct {
	client *qdrant.Client
}

// NewCentroidManager 创建质心管理器 / Create centroid manager
func NewCentroidManager(baseURL, collection string, dimension int) (*CentroidManager, error) {
	client := qdrant.NewClient(baseURL, collection, dimension)

	ctx := context.Background()
	if err := client.EnsureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure centroid collection: %w", err)
	}
	if err := client.EnsureFieldIndex(ctx, "entity_id"); err != nil {
		logger.Warn("centroid: field index failed (non-fatal)", zap.Error(err))
	}

	return &CentroidManager{client: client}, nil
}

// UpsertCentroid 更新实体质心向量 / Update entity centroid vector
func (m *CentroidManager) UpsertCentroid(ctx context.Context, entityID, entityName, scope string, vector []float32, memoryCount int) error {
	point := qdrant.PointStruct{
		ID:     entityID,
		Vector: vector,
		Payload: map[string]any{
			"entity_id":    entityID,
			"entity_name":  entityName,
			"scope":        scope,
			"memory_count": memoryCount,
		},
	}
	return m.client.UpsertPoints(ctx, []qdrant.PointStruct{point})
}

// SearchSimilar 查找与向量相似的实体 / Find entities similar to a vector
func (m *CentroidManager) SearchSimilar(ctx context.Context, vector []float32, limit int, minScore float64) ([]CentroidMatch, error) {
	results, err := m.client.Search(ctx, qdrant.SearchRequest{
		Vector:      vector,
		Limit:       limit,
		WithPayload: true,
	})
	if err != nil {
		return nil, fmt.Errorf("centroid search: %w", err)
	}

	var matches []CentroidMatch
	for _, r := range results {
		if r.Score < minScore {
			continue
		}
		entityID, _ := r.Payload["entity_id"].(string)
		if entityID == "" {
			continue
		}
		matches = append(matches, CentroidMatch{
			EntityID: entityID,
			Score:    r.Score,
		})
	}
	return matches, nil
}

// DeleteCentroid 删除实体质心 / Delete entity centroid
func (m *CentroidManager) DeleteCentroid(ctx context.Context, entityID string) error {
	return m.client.DeletePoints(ctx, []string{entityID})
}

// CentroidMatch 质心匹配结果 / Centroid match result
type CentroidMatch struct {
	EntityID string
	Score    float64
}
