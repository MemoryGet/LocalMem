// Package memory 提供记忆管理核心业务逻辑 / Core memory management business logic
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
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

// normalizeLLMOutput 规范化LLM输出 / Normalization LLM output
type normalizeLLMOutput struct {
	Match         bool   `json:"match"`
	MatchedEntity string `json:"matched_entity"`
}


const normalizeSystemPrompt = `You are an entity normalization engine. Determine if a new entity refers to the same thing as one of the candidate entities.

Output strict JSON: {"match": true, "matched_entity": "candidate name"} or {"match": false}`

// Extractor 自动实体抽取器 / Auto entity extractor
type Extractor struct {
	llm           llm.Provider
	graphManager  *GraphManager
	memStore      store.MemoryStore
	cfg           config.ExtractConfig
	entityTypes   map[string]bool // 从配置加载 / Loaded from config
	relationTypes map[string]bool // 从配置加载 / Loaded from config
}

// NewExtractor 创建实体抽取器 / Create entity extractor
func NewExtractor(llmProvider llm.Provider, graphManager *GraphManager, memStore store.MemoryStore, cfg config.ExtractConfig) *Extractor {
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
		llm:           llmProvider,
		graphManager:  graphManager,
		memStore:      memStore,
		cfg:           cfg,
		entityTypes:   entityTypes,
		relationTypes: relationTypes,
	}
}

// GetMemoryStore 获取记忆存储（供 API handler 使用）/ Get memory store (for API handler)
func (e *Extractor) GetMemoryStore() store.MemoryStore {
	return e.memStore
}

// buildExtractPrompt 构建抽取提示词（类型列表从配置注入）/ Build extraction prompt with configured types
func (e *Extractor) buildExtractPrompt() string {
	entityList := strings.Join(mapKeys(e.entityTypes), ", ")
	relationList := strings.Join(mapKeys(e.relationTypes), ", ")
	return fmt.Sprintf(`You are a knowledge extraction engine. Extract structured entities and relationships from the given text.

Rules:
- Entity types: %s
- Relation types: %s
- Output strict JSON with "entities" and "relations" arrays
- Each entity has: name, entity_type, description
- Each relation has: source (entity name), target (entity name), relation_type
- Deduplicate: same entity appears only once
- Relations: source is the actor, target is the object
- Only extract entities and relations that are clearly stated in the text`, entityList, relationList)
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

// Extract 从文本中抽取实体和关系 / Extract entities and relations from text
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

	// 1) LLM 调用抽取实体和关系 / LLM call to extract entities and relations
	output, err := e.callLLM(ctx, req.Content)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", model.ErrExtractTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", model.ErrExtractLLMFailed, err)
	}

	if output == nil {
		return nil, model.ErrExtractParseFailed
	}

	// 校验并截断 / Validate and truncate
	e.validateAndTruncate(output)

	resp := &model.ExtractResponse{}
	resp.TotalTokens = 0 // 将在 LLM 调用中累加

	// 2) 实体规范化 + 创建 / Entity normalization + creation
	entityIDMap := make(map[string]string) // name → entityID
	for _, ent := range output.Entities {
		result, normalized, err := e.resolveEntity(ctx, ent, req.Scope)
		if err != nil {
			logger.Warn("entity resolution failed", zap.String("name", ent.Name), zap.Error(err))
			continue
		}
		entityIDMap[ent.Name] = result.EntityID
		if normalized {
			resp.Normalized++
		}
		resp.Entities = append(resp.Entities, *result)

		// 写入记忆-实体关联 / Write memory-entity association
		if req.MemoryID != "" {
			err := e.graphManager.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{
				MemoryID: req.MemoryID,
				EntityID: result.EntityID,
				Role:     "mentioned",
			})
			if err != nil {
				logger.Warn("create memory-entity failed",
					zap.String("memory_id", req.MemoryID),
					zap.String("entity_id", result.EntityID),
					zap.Error(err))
			}
		}
	}

	// 3) 写入关系 / Write relations
	for _, rel := range output.Relations {
		sourceID, sok := entityIDMap[rel.Source]
		targetID, tok := entityIDMap[rel.Target]
		if !sok || !tok {
			continue
		}

		result := e.resolveRelation(ctx, sourceID, targetID, rel.RelationType)
		resp.Relations = append(resp.Relations, *result)
	}

	return resp, nil
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

// validateAndTruncate 校验并截断输出 / Validate and truncate output
func (e *Extractor) validateAndTruncate(output *extractLLMOutput) {
	// 过滤无效实体类型（新切片避免原地修改）/ Filter invalid entity types into new slice to avoid in-place mutation
	valid := make([]extractedEntity, 0, len(output.Entities))
	for _, ent := range output.Entities {
		ent.EntityType = strings.ToLower(strings.TrimSpace(ent.EntityType))
		if e.entityTypes[ent.EntityType] && strings.TrimSpace(ent.Name) != "" {
			valid = append(valid, ent)
		}
	}
	output.Entities = valid

	// 截断到上限 / Truncate to limit
	if e.cfg.MaxEntities > 0 && len(output.Entities) > e.cfg.MaxEntities {
		output.Entities = output.Entities[:e.cfg.MaxEntities]
	}

	// 过滤无效关系类型（新切片避免原地修改）/ Filter invalid relation types into new slice
	validRels := make([]extractedRelation, 0, len(output.Relations))
	for _, rel := range output.Relations {
		rel.RelationType = strings.ToLower(strings.TrimSpace(rel.RelationType))
		if e.relationTypes[rel.RelationType] && rel.Source != "" && rel.Target != "" {
			validRels = append(validRels, rel)
		}
	}
	output.Relations = validRels

	if e.cfg.MaxRelations > 0 && len(output.Relations) > e.cfg.MaxRelations {
		output.Relations = output.Relations[:e.cfg.MaxRelations]
	}
}

