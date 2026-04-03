// summarizer.go 会话总结器 / Session summarizer — produces semantic memories from session episodic memories (B7)
package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// SummarizerConfig 会话总结器配置 / Session summarizer config
type SummarizerConfig struct {
	MinMemories int `yaml:"min_memories"` // 最少记忆数才触发总结 / Minimum memories to trigger summarization
	MaxTokens   int `yaml:"max_tokens"`   // LLM 最大输出 token / Max LLM output tokens
}

// DefaultSummarizerConfig 默认配置 / Default summarizer config
func DefaultSummarizerConfig() SummarizerConfig {
	return SummarizerConfig{
		MinMemories: 3,
		MaxTokens:   1024,
	}
}

// SessionSummarizer 会话总结器 / Summarizes session memories into semantic memories
type SessionSummarizer struct {
	memStore     store.MemoryStore
	contextStore store.ContextStore
	llm          llm.Provider
	manager      *Manager
	cfg          SummarizerConfig
}

// NewSessionSummarizer 创建会话总结器 / Create session summarizer
func NewSessionSummarizer(memStore store.MemoryStore, contextStore store.ContextStore, llmProvider llm.Provider, manager *Manager, cfg SummarizerConfig) *SessionSummarizer {
	return &SessionSummarizer{
		memStore:     memStore,
		contextStore: contextStore,
		llm:          llmProvider,
		manager:      manager,
		cfg:          cfg,
	}
}

// SummarizeResponse 总结响应 / Summarize response
type SummarizeResponse struct {
	SemanticMemory *model.Memory `json:"semantic_memory"`           // 产出的 semantic 记忆 / Produced semantic memory
	SourceCount    int           `json:"source_count"`              // 源 episodic 记忆数 / Source episodic memory count
	Skipped        bool          `json:"skipped,omitempty"`         // 是否跳过（记忆太少）/ Whether skipped (too few memories)
	SkipReason     string        `json:"skip_reason,omitempty"`     // 跳过原因 / Skip reason
}

// sessionSummarizePrompt 会话总结提示词 / Session summarization system prompt
const sessionSummarizePrompt = `You are a knowledge distillation engine. Given a set of episodic memories from a session, synthesize them into a single comprehensive semantic summary.

Rules:
- Extract the key decisions, insights, and outcomes from the session
- Preserve important facts, names, and technical details
- Omit trivial conversational filler
- Write in a concise, factual style
- If the session contains no substantive content, respond with exactly: SKIP
- Output only the summary text, no JSON or markdown formatting`

// Summarize 总结指定上下文的会话记忆 / Summarize session memories for a given context
func (s *SessionSummarizer) Summarize(ctx context.Context, contextID string, identity *model.Identity) (*SummarizeResponse, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("LLM provider is required for summarization: %w", model.ErrInvalidInput)
	}

	// 获取上下文信息 / Get context info
	var contextScope, contextName string
	if s.contextStore != nil && contextID != "" {
		ctxObj, err := s.contextStore.Get(ctx, contextID)
		if err != nil {
			return nil, fmt.Errorf("failed to get context: %w", err)
		}
		contextScope = ctxObj.Scope
		contextName = ctxObj.Name
	}

	// 查询会话记忆 / Query session memories
	memories, err := s.memStore.ListByContextOrdered(ctx, contextID, identity, 0, 200)
	if err != nil {
		return nil, fmt.Errorf("failed to list session memories: %w", err)
	}

	if len(memories) < s.cfg.MinMemories {
		return &SummarizeResponse{
			Skipped:    true,
			SkipReason: fmt.Sprintf("only %d memories, minimum %d required", len(memories), s.cfg.MinMemories),
		}, nil
	}

	// 构建 LLM 输入 / Build LLM input
	var sb strings.Builder
	sourceIDs := make([]string, 0, len(memories))
	for i, mem := range memories {
		fmt.Fprintf(&sb, "[%d] (%s) %s\n", i+1, mem.MessageRole, mem.Content)
		sourceIDs = append(sourceIDs, mem.ID)
	}

	maxTokens := s.cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	resp, err := s.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: sessionSummarizePrompt},
			{Role: "user", Content: fmt.Sprintf("Session: %s\n\nMemories:\n%s", contextName, sb.String())},
		},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM summarization failed: %w", err)
	}

	summary := strings.TrimSpace(resp.Content)
	if summary == "" || summary == "SKIP" {
		return &SummarizeResponse{
			Skipped:    true,
			SkipReason: "LLM determined session has no substantive content",
		}, nil
	}

	// 创建 semantic 记忆 / Create semantic memory
	now := time.Now().UTC()
	semanticMem, err := s.manager.Create(ctx, &model.CreateMemoryRequest{
		Content:       summary,
		Kind:          "mental_model",
		Scope:         contextScope,
		ContextID:     contextID,
		SourceType:    "session_summary",
		SourceRef:     contextID,
		RetentionTier: model.TierLongTerm,
		MemoryClass:   "semantic",
		DerivedFrom:   sourceIDs,
		HappenedAt:    &now,
		TeamID:        identity.TeamID,
		OwnerID:       identity.OwnerID,
		Visibility:    model.VisibilityPrivate,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create semantic summary: %w", err)
	}

	logger.Info("session summarized",
		zap.String("context_id", contextID),
		zap.Int("source_count", len(sourceIDs)),
		zap.String("semantic_id", semanticMem.ID),
	)

	return &SummarizeResponse{
		SemanticMemory: semanticMem,
		SourceCount:    len(sourceIDs),
	}, nil
}

