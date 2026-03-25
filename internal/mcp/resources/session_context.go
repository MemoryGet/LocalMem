package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

const sessionURIPrefix = "iclude://context/session/"

// SessionContextResource iclude://context/session/{id} 资源
type SessionContextResource struct{ retriever TimelineReader }

// NewSessionContextResource 创建会话上下文资源处理器 / Create session context resource handler
func NewSessionContextResource(retriever TimelineReader) *SessionContextResource {
	return &SessionContextResource{retriever: retriever}
}

// Definition 返回资源定义 / Returns the resource definition
func (r *SessionContextResource) Definition() mcp.ResourceDefinition {
	return mcp.ResourceDefinition{
		URI:         "iclude://context/session/{session_id}",
		Name:        "Session Context",
		Description: "Memories attached to a specific session or context node.",
		MimeType:    "application/json",
	}
}

// Match 匹配资源 URI / Check if this handler matches the given URI
func (r *SessionContextResource) Match(uri string) bool {
	return strings.HasPrefix(uri, sessionURIPrefix) && len(uri) > len(sessionURIPrefix)
}

// Read 读取会话上下文记忆并返回 JSON 字符串 / Read session context memories and return JSON string
func (r *SessionContextResource) Read(ctx context.Context, uri string) (string, error) {
	sessionID := strings.TrimPrefix(uri, sessionURIPrefix)
	if sessionID == "" || sessionID == uri {
		return "", fmt.Errorf("invalid session context URI: %s", uri)
	}
	id := mcp.IdentityFromContext(ctx)
	req := &model.TimelineRequest{Scope: sessionID, Limit: 50}
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
