// Package eval LLM 用量追踪包装器 / LLM usage tracking wrapper
package eval

import (
	"context"
	"fmt"
	"sync"

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

// stageProvider 固定阶段名的 Provider（构造时绑定 stage，线程安全）
// Provider with fixed stage name (bound at construction, goroutine-safe)
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