// resolveEntity 实体规范化（两阶段）/ Entity normalization (two-phase)
func (e *Extractor) resolveEntity(ctx context.Context, ent extractedEntity, scope string) (*model.ExtractedEntityResult, bool, error) {
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

	// 创建新实体 / Create new entity
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

// defaultBatchTokenThreshold 默认每批 token 阈值（适配 128K 上下文模型）/ Default batch token threshold (for 128K context models)
const defaultBatchTokenThreshold = 32000

// ExtractBatch 批量实体抽取：按 token 阈值分批 → LLM 全局抽取 → 系统侧匹配归属 → 落库
// Batch entity extraction: split by token threshold → global LLM extraction → system-side matching → store
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

	for batchIdx, batch := range batches {
		logger.Info("batch extraction started",
			zap.Int("batch", batchIdx+1),
			zap.Int("total_batches", len(batches)),
			zap.Int("items", len(batch)),
		)

		output, tokens, err := e.callBatchLLM(ctx, batch)
		resp.TotalTokens += tokens
		if err != nil {
			logger.Warn("batch LLM call failed, skipping batch",
				zap.Int("batch", batchIdx+1),
				zap.Error(err),
			)
			continue
		}
		if output == nil {
			logger.Warn("batch LLM returned nil output, skipping batch",
				zap.Int("batch", batchIdx+1),
			)
			continue
		}

		// 校验并截断 / Validate and truncate
		e.validateAndTruncate(output)

		// 系统侧匹配实体归属 / System-side entity-to-memory matching
		entityMemMap := matchEntitiesToMemories(output.Entities, batch)

		// 规范化+创建全局实体 / Normalize + create global entities
		entityIDMap := make(map[string]string, len(output.Entities)) // name → entityID
		for _, ent := range output.Entities {
			result, normalized, resolveErr := e.resolveEntity(ctx, ent, req.Scope)
			if resolveErr != nil {
				logger.Warn("batch entity resolution failed", zap.String("name", ent.Name), zap.Error(resolveErr))
				continue
			}
			entityIDMap[ent.Name] = result.EntityID

			// 为匹配到的每个 memory 写入关联 / Create memory-entity associations for matched memories
			for _, memID := range entityMemMap[ent.Name] {
				if resp.Results[memID] == nil {
					resp.Results[memID] = &model.ExtractResponse{}
				}
				resp.Results[memID].Entities = append(resp.Results[memID].Entities, *result)
				if normalized {
					resp.Results[memID].Normalized++
				}

				assocErr := e.graphManager.CreateMemoryEntity(ctx, &model.CreateMemoryEntityRequest{
					MemoryID: memID,
					EntityID: result.EntityID,
					Role:     "mentioned",
				})
				if assocErr != nil {
					logger.Warn("batch create memory-entity failed",
						zap.String("memory_id", memID),
						zap.String("entity_id", result.EntityID),
						zap.Error(assocErr),
					)
				}
			}
		}

		// 写入关系 / Create relations
		for _, rel := range output.Relations {
			sourceID, sok := entityIDMap[rel.Source]
			targetID, tok := entityIDMap[rel.Target]
			if !sok || !tok {
				continue
			}
			e.resolveRelation(ctx, sourceID, targetID, rel.RelationType)
		}
	}

	return resp, nil
}

// formBatches 按 token 阈值分批 / Split items into batches by token threshold
func (e *Extractor) formBatches(items []model.BatchExtractItem) [][]model.BatchExtractItem {
	threshold := e.cfg.BatchTokenThreshold
	if threshold <= 0 {
		threshold = defaultBatchTokenThreshold
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

// callBatchLLM 调用 LLM 进行批量抽取（复用现有 prompt + 解析）/ Call LLM for batch extraction (reuse existing prompt + parsing)
func (e *Extractor) callBatchLLM(ctx context.Context, items []model.BatchExtractItem) (*extractLLMOutput, int, error) {
	// 拼接多条 content / Concatenate multiple contents
	var sb strings.Builder
	for i, item := range items {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString(item.Content)
	}

	temp := 0.1
	messages := []llm.ChatMessage{
		{Role: "system", Content: e.buildExtractPrompt()},
		{Role: "user", Content: sb.String()},
	}

	req := &llm.ChatRequest{
		Messages:       messages,
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
		Temperature:    &temp,
	}

	resp, err := e.llm.Chat(ctx, req)
	if err != nil {
		return nil, 0, err
	}

	output, _ := parseExtractOutput(ctx, resp.Content, messages, e.llm)
	return output, resp.TotalTokens, nil
}

// matchEntitiesToMemories 系统侧匹配实体归属：entity.Name ∈ memory.Content → 建立关联
// System-side matching: if entity name appears in memory content, associate them
func matchEntitiesToMemories(entities []extractedEntity, items []model.BatchExtractItem) map[string][]string {
	// name → []memoryID
	result := make(map[string][]string, len(entities))
	for _, ent := range entities {
		nameLower := strings.ToLower(ent.Name)
		if nameLower == "" {
			continue
		}
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Content), nameLower) {
				result[ent.Name] = append(result[ent.Name], item.MemoryID)
			}
		}
	}
	return result
}
