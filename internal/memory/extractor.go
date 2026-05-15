// Package memory 提供记忆管理核心业务逻辑 / Core memory management business logic
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenutil"

	"go.uber.org/zap"
)

// 解析方式常量 / Parse method constants
const (
	ExtractParseJSON     = "json"
	ExtractParseExtract  = "extract"
	ExtractParseRetry    = "retry"
	ExtractParseFallback = "fallback"
)

// defaultEntityTypes 默认实体类型（配置为空时兜底）/ Default entity types when config is empty
var defaultEntityTypes = map[string]bool{
	"person": true, "org": true, "concept": true, "tool": true, "location": true,
}

// defaultRelationTypes 默认关系类型（配置为空时兜底）/ Default relation types when config is empty
var defaultRelationTypes = map[string]bool{
	"uses": true, "knows": true, "belongs_to": true, "related_to": true,
	"works_at": true, "colleague": true, "located_in": true, "created": true,
	"participated_in": true, "founded": true, "visited": true, "member_of": true,
	"reports_to": true, "manages": true, "friend_of": true, "part_of": true,
}

// extractLLMOutput LLM实体抽取输出（内部使用）/ LLM entity extraction output (internal)
type extractLLMOutput struct {
	Entities  []extractedEntity   `json:"entities"`
	Relations []extractedRelation `json:"relations"`
}

// extractedEntity 抽取的实体 / Extracted entity
type extractedEntity struct {
	Name        string `json:"name"`
	EntityType  string `json:"entity_type"`
	Description string `json:"description"`
}

// extractedRelation 抽取的关系 / Extracted relation
type extractedRelation struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	RelationType string `json:"relation_type"`
}

// batchExtractInput 批量抽取结构化输入（带 index）/ Structured input for batch extraction with index markers
type batchExtractInput struct {
	Memories []batchMemoryItem `json:"memories"`
}

type batchMemoryItem struct {
	Index   int    `json:"index"`
	Content string `json:"content"`
}

// batchExtractOutput 批量抽取结构化输出（按 index 分组）/ Structured output grouped by memory index
type batchExtractOutput struct {
	Results []batchExtractResult `json:"results"`
}

type batchExtractResult struct {
	Index     int                 `json:"index"`
	Entities  []extractedEntity   `json:"entities"`
	Relations []extractedRelation `json:"relations"`
}

// normalizeLLMOutput 规范化LLM输出 / Normalization LLM output
type normalizeLLMOutput struct {
	Match         bool   `json:"match"`
	MatchedEntity string `json:"matched_entity"`
}


const normalizeSystemPrompt = `You are an entity normalization engine. Determine if a new entity refers to the same thing as one of the candidate entities.

Output strict JSON: {"match": true, "matched_entity": "candidate name"} or {"match": false}`

// Extractor 自动实体抽取器 / Auto entity extractor
type Extractor struct {
	llm            llm.Provider
	fastLLM        llm.Provider    // 快速模型（仅实体名抽取，可为 nil）/ Fast model for entity-only extraction (may be nil)
	taskQueue      TaskEnqueuer    // 异步关系抽取队列（可为 nil）/ Async relation extraction queue (may be nil)
	graphManager   *GraphManager
	memStore       store.MemoryStore
	candidateStore store.CandidateStore
	cfg            config.ExtractConfig
	entityTypes    map[string]bool // 从配置加载 / Loaded from config
	relationTypes  map[string]bool // 从配置加载 / Loaded from config

	// 自适应批次阈值：首次 formBatches 调用时通过 llm.DetectContextWindow 探测，之后缓存。
	// Adaptive batch threshold: detected on first formBatches call, cached thereafter.
	detectedThreshold   int
	detectThresholdOnce sync.Once
}

// NewExtractor 创建实体抽取器 / Create entity extractor
func NewExtractor(llmProvider llm.Provider, graphManager *GraphManager, memStore store.MemoryStore, candidateStore store.CandidateStore, cfg config.ExtractConfig) *Extractor {
	// 从配置构建类型白名单 / Build type allow-lists from config
	entityTypes := make(map[string]bool, len(cfg.EntityTypes))
	for _, t := range cfg.EntityTypes {
		entityTypes[strings.ToLower(t)] = true
	}
	if len(entityTypes) == 0 {
		entityTypes = defaultEntityTypes
	}

	relationTypes := make(map[string]bool, len(cfg.RelationTypes))
	for _, t := range cfg.RelationTypes {
		relationTypes[strings.ToLower(t)] = true
	}
	if len(relationTypes) == 0 {
		relationTypes = defaultRelationTypes
	}

	return &Extractor{
		llm:            llmProvider,
		graphManager:   graphManager,
		memStore:       memStore,
		candidateStore: candidateStore,
		cfg:            cfg,
		entityTypes:    entityTypes,
		relationTypes: relationTypes,
	}
}

// WithFastLLM 注入快速模型（用于实体名抽取）/ Inject fast model (used for entity-only extraction)
func (e *Extractor) WithFastLLM(provider llm.Provider) {
	e.fastLLM = provider
}

// SetExtractorQueue 注入任务队列（用于异步关系抽取）/ Inject task queue (used for async relation extraction)
func (e *Extractor) SetExtractorQueue(q TaskEnqueuer) {
	e.taskQueue = q
}

// GetMemoryStore 获取记忆存储（供 API handler 使用）/ Get memory store (for API handler)
func (e *Extractor) GetMemoryStore() store.MemoryStore {
	return e.memStore
}

