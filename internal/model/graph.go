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
	DeletedAt   *time.Time     `json:"deleted_at,omitempty"` // 软删除时间 / Soft delete timestamp
}

// EntityRelation 实体关系 / Entity relationship
type EntityRelation struct {
	ID           string         `json:"id"`
	SourceID     string         `json:"source_id"`
	TargetID     string         `json:"target_id"`
	RelationType string         `json:"relation_type"` // uses / knows / belongs_to / related_to
	Weight       float64        `json:"weight"`
	MentionCount int            `json:"mention_count"`             // 被提及次数 / Number of times mentioned
	LastSeenAt   *time.Time     `json:"last_seen_at,omitempty"`    // 最近出现时间 / Last time this relation was observed
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// MemoryEntity 记忆-实体关联 / Memory-entity association
type MemoryEntity struct {
	MemoryID   string    `json:"memory_id"`
	EntityID   string    `json:"entity_id"`
	Role       string    `json:"role,omitempty"` // subject / object / mentioned
	Confidence float64   `json:"confidence,omitempty"` // 关联置信度 0-1 / Association confidence
	CreatedAt  time.Time `json:"created_at"`
}

// EntityProfile 实体聚合视图 / Entity profile aggregation view
type EntityProfile struct {
	Entity        *Entity              `json:"entity"`
	Relations     []*EntityRelation    `json:"relations"`
	BySource      map[string][]*Memory `json:"by_source"`      // source_type:source_ref → memories
	ByTimeline    map[string][]*Memory `json:"by_timeline"`    // YYYY-MM → memories
	ByScope       map[string]int       `json:"by_scope"`       // scope → count
	TotalMemories int                  `json:"total_memories"`
}

// EntityCandidate 候选实体（待晋升）/ Candidate entity pending promotion
type EntityCandidate struct {
	Name      string    `json:"name"`
	Scope     string    `json:"scope,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	HitCount  int       `json:"hit_count"`
	MemoryIDs []string  `json:"memory_ids"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Tag 标签 / Tag for memory categorization
type Tag struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
