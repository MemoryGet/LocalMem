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