// buildExtractPrompt 构建完整抽取提示词（实体+关系）/ Build full extraction prompt (entities + relations)
func (e *Extractor) buildExtractPrompt() string {
	entityList := strings.Join(mapKeys(e.entityTypes), ", ")
	relationList := strings.Join(mapKeys(e.relationTypes), ", ")
	return fmt.Sprintf(`You are a knowledge extraction engine. Extract entities and relationships from the given text. The text may be in Chinese or English.

Entity type rules (MUST use exactly one of: %s):
- person  : any human name, including Chinese names (e.g. 张明, 李华, 王芳)
- org     : companies, organizations, institutions (e.g. 阿里巴巴, Google)
- location: places, cities, countries (e.g. 北京, 上海, 中国)
- tool    : software, frameworks, products, technologies
- concept : abstract ideas — use ONLY when none of the above apply

Relation type MUST be exactly one of: %s

Output strict JSON: {"entities":[{"name":"...","entity_type":"...","description":"..."}],"relations":[{"source":"...","target":"...","relation_type":"..."}]}
- entity_type and relation_type MUST be in English
- entity names use the original language from the text
- Deduplicate entities
- Only extract what is clearly stated in the text`, entityList, relationList)
}

// buildEntityOnlyPrompt 构建仅实体抽取的提示词（不含关系）/ Build entity-only extraction prompt (no relations)
func (e *Extractor) buildEntityOnlyPrompt() string {
	entityList := strings.Join(mapKeys(e.entityTypes), ", ")
	return fmt.Sprintf(`You are a knowledge extraction engine. Extract named entities from the given text. The text may be in Chinese or English.

Entity type rules (MUST use exactly one of: %s):
- person  : any human name, including Chinese names (e.g. 张明, 李华, 王芳)
- org     : companies, organizations, institutions (e.g. 阿里巴巴, Google, 北京大学)
- location: places, cities, countries, addresses (e.g. 北京, 杭州, 中国)
- tool    : software, frameworks, products, technologies (e.g. Python, ChatGPT, iPhone)
- concept : abstract ideas, topics, events — use ONLY when none of the above apply

Output strict JSON: {"entities":[{"name":"...","entity_type":"...","description":"..."}],"relations":[]}
Rules:
- entity_type MUST be exactly one of the types listed above, in English
- Use the original language for entity names
- Deduplicate: same entity appears only once
- Only extract entities clearly stated in the text`, entityList)
}

// buildBatchExtractPrompt 构建批量抽取提示词（按 index 独立处理）/ Build batch extraction prompt (process each item independently by index)
func (e *Extractor) buildBatchExtractPrompt() string {
	entityList := strings.Join(mapKeys(e.entityTypes), ", ")
	relationList := strings.Join(mapKeys(e.relationTypes), ", ")
	return fmt.Sprintf(`You are a knowledge extraction engine. The input is a JSON object with a "memories" array. Each element has an "index" and "content".

Rules:
- Entity types: %s
- Relation types: %s
- Process each memory item independently by its index
- Only extract entities clearly stated in that item's text; do not infer across items
- Return a JSON object with a "results" array; each element must have: index (integer matching input), entities (array), relations (array)
- Each entity: name, entity_type, description
- Each relation: source (entity name), target (entity name), relation_type
- Include an entry for every input index, even if entities and relations are empty arrays`, entityList, relationList)
}

// entityLLM 返回用于实体抽取的 LLM 提供者（优先 fastLLM）/ Return LLM provider for entity extraction (prefer fastLLM)
func (e *Extractor) entityLLM() llm.Provider {
	if e.fastLLM != nil {
		return e.fastLLM
	}
	return e.llm
}

// mapKeys 提取 map 的键列表（排序）/ Extract map keys as sorted slice
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Extract 从文本中抽取实体；关系抽取根据配置同步或异步执行
// Extract entities from text; relation extraction is sync or async depending on config
func (e *Extractor) Extract(ctx context.Context, req *model.ExtractRequest) (*model.ExtractResponse, error) {
	if req.Content == "" {
		return &model.ExtractResponse{}, nil
	}

	// 超时保护 / Timeout protection
	if e.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
		defer cancel()
	}

	// 1) 快速实体抽取（实体专属提示词 + 优先 fastLLM）/ Fast entity extraction (entity-only prompt + prefer fastLLM)
	output, err := e.callEntityLLM(ctx, req.Content)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", model.ErrExtractTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", model.ErrExtractLLMFailed, err)
	}

	if output == nil {
		return nil, model.ErrExtractParseFailed
	}

	// 校验并截断（实体部分）/ Validate and truncate (entities only in this phase)
	e.validateAndTruncate(output)

	resp := &model.ExtractResponse{}

	// 2) 实体规范化 + 创建 / Entity normalization + creation
	entityIDMap := make(map[string]string) // name → entityID
	for _, ent := range output.Entities {
		result, normalized, err := e.resolveEntity(ctx, ent, req.Scope, req.MemoryID)
		if err != nil {
			logger.Warn("entity resolution failed", zap.String("name", ent.Name), zap.Error(err))
			continue
		}
		// nil 表示实体已进入候选队列，跳过主图写入 / nil means entity is pending candidate promotion
		if result == nil {
			continue
		}
		entityIDMap[ent.Name] = result.EntityID
		if normalized {
			resp.Normalized++
		}
		resp.Entities = append(resp.Entities, *result)

		// 写入记忆-实体关联 / Write memory-entity association
		if req.MemoryID != "" {
			if assocErr := e.graphManager.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{
				MemoryID: req.MemoryID,
				EntityID: result.EntityID,
				Role:     "mentioned",
			}); assocErr != nil {
				logger.Warn("create memory-entity failed",
					zap.String("memory_id", req.MemoryID),
					zap.String("entity_id", result.EntityID),
					zap.Error(assocErr))
			}
		}
	}

	// 3) 关系抽取：异步入队 or 同步执行 / Relation extraction: async queue or sync fallback
	if e.cfg.RelationExtractEnabled && len(entityIDMap) >= 2 {
		if e.taskQueue != nil {
			e.enqueueRelationExtract(req, entityIDMap)
		} else {
			// 队列不可用时同步执行关系抽取 / Queue unavailable — extract relations synchronously
			relations := e.extractAndWriteRelations(ctx, req.Content, entityIDMap)
			resp.Relations = relations
		}
	}

	return resp, nil
}

