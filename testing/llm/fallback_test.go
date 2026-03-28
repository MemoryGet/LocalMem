package llm_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider 可配置响应的 LLM mock / Configurable-response LLM mock for testing
type mockProvider struct {
	resp *llm.ChatResponse
	err  error
}

// Chat 返回预设响应或错误 / Return preset response or error
func (m *mockProvider) Chat(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.resp, m.err
}

func TestFallbackProvider_Chat(t *testing.T) {
	successResp := &llm.ChatResponse{Content: "hello", TotalTokens: 10}
	someErr := errors.New("provider error")

	tests := []struct {
		name        string
		providers   []llm.Provider
		names       []string
		wantContent string
		wantErr     error
	}{
		{
			name: "first provider succeeds",
			providers: []llm.Provider{
				&mockProvider{resp: successResp},
				&mockProvider{err: someErr},
			},
			names:       []string{"p1", "p2"},
			wantContent: "hello",
		},
		{
			name: "first fails second succeeds",
			providers: []llm.Provider{
				&mockProvider{err: someErr},
				&mockProvider{resp: successResp},
			},
			names:       []string{"p1", "p2"},
			wantContent: "hello",
		},
		{
			name: "all fail",
			providers: []llm.Provider{
				&mockProvider{err: someErr},
				&mockProvider{err: someErr},
			},
			names:   []string{"p1", "p2"},
			wantErr: llm.ErrAllProvidersFailed,
		},
		{
			name: "single provider succeeds",
			providers: []llm.Provider{
				&mockProvider{resp: successResp},
			},
			names:       []string{"only"},
			wantContent: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := llm.NewFallbackProvider(tt.providers, tt.names)
			resp, err := fp.Chat(context.Background(), &llm.ChatRequest{
				Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
			})
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "expected %v, got %v", tt.wantErr, err)
				assert.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				assert.Equal(t, tt.wantContent, resp.Content)
			}
		})
	}
}
