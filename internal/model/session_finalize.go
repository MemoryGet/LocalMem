package model

import "time"

// SessionFinalizeState 会话终态控制点 / Session finalize/ingest/repair state
type SessionFinalizeState struct {
	SessionID            string    `json:"session_id"`
	IngestVersion        int       `json:"ingest_version"`
	FinalizeVersion      int       `json:"finalize_version"`
	ConversationIngested bool      `json:"conversation_ingested"`
	SummaryMemoryID      string    `json:"summary_memory_id,omitempty"`
	LastError            string    `json:"last_error,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}
