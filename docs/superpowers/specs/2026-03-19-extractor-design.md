# Extractor 自动实体抽取设计规格 / Extractor Auto Entity Extraction Design Spec

**日期 / Date:** 2026-03-19
**状态 / Status:** Approved
**阶段 / Phase:** Phase 2 Task 2
**预估工期 / Estimated effort:** 2 weeks
**依赖 / Dependency:** Task 1 Reflect Engine（`internal/llm/` 包）

## 概述 / Overview

Extractor 是 IClude Phase 2 的第二项核心功能——在写入记忆时由 LLM 自动抽取结构化知识（实体 + 关系），写入知识图谱。支持两阶段实体规范化，避免重复实体。

Extractor is the second core feature of IClude Phase 2 — automatically extracts structured knowledge (entities + relations) via LLM when writing memories, and stores them in the knowledge graph. Supports two-phase entity normalization to avoid duplicate entities.

## 设计决策 / Design Decisions

| 决策项 / Decision | 选择 / Choice | 原因 / Rationale |
|-------------------|---------------|-------------------|
| 集成方式 / Integration | Manager 内部调用 + 独立端点 / Internal Manager call + standalone endpoint | auto_extract 覆盖常见场景；独立端点支持补提取/重提取 / auto_extract covers common case; standalone endpoint supports re-extraction |
| 实体规范化 / Normalization | 两阶段：精确匹配 → LLM 辅助 / Two-phase: exact match → LLM-assisted | 精确匹配覆盖大部分情况，LLM 辅助只在未命中时触发，兼顾性能和准确性 / Exact match covers most cases, LLM only triggered on miss |
| 抽取时机 / Timing | 同步 / Synchronous | Phase 2 先跑通，避免 goroutine 复杂度。auto_extract 可选，不开不影响性能 / Get it working first, async optimization in Task 7 |
| 抽取内容 / Output | 只抽实体和关系 / Entities and relations only | YAGNI，时间信息已在 happened_at，摘要是独立关注点 / Time info already in happened_at, summary is a separate concern |
| 降级策略 / Fallback | 解析失败返回空结果 / Return empty on parse failure | 宁可不抽也不抽错 / Better to extract nothing than extract wrong |

## 1. Extractor 核心 / Extractor Core（`internal/memory/extractor.go`）

### 1.1 结构体 / Struct

```go
// Extractor 自动实体抽取器 / Auto entity extractor
type Extractor struct {
    llm          llm.Provider
    graphManager *GraphManager
    memStore     store.MemoryStore // 独立端点获取已有记忆 / for standalone endpoint to fetch existing memory
    cfg          config.ExtractConfig
}

func NewExtractor(llm llm.Provider, graphManager *GraphManager, memStore store.MemoryStore, cfg config.ExtractConfig) *Extractor

// Extract 从文本中抽取实体和关系 / Extract entities and relations from text
func (e *Extractor) Extract(ctx context.Context, req *model.ExtractRequest) (*model.ExtractResponse, error)
```

### 1.2 LLM 抽取输出 JSON 结构 / LLM Output JSON Structure

```go
// extractLLMOutput LLM实体抽取输出（内部使用）/ LLM entity extraction output (internal)
type extractLLMOutput struct {
    Entities  []extractedEntity   `json:"entities"`
    Relations []extractedRelation `json:"relations"`
}

// extractedEntity 抽取的实体 / Extracted entity
type extractedEntity struct {
    Name        string `json:"name"`        // 实体名 / entity name
    EntityType  string `json:"entity_type"` // person / org / concept / tool / location
    Description string `json:"description"` // 简短描述 / brief description
}

// extractedRelation 抽取的关系 / Extracted relation
type extractedRelation struct {
    Source       string `json:"source"`        // 源实体名（引用 entities 中的 name）/ source entity name
    Target       string `json:"target"`        // 目标实体名 / target entity name
    RelationType string `json:"relation_type"` // uses / knows / belongs_to / related_to
}
```

### 1.3 核心流程 / Core Flow

