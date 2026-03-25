package store

import (
	"context"
	"fmt"

	"iclude/internal/model"
	"iclude/pkg/qdrant"
)

// 编译期接口检查 / Compile-time interface compliance check
var _ VectorStore = (*QdrantVectorStore)(nil)

// QdrantVectorStore 基于 Qdrant 的向量存储 / Qdrant-based vector store implementation
type QdrantVectorStore struct {
	client *qdrant.Client
}

// NewQdrantVectorStore 创建 Qdrant 向量存储实例 / Create a new Qdrant vector store instance
func NewQdrantVectorStore(baseURL, collection string, dimension int) *QdrantVectorStore {
	return &QdrantVectorStore{
		client: qdrant.NewClient(baseURL, collection, dimension),
	}
}

// Init 初始化向量存储，确保集合存在并创建常用过滤字段的 payload index / Initialize vector storage
func (s *QdrantVectorStore) Init(ctx context.Context) error {
	if err := s.client.EnsureCollection(ctx); err != nil {
		return fmt.Errorf("failed to initialize qdrant vector store: %w", err)
	}

	// 为高频过滤字段创建 keyword payload index（幂等，失败不阻断启动）
	// Create keyword payload indexes for high-frequency filter fields (idempotent, non-fatal)
	for _, field := range []string{"scope", "context_id", "kind", "team_id", "owner_id", "visibility"} {
		if err := s.client.EnsureFieldIndex(ctx, field); err != nil {
			// 非致命：Qdrant 不可用时降级为全量扫描 / Non-fatal: falls back to full scan
			_ = err
		}
	}
	return nil
}

// Close 关闭连接（HTTP 客户端无需关闭）/ Close connection (no-op for HTTP client)
func (s *QdrantVectorStore) Close() error {
	return nil
}

// Upsert 插入或更新向量 / Insert or update a vector with payload
// 自动注入 owner_id 和 visibility（如 payload 中已存在则保留）
// Auto-inject owner_id and visibility into payload if present in memory fields
func (s *QdrantVectorStore) Upsert(ctx context.Context, memoryID string, embedding []float32, payload map[string]any) error {
	if payload == nil {
		payload = make(map[string]any)
	}
	payload["memory_id"] = memoryID

	// 确保 owner_id / visibility 有默认值（调用方应提前填充）
	// Ensure owner_id/visibility have defaults (caller should populate beforehand)
	if _, ok := payload["visibility"]; !ok {
		payload["visibility"] = model.VisibilityPrivate
	}
	if _, ok := payload["owner_id"]; !ok {
		payload["owner_id"] = ""
	}

	point := qdrant.PointStruct{
		ID:      memoryID,
		Vector:  embedding,
		Payload: payload,
	}

	if err := s.client.UpsertPoints(ctx, []qdrant.PointStruct{point}); err != nil {
		return fmt.Errorf("failed to upsert vector for memory %q: %w", memoryID, err)
	}
	return nil
}

// Delete 删除向量 / Delete vector by memory ID
func (s *QdrantVectorStore) Delete(ctx context.Context, memoryID string) error {
	if err := s.client.DeletePoints(ctx, []string{memoryID}); err != nil {
		return fmt.Errorf("failed to delete vector for memory %q: %w", memoryID, err)
	}
	return nil
}

// Search 向量相似度检索（带可见性过滤）/ Vector similarity search with visibility filtering
// 可见性规则: public 对所有人可见; team 对同 team 可见; private 仅 owner 可见
// Visibility rules: public=everyone, team=same team, private=owner only
func (s *QdrantVectorStore) Search(ctx context.Context, embedding []float32, identity *model.Identity, limit int) ([]*model.SearchResult, error) {
	req := qdrant.SearchRequest{
		Vector:      embedding,
		Limit:       limit,
		WithPayload: true,
	}

	if identity != nil {
		req.Filter = buildVisibilityFilter(identity)
	}

	results, err := s.client.Search(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to search vectors: %w", err)
	}

	searchResults := make([]*model.SearchResult, 0, len(results))
	for _, r := range results {
		memID := r.ID
		if pid, ok := r.Payload["memory_id"].(string); ok && pid != "" {
			memID = pid
		}

		searchResults = append(searchResults, &model.SearchResult{
			Memory: &model.Memory{
				ID: memID,
			},
			Score:  r.Score,
			Source: "qdrant",
		})
	}

	return searchResults, nil
}

