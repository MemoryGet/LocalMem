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

	// 精确可见性后过滤 / Exact visibility post-filter
	results = filterByVisibility(results, identity)

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

// buildVisibilityFilter 构建 Qdrant 可见性过滤器（宽松预过滤）/ Build Qdrant visibility pre-filter
// Qdrant flat FieldCondition 无法表达嵌套 AND-in-OR，此处使用宽松 should 预过滤，
// 由 filterByVisibility 做精确后过滤保证正确性。
// Strategy: pre-filter allows superset, post-filter enforces exact visibility rules.
func buildVisibilityFilter(identity *model.Identity) *qdrant.Filter {
	should := []qdrant.FieldCondition{
		{Key: "visibility", Match: qdrant.MatchValue{Value: model.VisibilityPublic}},
	}

	if identity.TeamID != "" {
		should = append(should,
			qdrant.FieldCondition{Key: "visibility", Match: qdrant.MatchValue{Value: model.VisibilityTeam}},
		)
	}

	if identity.OwnerID != "" && !identity.IsSystem() {
		should = append(should,
			qdrant.FieldCondition{Key: "owner_id", Match: qdrant.MatchValue{Value: identity.OwnerID}},
		)
	}

	return &qdrant.Filter{Should: should}
}

// filterByVisibility 对 Qdrant 结果做精确可见性后过滤 / Post-filter results for exact visibility enforcement
// 弥补 Qdrant flat filter 无法表达 (team_id=X AND visibility=team) 的限制
func filterByVisibility(results []qdrant.SearchResult, identity *model.Identity) []qdrant.SearchResult {
	if identity == nil {
		// 无身份：仅保留 public / No identity: only public
		filtered := make([]qdrant.SearchResult, 0, len(results))
		for _, r := range results {
			if vis, _ := r.Payload["visibility"].(string); vis == model.VisibilityPublic {
				filtered = append(filtered, r)
			}
		}
		return filtered
	}

	filtered := make([]qdrant.SearchResult, 0, len(results))
	for _, r := range results {
		vis, _ := r.Payload["visibility"].(string)
		switch vis {
		case model.VisibilityPublic:
			filtered = append(filtered, r)
		case model.VisibilityTeam:
			if tid, _ := r.Payload["team_id"].(string); tid != "" && tid == identity.TeamID {
				filtered = append(filtered, r)
			}
		case model.VisibilityPrivate, "":
			if oid, _ := r.Payload["owner_id"].(string); oid != "" && oid == identity.OwnerID {
				filtered = append(filtered, r)
			}
		}
	}
	return filtered
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

	// 可见性过滤：从 filters 中提取身份信息构建 should 条件 / Visibility filtering from identity in filters
	var visibilityIdentity *model.Identity
	if filters != nil && (filters.TeamID != "" || filters.OwnerID != "") {
		visibilityIdentity = &model.Identity{TeamID: filters.TeamID, OwnerID: filters.OwnerID}
	}

	if visibilityIdentity != nil {
		visFilter := buildVisibilityFilter(visibilityIdentity)
		// 合并: must 条件（业务过滤）+ should 条件（可见性）共存于同一 Filter
		// Merge: must conditions (business) + should conditions (visibility) in the same Filter
		req.Filter = &qdrant.Filter{Must: must, Should: visFilter.Should}
	} else {
		// 无身份时仅返回公开记忆（与 Search 方法行为一致）/ No identity: only public (consistent with Search)
		publicOnly := []qdrant.FieldCondition{
			{Key: "visibility", Match: qdrant.MatchValue{Value: model.VisibilityPublic}},
		}
		if len(must) > 0 {
			req.Filter = &qdrant.Filter{Must: append(must, publicOnly...)}
		} else {
			req.Filter = &qdrant.Filter{Must: publicOnly}
		}
	}

	results, err := s.client.Search(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to search vectors with filters: %w", err)
	}

	// 精确可见性后过滤（nil identity 时 filterByVisibility 仅保留 public）
	// Exact visibility post-filter (nil identity keeps only public via filterByVisibility)
	results = filterByVisibility(results, visibilityIdentity)

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