```
输入 / Input: ExtractRequest{MemoryID, Content, Scope, TeamID}

0) 超时保护 / Timeout protection:
   ctx = context.WithTimeout(ctx, cfg.Timeout) // 默认 30s / default 30s

1) LLM 调用 / LLM Call:
   response_format: { type: "json_object" }
   temperature: 0.1
   system prompt: 硬编码实体抽取指令 / hardcoded entity extraction instructions
   user prompt: "从以下文本中抽取实体和关系" + content
   → extractLLMOutput（三级 fallback 解析）

2) 实体规范化（两阶段）/ Entity Normalization (two-phase):
   对每个 extractedEntity / for each extractedEntity:

   阶段 1：精确匹配 / Phase 1: Exact Match
     GraphStore.ListEntities(scope, entityType, limit=100)
     strings.EqualFold(existing.Name, extracted.Name)
     命中 → 复用 entityID，标记 reused=true / hit → reuse entityID, mark reused=true

     > **团队隔离说明 / Team isolation note:** scope 字段已编码团队信息（如 scope="team1/user/alice"），
     > Entity 表有 UNIQUE(name, entity_type, scope) 约束，因此按 scope 过滤即可保证团队隔离。
     > The scope field encodes team information (e.g., scope="team1/user/alice"),
     > and the entities table has a UNIQUE(name, entity_type, scope) constraint,
     > so filtering by scope is sufficient for team isolation.
     >
     > **已知限制 / Known limitation:** limit=100 在单个 scope+type 下超过 100 个实体时会遗漏匹配。
     > Phase 2 可接受，后续可优化为 FindEntityByName(scope, name, entityType) 专用查询。
     > limit=100 may miss matches when a scope+type has >100 entities.
     > Acceptable for Phase 2, can optimize later with a dedicated FindEntityByName query.

   阶段 2：LLM 辅助判断（仅阶段1未命中时）/ Phase 2: LLM-Assisted (only when phase 1 misses)
     条件：cfg.NormalizeEnabled == true / condition: cfg.NormalizeEnabled == true
     从同 scope 同 entityType 取 top-N 候选 / fetch top-N candidates from same scope+type
     LLM prompt: "以下新实体是否与某个候选实体指同一事物？"
     输出 JSON / output JSON:
       {"match": true, "matched_entity": "候选name"} 或 / or
       {"match": false}
     命中 → 复用，标记 normalized_from=原始名 / hit → reuse, mark normalized_from
     未命中 → GraphManager.CreateEntity() / miss → create new entity

3) 写入关系 / Write Relations:
   对每个 extractedRelation / for each extractedRelation:
     查找 source/target 对应的 entityID / find entityIDs for source/target
     去重检查：GetEntityRelations → 已存在则跳过，标记 skipped=true
     / dedup check: skip if exists, mark skipped=true
     GraphManager.CreateRelation()

4) 写入记忆-实体关联 / Write Memory-Entity Associations:
   对每个实体 / for each entity:
     GraphManager.CreateMemoryEntity(memoryID, entityID, role="mentioned")
     PK 约束自动去重 / PK constraint auto-dedup

5) 返回 / Return: ExtractResponse
```

### 1.4 System Prompt 摘要 / System Prompt Summary

```
System Prompt 核心要点 / Core points:
1. 角色：你是一个知识抽取引擎，从文本中提取结构化的实体和关系
   Role: You are a knowledge extraction engine that extracts structured entities and relations from text
2. 实体类型限制：person / org / concept / tool / location
   Entity types: person / org / concept / tool / location
3. 关系类型限制：uses / knows / belongs_to / related_to
   Relation types: uses / knows / belongs_to / related_to
4. 输出格式：严格 JSON，entities[] + relations[]
   Output: strict JSON with entities[] + relations[]
5. 去重：同一文本中重复的实体只出现一次
   Dedup: duplicate entities in same text appear only once
6. 关系方向：source 是动作发起方，target 是对象
   Direction: source is the actor, target is the object
```

### 1.5 三级 Fallback 解析 / 3-Level Fallback Parsing

