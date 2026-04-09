// Package eval LLM 用量追踪包装器 / LLM usage tracking wrapper
package eval

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"iclude/internal/llm"
)

// LLMUsage 单阶段 LLM 使用统计 / Per-stage LLM usage stats
type LLMUsage struct {
	Stage            string `json:"stage"`
	Calls            int64  `json:"calls"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

// LLMTracker 追踪所有阶段的 LLM 调用 / Tracks LLM calls across all stages
type LLMTracker struct {
	mu     sync.Mutex
	stages map[string]*LLMUsage
}

// NewLLMTracker 创建追踪器 / Create a new tracker
func NewLLMTracker() *LLMTracker {
	return &LLMTracker{stages: make(map[string]*LLMUsage)}
}

// Record 记录一次 LLM 调用 / Record a single LLM call
func (t *LLMTracker) Record(stage string, resp *llm.ChatResponse) {
	if resp == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.stages[stage]
	if !ok {
		u = &LLMUsage{Stage: stage}
		t.stages[stage] = u
	}
	u.Calls++
	u.PromptTokens += int64(resp.PromptTokens)
	u.CompletionTokens += int64(resp.CompletionTokens)
	u.TotalTokens += int64(resp.TotalTokens)
}

// Summary 返回所有阶段的用量汇总 / Return usage summary for all stages
func (t *LLMTracker) Summary() []LLMUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]LLMUsage, 0, len(t.stages))
	for _, u := range t.stages {
		result = append(result, *u)
	}
	return result
}

// Total 返回汇总 / Return grand total
func (t *LLMTracker) Total() LLMUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	var total LLMUsage
	total.Stage = "TOTAL"
	for _, u := range t.stages {
		total.Calls += u.Calls
		total.PromptTokens += u.PromptTokens
		total.CompletionTokens += u.CompletionTokens
		total.TotalTokens += u.TotalTokens
	}
	return total
}

// PrintUsage 打印用量报告 / Print usage report
func (t *LLMTracker) PrintUsage() {
	summary := t.Summary()
	total := t.Total()
	fmt.Println("\n=== LLM Usage Report ===")
	fmt.Printf("%-25s %8s %12s %12s %12s\n", "Stage", "Calls", "Prompt", "Completion", "Total")
	fmt.Println("-------------------------------------------------------------------")
	for _, u := range summary {
		fmt.Printf("%-25s %8d %12d %12d %12d\n", u.Stage, u.Calls, u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	fmt.Println("-------------------------------------------------------------------")
	fmt.Printf("%-25s %8d %12d %12d %12d\n", total.Stage, total.Calls, total.PromptTokens, total.CompletionTokens, total.TotalTokens)
	fmt.Println()
}

// TrackedProvider 包装 llm.Provider 自动追踪用量 / Wraps llm.Provider for automatic usage tracking
type TrackedProvider struct {
	inner   llm.Provider
	tracker *LLMTracker
	stage   atomic.Value // current stage name (string)
}

// NewTrackedProvider 创建追踪包装 / Create tracked provider
func NewTrackedProvider(inner llm.Provider, tracker *LLMTracker) *TrackedProvider {
	tp := &TrackedProvider{inner: inner, tracker: tracker}
	tp.stage.Store("unknown")
	return tp
}

// SetStage 设置当前阶段名（后续调用都归入此阶段）/ Set current stage name
func (p *TrackedProvider) SetStage(name string) {
	p.stage.Store(name)
}

// Chat 转发并追踪 / Forward and track
func (p *TrackedProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	resp, err := p.inner.Chat(ctx, req)
	if err == nil && resp != nil {
		stage := p.stage.Load().(string)
		p.tracker.Record(stage, resp)
	}
	return resp, err
}

// StageProvider 创建固定阶段名的子 Provider / Create a sub-provider pinned to a stage name
func (p *TrackedProvider) StageProvider(stage string) llm.Provider {
	return &stageProvider{inner: p.inner, tracker: p.tracker, stage: stage}
}

// stageProvider 固定阶段名的 Provider / Provider with fixed stage name
type stageProvider struct {
	inner   llm.Provider
	tracker *LLMTracker
	stage   string
}

func (p *stageProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	resp, err := p.inner.Chat(ctx, req)
	if err == nil && resp != nil {
		p.tracker.Record(p.stage, resp)
	}
	return resp, err
}