// enqueueRelationExtract 将关系抽取任务投入队列（非阻塞，失败仅记录日志）
// Enqueue a relation extraction task (non-blocking, failures are only logged)
func (e *Extractor) enqueueRelationExtract(req *model.ExtractRequest, entityIDMap map[string]string) {
	payload := model.RelationExtractRequest{
		MemoryID:      req.MemoryID,
		Content:       req.Content,
		Scope:         req.Scope,
		TeamID:        req.TeamID,
		EntityContext: entityIDMap,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		logger.Warn("marshal relation extract payload failed", zap.Error(err))
		return
	}
	// 使用 background context — 入队操作不应受原始请求超时影响
	// Use background context — enqueue must not be cancelled by the caller's timeout
	if _, err := e.taskQueue.Enqueue(context.Background(), "relation_extract", raw); err != nil {
		logger.Warn("enqueue relation_extract task failed",
			zap.String("memory_id", req.MemoryID),
			zap.Error(err))
	}
}

// ExtractRelations 异步关系抽取入口（由队列 worker 调用）/ Async relation extraction entry (called by queue worker)
func (e *Extractor) ExtractRelations(ctx context.Context, req *model.RelationExtractRequest) error {
	if req.Content == "" || len(req.EntityContext) < 2 {
		return nil
	}

	if e.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
		defer cancel()
	}

	e.extractAndWriteRelations(ctx, req.Content, req.EntityContext)
	return nil
}

// extractAndWriteRelations 调用 LLM 抽取关系并写入图数据库 / Call LLM to extract relations and persist to graph
func (e *Extractor) extractAndWriteRelations(ctx context.Context, content string, entityIDMap map[string]string) []model.ExtractedRelationResult {
	output, err := e.callRelationLLM(ctx, content, entityIDMap)
	if err != nil || output == nil {
		if err != nil {
			logger.Warn("relation LLM call failed", zap.Error(err))
		}
		return nil
	}

	// 仅保留类型合法的关系 / Keep only relations with valid types
	filtered := output.Relations[:0]
	for _, rel := range output.Relations {
		if e.relationTypes[strings.ToLower(rel.RelationType)] {
			filtered = append(filtered, rel)
		}
	}

	var results []model.ExtractedRelationResult
	for _, rel := range filtered {
		sourceID, sok := entityIDMap[rel.Source]
		targetID, tok := entityIDMap[rel.Target]
		if !sok || !tok {
			continue
		}
		result := e.resolveRelation(ctx, sourceID, targetID, rel.RelationType)
		results = append(results, *result)
	}
	return results
}

