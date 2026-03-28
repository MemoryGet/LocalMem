package llm

import (
	"context"
	"errors"
	"fmt"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// ErrAllProvidersFailed 所有 LLM 提供者均失败时返回 / Returned when all LLM providers in the chain have failed
var ErrAllProvidersFailed = errors.New("all llm providers failed")

// compile-time assertion that FallbackProvider implements Provider
var _ Provider = (*FallbackProvider)(nil)

// FallbackProvider 顺序尝试多个 LLM 提供者的链式 provider / Chain provider that tries multiple LLM providers in order
type FallbackProvider struct {
	providers []Provider
	names     []string
}

// NewFallbackProvider 创建多提供者降级链 / Create a multi-provider fallback chain
func NewFallbackProvider(providers []Provider, names []string) *FallbackProvider {
	if len(providers) != len(names) {
		panic("FallbackProvider: providers and names must have same length")
	}
	if len(providers) == 0 {
		panic("FallbackProvider: at least one provider required")
	}
	return &FallbackProvider{
		providers: providers,
		names:     names,
	}
}

// Chat 按顺序尝试每个提供者，直到成功或全部失败 / Try each provider in order until one succeeds or all fail
func (f *FallbackProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var lastErr error
	for i, p := range f.providers {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: context cancelled: %v", ErrAllProvidersFailed, ctx.Err())
		}
		name := f.providerName(i)
		resp, err := p.Chat(ctx, req)
		if err == nil {
			// 降级成功时记录 / Log when fallback succeeds (non-primary provider)
			if i > 0 {
				logger.Info("llm fallback succeeded",
					zap.String("provider", name),
					zap.Int("index", i),
				)
			}
			return resp, nil
		}
		// 单个提供者失败时记录警告 / Log warning on individual provider failure
		logger.Warn("llm provider failed, trying next",
			zap.String("provider", name),
			zap.Int("index", i),
			zap.Error(err),
		)
		lastErr = err
	}
	return nil, fmt.Errorf("%w: last error: %v", ErrAllProvidersFailed, lastErr)
}

// providerName 返回指定索引的提供者名称 / Return the name of the provider at the given index
func (f *FallbackProvider) providerName(i int) string {
	if i < len(f.names) {
		return f.names[i]
	}
	return fmt.Sprintf("provider-%d", i)
}
