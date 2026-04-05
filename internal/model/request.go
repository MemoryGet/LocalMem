package model

import "time"

// CreateMemoryRequest 创建记忆请求 / Create memory request DTO
type CreateMemoryRequest struct {
	Content    string         `json:"content" binding:"required"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	TeamID     string         `json:"team_id,omitempty"`
	Embedding  []float32      `json:"embedding,omitempty"`
	ContextID  string         `json:"context_id,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	SubKind    string         `json:"sub_kind,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
	Summary string `json:"summary,omitempty"`
	HappenedAt *time.Time     `json:"happened_at,omitempty"`
	SourceType string         `json:"source_type,omitempty"`
	SourceRef  string         `json:"source_ref,omitempty"`
	Tags       []string       `json:"tags,omitempty"`

	// V3: 知识分级 / Knowledge retention tier
	RetentionTier string     `json:"retention_tier,omitempty"`
	Strength      *float64   `json:"strength,omitempty"`
	DecayRate     *float64   `json:"decay_rate,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`

	// V3: LLM 对话集成 / LLM conversation integration
	MessageRole string `json:"message_role,omitempty"`
	TurnNumber  int    `json:"turn_number,omitempty"`

	// Phase 2: 自动实体抽取 / Auto entity extraction
	AutoExtract bool `json:"auto_extract,omitempty"`

	// 文档关联 / Document association
	DocumentID string `json:"document_id,omitempty"`
	ChunkIndex int    `json:"chunk_index,omitempty"`

	// V6: 身份与可见性 / Identity & Visibility
	OwnerID    string `json:"-"`                    // API 层注入 / Injected by API layer
	Visibility string `json:"visibility,omitempty"` // private(default) / team / public

	// V12: Memory evolution layer / 记忆演化层级
	MemoryClass  string   `json:"memory_class,omitempty"`  // episodic(default) / semantic / procedural
	CandidateFor string   `json:"candidate_for,omitempty"` // semantic_candidate / procedural_candidate / core_candidate
	DerivedFrom  []string `json:"derived_from,omitempty"`  // 来源记忆 ID / Source memory IDs
}

// UpdateMemoryRequest 更新记忆请求 / Update memory request DTO
type UpdateMemoryRequest struct {
	Content    *string        `json:"content,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Embedding  []float32      `json:"embedding,omitempty"`
	ContextID  *string        `json:"context_id,omitempty"`
	Kind       *string        `json:"kind,omitempty"`
	SubKind    *string        `json:"sub_kind,omitempty"`
	Scope      *string        `json:"scope,omitempty"`
	Excerpt *string `json:"excerpt,omitempty"`
	Summary *string `json:"summary,omitempty"`
	HappenedAt *time.Time     `json:"happened_at,omitempty"`
	SourceType *string        `json:"source_type,omitempty"`
	SourceRef  *string        `json:"source_ref,omitempty"`
	Tags       []string       `json:"tags,omitempty"`

	// V3: 知识分级 / Knowledge retention tier
	RetentionTier *string    `json:"retention_tier,omitempty"`
	Strength      *float64   `json:"strength,omitempty"`
	DecayRate     *float64   `json:"decay_rate,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`

	// V3: LLM 对话集成 / LLM conversation integration
	MessageRole *string `json:"message_role,omitempty"`
	TurnNumber  *int    `json:"turn_number,omitempty"`

	// V6: 可见性 / Visibility level
	Visibility *string `json:"visibility,omitempty"`
}

// RetrieveRequest 检索请求 / Retrieve request DTO
type RetrieveRequest struct {
	Query        string         `json:"query,omitempty"`
	Embedding    []float32      `json:"embedding,omitempty"`
	TeamID       string         `json:"team_id,omitempty"`
	OwnerID      string         `json:"owner_id,omitempty"`
	Limit        int            `json:"limit,omitempty"`
	Filters      *SearchFilters `json:"filters,omitempty"`
	DetailLevel  string         `json:"detail_level,omitempty"` // excerpt_only / summary / full
	MaxTokens    int            `json:"max_tokens,omitempty"`
	GraphEnabled *bool          `json:"graph_enabled,omitempty"`

	// Phase 2: MMR 多样性重排（覆盖全局配置）/ Per-request MMR override
	MmrEnabled     *bool    `json:"mmr_enabled,omitempty"`     // nil=使用配置文件默认值 / nil=use config default
	MmrLambda      *float64 `json:"mmr_lambda,omitempty"`      // 相关性 vs 多样性，推荐 0.7 / Relevance vs diversity
	RerankEnabled  *bool    `json:"rerank_enabled,omitempty"`  // nil=使用配置文件默认值 / nil=use config default
	RerankProvider string   `json:"rerank_provider,omitempty"` // overlap | remote
	NoRetry        bool     `json:"-"`                         // 内部标记，防止自适应重试递归 / Internal flag to prevent adaptive retry recursion

	// V12: Memory class filter / 记忆层级过滤
	MemoryClass string `json:"memory_class,omitempty"` // 过滤指定层级 / Filter by memory class

	// V23: Core memory injection / 核心记忆注入
	IncludeCore *bool `json:"include_core,omitempty"` // 默认 true，自动注入 core memory / Default true, auto-inject core memories
}