// callEntityLLM 调用实体专属 LLM（优先 fastLLM，使用实体专属提示词）
// Call entity-dedicated LLM (prefer fastLLM, entity-only prompt)
func (e *Extractor) callEntityLLM(ctx context.Context, content string) (*extractLLMOutput, error) {
	temp := 0.1
	messages := []llm.ChatMessage{
		{Role: "system", Content: e.buildEntityOnlyPrompt()},
		{Role: "user", Content: content},
	}

	req := &llm.ChatRequest{
		Messages:       messages,
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	provider := e.entityLLM()
	resp, err := provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	output, _ := parseExtractOutput(ctx, resp.Content, messages, provider)
	return output, nil
}

// callRelationLLM 调用关系抽取 LLM（使用完整提示词 + 已知实体上下文）
// Call relation-extraction LLM (full prompt + known entity context)
func (e *Extractor) callRelationLLM(ctx context.Context, content string, entityIDMap map[string]string) (*extractLLMOutput, error) {
	// 将已解析实体名列表附加到用户消息，帮助 LLM 锚定关系主体
	// Append resolved entity names to user message to anchor relation subjects
	entityNames := make([]string, 0, len(entityIDMap))
	for name := range entityIDMap {
		entityNames = append(entityNames, name)
	}
	sort.Strings(entityNames)
	userContent := content + "\n\nKnown entities: " + strings.Join(entityNames, ", ")

	temp := 0.1
	messages := []llm.ChatMessage{
		{Role: "system", Content: e.buildExtractPrompt()},
		{Role: "user", Content: userContent},
	}

	req := &llm.ChatRequest{
		Messages:       messages,
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	resp, err := e.llm.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	output, _ := parseExtractOutput(ctx, resp.Content, messages, e.llm)
	return output, nil
}

// callLLM 调用LLM抽取实体和关系 / Call LLM to extract entities and relations
func (e *Extractor) callLLM(ctx context.Context, content string) (*extractLLMOutput, error) {
	temp := 0.1
	messages := []llm.ChatMessage{
		{Role: "system", Content: e.buildExtractPrompt()},
		{Role: "user", Content: content},
	}

	req := &llm.ChatRequest{
		Messages:       messages,
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	resp, err := e.llm.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	output, _ := parseExtractOutput(ctx, resp.Content, messages, e.llm)
	return output, nil
}

// parseExtractOutput 解析LLM输出（三级fallback）/ Parse LLM output with 3-level fallback
func parseExtractOutput(ctx context.Context, raw string, prevMessages []llm.ChatMessage, provider llm.Provider) (*extractLLMOutput, string) {
	// L1: 直接 JSON 解析 / Direct JSON unmarshal
	var output extractLLMOutput
	if err := json.Unmarshal([]byte(raw), &output); err == nil {
		if len(output.Entities) > 0 || len(output.Relations) > 0 {
			return &output, ExtractParseJSON
		}
	}

	// L2: 正则提取 JSON 对象 / Regex extract JSON object
	re := regexp.MustCompile(`\{(?:[^{}]|\{[^{}]*\})*"entities"(?:[^{}]|\{[^{}]*\})*\}`)
	if match := re.FindString(raw); match != "" {
		var extracted extractLLMOutput
		if err := json.Unmarshal([]byte(match), &extracted); err == nil {
			if len(extracted.Entities) > 0 || len(extracted.Relations) > 0 {
				return &extracted, ExtractParseExtract
			}
		}
	}

	// L3: 重试 LLM / Retry with correction message
	retryMessages := make([]llm.ChatMessage, len(prevMessages), len(prevMessages)+2)
	copy(retryMessages, prevMessages)
	retryMessages = append(retryMessages,
		llm.ChatMessage{Role: "assistant", Content: raw},
		llm.ChatMessage{Role: "user", Content: "Your previous response was not valid JSON. Please respond with ONLY a valid JSON object containing 'entities' and 'relations' arrays."},
	)

	temp := 0.1
	retryReq := &llm.ChatRequest{
		Messages:       retryMessages,
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	retryResp, err := provider.Chat(ctx, retryReq)
	if err == nil {
		var retryOutput extractLLMOutput
		if err := json.Unmarshal([]byte(retryResp.Content), &retryOutput); err == nil {
			if len(retryOutput.Entities) > 0 || len(retryOutput.Relations) > 0 {
				return &retryOutput, ExtractParseRetry
			}
		}
	}

	// L4: 返回 nil（全部失败）/ Return nil (all failed)
	logger.Warn("extract parse failed, all levels exhausted",
		zap.String("raw_content", raw),
	)
	return nil, ExtractParseFallback
}

// filterEntities 过滤并截断实体列表 / Filter invalid entity types and truncate to limit
func (e *Extractor) filterEntities(entities []extractedEntity) []extractedEntity {
	valid := make([]extractedEntity, 0, len(entities))
	for _, ent := range entities {
		ent.EntityType = strings.ToLower(strings.TrimSpace(ent.EntityType))
		if e.entityTypes[ent.EntityType] && strings.TrimSpace(ent.Name) != "" {
			valid = append(valid, ent)
		}
	}
	if e.cfg.MaxEntities > 0 && len(valid) > e.cfg.MaxEntities {
		valid = valid[:e.cfg.MaxEntities]
	}
	return valid
}

// filterRelations 过滤并截断关系列表 / Filter invalid relation types and truncate to limit
func (e *Extractor) filterRelations(relations []extractedRelation) []extractedRelation {
	valid := make([]extractedRelation, 0, len(relations))
	for _, rel := range relations {
		rel.RelationType = strings.ToLower(strings.TrimSpace(rel.RelationType))
		if e.relationTypes[rel.RelationType] && rel.Source != "" && rel.Target != "" {
			valid = append(valid, rel)
		}
	}
	if e.cfg.MaxRelations > 0 && len(valid) > e.cfg.MaxRelations {
		valid = valid[:e.cfg.MaxRelations]
	}
	return valid
}

// validateAndTruncate 校验并截断输出 / Validate and truncate output
func (e *Extractor) validateAndTruncate(output *extractLLMOutput) {
	output.Entities = e.filterEntities(output.Entities)
	output.Relations = e.filterRelations(output.Relations)
}

// resolveEntity 实体规范化（两阶段）/ Entity normalization (two-phase)
// 返回 nil result 表示实体已写入候选队列，尚未进入主图 / nil result means entity is queued as candidate
func (e *Extractor) resolveEntity(ctx context.Context, ent extractedEntity, scope, memoryID string) (*model.ExtractedEntityResult, bool, error) {
	// 阶段 1: 精确匹配（索引查询）/ Phase 1: Exact match via indexed query
	exactMatches, err := e.graphManager.FindEntitiesByName(ctx, ent.Name, scope, 5)
	if err != nil {
		logger.Warn("FindEntitiesByName failed during normalization", zap.Error(err))
	} else {
		for _, ex := range exactMatches {
			if strings.EqualFold(ex.Name, ent.Name) {
				return &model.ExtractedEntityResult{
					EntityID:   ex.ID,
					Name:       ex.Name,
					EntityType: ex.EntityType,
					Reused:     true,
				}, false, nil
			}
		}
	}

	// 阶段 2: LLM 辅助规范化（候选列表）/ Phase 2: LLM-assisted normalization
	existing, listErr := e.graphManager.ListEntities(ctx, scope, ent.EntityType, e.cfg.NormalizeCandidates)
	if listErr != nil {
		logger.Warn("list entities failed during normalization", zap.Error(listErr))
	}
	if e.cfg.NormalizeEnabled && len(existing) > 0 {
		candidates := existing
		if len(candidates) > e.cfg.NormalizeCandidates {
			candidates = candidates[:e.cfg.NormalizeCandidates]
		}

		matched, matchedName := e.llmNormalize(ctx, ent.Name, candidates)
		if matched {
			for _, ex := range candidates {
				if strings.EqualFold(ex.Name, matchedName) {
					return &model.ExtractedEntityResult{
						EntityID:       ex.ID,
						Name:           ex.Name,
						EntityType:     ex.EntityType,
						Reused:         true,
						NormalizedFrom: ent.Name,
					}, true, nil
				}
			}
		}
	}

	// 新实体：有候选存储则先进候选队列，否则直接入主图
	// New entity: route to candidate store if available, else create directly
	if e.candidateStore != nil {
		if err := e.candidateStore.UpsertCandidate(ctx, ent.Name, ent.EntityType, scope, memoryID); err != nil {
			logger.Warn("upsert candidate failed", zap.String("name", ent.Name), zap.Error(err))
		}
		return nil, false, nil
	}

	entity, err := e.graphManager.CreateEntity(ctx, &model.CreateEntityRequest{
		Name:        ent.Name,
		EntityType:  ent.EntityType,
		Scope:       scope,
		Description: ent.Description,
	})
	if err != nil {
		return nil, false, fmt.Errorf("create entity: %w", err)
	}

	return &model.ExtractedEntityResult{
		EntityID:   entity.ID,
		Name:       entity.Name,
		EntityType: entity.EntityType,
		Reused:     false,
	}, false, nil
}

// normalizeLLMTimeout 单次规范化 LLM 超时 / Per-call timeout for normalization LLM call
const normalizeLLMTimeout = 10 * time.Second

// llmNormalize LLM辅助实体规范化 / LLM-assisted entity normalization
func (e *Extractor) llmNormalize(ctx context.Context, name string, candidates []*model.Entity) (bool, string) {
	candidateNames := make([]string, 0, len(candidates))
	for _, c := range candidates {
		candidateNames = append(candidateNames, c.Name)
	}

	prompt := fmt.Sprintf("New entity: %q\nCandidate entities: %s\n\nDoes the new entity refer to the same thing as any candidate?",
		name, strings.Join(candidateNames, ", "))

	temp := 0.1
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: normalizeSystemPrompt},
			{Role: "user", Content: prompt},
		},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	// 独立超时防止单次规范化 hang 住整个抽取流程 / Per-call timeout prevents blocking extraction
	llmCtx, cancel := context.WithTimeout(ctx, normalizeLLMTimeout)
	defer cancel()

	resp, err := e.llm.Chat(llmCtx, req)
	if err != nil {
		logger.Warn("normalize LLM call failed, creating new entity",
			zap.String("name", name), zap.Error(err))
		return false, ""
	}

	var output normalizeLLMOutput
	if err := json.Unmarshal([]byte(resp.Content), &output); err != nil {
		logger.Warn("normalize LLM parse failed",
			zap.String("name", name), zap.String("raw", resp.Content))
		return false, ""
	}

	return output.Match, output.MatchedEntity
}

// resolveRelation 创建关系（去重）/ Create relation with dedup
func (e *Extractor) resolveRelation(ctx context.Context, sourceID, targetID, relationType string) *model.ExtractedRelationResult {
	// 去重检查 / Dedup check
	existing, err := e.graphManager.GetEntityRelations(ctx, sourceID)
	if err == nil {
		for _, rel := range existing {
			if rel.TargetID == targetID && rel.RelationType == relationType {
				return &model.ExtractedRelationResult{
					RelationID:   rel.ID,
					SourceID:     sourceID,
					TargetID:     targetID,
					RelationType: relationType,
					Skipped:      true,
				}
			}
		}
	}

	// 创建新关系 / Create new relation
	relation, err := e.graphManager.CreateRelation(ctx, &model.CreateEntityRelationRequest{
		SourceID:     sourceID,
		TargetID:     targetID,
		RelationType: relationType,
	})
	if err != nil {
		logger.Warn("create relation failed",
			zap.String("source_id", sourceID),
			zap.String("target_id", targetID),
			zap.Error(err))
		return &model.ExtractedRelationResult{
			SourceID:     sourceID,
			TargetID:     targetID,
			RelationType: relationType,
			Skipped:      true,
		}
	}

	return &model.ExtractedRelationResult{
		RelationID:   relation.ID,
		SourceID:     sourceID,
		TargetID:     targetID,
		RelationType: relationType,
		Skipped:      false,
	}
}

// ============================================================
// 批量实体抽取 / Batch entity extraction
// ============================================================

// defaultBatchTokenThreshold 默认每批 token 阈值（约 80-100 条）/ Default batch token threshold (~80-100 items)
const defaultBatchTokenThreshold = 32000

// ExtractBatch 批量实体抽取：按 token 阈值分批 → LLM 全局抽取 → 系统侧匹配归属 → 落库
// Batch entity extraction: split by token threshold → global LLM extraction → system-side matching → store
// defaultBatchConcurrency 批量抽取最大并发批次数 / Max concurrent batches for batch extraction
const defaultBatchConcurrency = 20

// batchLLMResult 单批 LLM 调用结果 / Result of a single batch LLM call
type batchLLMResult struct {
	batchIdx int
	batch    []model.BatchExtractItem
	output   *batchExtractOutput
	tokens   int
	err      error
}

func (e *Extractor) ExtractBatch(ctx context.Context, req *model.BatchExtractRequest) (*model.BatchExtractResponse, error) {
	if len(req.Items) == 0 {
		return &model.BatchExtractResponse{Results: make(map[string]*model.ExtractResponse)}, nil
	}

	// 超时保护 / Timeout protection
	if e.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.cfg.Timeout*time.Duration(len(req.Items)))
		defer cancel()
	}

	batches := e.formBatches(req.Items)
	resp := &model.BatchExtractResponse{
		Results:    make(map[string]*model.ExtractResponse, len(req.Items)),
		BatchCount: len(batches),
	}

	// 并发调用 LLM（semaphore 控制最大并发数）/ Concurrent LLM calls with semaphore
	concurrency := defaultBatchConcurrency
	if c := e.cfg.BatchConcurrency; c > 0 {
		concurrency = c
	}
	sem := make(chan struct{}, concurrency)
	results := make([]batchLLMResult, len(batches))
	var wg sync.WaitGroup

	for i, batch := range batches {
		wg.Add(1)
		go func(idx int, b []model.BatchExtractItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			logger.Info("batch extraction started",
				zap.Int("batch", idx+1),
				zap.Int("total_batches", len(batches)),
				zap.Int("items", len(b)),
			)
			output, tokens, err := e.callBatchLLMWithRetry(ctx, b, idx+1)
			results[idx] = batchLLMResult{batchIdx: idx, batch: b, output: output, tokens: tokens, err: err}
		}(i, batch)
	}
	wg.Wait()

	// 串行写入 DB（SQLite 不支持并发写）/ Serial DB writes (SQLite doesn't support concurrent writes)
	for _, r := range results {
		resp.TotalTokens += r.tokens
		if r.err != nil {
			logger.Warn("batch LLM call failed after retry, skipping batch",
				zap.Int("batch", r.batchIdx+1),
				zap.Error(r.err),
			)
			continue
		}
		if r.output == nil {
			logger.Warn("batch LLM returned nil output, skipping batch",
				zap.Int("batch", r.batchIdx+1),
			)
			continue
		}

		// 按 index 精准归属，逐条处理 / Process each result by its memory index
		for _, result := range r.output.Results {
			if result.Index < 0 || result.Index >= len(r.batch) {
				logger.Warn("batch result index out of range",
					zap.Int("index", result.Index),
					zap.Int("batch_size", len(r.batch)),
				)
				continue
			}
			memID := r.batch[result.Index].MemoryID
			if resp.Results[memID] == nil {
				resp.Results[memID] = &model.ExtractResponse{}
			}

			validEnts := e.filterEntities(result.Entities)
			validRels := e.filterRelations(result.Relations)

			entityIDMap := make(map[string]string, len(validEnts))
			for _, ent := range validEnts {
				entity, normalized, resolveErr := e.resolveEntity(ctx, ent, req.Scope, "")
				if resolveErr != nil {
					logger.Warn("batch entity resolution failed", zap.String("name", ent.Name), zap.Error(resolveErr))
					continue
				}
				// nil 表示已进候选队列，跳过主图写入 / nil means queued as candidate
				if entity == nil {
					continue
				}
				entityIDMap[ent.Name] = entity.EntityID
				resp.Results[memID].Entities = append(resp.Results[memID].Entities, *entity)
				if normalized {
					resp.Results[memID].Normalized++
				}
				assocErr := e.graphManager.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{
					MemoryID: memID,
					EntityID: entity.EntityID,
					Role:     "mentioned",
				})
				if assocErr != nil {
					logger.Warn("batch create memory-entity failed",
						zap.String("memory_id", memID),
						zap.String("entity_id", entity.EntityID),
						zap.Error(assocErr),
					)
				}
			}

			for _, rel := range validRels {
				sourceID, sok := entityIDMap[rel.Source]
				targetID, tok := entityIDMap[rel.Target]
				if !sok || !tok {
					continue
				}
				e.resolveRelation(ctx, sourceID, targetID, rel.RelationType)
			}
		}
	}

	return resp, nil
}

