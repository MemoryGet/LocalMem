// Package stage 检索管线阶段实现 / Pipeline stage implementations
package stage

import (
	"context"

	"iclude/internal/model"
)

// FTSSearcher FTS 检索所需的最小接口 / Minimal interface for FTS search
type FTSSearcher interface {
	SearchText(ctx context.Context, query string, identity *model.Identity, limit int) ([]*model.SearchResult, error)
	SearchTextFiltered(ctx context.Context, query string, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error)
}

// GraphRetriever 图检索所需的最小接口 / Minimal interface for graph retrieval
type GraphRetriever interface {
	FindEntitiesByName(ctx context.Context, name, scope string, limit int) ([]*model.Entity, error)
	GetEntityRelations(ctx context.Context, entityID string) ([]*model.EntityRelation, error)
	GetEntityMemories(ctx context.Context, entityID string, limit int) ([]*model.Memory, error)
	GetMemoryEntities(ctx context.Context, memoryID string) ([]*model.Entity, error)
}

// VectorSearcher 向量检索所需的最小接口 / Minimal interface for vector search
type VectorSearcher interface {
	Search(ctx context.Context, embedding []float32, identity *model.Identity, limit int) ([]*model.SearchResult, error)
	SearchFiltered(ctx context.Context, embedding []float32, filters *model.SearchFilters, limit int) ([]*model.SearchResult, error)
	GetVectors(ctx context.Context, ids []string) (map[string][]float32, error)
}

// Embedder 文本向量化最小接口 / Minimal interface for text embedding
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// TimelineSearcher 时间线检索最小接口 / Minimal interface for timeline search
type TimelineSearcher interface {
	ListTimeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error)
}

// CoreProvider 核心记忆提供者接口 / Core memory provider interface
type CoreProvider interface {
	GetCoreBlocksMultiScope(ctx context.Context, scopes []string, identity *model.Identity) ([]*model.Memory, error)
}
