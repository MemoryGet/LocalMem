package model

import "time"

// Context 层级容器 / Hierarchical context container
type Context struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Path        string         `json:"path"` // 物化路径 e.g. "/root/sub1/sub2"
	ParentID    string         `json:"parent_id,omitempty"`
	Scope       string         `json:"scope,omitempty"`
	Kind        string         `json:"kind,omitempty"` // project / topic / session
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Depth       int            `json:"depth"`
	SortOrder   int            `json:"sort_order"`
	MemoryCount int            `json:"memory_count"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