// formBatches 按 token 阈值分批 / Split items into batches by token threshold
func (e *Extractor) formBatches(items []model.BatchExtractItem) [][]model.BatchExtractItem {
	threshold := e.cfg.BatchTokenThreshold
	if threshold <= 0 {
		// 首次调用时自适应探测（进程内只跑一次），之后复用缓存值。
		// Auto-detect on first call (once per Extractor instance), reuse cached value after.
		e.detectThresholdOnce.Do(func() {
			e.detectedThreshold = llm.DetectContextWindow(context.Background(), e.llm)
		})
		threshold = e.detectedThreshold
	}

	var batches [][]model.BatchExtractItem
	var current []model.BatchExtractItem
	currentTokens := 0

	for _, item := range items {
		tokens := tokenutil.EstimateTokens(item.Content)
		// 超阈值且当前批非空 → 封批 / Seal batch if threshold exceeded and current batch non-empty
		if currentTokens+tokens > threshold && len(current) > 0 {
			batches = append(batches, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, item)
		currentTokens += tokens
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// ─── 去重先行 + 流式 + 实体级标注 ───────────────────────────────────────────
// Dedup-first + Streaming + Entity-level annotation batch extraction

// properNounRe 匹配英文专有名词（大写开头的词组）/ Match English proper nouns
var properNounRe = regexp.MustCompile(`\b[A-Z][a-zA-Z'-]+(?:\s+[A-Z][a-zA-Z'-]+)*\b`)

// dedupCandidate 去重后的实体候选 / Deduplicated entity candidate
type dedupCandidate struct {
	Name  string
	Items []int // 出现在哪些批次 item 中 / which batch item indices this name appears in
}

// dedupClassifyOutput LLM 去重分类输出（实体级 item 标注）/ Dedup classification output with entity-level item annotation
type dedupClassifyOutput struct {
	Entities []struct {
		Name        string `json:"name"`
		EntityType  string `json:"entity_type"`
		Description string `json:"description"`
		Items       []int  `json:"items"` // 来源 item 索引列表 / source item index list
	} `json:"entities"`
	Relations []struct {
		Source       string `json:"source"`
		Target       string `json:"target"`
		RelationType string `json:"relation_type"`
		Items        []int  `json:"items"` // 关系出现在哪些 item 中 / relation appears in which items
	} `json:"relations"`
}

// collectDedupCandidates 从批次中提取去重候选实体
// Extract deduplicated entity candidates from batch via regex scan
func collectDedupCandidates(items []model.BatchExtractItem) []dedupCandidate {
	nameToItems := make(map[string][]int)
	for i, item := range items {
		seen := make(map[string]bool)
		for _, m := range properNounRe.FindAllString(item.Content, -1) {
			if len([]rune(m)) < 2 {
				continue
			}
			if !seen[m] {
				seen[m] = true
				nameToItems[m] = append(nameToItems[m], i)
			}
		}
	}
	candidates := make([]dedupCandidate, 0, len(nameToItems))
	for name, idxs := range nameToItems {
		candidates = append(candidates, dedupCandidate{Name: name, Items: idxs})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates
}

// callBatchLLMDedup 去重先行+流式+实体级标注的批量抽取
// Dedup-first + streaming + entity-level annotation batch extraction
// Returns (nil, 0, errNoCandidates) when regex finds no candidates — caller falls back to indexed batch.
var errNoCandidates = fmt.Errorf("no regex candidates found")

func (e *Extractor) callBatchLLMDedup(ctx context.Context, items []model.BatchExtractItem) (*batchExtractOutput, int, error) {
	candidates := collectDedupCandidates(items)
	if len(candidates) == 0 {
		return nil, 0, errNoCandidates
	}

	// 构建候选名列表（附带 item 映射）供 LLM 分类 / Build candidate list with item mapping for LLM
	type candidateEntry struct {
		Name  string `json:"name"`
		Items []int  `json:"items"`
	}
	entries := make([]candidateEntry, len(candidates))
	for i, c := range candidates {
		entries[i] = candidateEntry{Name: c.Name, Items: c.Items}
	}

	inputJSON, _ := json.Marshal(map[string]any{
		"candidates": entries,
		"total_items": len(items),
	})

	entityList := strings.Join(mapKeys(e.entityTypes), ", ")
	relationList := strings.Join(mapKeys(e.relationTypes), ", ")
	systemPrompt := fmt.Sprintf(`You are a knowledge extraction engine.

You receive a list of entity name candidates extracted from %d memory items, each with an "items" field listing which item indices contain that name.

Your task:
1. For each candidate that is a real entity, classify it: entity_type (%s), brief description
2. Identify relations between entities that co-occur in the same item: relation_type (%s)
3. Preserve the "items" field as-is from the input for each entity/relation

Return JSON:
{"entities":[{"name":"...","entity_type":"...","description":"...","items":[...]}],"relations":[{"source":"...","target":"...","relation_type":"...","items":[...]}]}

Only include real entities (not common words). Relations must have source and target both present in the same item.`,
		len(items), entityList, relationList)

	temp := 0.1
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: string(inputJSON)},
		},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	// 优先使用流式，避免 awaiting-headers 超时 / Prefer streaming to avoid awaiting-headers timeout
	type streamProvider interface {
		ChatStream(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error)
	}
	var resp *llm.ChatResponse
	var err error
	if sp, ok := e.llm.(streamProvider); ok {
		resp, err = sp.ChatStream(ctx, req)
	} else {
		resp, err = e.llm.Chat(ctx, req)
	}
	if err != nil {
		return nil, 0, err
	}

	var output dedupClassifyOutput
	if jsonErr := json.Unmarshal([]byte(resp.Content), &output); jsonErr != nil {
		return nil, resp.TotalTokens, fmt.Errorf("dedup parse: %w", jsonErr)
	}

	// 转换为 batchExtractOutput 格式 / Convert to batchExtractOutput format
	result := &batchExtractOutput{Results: make([]batchExtractResult, len(items))}
	for i := range result.Results {
		result.Results[i].Index = i
	}
	for _, ent := range output.Entities {
		ee := extractedEntity{Name: ent.Name, EntityType: ent.EntityType, Description: ent.Description}
		for _, idx := range ent.Items {
			if idx >= 0 && idx < len(items) {
				result.Results[idx].Entities = append(result.Results[idx].Entities, ee)
			}
		}
	}
	for _, rel := range output.Relations {
		rr := extractedRelation{Source: rel.Source, Target: rel.Target, RelationType: rel.RelationType}
		for _, idx := range rel.Items {
			if idx >= 0 && idx < len(items) {
				result.Results[idx].Relations = append(result.Results[idx].Relations, rr)
			}
		}
	}
	return result, resp.TotalTokens, nil
}

// callBatchLLM 调用 LLM 进行批量抽取（结构化输入/输出，index 精准归属）/ Call LLM for batch extraction with indexed input/output
// callBatchLLMWithRetry 超时自动对半重试：首次失败时拆成两半分别调用，合并结果
// On timeout: split into two halves, call each, merge results
func (e *Extractor) callBatchLLMWithRetry(ctx context.Context, items []model.BatchExtractItem, batchNum int) (*batchExtractOutput, int, error) {
	// 优先走去重先行+流式路径，失败才降级到原始路径 / Prefer dedup+streaming, fallback to original on error
	output, tokens, err := e.callBatchLLMDedup(ctx, items)
	if err == nil {
		return output, tokens, nil
	}
	logger.Warn("dedup extraction failed, falling back to indexed batch",
		zap.Int("batch", batchNum), zap.Error(err))

	output, tokens, err = e.callBatchLLM(ctx, items)
	if err == nil {
		return output, tokens, nil
	}

	// 非超时错误不重试 / Don't retry non-timeout errors
	if ctx.Err() == nil && !isTimeoutOrServerError(err) {
		return nil, tokens, err
	}

	if len(items) <= 1 {
		return nil, tokens, err
	}

	logger.Info("batch LLM timed out, splitting in half and retrying",
		zap.Int("batch", batchNum),
		zap.Int("original_size", len(items)),
	)

	mid := len(items) / 2
	out1, tok1, err1 := e.callBatchLLM(ctx, items[:mid])
	out2, tok2, err2 := e.callBatchLLM(ctx, items[mid:])
	totalTokens := tokens + tok1 + tok2

	// 至少一半成功就返回合并结果 / Return merged result if at least one half succeeded
	if err1 != nil && err2 != nil {
		return nil, totalTokens, fmt.Errorf("both halves failed: %v / %v", err1, err2)
	}

	merged := &batchExtractOutput{}
	if out1 != nil {
		merged.Results = append(merged.Results, out1.Results...)
	}
	if out2 != nil {
		// 第二半的 index 需要加上偏移量 / Offset second half indices
		for _, r := range out2.Results {
			r.Index += mid
			merged.Results = append(merged.Results, r)
		}
	}

	if err1 != nil {
		logger.Warn("batch retry: first half failed", zap.Int("batch", batchNum), zap.Error(err1))
	}
	if err2 != nil {
		logger.Warn("batch retry: second half failed", zap.Int("batch", batchNum), zap.Error(err2))
	}

	return merged, totalTokens, nil
}

// isTimeoutOrServerError 判断是否为超时或服务端错误（值得重试）/ Check if error is timeout or server error (retriable)
func isTimeoutOrServerError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "status 502") ||
		strings.Contains(msg, "status 503") ||
		strings.Contains(msg, "status 429")
}