// ListRequest 分页列表请求 / Paginated list request DTO
type ListRequest struct {
	TeamID string `json:"team_id,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// SearchFilters 检索过滤条件 / Search filter conditions
type SearchFilters struct {
	Scope          string     `json:"scope,omitempty"`
	ContextID      string     `json:"context_id,omitempty"`
	ContextPath    string     `json:"context_path,omitempty"`
	Kind           string     `json:"kind,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
	HappenedAfter  *time.Time `json:"happened_after,omitempty"`
	HappenedBefore *time.Time `json:"happened_before,omitempty"`
	SourceType     string     `json:"source_type,omitempty"`
	MinStrength    float64    `json:"min_strength,omitempty"`
	IncludeExpired bool       `json:"include_expired,omitempty"`

	// V3: 知识分级 + LLM 过滤 / Retention tier and message role filters
	RetentionTier string `json:"retention_tier,omitempty"`
	MessageRole   string `json:"message_role,omitempty"`

	// V6: 身份过滤（API 层自动注入）/ Identity filtering (auto-injected by API layer)
	TeamID  string `json:"-"` // 不从 JSON 反序列化 / Not deserialized from JSON
	OwnerID string `json:"-"` // 不从 JSON 反序列化 / Not deserialized from JSON
}

// TimelineRequest 时间线请求 / Timeline query request DTO
type TimelineRequest struct {
	Scope     string     `json:"scope,omitempty"`
	SourceRef string     `json:"source_ref,omitempty"` // B6: 按来源引用过滤 / Filter by source_ref
	After     *time.Time `json:"after,omitempty"`
	Before    *time.Time `json:"before,omitempty"`
	Limit     int        `json:"limit,omitempty"`

	// V6: 身份过滤 / Identity filtering
	TeamID  string `json:"-"`
	OwnerID string `json:"-"`
}

// CreateContextRequest 创建上下文请求 / Create context request DTO
type CreateContextRequest struct {
	Name        string         `json:"name" binding:"required"`
	ParentID    string         `json:"parent_id,omitempty"`
	Scope       string         `json:"scope,omitempty"`
	ContextType string         `json:"context_type,omitempty"`
	Description string         `json:"description,omitempty"`
	Mission     string         `json:"mission,omitempty"`     // V13: 上下文使命 / Context mission
	Directives  string         `json:"directives,omitempty"`  // V13: 行为指令 / Behavioral directives
	Disposition string         `json:"disposition,omitempty"` // V13: 性格/风格 / Response style
	Metadata    map[string]any `json:"metadata,omitempty"`
	SortOrder   int            `json:"sort_order,omitempty"`
}

// UpdateContextRequest 更新上下文请求 / Update context request DTO
type UpdateContextRequest struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	ContextType *string        `json:"context_type,omitempty"`
	Mission     *string        `json:"mission,omitempty"`     // V13: 上下文使命 / Context mission
	Directives  *string        `json:"directives,omitempty"`  // V13: 行为指令 / Behavioral directives
	Disposition *string        `json:"disposition,omitempty"` // V13: 性格/风格 / Response style
	Metadata    map[string]any `json:"metadata,omitempty"`
	SortOrder   *int           `json:"sort_order,omitempty"`
}

// MoveContextRequest 移动上下文请求 / Move context request DTO
type MoveContextRequest struct {
	NewParentID string `json:"new_parent_id"` // 空字符串表示移到根
}