// SummarizeBySourceRef 按 source_ref 总结会话记忆 / Summarize session memories by source_ref
func (s *SessionSummarizer) SummarizeBySourceRef(ctx context.Context, sourceRef string, identity *model.Identity) (*SummarizeResponse, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("LLM provider is required for summarization: %w", model.ErrInvalidInput)
	}

	// 查询会话记忆 / Query session memories by source_ref
	memories, err := s.memStore.ListBySourceRef(ctx, sourceRef, identity, 0, 200)
	if err != nil {
		return nil, fmt.Errorf("failed to list session memories: %w", err)
	}

	if len(memories) < s.cfg.MinMemories {
		return &SummarizeResponse{
			Skipped:    true,
			SkipReason: fmt.Sprintf("only %d memories, minimum %d required", len(memories), s.cfg.MinMemories),
		}, nil
	}

	// 构建 LLM 输入 / Build LLM input
	var sb strings.Builder
	sourceIDs := make([]string, 0, len(memories))
	var scope string
	for i, mem := range memories {
		fmt.Fprintf(&sb, "[%d] (%s) %s\n", i+1, mem.MessageRole, mem.Content)
		sourceIDs = append(sourceIDs, mem.ID)
		if scope == "" {
			scope = mem.Scope
		}
	}

	maxTokens := s.cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	resp, err := s.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: sessionSummarizePrompt},
			{Role: "user", Content: fmt.Sprintf("Session: %s\n\nMemories:\n%s", sourceRef, sb.String())},
		},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM summarization failed: %w", err)
	}

	summary := strings.TrimSpace(resp.Content)
	if summary == "" || summary == "SKIP" {
		return &SummarizeResponse{
			Skipped:    true,
			SkipReason: "LLM determined session has no substantive content",
		}, nil
	}

	now := time.Now().UTC()
	semanticMem, err := s.manager.Create(ctx, &model.CreateMemoryRequest{
		Content:       summary,
		Kind:          "mental_model",
		Scope:         scope,
		SourceType:    "session_summary",
		SourceRef:     sourceRef,
		RetentionTier: model.TierLongTerm,
		MemoryClass:   "semantic",
		DerivedFrom:   sourceIDs,
		HappenedAt:    &now,
		TeamID:        identity.TeamID,
		OwnerID:       identity.OwnerID,
		Visibility:    model.VisibilityPrivate,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create semantic summary: %w", err)
	}

	logger.Info("session summarized by source_ref",
		zap.String("source_ref", sourceRef),
		zap.Int("source_count", len(sourceIDs)),
		zap.String("semantic_id", semanticMem.ID),
	)

	return &SummarizeResponse{
		SemanticMemory: semanticMem,
		SourceCount:    len(sourceIDs),
	}, nil
}