func (e *Extractor) callBatchLLM(ctx context.Context, items []model.BatchExtractItem) (*batchExtractOutput, int, error) {
	input := batchExtractInput{
		Memories: make([]batchMemoryItem, len(items)),
	}
	for i, item := range items {
		input.Memories[i] = batchMemoryItem{Index: i, Content: item.Content}
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal batch input: %w", err)
	}

	temp := 0.1
	messages := []llm.ChatMessage{
		{Role: "system", Content: e.buildBatchExtractPrompt()},
		{Role: "user", Content: string(inputJSON)},
	}
	responseFormat := &llm.ResponseFormat{Type: "json_object"}
	if schema := e.buildBatchExtractionSchema(); schema != nil {
		responseFormat = &llm.ResponseFormat{Type: "json_schema", JSONSchema: schema}
	}
	req := &llm.ChatRequest{
		Messages:       messages,
		ResponseFormat: responseFormat,
		Temperature:    &temp,
	}

	resp, err := e.llm.Chat(ctx, req)
	if err != nil {
		return nil, 0, err
	}

	var output batchExtractOutput
	if err := json.Unmarshal([]byte(resp.Content), &output); err != nil {
		return nil, resp.TotalTokens, fmt.Errorf("parse batch LLM output: %w", err)
	}
	return &output, resp.TotalTokens, nil
}

