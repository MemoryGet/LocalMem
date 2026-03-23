package model

import "time"

// Entity 知识图谱实体 / Knowledge graph entity
type Entity struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	EntityType  string         `json:"entity_type"` // person / org / concept / tool / location
	Scope       string         `json:"scope,omitempty"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// EntityRelation 实体关系 / Entity relationship
type EntityRelation struct {
	ID           string         `json:"id"`
	SourceID     string         `json:"source_id"`
	TargetID     string         `json:"target_id"`
	RelationType string         `json:"relation_type"` // uses / knows / belongs_to / related_to
	Weight       float64        `json:"weight"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// MemoryEntity 记忆-实体关联 / Memory-entity association
type MemoryEntity struct {
	MemoryID  string    `json:"memory_id"`
	EntityID  string    `json:"entity_id"`
	Role      string    `json:"role,omitempty"` // subject / object / mentioned
	CreatedAt time.Time `json:"created_at"`
}

// Tag 标签 / Tag for memory categorization
type Tag struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
