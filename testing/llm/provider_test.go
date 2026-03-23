package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iclude/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockChatResponse(content string, totalTokens int) []byte {
	resp := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"content": content}},
		},
		"usage": map[string]int{
			"prompt_tokens":     totalTokens / 2,
			"completion_tokens": totalTokens / 2,
			"total_tokens":      totalTokens,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestOpenAIProvider_Chat_Success(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		tokens      int
		temperature *float64
	}{
		{name: "basic chat", content: "Hello world", tokens: 100},
		{name: "with temperature", content: "response", tokens: 50, temperature: func() *float64 { v := 0.1; return &v }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/chat/completions", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, "Bearer fake-key", r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusOK)
				w.Write(mockChatResponse(tt.content, tt.tokens))
			}))
			defer ts.Close()

			provider := llm.NewOpenAIProvider(ts.URL, "fake-key", "gpt-4")
			resp, err := provider.Chat(context.Background(), &llm.ChatRequest{
				Messages:    []llm.ChatMessage{{Role: "user", Content: "hi"}},
				Temperature: tt.temperature,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.content, resp.Content)
			assert.Equal(t, tt.tokens, resp.TotalTokens)
		})
	}
}

func TestOpenAIProvider_Chat_NoAPIKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		w.Write(mockChatResponse("ok", 10))
	}))
	defer ts.Close()

	provider := llm.NewOpenAIProvider(ts.URL, "", "model")
	resp, err := provider.Chat(context.Background(), &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestOpenAIProvider_Chat_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	provider := llm.NewOpenAIProvider(ts.URL, "key", "model")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := provider.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
	})
	assert.Error(t, err)
}

func TestOpenAIProvider_Chat_APIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "500 error", statusCode: http.StatusInternalServerError, body: `{"error": {"message": "internal error"}}`},
		{name: "429 rate limit", statusCode: http.StatusTooManyRequests, body: `{"error": {"message": "rate limited"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			provider := llm.NewOpenAIProvider(ts.URL, "key", "model")
			_, err := provider.Chat(context.Background(), &llm.ChatRequest{
				Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
			})
			assert.Error(t, err)
		})
	}
}

func TestOpenAIProvider_Chat_JSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		rf, ok := body["response_format"].(map[string]any)
		assert.True(t, ok, "response_format should be present")
		assert.Equal(t, "json_object", rf["type"])
		w.WriteHeader(http.StatusOK)
		w.Write(mockChatResponse(`{"action":"conclusion","conclusion":"done","reasoning":"ok"}`, 80))
	}))
	defer ts.Close()

	provider := llm.NewOpenAIProvider(ts.URL, "key", "model")
	resp, err := provider.Chat(context.Background(), &llm.ChatRequest{
		Messages:       []llm.ChatMessage{{Role: "user", Content: "hi"}},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Content, "conclusion")
}