// buildBatchExtractionSchema 动态构建批量抽取的 JSON Schema（枚举值来自配置）
// Dynamically build JSON Schema for batch extraction using configured type enums
func (e *Extractor) buildBatchExtractionSchema() *llm.JSONSchema {
	entityEnum := make([]string, 0, len(e.entityTypes))
	for t := range e.entityTypes {
		entityEnum = append(entityEnum, t)
	}
	sort.Strings(entityEnum)

	relationEnum := make([]string, 0, len(e.relationTypes))
	for t := range e.relationTypes {
		relationEnum = append(relationEnum, t)
	}
	sort.Strings(relationEnum)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"results": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index": map[string]any{"type": "integer"},
						"entities": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"name":        map[string]any{"type": "string"},
									"entity_type": map[string]any{"type": "string", "enum": entityEnum},
									"description": map[string]any{"type": "string"},
								},
								"required":             []string{"name", "entity_type", "description"},
								"additionalProperties": false,
							},
						},
						"relations": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"source":        map[string]any{"type": "string"},
									"target":        map[string]any{"type": "string"},
									"relation_type": map[string]any{"type": "string", "enum": relationEnum},
								},
								"required":             []string{"source", "target", "relation_type"},
								"additionalProperties": false,
							},
						},
					},
					"required":             []string{"index", "entities", "relations"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"results"},
		"additionalProperties": false,
	}

	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	return &llm.JSONSchema{
		Name:   "batch_extraction",
		Strict: true,
		Schema: schemaBytes,
	}
}

