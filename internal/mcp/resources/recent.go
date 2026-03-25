// Package resources MCP 资源处理器 / MCP resource handlers
package resources

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// TimelineReader 时间线读取接口 / Interface for timeline reads
type TimelineReader interface {
	Timeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error)
}

// RecentResource iclude://context/recent 资源
type RecentResource struct {
	retriever TimelineReader
	limit     int
}

// NewRecentResource 创建最近记忆资源处理器 / Create recent memories resource handler
func NewRecentResource(retriever TimelineReader, limit ...int) *RecentResource {
	lim := 20
	if len(limit) > 0 && limit[0] > 0 {
		lim = limit[0]
	}
	return &RecentResource{retriever: retriever, limit: lim}
}

// Definition 返回资源定义 / Returns the resource definition
func (r *RecentResource) Definition() mcp.ResourceDefinition {
	return mcp.ResourceDefinition{
		URI:         "iclude://context/recent",
		Name:        "Recent Memories",
		Description: "Most recent memories sorted by access time. Auto-injected at session start.",
		MimeType:    "application/json",
	}
}

// Match 匹配资源 URI / Check if this handler matches the given URI
func (r *RecentResource) Match(uri string) bool { return uri == "iclude://context/recent" }

// Read 读取最近记忆并返回 JSON 字符串 / Read recent memories and return JSON string
func (r *RecentResource) Read(ctx context.Context, _ string) (string, error) {
	id := mcp.IdentityFromContext(ctx)
	req := &model.TimelineRequest{Limit: r.limit}
	if id != nil {
		req.TeamID = id.TeamID
		req.OwnerID = id.OwnerID
	}
	memories, err := r.retriever.Timeline(ctx, req)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(memories)
	return string(data), nil
}
