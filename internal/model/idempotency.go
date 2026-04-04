package model

import "time"

// IdempotencyRecord 幂等键记录 / Idempotency key record
type IdempotencyRecord struct {
	Scope        string    `json:"scope"`
	IdemKey      string    `json:"idem_key"`
	ResourceType string    `json:"resource_type,omitempty"`
	ResourceID   string    `json:"resource_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
