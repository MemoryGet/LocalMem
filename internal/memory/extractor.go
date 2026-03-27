// Package memory 提供记忆管理核心业务逻辑 / Core memory management business logic
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// 解析方式常量 / Parse method constants
const (
	ExtractParseJSON     = "json"
	ExtractParseExtract  = "extract"
	ExtractParseRetry    = "retry"
	ExtractParseFallback = "fallback"
)

// 合法的实体类型 / Valid entity types
var validEntityTypes = map[string]bool{
	"person": true, "org": true, "concept": true, "tool": true, "location": true,
}

// 合法的关系类型 / Valid relation types
var validRelationTypes = map[string]bool{
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

const extractSystemPrompt = `You are a knowledge extraction engine. Extract structured entities and relationships from the given text.

Rules:
- Entity types: person, org, concept, tool, location
- Relation types: uses, knows, belongs_to, related_to
- Output strict JSON with "entities" and "relations" arrays
- Each entity has: name, entity_type, description
- Each relation has: source (entity name), target (entity name), relation_type
- Deduplicate: same entity appears only once
- Relations: source is the actor, target is the object
- Only extract entities and relations that are clearly stated in the text`

const normalizeSystemPrompt = `You are an entity normalization engine. Determine if a new entity refers to the same thing as one of the candidate entities.

Output strict JSON: {"match": true, "matched_entity": "candidate name"} or {"match": false}`

// Extractor 自动实体抽取器 / Auto entity extractor
type Extractor struct {
	llm          llm.Provider
	graphManager *GraphManager
	memStore     store.MemoryStore
	cfg          config.ExtractConfig
}

// NewExtractor 创建实体抽取器 / Create entity extractor
func NewExtractor(llm llm.Provider, graphManager *GraphManager, memStore store.MemoryStore, cfg config.ExtractConfig) *Extractor {
	return &Extractor{
		llm:          llm,
		graphManager: graphManager,
		memStore:     memStore,
		cfg:          cfg,
	}
}

// GetMemoryStore 获取记忆存储（供 API handler 使用）/ Get memory store (for API handler)
func (e *Extractor) GetMemoryStore() store.MemoryStore {
	return e.memStore
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
		{Role: "system", Content: extractSystemPrompt},
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
	re := regexp.MustCompile(`\{[\s\S]*"entities"[\s\S]*\}`)
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
		if validEntityTypes[ent.EntityType] && strings.TrimSpace(ent.Name) != "" {
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
		if validRelationTypes[rel.RelationType] && rel.Source != "" && rel.Target != "" {
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
	// 阶段 1: 精确匹配 / Phase 1: Exact match
	existing, err := e.graphManager.graphStore.ListEntities(ctx, scope, ent.EntityType, 100)
	if err != nil {
		logger.Warn("list entities failed during normalization", zap.Error(err))
		// 继续创建新实体 / Continue to create new entity
	} else {
		for _, ex := range existing {
			if strings.EqualFold(ex.Name, ent.Name) {
				return &model.ExtractedEntityResult{
					EntityID:   ex.ID,
					Name:       ex.Name,
					EntityType: ex.EntityType,
					Reused:     true,
				}, false, nil
			}
		}

		// 阶段 2: LLM 辅助规范化 / Phase 2: LLM-assisted normalization
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
	existing, err := e.graphManager.graphStore.GetEntityRelations(ctx, sourceID)
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
