package model

import "time"

// Session 宿主工具会话实体 / Host tool session entity
type Session struct {
	ID          string     `json:"id"`
	ContextID   string     `json:"context_id"`
	UserID      string     `json:"user_id"`
	ToolName    string     `json:"tool_name"`
	ProjectID   string     `json:"project_id"`
	ProjectDir  string     `json:"project_dir"`
	Profile     string     `json:"profile"`
	State       string     `json:"state"`
	StartedAt   time.Time  `json:"started_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	FinalizedAt *time.Time      `json:"finalized_at,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Session state constants / 会话状态常量
const (
	SessionStateCreated       = "created"
	SessionStateBootstrapped  = "bootstrapped"
	SessionStateActive        = "active"
	SessionStateFinalizing    = "finalizing"
	SessionStateFinalized     = "finalized"
	SessionStatePendingRepair = "pending_repair"
	SessionStateAbandoned     = "abandoned"
)