```
L1: json.Unmarshal → validate（entities 类型合法、关系引用实体存在）
    / validate (entity types valid, relation references exist in entities)
L2: 正则提取 JSON 片段 → Unmarshal → validate / regex extract → parse → validate
L3: 追加提示重试 1 次 / retry with hint once
L4: 返回 ErrExtractParseFailed 错误 / return ErrExtractParseFailed error
```

> **错误处理契约 / Error handling contract:**
> - `Extract()` 方法本身在 L4 时返回 `ErrExtractParseFailed` 错误
> - **Manager.Create 调用方**：捕获错误后 `logger.Warn`，不回滚记忆创建——最佳努力，宁可不抽也不抽错
> - **独立端点调用方**：将错误传播为 HTTP 502
>
> `Extract()` always returns `ErrExtractParseFailed` on L4. The **caller** decides how to handle:
> - Manager.Create: catches error, logs warning, does not rollback — best-effort
> - Standalone endpoint: propagates error as HTTP 502

> **规范化 LLM 失败处理 / Normalization LLM failure handling:** 当阶段 2 规范化 LLM 调用失败时，安全降级为创建新实体（而非中止整个抽取）。
> When phase 2 normalization LLM call fails, safely degrade to creating a new entity (rather than aborting the entire extraction).

### 1.6 校验规则 / Validation Rules

```go
// validateExtractOutput 校验LLM抽取输出 / Validate LLM extraction output
func validateExtractOutput(output *extractLLMOutput, cfg config.ExtractConfig) error {
    // 实体数量上限 / entity count limit
    if len(output.Entities) > cfg.MaxEntities { truncate }
    // entity_type 合法性 / entity_type validity
    validTypes := {"person", "org", "concept", "tool", "location"}
    // relation_type 合法性 / relation_type validity
    validRelTypes := {"uses", "knows", "belongs_to", "related_to"}
    // 关系引用的实体必须存在于 entities 列表中 / relation references must exist in entities list
}
```

## 2. 请求/响应模型 / Request/Response Models（`internal/model/request.go` 新增 / additions）

```go
// ExtractRequest 实体抽取请求 / Entity extraction request
type ExtractRequest struct {
    MemoryID string // 已有记忆ID（独立端点用）/ existing memory ID (for standalone endpoint)
    Content  string // 文本内容（Manager.Create 内部调用时传入）/ text content (from Manager.Create)
    Scope    string // 命名空间 / namespace
    TeamID   string
}

// ExtractResponse 实体抽取响应 / Entity extraction response
type ExtractResponse struct {
    Entities    []ExtractedEntityResult   `json:"entities"`    // 抽取的实体 / extracted entities
    Relations   []ExtractedRelationResult `json:"relations"`   // 抽取的关系 / extracted relations
    Normalized  int                       `json:"normalized"`  // 规范化合并的实体数 / normalized entity count
    TotalTokens int                       `json:"total_tokens"` // LLM token 消耗 / LLM token consumption
}

// ExtractedEntityResult 单个实体抽取结果 / Single entity extraction result
type ExtractedEntityResult struct {
    EntityID       string `json:"entity_id"`       // 实体ID（新建或复用）/ entity ID (new or reused)
    Name           string `json:"name"`
    EntityType     string `json:"entity_type"`
    Reused         bool   `json:"reused"`          // 是否复用已有实体 / reused existing entity
    NormalizedFrom string `json:"normalized_from"` // 规范化前的原始名 / original name before normalization
}

// ExtractedRelationResult 单个关系抽取结果 / Single relation extraction result
type ExtractedRelationResult struct {
    RelationID   string `json:"relation_id"`
    SourceID     string `json:"source_id"`
    TargetID     string `json:"target_id"`
    RelationType string `json:"relation_type"`
    Skipped      bool   `json:"skipped"` // 因已存在而跳过 / skipped due to existing
}
```

### CreateMemoryRequest 新增字段 / New field