// CreateEntityRequest 创建实体请求 / Create entity request DTO
type CreateEntityRequest struct {
	Name        string         `json:"name" binding:"required"`
	EntityType  string         `json:"entity_type" binding:"required"`
	Scope       string         `json:"scope,omitempty"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// UpdateEntityRequest 更新实体请求 / Update entity request DTO
type UpdateEntityRequest struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// CreateEntityRelationRequest 创建实体关系请求 / Create entity relation request DTO
type CreateEntityRelationRequest struct {
	SourceID     string         `json:"source_id" binding:"required"`
	TargetID     string         `json:"target_id" binding:"required"`
	RelationType string         `json:"relation_type" binding:"required"`
	Weight       float64        `json:"weight,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// CreateMemoryEntityRequest 创建记忆-实体关联请求 / Create memory-entity association request DTO
type CreateMemoryEntityRequest struct {
	MemoryID string `json:"memory_id" binding:"required"`
	EntityID string `json:"entity_id" binding:"required"`
	Role     string `json:"role,omitempty"`
}

// CreateDocumentRequest 创建文档请求 / Create document request DTO
type CreateDocumentRequest struct {
	Name      string         `json:"name" binding:"required"`
	DocType   string         `json:"doc_type" binding:"required"`
	Scope     string         `json:"scope,omitempty"`
	ContextID string         `json:"context_id,omitempty"`
	FilePath  string         `json:"file_path,omitempty"`
	FileSize  int64          `json:"file_size,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// IngestConversationRequest 批量对话导入请求 / Batch conversation ingest request DTO
type IngestConversationRequest struct {
	Provider   string                `json:"provider"`              // openai / claude / langchain / generic
	ExternalID string                `json:"external_id,omitempty"` // thread_id 等外部标识
	Scope      string                `json:"scope,omitempty"`
	ContextID  string                `json:"context_id,omitempty"` // 已有 context 则复用
	Messages   []ConversationMessage `json:"messages" binding:"required"`
	Metadata   map[string]any        `json:"metadata,omitempty"`
}

// ConversationMessage 对话消息 / Conversation message within an ingest request
type ConversationMessage struct {
	Role       string         `json:"role" binding:"required"`
	Content    string         `json:"content" binding:"required"`
	TurnNumber int            `json:"turn_number,omitempty"` // 0=自动分配
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// ReflectRequest 反思请求 / Reflect request DTO
type ReflectRequest struct {
	Question    string `json:"question" binding:"required"`
	ContextID   string `json:"context_id,omitempty"` // 加载行为约束 / Load behavioral constraints
	Scope       string `json:"scope,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
	OwnerID     string `json:"owner_id,omitempty"`
	MaxRounds   int    `json:"max_rounds,omitempty"`
	TokenBudget int    `json:"token_budget,omitempty"`
	AutoSave    *bool  `json:"auto_save,omitempty"`
}

// ReflectResponse 反思响应 / Reflect response DTO
type ReflectResponse struct {
	Result      string         `json:"result"`
	NewMemoryID string         `json:"new_memory_id,omitempty"`
	Trace       []ReflectRound `json:"trace"`
	Sources     []string       `json:"sources"`
	Metadata    ReflectMeta    `json:"metadata"`
}

// ReflectRound 反思轮次记录 / Reflect round trace
type ReflectRound struct {
	Round        int      `json:"round"`
	Query        string   `json:"query"`
	RetrievedIDs []string `json:"retrieved_ids"`
	Reasoning    string   `json:"reasoning"`
	NeedMore     bool     `json:"need_more"`
	ParseMethod  string   `json:"parse_method"`
	TokensUsed   int      `json:"tokens_used"`
}

// ReflectMeta 反思元数据 / Reflect metadata
type ReflectMeta struct {
	RoundsUsed     int  `json:"rounds_used"`
	TotalTokens    int  `json:"total_tokens"`
	EvidenceTokens int  `json:"evidence_tokens"`  // B3: 检索证据 token 消耗 / Token count consumed by retrieved evidence
	ParseFallbacks int  `json:"parse_fallbacks"`
	Timeout        bool `json:"timeout"`
	QueryDeduped   bool `json:"query_deduped"`
}

// ExtractRequest 实体抽取请求 / Entity extraction request
type ExtractRequest struct {
	MemoryID string `json:"memory_id"`
	Content  string `json:"content"`
	Scope    string `json:"scope"`
	TeamID   string `json:"team_id"`
}

// ExtractResponse 实体抽取响应 / Entity extraction response
type ExtractResponse struct {
	Entities    []ExtractedEntityResult   `json:"entities"`
	Relations   []ExtractedRelationResult `json:"relations"`
	Normalized  int                       `json:"normalized"`
	TotalTokens int                       `json:"total_tokens"`
}

// ExtractedEntityResult 单个实体抽取结果 / Single entity extraction result
type ExtractedEntityResult struct {
	EntityID       string `json:"entity_id"`
	Name           string `json:"name"`
	EntityType     string `json:"entity_type"`
	Reused         bool   `json:"reused"`
	NormalizedFrom string `json:"normalized_from,omitempty"`
}

// ExtractedRelationResult 单个关系抽取结果 / Single relation extraction result
type ExtractedRelationResult struct {
	RelationID   string `json:"relation_id"`
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Skipped      bool   `json:"skipped"`
}

// RetrieveResponse 检索响应（增强）/ Enhanced retrieve response
type RetrieveResponse struct {
	Results     []*SearchResult `json:"results"`
	TotalTokens int             `json:"total_tokens"`
	Truncated   bool            `json:"truncated"`
}
