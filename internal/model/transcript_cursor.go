package model

import "time"

// TranscriptCursor transcript 增量读取游标 / Transcript incremental read cursor
type TranscriptCursor struct {
	SessionID  string    `json:"session_id"`
	SourcePath string    `json:"source_path"`
	ByteOffset int64     `json:"byte_offset"`
	LastTurnID string    `json:"last_turn_id,omitempty"`
	LastReadAt time.Time `json:"last_read_at"`
}