```go
type CreateMemoryRequest struct {
    // ... 现有字段 / existing fields ...
    AutoExtract bool // 新增：是否自动抽取实体 / new: auto extract entities on create
}
```

## 3. Manager 集成 / Manager Integration（`internal/memory/manager.go` 修改 / modification）

### Manager struct 新增字段 / New field

```go
type Manager struct {
    memStore     store.MemoryStore
    vecStore     store.VectorStore
    embedder     store.Embedder
    tagStore     store.TagStore
    contextStore store.ContextStore
    extractor    *Extractor // 新增，可为 nil / new, may be nil
}

// NewManager 构造函数增加 extractor 参数（可传 nil）
// Constructor adds extractor parameter (nil-able), consistent with existing optional dependency pattern
// func NewManager(memStore, vecStore, embedder, tagStore, contextStore, extractor) *Manager
```

### Create() 集成 / Create() integration

```go
func (m *Manager) Create(ctx context.Context, req *model.CreateMemoryRequest) (*model.Memory, error) {
    // ... 现有流程（SQLite写入、标签、Context、向量）
    // ... existing flow (SQLite write, tags, context, vector)

    // 新增：自动实体抽取（同步，最佳努力）
    // New: auto entity extraction (synchronous, best-effort)
    if req.AutoExtract && m.extractor != nil {
        _, err := m.extractor.Extract(ctx, &model.ExtractRequest{
            MemoryID: mem.ID,
            Content:  mem.Content,
            Scope:    mem.Scope,
            TeamID:   mem.TeamID,
        })
        if err != nil {
            logger.Warn("auto extract failed", zap.String("memory_id", mem.ID), zap.Error(err))
            // 最佳努力，不回滚 / best-effort, no rollback
        }
    }

    return mem, nil
}
```

## 4. API 层 / API Layer

### 4.1 Handler（`internal/api/extract_handler.go`）

```go
// ExtractHandler 实体抽取处理器 / Entity extraction handler
type ExtractHandler struct {
    extractor *memory.Extractor
}

func NewExtractHandler(extractor *memory.Extractor) *ExtractHandler

// Extract 对已有记忆触发实体抽取 / Trigger entity extraction for existing memory
// POST /v1/memories/:id/extract
func (h *ExtractHandler) Extract(c *gin.Context)
```

### 4.2 请求/响应示例 / Request/Response Examples

**请求 / Request:** `POST /v1/memories/mem-123/extract`（无 body / no body）

**响应 / Response:**
```json
{
  "code": 0,
  "data": {
    "entities": [
      {"entity_id": "e1", "name": "Go", "entity_type": "tool", "reused": true, "normalized_from": ""},
      {"entity_id": "e2", "name": "Alice", "entity_type": "person", "reused": false, "normalized_from": ""},
      {"entity_id": "e3", "name": "Alibaba", "entity_type": "org", "reused": true, "normalized_from": "阿里巴巴"}
    ],
    "relations": [
      {"relation_id": "r1", "source_id": "e2", "target_id": "e1", "relation_type": "uses", "skipped": false},
      {"relation_id": "r2", "source_id": "e2", "target_id": "e3", "relation_type": "belongs_to", "skipped": false}
    ],
    "normalized": 1,
    "total_tokens": 850
  }
}
```

### 4.3 路由注册 / Route Registration（`router.go` 修改 / modification）

```go
// RouterDeps 新增 / addition
Extractor *memory.Extractor // 可为 nil / may be nil

// 挂在 memories 路由组下 / nested under memories route group
if deps.Extractor != nil {
    extractHandler := NewExtractHandler(deps.Extractor)
    memoriesGroup.POST("/:id/extract", extractHandler.Extract)
}
```

> **端点选择说明 / Endpoint choice:** `POST /v1/memories/:id/extract` 而非 `POST /v1/extract`——因为抽取始终针对一条具体记忆，RESTful 语义更清晰。
> Extraction always targets a specific memory, making the nested resource path more RESTful.

## 5. 启动集成 / Startup Integration（`cmd/server/main.go`）

