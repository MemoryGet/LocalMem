// Package model 核心数据模型 / Core data model definitions
package model

import "time"

// 知识保留等级常量 / Knowledge retention tier constants
const (
	TierPermanent = "permanent"  // 永久记忆，不衰减 / Permanent memory, no decay
	TierLongTerm  = "long_term"  // 长期记忆，半衰期 ~29 天 / Long-term, ~29d half-life
	TierStandard  = "standard"   // 标准记忆，半衰期 ~2.9 天 / Standard, ~2.9d half-life
	TierShortTerm = "short_term" // 短期记忆，半衰期 ~14 小时 / Short-term, ~14h half-life
	TierEphemeral = "ephemeral"  // 临时记忆，半衰期 ~7 小时 + 24h 过期 / Ephemeral, ~7h half-life + 24h expiry
)

// LLM 对话角色常量 / LLM conversation message role constants
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
)

// DefaultDecayParams 返回等级的默认衰减参数 / Return default decay parameters for a retention tier
func DefaultDecayParams(tier string) (decayRate float64, expiresIn *time.Duration) {
	switch tier {
	case TierPermanent:
		return 0, nil
	case TierLongTerm:
		return 0.001, nil
	case TierStandard:
		return 0.01, nil
	case TierShortTerm:
		return 0.05, nil
	case TierEphemeral:
		d := 24 * time.Hour
		return 0.1, &d
	default:
		return 0.01, nil
	}
}

// Memory 记忆体核心模型 / Core memory model
type Memory struct {
	ID          string         `json:"id"`
	Content     string         `json:"content"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	TeamID      string         `json:"team_id,omitempty"`
	Embedding   []float32      `json:"embedding,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
	IsLatest    bool           `json:"is_latest"`
	AccessCount int            `json:"access_count"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`

	// 分层扩展字段 / Hierarchical extension fields
	URI       string `json:"uri,omitempty"`        // iclude://{scope}/{path}#{id}
	ContextID string `json:"context_id,omitempty"` // FK → contexts.id
	Kind      string `json:"kind,omitempty"`       // note / fact / skill / profile
	SubKind   string `json:"sub_kind,omitempty"`   // entity / event / pattern / preference / case
	Scope     string `json:"scope,omitempty"`      // 顶级命名空间: user/alice, team/eng, agent/bot
	Excerpt string `json:"excerpt,omitempty"` // 一句话摘要 ≤100字 / One-line abstract ≤100 chars
	Summary   string `json:"summary,omitempty"`    // 核心信息 ≤500字

	// 时间线与来源 / Timeline and source tracking
	HappenedAt *time.Time `json:"happened_at,omitempty"`
	SourceType string     `json:"source_type,omitempty"` // manual / conversation / document / api
	SourceRef  string     `json:"source_ref,omitempty"`  // 来源引用标识

	// 文档关联 / Document association
	DocumentID string `json:"document_id,omitempty"` // FK → documents.id
	ChunkIndex int    `json:"chunk_index,omitempty"` // 文档分块序号

	// 生命周期 / Lifecycle fields
	DeletedAt       *time.Time `json:"deleted_at,omitempty"` // 软删除时间
	Strength        float64    `json:"strength"`             // 记忆强度 0~1, default 1.0
	DecayRate       float64    `json:"decay_rate"`           // 衰减速率, default 0.01
	LastAccessedAt  *time.Time `json:"last_accessed_at,omitempty"`
	ReinforcedCount int        `json:"reinforced_count"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"` // 过期时间

	// V3: 知识分级 / Knowledge retention tier
	RetentionTier string `json:"retention_tier,omitempty"` // permanent/long_term/standard/short_term/ephemeral

	// V3: LLM 对话集成 / LLM conversation integration
	MessageRole string `json:"message_role,omitempty"` // user / assistant / system / tool
	TurnNumber  int    `json:"turn_number,omitempty"`  // 对话中的轮次序号

	// V4: 内容哈希去重 / Content hash for deduplication
	ContentHash string `json:"content_hash,omitempty"` // SHA-256 of normalized content

	// V5: 记忆归纳审计 / Memory consolidation audit
	ConsolidatedInto string `json:"consolidated_into,omitempty"` // 被归纳到的目标记忆 ID

	// V6: 身份与归属 / Identity & Ownership
	OwnerID    string `json:"owner_id,omitempty"`   // 创建者 ID / Creator ID
	Visibility string `json:"visibility,omitempty"` // private / team / public

	// V12: 记忆演化层级 / Memory evolution layer
	MemoryClass string   `json:"memory_class,omitempty"` // episodic(default) / semantic / procedural
	DerivedFrom []string `json:"derived_from,omitempty"` // 来源记忆 ID 列表 / Source memory IDs (JSON array)
}

// SearchResult 检索结果 / Search result with score
type SearchResult struct {
	Memory *Memory `json:"memory"`
	Score  float64 `json:"score"`
	Source string  `json:"source"` // "sqlite", "qdrant", "hybrid"
}
