// Package embed 向量嵌入适配器 / Embedding adapter layer
package embed

import (
	"fmt"

	"iclude/internal/store"
)

// NewEmbedder 根据配置创建嵌入客户端 / Create embedder based on provider config
// 支持 "openai" 和 "ollama" 两种 provider
// Returns: error if provider is not supported
func NewEmbedder(provider, model, apiKeyOrBaseURL string) (store.Embedder, error) {
	switch provider {
	case "openai":
		return NewOpenAIEmbedder(apiKeyOrBaseURL, model), nil
	case "ollama":
		return NewOllamaEmbedder(apiKeyOrBaseURL, model), nil
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s", provider)
	}
}