```go
// 初始化 Extractor（需要 llmProvider + graphManager 都存在）
// Initialize Extractor (requires both llmProvider and graphManager)
var extractor *memory.Extractor
if llmProvider != nil && graphManager != nil {
    extractor = memory.NewExtractor(llmProvider, graphManager, stores.MemoryStore, cfg.Extract)
}

// Extractor 通过构造函数注入 Manager（与 vecStore/tagStore/contextStore 同模式）
// Extractor injected via constructor (same pattern as vecStore/tagStore/contextStore)
memManager = memory.NewManager(stores.MemoryStore, stores.VectorStore, embedder,
    tagStore, contextStore, extractor)

// 注入 RouterDeps / Inject into RouterDeps
deps.Extractor = extractor
```

## 6. 配置变更 / Config Changes

### 6.1 `internal/config/config.go` 新增 / Addition

```go
// ExtractConfig 实体抽取配置 / Entity extraction config
type ExtractConfig struct {
    MaxEntities         int           `mapstructure:"max_entities"`          // 默认 20 / default 20
    MaxRelations        int           `mapstructure:"max_relations"`         // 默认 30 / default 30
    NormalizeEnabled    bool          `mapstructure:"normalize_enabled"`     // 默认 true / default true
    NormalizeCandidates int           `mapstructure:"normalize_candidates"`  // 默认 20 / default 20
    Timeout             time.Duration `mapstructure:"timeout"`               // 默认 30s / default 30s
}

// Config 顶层配置新增 / Top-level config addition
type Config struct {
    // ... 现有字段 / existing fields ...
    Extract ExtractConfig `mapstructure:"extract"` // 新增 / new
}
```

**Viper 默认值 / Viper defaults:**
```go
viper.SetDefault("extract.max_entities", 20)
viper.SetDefault("extract.max_relations", 30)
viper.SetDefault("extract.normalize_enabled", true)
viper.SetDefault("extract.normalize_candidates", 20)
viper.SetDefault("extract.timeout", "30s")
```

### 6.2 `config.yaml` 新增段 / New config section

```yaml
extract:
  max_entities: 20
  max_relations: 30
  normalize_enabled: true
  normalize_candidates: 20
  timeout: 30s
```

### 6.3 Sentinel Errors / 哨兵错误（`internal/model/errors.go` 新增 / additions）

```go
// Extractor 错误 / Extractor errors
var (
    // ErrExtractTimeout 实体抽取超时 / entity extraction timeout
    ErrExtractTimeout = errors.New("extract: timeout exceeded")
    // ErrExtractLLMFailed 实体抽取LLM调用失败 / entity extraction LLM call failed
    ErrExtractLLMFailed = errors.New("extract: llm call failed")
    // ErrExtractParseFailed 实体抽取输出解析全部失败 / entity extraction output parse failed
    ErrExtractParseFailed = errors.New("extract: output parse failed")
)
```

### 6.4 HTTP 状态码映射 / HTTP Status Code Mapping

| Sentinel Error | HTTP Status | 说明 / Description |
|---------------|-------------|---------------------|
| memory not found | 404 Not Found | 独立端点传入的 memoryID 不存在 / memory ID not found for standalone endpoint |
| `ErrExtractTimeout` | 408 Request Timeout | LLM 调用超时 / LLM call timeout |
| `ErrExtractLLMFailed` | 502 Bad Gateway | LLM 调用失败 / LLM call failure |
| `ErrExtractParseFailed` | 502 Bad Gateway | LLM 输出解析全部失败 / LLM output parse all failed |

## 7. 测试计划 / Test Plan