// buildVisibilityFilter 构建 Qdrant 可见性过滤器 / Build Qdrant visibility filter from identity
// 策略: public OR (team_id 匹配 AND visibility=team) OR (owner_id 匹配 AND visibility=private)
// Strategy: public OR (same team AND team) OR (same owner AND private)
func buildVisibilityFilter(identity *model.Identity) *qdrant.Filter {
	// public 记忆总是可见 / public memories are always visible
	should := []qdrant.FieldCondition{
		{Key: "visibility", Match: qdrant.MatchValue{Value: model.VisibilityPublic}},
	}

	if identity.TeamID != "" {
		// team 记忆对同 team 成员可见 / team memories visible to same team members
		should = append(should,
			qdrant.FieldCondition{Key: "team_id", Match: qdrant.MatchValue{Value: identity.TeamID}},
		)
	}

	if identity.OwnerID != "" && !identity.IsSystem() {
		// private 记忆仅 owner 可见 / private memories visible only to owner
		should = append(should,
			qdrant.FieldCondition{Key: "owner_id", Match: qdrant.MatchValue{Value: identity.OwnerID}},
		)
	}

	return &qdrant.Filter{Should: should}
}

// SearchFiltered 带过滤条件的向量检索 / Vector search with filters
func (s *QdrantVectorStore) SearchFiltered(ctx context.Context, embedding []float32, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	req := qdrant.SearchRequest{
		Vector:      embedding,
		Limit:       limit,
		WithPayload: true,
	}

	// 构建 Qdrant Filter.Must 条件
	var must []qdrant.FieldCondition
	if filters != nil {
		if filters.Scope != "" {
			must = append(must, qdrant.FieldCondition{
				Key:   "scope",
				Match: qdrant.MatchValue{Value: filters.Scope},
			})
		}
		if filters.Kind != "" {
			must = append(must, qdrant.FieldCondition{
				Key:   "kind",
				Match: qdrant.MatchValue{Value: filters.Kind},
			})
		}
		if filters.ContextID != "" {
			must = append(must, qdrant.FieldCondition{
				Key:   "context_id",
				Match: qdrant.MatchValue{Value: filters.ContextID},
			})
		}
		if filters.SourceType != "" {
			must = append(must, qdrant.FieldCondition{
				Key:   "source_type",
				Match: qdrant.MatchValue{Value: filters.SourceType},
			})
		}
		if filters.RetentionTier != "" {
			must = append(must, qdrant.FieldCondition{
				Key:   "retention_tier",
				Match: qdrant.MatchValue{Value: filters.RetentionTier},
			})
		}
		if filters.MessageRole != "" {
			must = append(must, qdrant.FieldCondition{
				Key:   "message_role",
				Match: qdrant.MatchValue{Value: filters.MessageRole},
			})
		}
	}

	if len(must) > 0 {
		req.Filter = &qdrant.Filter{Must: must}
	}

	results, err := s.client.Search(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to search vectors with filters: %w", err)
	}

	searchResults := make([]*model.SearchResult, 0, len(results))
	for _, r := range results {
		memID := r.ID
		if pid, ok := r.Payload["memory_id"].(string); ok && pid != "" {
			memID = pid
		}
		searchResults = append(searchResults, &model.SearchResult{
			Memory: &model.Memory{ID: memID},
			Score:  r.Score,
			Source: "qdrant",
		})
	}

	return searchResults, nil
}

// GetVectors 批量获取向量 / Batch retrieve vectors by memory IDs
func (s *QdrantVectorStore) GetVectors(ctx context.Context, ids []string) (map[string][]float32, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	points, err := s.client.GetPoints(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to get vectors from qdrant: %w", err)
	}

	result := make(map[string][]float32, len(points))
	for _, p := range points {
		if len(p.Vector) > 0 {
			result[p.ID] = p.Vector
		}
	}
	return result, nil
}