### 7.1 Extractor 核心测试 / Extractor Core Tests（`testing/memory/extractor_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestExtract_BasicEntitiesAndRelations | 正常抽取实体+关系，全部新建 / Normal extraction, all new entities |
| TestExtract_EntityReuse_ExactMatch | 精确匹配复用已有实体 / Exact match reuses existing entity |
| TestExtract_EntityNormalize_LLMMatch | LLM 辅助规范化命中 / LLM normalization match |
| TestExtract_EntityNormalize_LLMNoMatch | LLM 辅助判断不匹配，创建新实体 / LLM normalization no match, create new |
| TestExtract_NormalizeDisabled | 配置关闭规范化，只走精确匹配 / Normalization disabled, exact match only |
| TestExtract_RelationDedup | 已存在关系跳过，标记 skipped / Existing relation skipped |
| TestExtract_EmptyContent | 空内容返回空结果 / Empty content returns empty result |
| TestExtract_LLMFailed | LLM 调用失败返回错误 / LLM failure returns error |
| TestExtract_ParseFallback | JSON 解析全部失败返回空结果 / Parse failure returns empty result |
| TestExtract_Timeout | 超时处理 / Timeout handling |
| TestExtract_NormalizeLLMFailed_CreatesNew | 规范化 LLM 失败时创建新实体 / Creates new entity when normalization LLM fails |

### 7.2 JSON 解析测试 / JSON Parsing Tests（`testing/memory/extractor_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestParseExtractOutput_ValidJSON | 正常 JSON 解析 / Normal JSON parsing |
| TestParseExtractOutput_InvalidEntityType | 非法 entity_type 校验 / Invalid entity_type validation |
| TestParseExtractOutput_ExtractFromText | 文本中提取 JSON 片段 / Extract JSON from text |
| TestParseExtractOutput_Fallback | 全部失败返回空 / All failed returns empty |

### 7.3 Manager.Create 集成测试 / Manager.Create Integration Tests（`testing/memory/manager_extract_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestManagerCreate_AutoExtractTrue | auto_extract=true 触发抽取 / auto_extract=true triggers extraction |
| TestManagerCreate_AutoExtractFalse | auto_extract=false 不触发 / auto_extract=false skips extraction |
| TestManagerCreate_ExtractorNil | extractor 为 nil 不触发 / extractor nil skips extraction |
| TestManagerCreate_ExtractFails_MemoryStillCreated | 抽取失败不影响记忆创建 / Extract failure doesn't affect memory creation |

### 7.4 API 集成测试 / API Integration Tests（`testing/api/extract_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestExtractAPI_Success | POST /v1/memories/:id/extract 正常流程 / Normal flow |
| TestExtractAPI_MemoryNotFound | 记忆不存在返回 404 / Memory not found returns 404 |
| TestExtractAPI_LLMFailure | LLM 失败返回 502 / LLM failure returns 502 |

## 8. 文件变更清单 / File Change List

### 新增 5 个文件 / 5 New Files

| 文件 / File | 说明 / Description |
|-------------|---------------------|
| `internal/memory/extractor.go` | Extractor 核心（LLM 抽取 + 两阶段规范化 + GraphStore 写入）/ Extractor core |
| `internal/api/extract_handler.go` | POST /v1/memories/:id/extract Handler |
| `testing/memory/extractor_test.go` | Extractor 核心 + 解析测试 / Extractor core + parsing tests |
| `testing/memory/manager_extract_test.go` | Manager.Create 集成测试 / Manager.Create integration tests |
| `testing/api/extract_test.go` | API 集成测试 / API integration tests |

### 修改 6 个文件 / 6 Modified Files

| 文件 / File | 变更 / Changes |
|-------------|----------------|
| `internal/model/request.go` | CreateMemoryRequest.AutoExtract + ExtractRequest/Response 及相关 DTO / Add AutoExtract + Extract DTOs |
| `internal/model/errors.go` | 新增 Extract sentinel errors / Add Extract sentinel errors |
| `internal/config/config.go` | ExtractConfig + Config.Extract + Viper defaults / Add ExtractConfig |
| `internal/memory/manager.go` | Manager.extractor 字段 + SetExtractor() + Create() 集成 auto_extract / Add extractor field + integration |
| `internal/api/router.go` | RouterDeps.Extractor + 条件注册 / RouterDeps.Extractor + conditional registration |
| `cmd/server/main.go` | 初始化 Extractor → 注入 Manager → 注入 RouterDeps / Initialize → inject |
