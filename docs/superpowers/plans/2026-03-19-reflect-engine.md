# Reflect Engine 实施计划 / Reflect Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 Reflect 反思引擎——对已有记忆做多步 LLM 推理，自动生成心智模型记忆，暴露为 `POST /v1/reflect` 端点。

**Architecture:** 新建 `internal/llm/` 包提供 OpenAI 兼容的 Chat 接口，新建 `ReflectEngine` struct 在独立包 `internal/reflect/engine.go` 中实现多轮循环（检索→LLM推理→决策）——独立包避免 `memory` ↔ `search` 循环依赖，通过 `internal/api/reflect_handler.go` 暴露 HTTP 端点，启动时按 nil-check 门控条件注册。

**Tech Stack:** Go 1.25, Gin, net/http, encoding/json, httptest (for mock), testify

**Spec:** `docs/superpowers/specs/2026-03-19-reflect-engine-design.md`

---

## 文件结构 / File Structure

### 新增文件 / New Files

| 文件 / File | 职责 / Responsibility |
|-------------|----------------------|
| `internal/llm/provider.go` | Provider 接口 + ChatMessage/ChatRequest/ChatResponse/ResponseFormat 类型定义 |
| `internal/llm/openai.go` | OpenAI 兼容 Chat 实现（POST /chat/completions） |
| `internal/reflect/engine.go` | ReflectEngine 核心：多轮循环 + JSON 解析 fallback + 写回（独立包，避免 memory↔search 循环依赖） |
| `internal/api/reflect_handler.go` | POST /v1/reflect HTTP handler |
| `testing/llm/provider_test.go` | LLM Provider 单元测试（httptest mock） |
| `testing/reflect/engine_test.go` | ReflectEngine 单元测试 + JSON 解析测试 |
| `testing/api/reflect_test.go` | API 集成测试（HTTP 链路验证） |

### 修改文件 / Modified Files

| 文件 / File | 变更 / Changes |
|-------------|----------------|
| `internal/config/config.go` | OpenAIConfig 加 BaseURL；新增 ReflectConfig；Config 加 Reflect 字段；LoadConfig 加 Viper defaults |
| `internal/model/errors.go` | 新增 5 个 Reflect sentinel errors |
| `internal/model/request.go` | 新增 ReflectRequest/ReflectResponse/ReflectRound/ReflectMeta |
| `internal/api/response.go` | mapError 增加 Reflect 错误映射 |
| `internal/api/router.go` | RouterDeps 加 ReflectEngine；条件注册 POST /v1/reflect |
| `cmd/server/main.go` | 初始化 LLM Provider + ReflectEngine + 注入 RouterDeps |

---

## Task 1: Config 扩展 / Config Extensions

**Files:**
- Modify: `internal/config/config.go`

- [x] **Step 1: 在 OpenAIConfig 中添加 BaseURL 字段**

```go
// OpenAIConfig OpenAI 配置 / OpenAI configuration
type OpenAIConfig struct {
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"` // 新增 / new
	Model   string `mapstructure:"model"`
}
```

在 `internal/config/config.go:88-92` 中将 `OpenAIConfig` 替换为上面的版本。

- [x] **Step 2: 新增 ReflectConfig 类型**

在 `OllamaConfig` 定义之后（约第 104 行）添加：

```go
// ReflectConfig 反思引擎配置 / Reflect engine configuration
type ReflectConfig struct {
	MaxRounds    int           `mapstructure:"max_rounds"`
	TokenBudget  int           `mapstructure:"token_budget"`
	RoundTimeout time.Duration `mapstructure:"round_timeout"`
	AutoSave     bool          `mapstructure:"auto_save"`
}
```

需要在文件顶部 import 中添加 `"time"`。

- [x] **Step 3: Config struct 添加 Reflect 字段**

在 `internal/config/config.go:14-19` 的 `Config` struct 中添加：

```go
type Config struct {
	Storage   StorageConfig   `mapstructure:"storage"`
	Server    ServerConfig    `mapstructure:"server"`
	Partition PartitionConfig `mapstructure:"partitions"`
	LLM       LLMConfig       `mapstructure:"llm"`
	Reflect   ReflectConfig   `mapstructure:"reflect"` // 新增 / new
}
```

- [x] **Step 4: LoadConfig 添加 Viper 默认值**

在 `LoadConfig()` 函数中，在 `viper.SetDefault("llm.embedding.model", ...)` 之后添加：

```go
	// Reflect 默认值 / Reflect defaults
	viper.SetDefault("llm.openai.base_url", "")
	viper.SetDefault("reflect.max_rounds", 3)
	viper.SetDefault("reflect.token_budget", 4096)
	viper.SetDefault("reflect.round_timeout", "30s")
	viper.SetDefault("reflect.auto_save", true)
```

- [x] **Step 5: 验证编译通过**

Run: `cd /d/workspace/AI_P/mem0 && go build ./internal/config/...`
Expected: 编译成功，无错误

- [x] **Step 6: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add ReflectConfig and OpenAI BaseURL for Phase 2 Reflect Engine"
```

---

## Task 2: Sentinel Errors + Request/Response 模型 / Models

**Files:**
- Modify: `internal/model/errors.go`
- Modify: `internal/model/request.go`

- [x] **Step 1: 在 errors.go 末尾添加 Reflect sentinel errors**

在 `ErrInvalidRetentionTier` 之后添加：

```go
	// ErrReflectTimeout 反思推理超时 / Reflect reasoning timeout
	ErrReflectTimeout = errors.New("reflect: timeout exceeded")

	// ErrReflectTokenBudgetExceeded 反思推理token预算超出 / Reflect token budget exceeded
	ErrReflectTokenBudgetExceeded = errors.New("reflect: token budget exceeded")

	// ErrReflectNoMemories 反思检索无结果 / No memories found for reflection
	ErrReflectNoMemories = errors.New("reflect: no relevant memories found")

	// ErrReflectLLMFailed LLM调用失败 / LLM call failed during reflection
	ErrReflectLLMFailed = errors.New("reflect: llm call failed")

	// ErrReflectInvalidRequest 反思请求参数无效 / Invalid reflect request
	ErrReflectInvalidRequest = errors.New("reflect: invalid request")
```

- [x] **Step 2: 在 request.go 末尾添加 Reflect 请求/响应类型**

在文件末尾（`ConversationMessage` 之后）添加：

```go
// ReflectRequest 反思请求 / Reflect request DTO
type ReflectRequest struct {
	Question    string `json:"question" binding:"required"`
	Scope       string `json:"scope,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
	MaxRounds   int    `json:"max_rounds,omitempty"`
	TokenBudget int    `json:"token_budget,omitempty"`
	AutoSave    *bool  `json:"auto_save,omitempty"`
}

// ReflectResponse 反思响应 / Reflect response DTO
type ReflectResponse struct {
	Result      string         `json:"result"`
	NewMemoryID string         `json:"new_memory_id,omitempty"`
	Trace       []ReflectRound `json:"trace"`
	Sources     []string       `json:"sources"`
	Metadata    ReflectMeta    `json:"metadata"`
}

// ReflectRound 反思轮次记录 / Reflect round trace
type ReflectRound struct {
	Round        int      `json:"round"`
	Query        string   `json:"query"`
	RetrievedIDs []string `json:"retrieved_ids"`
	Reasoning    string   `json:"reasoning"`
	NeedMore     bool     `json:"need_more"`
	ParseMethod  string   `json:"parse_method"`
	TokensUsed   int      `json:"tokens_used"`
}

// ReflectMeta 反思元数据 / Reflect metadata
type ReflectMeta struct {
	RoundsUsed     int  `json:"rounds_used"`
	TotalTokens    int  `json:"total_tokens"`
	ParseFallbacks int  `json:"parse_fallbacks"`
	Timeout        bool `json:"timeout"`
	QueryDeduped   bool `json:"query_deduped"`
}
```

- [x] **Step 3: 验证编译通过**

Run: `cd /d/workspace/AI_P/mem0 && go build ./internal/model/...`
Expected: 编译成功

- [x] **Step 4: Commit**

```bash
git add internal/model/errors.go internal/model/request.go
git commit -m "feat(model): add Reflect request/response DTOs and sentinel errors"
```

---

## Task 3: LLM Provider 接口 + OpenAI 实现 / LLM Provider

**Files:**
- Create: `internal/llm/provider.go`
- Create: `internal/llm/openai.go`
- Create: `testing/llm/provider_test.go`

- [x] **Step 1: 创建 provider.go 接口定义**

```go
// Package llm LLM推理调用抽象层 / LLM inference call abstraction layer
package llm

import "context"

// ChatMessage LLM对话消息 / LLM chat message
type ChatMessage struct {
	Role    string `json:"role"`    // system / user / assistant
	Content string `json:"content"`
}

// ResponseFormat LLM响应格式约束 / LLM response format constraint
type ResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

// ChatRequest LLM对话请求 / LLM chat request
type ChatRequest struct {
	Messages       []ChatMessage   // 对话消息列表 / message list
	ResponseFormat *ResponseFormat // 可选，nil则不约束 / optional, nil means no constraint
	Temperature    *float64        // 可选，nil则使用模型默认值 / optional, nil uses model default
	MaxTokens      int             // 可选，0则不限制 / optional, 0 means no limit
}

// ChatResponse LLM对话响应 / LLM chat response
type ChatResponse struct {
	Content          string // 响应内容 / response content
	PromptTokens     int    // 提示词 token 数 / prompt token count
	CompletionTokens int    // 补全 token 数 / completion token count
	TotalTokens      int    // 总 token 数 / total token count
}

// Provider LLM推理接口 / LLM inference provider interface
type Provider interface {
	// Chat 发送对话请求并返回响应 / Send chat request and return response
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
```

- [x] **Step 2: 创建 openai.go 实现**

```go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIProvider OpenAI兼容LLM实现 / OpenAI-compatible LLM provider
// 覆盖 OpenAI/DeepSeek/Moonshot/vLLM/Ollama 等所有兼容接口
type OpenAIProvider struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIProvider 创建OpenAI兼容LLM客户端 / Create OpenAI-compatible LLM client
func NewOpenAIProvider(baseURL, apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// openaiChatRequest OpenAI Chat API 请求体
type openaiChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
}

// openaiChatResponse OpenAI Chat API 响应体
type openaiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat 发送对话请求 / Send chat completion request
func (p *OpenAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	reqBody := openaiChatRequest{
		Model:          p.model,
		Messages:       req.Messages,
		ResponseFormat: req.ResponseFormat,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("llm chat: marshal request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("llm chat: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm chat: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("llm chat: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm chat: API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result openaiChatResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("llm chat: unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("llm chat: API error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("llm chat: empty choices in response")
	}

	return &ChatResponse{
		Content:          result.Choices[0].Message.Content,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
	}, nil
}
```

- [x] **Step 3: 验证 llm 包编译通过**

Run: `cd /d/workspace/AI_P/mem0 && go build ./internal/llm/...`
Expected: 编译成功

- [x] **Step 4: 编写 LLM Provider 测试**

创建 `testing/llm/provider_test.go`：

```go
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

// mockChatResponse 构造 mock OpenAI 响应
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
		{
			name:    "basic chat",
			content: "Hello world",
			tokens:  100,
		},
		{
			name:        "with temperature",
			content:     "response",
			tokens:      50,
			temperature: func() *float64 { v := 0.1; return &v }(),
		},
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
		{
			name:       "500 error",
			statusCode: http.StatusInternalServerError,
			body:       `{"error": {"message": "internal error"}}`,
		},
		{
			name:       "429 rate limit",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error": {"message": "rate limited"}}`,
		},
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
```

- [x] **Step 5: 运行测试**

Run: `cd /d/workspace/AI_P/mem0 && go test ./testing/llm/... -v -count=1`
Expected: 所有测试 PASS

- [x] **Step 6: Commit**

```bash
git add internal/llm/provider.go internal/llm/openai.go testing/llm/provider_test.go
git commit -m "feat(llm): add LLM Provider interface with OpenAI-compatible implementation"
```

---

## Task 4: ReflectEngine 核心实现 / ReflectEngine Core

> **注意 / Note:** ReflectEngine 放在独立包 `internal/reflect/` 而非 `internal/memory/`，因为 `search` 已 import `memory`（`ApplyStrengthWeighting`），若 `memory` 再 import `search` 则形成循环依赖。独立包同时依赖 `memory` 和 `search`，无循环。

**Files:**
- Create: `internal/reflect/engine.go`

- [x] **Step 1: 创建 engine.go 完整实现**

```go
// Package reflect 反思引擎 / Reflect engine for multi-step memory reasoning
package reflect

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
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/search"

	"go.uber.org/zap"
)

// ParseMethod 常量 / Parse method constants
const (
	ParseMethodJSON     = "json"
	ParseMethodExtract  = "extract"
	ParseMethodRetry    = "retry"
	ParseMethodFallback = "fallback"
)

// reflectLLMOutput Reflect引擎LLM输出结构 / Reflect engine LLM output structure
type reflectLLMOutput struct {
	Action     string `json:"action"`
	NextQuery  string `json:"next_query"`
	Conclusion string `json:"conclusion"`
	Reasoning  string `json:"reasoning"`
}

// validate 校验输出字段合法性 / Validate output field legality
func (o *reflectLLMOutput) validate() error {
	if o.Action != "need_more" && o.Action != "conclusion" {
		return fmt.Errorf("invalid action: %s", o.Action)
	}
	if o.Action == "need_more" && strings.TrimSpace(o.NextQuery) == "" {
		return fmt.Errorf("need_more requires next_query")
	}
	if o.Action == "conclusion" && strings.TrimSpace(o.Conclusion) == "" {
		return fmt.Errorf("conclusion is empty")
	}
	return nil
}

// ReflectEngine 反思引擎 / Reflect engine for multi-step memory reasoning
type ReflectEngine struct {
	retriever *search.Retriever
	manager   *memory.Manager
	llm       llm.Provider
	cfg       config.ReflectConfig
}

// NewReflectEngine 创建反思引擎 / Create a new reflect engine
func NewReflectEngine(retriever *search.Retriever, manager *memory.Manager, llmProvider llm.Provider, cfg config.ReflectConfig) *ReflectEngine {
	return &ReflectEngine{
		retriever: retriever,
		manager:   manager,
		llm:       llmProvider,
		cfg:       cfg,
	}
}

// Reflect 执行反思推理 / Execute reflect reasoning
func (e *ReflectEngine) Reflect(ctx context.Context, req *model.ReflectRequest) (*model.ReflectResponse, error) {
	if req.Question == "" {
		return nil, fmt.Errorf("question is required: %w", model.ErrReflectInvalidRequest)
	}

	maxRounds := req.MaxRounds
	if maxRounds <= 0 {
		maxRounds = e.cfg.MaxRounds
	}
	tokenBudget := req.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = e.cfg.TokenBudget
	}
	autoSave := e.cfg.AutoSave
	if req.AutoSave != nil {
		autoSave = *req.AutoSave
	}

	roundTimeout := e.cfg.RoundTimeout
	if roundTimeout == 0 {
		roundTimeout = 30 * time.Second
	}
	totalTimeout := time.Duration(maxRounds) * roundTimeout
	totalCtx, totalCancel := context.WithTimeout(ctx, totalTimeout)
	defer totalCancel()

	query := req.Question
	seenQueries := map[string]bool{query: true}
	var trace []model.ReflectRound
	sourceSet := make(map[string]bool)
	totalTokens := 0
	parseFallbacks := 0
	isTimeout := false
	isDeduped := false
	var lastConclusion string

	for round := 1; round <= maxRounds; round++ {
		roundCtx, roundCancel := context.WithTimeout(totalCtx, roundTimeout)

		// 1) 检索 / Retrieve
		retrieveReq := &model.RetrieveRequest{
			Query:  query,
			TeamID: req.TeamID,
			Limit:  10,
		}
		if req.Scope != "" {
			retrieveReq.Filters = &model.SearchFilters{Scope: req.Scope}
		}

		results, err := e.retriever.Retrieve(roundCtx, retrieveReq)
		roundCancel()
		if err != nil {
			logger.Warn("reflect retrieve failed", zap.Int("round", round), zap.Error(err))
			if round == 1 {
				return nil, fmt.Errorf("reflect retrieve: %w", model.ErrReflectNoMemories)
			}
			break
		}

		if len(results) == 0 && round == 1 {
			return nil, fmt.Errorf("no memories found for question: %w", model.ErrReflectNoMemories)
		}

		// 收集 source IDs
		var retrievedIDs []string
		for _, r := range results {
			if r.Memory != nil {
				retrievedIDs = append(retrievedIDs, r.Memory.ID)
				sourceSet[r.Memory.ID] = true
			}
		}

		// 2) 构造 LLM 请求 / Build LLM request
		memoriesText := formatMemoriesForLLM(results)
		temperature := 0.1

		roundCtx2, roundCancel2 := context.WithTimeout(totalCtx, roundTimeout)
		chatReq := &llm.ChatRequest{
			Messages: []llm.ChatMessage{
				{Role: "system", Content: reflectSystemPrompt},
				{Role: "user", Content: fmt.Sprintf("Question: %s\n\nRelevant memories:\n%s", req.Question, memoriesText)},
			},
			ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
			Temperature:    &temperature,
		}

		// 3) LLM 调用 / LLM call
		chatResp, err := e.llm.Chat(roundCtx2, chatReq)
		roundCancel2()
		if err != nil {
			logger.Warn("reflect llm call failed", zap.Int("round", round), zap.Error(err))
			if roundCtx2.Err() == context.DeadlineExceeded || totalCtx.Err() == context.DeadlineExceeded {
				isTimeout = true
			}
			if lastConclusion == "" {
				return nil, fmt.Errorf("reflect llm call: %w", model.ErrReflectLLMFailed)
			}
			break
		}

		totalTokens += chatResp.TotalTokens

		// 4) 解析输出 / Parse output
		output, parseMethod := e.parseOutput(totalCtx, chatResp.Content, chatReq.Messages)
		if parseMethod == ParseMethodFallback || parseMethod == ParseMethodRetry {
			parseFallbacks++
		}
		if parseMethod == ParseMethodRetry {
			totalTokens += chatResp.TotalTokens / 2 // 估算重试 token
		}

		// 5) 记录 trace / Record trace
		traceRound := model.ReflectRound{
			Round:        round,
			Query:        query,
			RetrievedIDs: retrievedIDs,
			Reasoning:    output.Reasoning,
			NeedMore:     output.Action == "need_more",
			ParseMethod:  parseMethod,
			TokensUsed:   chatResp.TotalTokens,
		}
		trace = append(trace, traceRound)

		// 6) 判断 / Decide
		if output.Action == "conclusion" {
			lastConclusion = output.Conclusion
			break
		}

		lastConclusion = output.Reasoning // 暂存推理内容作为 fallback 结论

		// token 预算检查
		if totalTokens >= tokenBudget {
			logger.Info("reflect token budget exceeded", zap.Int("total", totalTokens), zap.Int("budget", tokenBudget))
			if lastConclusion == "" {
				lastConclusion = output.Reasoning
			}
			break
		}

		// query 去重
		if seenQueries[output.NextQuery] {
			isDeduped = true
			logger.Info("reflect query deduped", zap.String("query", output.NextQuery))
			break
		}
		seenQueries[output.NextQuery] = true
		query = output.NextQuery
	}

	if lastConclusion == "" {
		lastConclusion = "Unable to draw a conclusion from available memories."
	}

	// 收集 sources
	var sources []string
	for id := range sourceSet {
		sources = append(sources, id)
	}

	resp := &model.ReflectResponse{
		Result:  lastConclusion,
		Trace:   trace,
		Sources: sources,
		Metadata: model.ReflectMeta{
			RoundsUsed:     len(trace),
			TotalTokens:    totalTokens,
			ParseFallbacks: parseFallbacks,
			Timeout:        isTimeout,
			QueryDeduped:   isDeduped,
		},
	}

	// 写回 mental_model / Write back mental_model
	if autoSave {
		metadataMap := map[string]any{
			"question": req.Question,
			"sources":  sources,
		}
		createReq := &model.CreateMemoryRequest{
			Content:    lastConclusion,
			Kind:       "mental_model",
			SourceType: "reflect",
			Scope:      req.Scope,
			TeamID:     req.TeamID,
			Metadata:   metadataMap,
		}
		mem, err := e.manager.Create(ctx, createReq)
		if err != nil {
			logger.Error("reflect failed to save mental_model", zap.Error(err))
		} else {
			resp.NewMemoryID = mem.ID
		}
	}

	return resp, nil
}

// parseOutput 解析LLM输出（三级降级）/ Parse LLM output with 3-level fallback
func (e *ReflectEngine) parseOutput(ctx context.Context, raw string, prevMessages []llm.ChatMessage) (*reflectLLMOutput, string) {
	// L1: 直接 JSON 解析
	var output reflectLLMOutput
	if err := json.Unmarshal([]byte(raw), &output); err == nil {
		if output.validate() == nil {
			return &output, ParseMethodJSON
		}
	}

	// L2: 从文本中提取 JSON 片段
	re := regexp.MustCompile(`\{[^{}]*"action"[^{}]*\}`)
	if match := re.FindString(raw); match != "" {
		var extracted reflectLLMOutput
		if err := json.Unmarshal([]byte(match), &extracted); err == nil {
			if extracted.validate() == nil {
				return &extracted, ParseMethodExtract
			}
		}
	}

	// L3: 重试一次（复制 slice 避免修改原始数据）/ Retry once (copy slice to avoid mutation)
	if ctx.Err() == nil {
		retryMessages := make([]llm.ChatMessage, len(prevMessages), len(prevMessages)+1)
		copy(retryMessages, prevMessages)
		retryMessages = append(retryMessages, llm.ChatMessage{
			Role:    "user",
			Content: "Your previous response was not valid JSON. Please respond with ONLY a JSON object containing: action (\"need_more\" or \"conclusion\"), reasoning, conclusion, next_query.",
		})
		temperature := 0.0
		retryReq := &llm.ChatRequest{
			Messages:       retryMessages,
			ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
			Temperature:    &temperature,
		}
		retryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if retryResp, err := e.llm.Chat(retryCtx, retryReq); err == nil {
			var retryOutput reflectLLMOutput
			if err := json.Unmarshal([]byte(retryResp.Content), &retryOutput); err == nil {
				if retryOutput.validate() == nil {
					return &retryOutput, ParseMethodRetry
				}
			}
		}
	}

	// L4: 降级为 conclusion
	return &reflectLLMOutput{
		Action:     "conclusion",
		Conclusion: raw,
		Reasoning:  raw,
	}, ParseMethodFallback
}

// formatMemoriesForLLM 将检索结果格式化为 LLM 可读文本 / Format search results as LLM-readable text
func formatMemoriesForLLM(results []*model.SearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		if r.Memory == nil {
			continue
		}
		m := r.Memory
		sb.WriteString(fmt.Sprintf("[%d] ID: %s\n", i+1, m.ID))
		sb.WriteString(fmt.Sprintf("    Content: %s\n", m.Content))
		if m.Kind != "" {
			sb.WriteString(fmt.Sprintf("    Kind: %s\n", m.Kind))
		}
		if m.Scope != "" {
			sb.WriteString(fmt.Sprintf("    Scope: %s\n", m.Scope))
		}
		if !m.CreatedAt.IsZero() {
			sb.WriteString(fmt.Sprintf("    Created: %s\n", m.CreatedAt.Format(time.RFC3339)))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// reflectSystemPrompt 硬编码的反思引擎系统提示 / Hardcoded reflect engine system prompt
const reflectSystemPrompt = `You are a memory analysis engine. Your task is to analyze retrieved memories and answer the user's question.

Rules:
1. Only use information from the provided memories. Do not fabricate information.
2. Analyze associations, contradictions, and timelines between memories.
3. You MUST respond with a JSON object containing exactly these fields:
   - "action": either "need_more" or "conclusion"
   - "reasoning": your analysis of the memories (explain connections, not just restate content)
   - "conclusion": your final answer (only when action is "conclusion")
   - "next_query": new search keywords (only when action is "need_more")
4. If the provided memories are sufficient to answer the question, set action to "conclusion".
5. If you need more information, set action to "need_more" and provide specific search keywords in "next_query".
6. Do NOT output anything outside the JSON object.`
```

- [x] **Step 2: 验证编译通过**

Run: `cd /d/workspace/AI_P/mem0 && go build ./internal/reflect/...`
Expected: 编译成功

- [x] **Step 3: Commit**

```bash
git add internal/reflect/engine.go
git commit -m "feat(reflect): add ReflectEngine core with multi-round reasoning and 3-level fallback parsing"
```

---

## Task 5: ReflectEngine 测试 / ReflectEngine Tests

**Files:**
- Create: `testing/reflect/engine_test.go`

- [x] **Step 1: 创建 engine_test.go**

```go
package reflect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/search"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLMProvider 模拟 LLM Provider
type mockLLMProvider struct {
	responses []*llm.ChatResponse
	errors    []error
	callIndex int
}

func (m *mockLLMProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return nil, fmt.Errorf("no more mock responses")
	}
	idx := m.callIndex
	m.callIndex++
	if m.errors != nil && idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	return m.responses[idx], nil
}

func conclusionJSON(conclusion, reasoning string) string {
	b, _ := json.Marshal(map[string]string{
		"action":     "conclusion",
		"conclusion": conclusion,
		"reasoning":  reasoning,
	})
	return string(b)
}

func needMoreJSON(nextQuery, reasoning string) string {
	b, _ := json.Marshal(map[string]string{
		"action":     "need_more",
		"next_query": nextQuery,
		"reasoning":  reasoning,
	})
	return string(b)
}

// setupTestEngine 创建测试用 ReflectEngine（需要真实 SQLite）
func setupTestEngine(t *testing.T, mockLLM *mockLLMProvider) (*reflectpkg.ReflectEngine, *memory.Manager, store.MemoryStore) {
	t.Helper()
	ctx := context.Background()

	// 使用内存 SQLite
	stores, err := store.InitStores(ctx, config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled:   true,
				Path:      fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()),
				Tokenizer: config.TokenizerConfig{Provider: "noop"},
			},
		},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { stores.Close() })

	mgr := memory.NewManager(stores.MemoryStore, nil, nil, nil, nil)
	ret := search.NewRetriever(stores.MemoryStore, nil, nil)
	cfg := config.ReflectConfig{
		MaxRounds:    3,
		TokenBudget:  4096,
		RoundTimeout: 0, // 使用默认 30s
		AutoSave:     true,
	}
	engine := reflectpkg.NewReflectEngine(ret, mgr, mockLLM, cfg)
	return engine, mgr, stores.MemoryStore
}

func TestReflect_SingleRound(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("User knows Go", "Memory mentions Go experience"), TotalTokens: 100},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	// 先创建一些记忆
	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "User has 5 years of Go experience", Scope: "test"})
	require.NoError(t, err)

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{
		Question: "What does the user know?",
		Scope:    "test",
	})
	require.NoError(t, err)
	assert.Equal(t, "User knows Go", resp.Result)
	assert.Len(t, resp.Trace, 1)
	assert.Equal(t, 1, resp.Metadata.RoundsUsed)
	assert.NotEmpty(t, resp.NewMemoryID) // auto_save=true
}

func TestReflect_MultiRound(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("Go projects", "Need more info about projects"), TotalTokens: 80},
			{Content: conclusionJSON("Expert in Go web", "Combined memories show web expertise"), TotalTokens: 120},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "User writes Go code daily", Scope: "test"})
	require.NoError(t, err)
	_, err = mgr.Create(ctx, &model.CreateMemoryRequest{Content: "User built a Go web framework", Scope: "test"})
	require.NoError(t, err)

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{
		Question: "What is the user good at?",
		Scope:    "test",
	})
	require.NoError(t, err)
	assert.Equal(t, "Expert in Go web", resp.Result)
	assert.Len(t, resp.Trace, 2)
	assert.Equal(t, 2, resp.Metadata.RoundsUsed)
}

func TestReflect_MaxRoundsExceeded(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("query1", "need more"), TotalTokens: 50},
			{Content: needMoreJSON("query2", "still need more"), TotalTokens: 50},
			{Content: needMoreJSON("query3", "keep searching"), TotalTokens: 50},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Some memory", Scope: "test"})
	require.NoError(t, err)

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{
		Question:  "complex question",
		Scope:     "test",
		MaxRounds: 3,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Trace, 3)
	assert.Equal(t, 3, resp.Metadata.RoundsUsed)
}

func TestReflect_AutoSaveFalse(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("result", "reasoning"), TotalTokens: 100},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test memory", Scope: "test"})
	require.NoError(t, err)

	autoSave := false
	resp, err := engine.Reflect(ctx, &model.ReflectRequest{
		Question: "question",
		Scope:    "test",
		AutoSave: &autoSave,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.NewMemoryID)
}

func TestReflect_QueryDedup(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("same query", "need more"), TotalTokens: 50},
			{Content: needMoreJSON("same query", "still same"), TotalTokens: 50},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test", Scope: "test"})
	require.NoError(t, err)

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{
		Question: "question",
		Scope:    "test",
	})
	require.NoError(t, err)
	assert.True(t, resp.Metadata.QueryDeduped)
}

func TestReflect_EmptyRetrieval(t *testing.T) {
	mock := &mockLLMProvider{}
	engine, _, _ := setupTestEngine(t, mock)

	_, err := engine.Reflect(context.Background(), &model.ReflectRequest{
		Question: "question about nothing",
		Scope:    "nonexistent",
	})
	assert.Error(t, err)
}

func TestReflect_InvalidRequest(t *testing.T) {
	mock := &mockLLMProvider{}
	engine, _, _ := setupTestEngine(t, mock)

	_, err := engine.Reflect(context.Background(), &model.ReflectRequest{})
	assert.Error(t, err)
}

// JSON 解析测试 / JSON parsing tests

func TestParseReflectOutput_ValidJSON(t *testing.T) {
	// 通过单轮 reflect 测试解析正常 JSON
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("valid", "ok"), TotalTokens: 50},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, _ = mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test", Scope: "test"})

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{Question: "q", Scope: "test"})
	require.NoError(t, err)
	assert.Equal(t, "json", resp.Trace[0].ParseMethod)
}

func TestReflect_AutoSaveTrue(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: conclusionJSON("saved result", "analysis"), TotalTokens: 100},
		},
	}
	engine, mgr, memStore := setupTestEngine(t, mock)

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test memory", Scope: "test"})
	require.NoError(t, err)

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{Question: "q", Scope: "test"})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.NewMemoryID)

	// 验证写回的记忆 / Verify written memory
	saved, err := memStore.Get(ctx, resp.NewMemoryID)
	require.NoError(t, err)
	assert.Equal(t, "mental_model", saved.Kind)
	assert.Equal(t, "reflect", saved.SourceType)
	assert.Equal(t, "saved result", saved.Content)
}

func TestReflect_TokenBudgetExceeded(t *testing.T) {
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: needMoreJSON("q2", "reasoning1"), TotalTokens: 3000},
			{Content: needMoreJSON("q3", "reasoning2"), TotalTokens: 2000},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test", Scope: "test"})
	require.NoError(t, err)

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{
		Question:    "q",
		Scope:       "test",
		TokenBudget: 4096,
	})
	require.NoError(t, err)
	// 第一轮 3000 token，未超；第二轮累计 5000 > 4096，应停止
	assert.True(t, resp.Metadata.TotalTokens >= 4096)
}

func TestParseReflectOutput_ExtractFromText(t *testing.T) {
	// LLM 返回文本中嵌入 JSON 片段
	embedded := `Here is my analysis: {"action":"conclusion","conclusion":"extracted","reasoning":"from text"} end`
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: embedded, TotalTokens: 50},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, _ = mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test", Scope: "test"})

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{Question: "q", Scope: "test"})
	require.NoError(t, err)
	assert.Equal(t, "extract", resp.Trace[0].ParseMethod)
	assert.Equal(t, "extracted", resp.Result)
}

func TestParseReflectOutput_Fallback(t *testing.T) {
	// LLM 返回完全非 JSON 文本
	mock := &mockLLMProvider{
		responses: []*llm.ChatResponse{
			{Content: "This is not JSON at all, just plain text answer", TotalTokens: 50},
			// L3 重试也返回非 JSON
			{Content: "Still not JSON", TotalTokens: 30},
		},
	}
	engine, mgr, _ := setupTestEngine(t, mock)

	ctx := context.Background()
	_, _ = mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test", Scope: "test"})

	resp, err := engine.Reflect(ctx, &model.ReflectRequest{Question: "q", Scope: "test"})
	require.NoError(t, err)
	assert.Equal(t, "fallback", resp.Trace[0].ParseMethod)
	assert.True(t, resp.Metadata.ParseFallbacks > 0)
}
```

- [x] **Step 2: 运行测试**

Run: `cd /d/workspace/AI_P/mem0 && go test ./testing/reflect/... -v -count=1`
Expected: 所有测试 PASS

- [x] **Step 3: Commit**

```bash
git add testing/reflect/engine_test.go
git commit -m "test(reflect): add ReflectEngine unit tests with mock LLM provider"
```

---

## Task 6: API Handler + 路由注册 / API Handler + Route Registration

**Files:**
- Create: `internal/api/reflect_handler.go`
- Modify: `internal/api/response.go`
- Modify: `internal/api/router.go`

- [x] **Step 1: 创建 reflect_handler.go**

```go
package api

import (
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"

	"github.com/gin-gonic/gin"
)

// ReflectHandler 反思推理处理器 / Reflect reasoning handler
type ReflectHandler struct {
	engine *reflectpkg.ReflectEngine
}

// NewReflectHandler 创建反思处理器 / Create reflect handler
func NewReflectHandler(engine *reflectpkg.ReflectEngine) *ReflectHandler {
	return &ReflectHandler{engine: engine}
}

// Reflect 处理反思推理请求 / Handle reflect reasoning request
// POST /v1/reflect
func (h *ReflectHandler) Reflect(c *gin.Context) {
	var req model.ReflectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrReflectInvalidRequest)
		return
	}

	resp, err := h.engine.Reflect(c.Request.Context(), &req)
	if err != nil {
		Error(c, err)
		return
	}
	Success(c, resp)
}
```

- [x] **Step 2: 在 response.go 的 mapError 中添加 Reflect 错误映射**

在 `case errors.Is(err, model.ErrInvalidRetentionTier):` 之后、`default:` 之前添加：

```go
	case errors.Is(err, model.ErrReflectInvalidRequest):
		return http.StatusBadRequest, 400, err.Error()
	case errors.Is(err, model.ErrReflectNoMemories):
		return http.StatusNotFound, 404, err.Error()
	case errors.Is(err, model.ErrReflectTimeout):
		return http.StatusRequestTimeout, 408, err.Error()
	case errors.Is(err, model.ErrReflectLLMFailed):
		return http.StatusBadGateway, 502, err.Error()
```

- [x] **Step 3: 在 router.go 的 import 中添加 `reflectpkg "iclude/internal/reflect"` 并修改 RouterDeps**

```go
type RouterDeps struct {
	MemManager     *memory.Manager
	ContextManager *memory.ContextManager
	GraphManager   *memory.GraphManager
	Retriever      *search.Retriever
	DocProcessor   *document.Processor
	TagStore       store.TagStore
	ReflectEngine  *reflectpkg.ReflectEngine // 新增 / new
}
```

- [x] **Step 4: 在 SetupRouter 中条件注册 reflect 端点**

在 `Documents` 注册块之后（`if deps.DocProcessor != nil { ... }` 之后）添加：

```go
		// Reflect
		if deps.ReflectEngine != nil {
			reflectHandler := NewReflectHandler(deps.ReflectEngine)
			v1.POST("/reflect", reflectHandler.Reflect)
		}
```

- [x] **Step 5: 验证编译通过**

Run: `cd /d/workspace/AI_P/mem0 && go build ./internal/api/...`
Expected: 编译成功

- [x] **Step 6: Commit**

```bash
git add internal/api/reflect_handler.go internal/api/response.go internal/api/router.go
git commit -m "feat(api): add POST /v1/reflect handler with error mapping and conditional registration"
```

---

## Task 7: 启动集成 / Startup Integration

**Files:**
- Modify: `cmd/server/main.go`

- [x] **Step 1: 在 main.go 中添加 LLM Provider + ReflectEngine 初始化**

在 import 中添加：
```go
	"strings"
	"iclude/internal/llm"
	reflectpkg "iclude/internal/reflect"
```

在 `docProcessor` 初始化之后（约第 97 行）、`router := api.SetupRouter(...)` 之前添加：

```go
	// 初始化 LLM Provider / Initialize LLM Provider
	var llmProvider llm.Provider
	switch {
	case cfg.LLM.OpenAI.APIKey != "":
		baseURL := cfg.LLM.OpenAI.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		llmProvider = llm.NewOpenAIProvider(baseURL, cfg.LLM.OpenAI.APIKey, cfg.LLM.OpenAI.Model)
		logger.Info("llm provider initialized",
			zap.String("provider", "openai"),
			zap.String("model", cfg.LLM.OpenAI.Model),
		)
	case cfg.LLM.Ollama.BaseURL != "":
		ollamaBase := strings.TrimSuffix(cfg.LLM.Ollama.BaseURL, "/") + "/v1"
		model := cfg.LLM.Ollama.Model
		if model == "" {
			model = cfg.LLM.OpenAI.Model
		}
		llmProvider = llm.NewOpenAIProvider(ollamaBase, "", model)
		logger.Info("llm provider initialized",
			zap.String("provider", "ollama"),
			zap.String("model", model),
		)
	}

	// 初始化 ReflectEngine / Initialize ReflectEngine
	var reflectEngine *reflectpkg.ReflectEngine
	if llmProvider != nil {
		reflectEngine = reflectpkg.NewReflectEngine(ret, memManager, llmProvider, cfg.Reflect)
		logger.Info("reflect engine initialized")
	}
```

- [x] **Step 2: 在 RouterDeps 中添加 ReflectEngine**

将 `router := api.SetupRouter(...)` 调用修改为：

```go
	router := api.SetupRouter(&api.RouterDeps{
		MemManager:     memManager,
		Retriever:      ret,
		ContextManager: ctxManager,
		GraphManager:   graphManager,
		DocProcessor:   docProcessor,
		TagStore:       stores.TagStore,
		ReflectEngine:  reflectEngine,
	})
```

- [x] **Step 3: 验证整体编译通过**

Run: `cd /d/workspace/AI_P/mem0 && go build ./cmd/server/...`
Expected: 编译成功

- [x] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat(server): wire LLM Provider and ReflectEngine into startup"
```

---

## Task 8: config.yaml 更新 / Config File Update

**Files:**
- Modify: `config.yaml`

- [x] **Step 1: 在 config.yaml 的 llm.openai 段添加 base_url**

在 `api_key` 行之后添加：
```yaml
    base_url: ""  # 留空使用 OpenAI 默认，可改为兼容 API 地址
```

- [x] **Step 2: 在 config.yaml 末尾添加 reflect 段**

```yaml
# Reflect 反思引擎配置
reflect:
  max_rounds: 3
  token_budget: 4096
  round_timeout: 30s
  auto_save: true
```

- [x] **Step 3: Commit**

```bash
git add config.yaml
git commit -m "feat(config): add reflect engine and LLM base_url config sections"
```

---

## Task 9: API 集成测试 / API Integration Tests

**Files:**
- Create: `testing/api/reflect_test.go`

- [x] **Step 1: 创建 reflect_test.go**

> 此测试需要在已有的 `testing/api/` 目录下创建，与现有 API 测试风格保持一致。使用 `httptest` 启动 Gin router，mock LLM Provider。

```go
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"iclude/internal/api"
	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/search"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLMProvider 模拟 LLM Provider / Mock LLM Provider
type mockLLMProvider struct {
	response *llm.ChatResponse
	err      error
}

func (m *mockLLMProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func setupReflectTestRouter(t *testing.T, mockLLM llm.Provider) (*httptest.Server, *memory.Manager) {
	t.Helper()
	ctx := context.Background()

	stores, err := store.InitStores(ctx, config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled:   true,
				Path:      fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()),
				Tokenizer: config.TokenizerConfig{Provider: "noop"},
			},
		},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { stores.Close() })

	mgr := memory.NewManager(stores.MemoryStore, nil, nil, nil, nil)
	ret := search.NewRetriever(stores.MemoryStore, nil, nil)
	engine := reflectpkg.NewReflectEngine(ret, mgr, mockLLM, config.ReflectConfig{
		MaxRounds: 3, TokenBudget: 4096, AutoSave: true,
	})

	router := api.SetupRouter(&api.RouterDeps{
		MemManager:    mgr,
		Retriever:     ret,
		ReflectEngine: engine,
	})

	return httptest.NewServer(router), mgr
}

func TestReflectAPI_Success(t *testing.T) {
	conclusionJSON, _ := json.Marshal(map[string]string{
		"action": "conclusion", "conclusion": "result", "reasoning": "ok",
	})
	mock := &mockLLMProvider{response: &llm.ChatResponse{Content: string(conclusionJSON), TotalTokens: 100}}
	ts, mgr := setupReflectTestRouter(t, mock)
	defer ts.Close()

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test memory", Scope: "test"})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"question": "test question", "scope": "test"})
	resp, err := http.Post(ts.URL+"/v1/reflect", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var apiResp api.APIResponse
	json.NewDecoder(resp.Body).Decode(&apiResp)
	assert.Equal(t, 0, apiResp.Code)
}

func TestReflectAPI_MissingQuestion(t *testing.T) {
	mock := &mockLLMProvider{}
	ts, _ := setupReflectTestRouter(t, mock)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"scope": "test"})
	resp, err := http.Post(ts.URL+"/v1/reflect", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestReflectAPI_LLMFailure(t *testing.T) {
	mock := &mockLLMProvider{err: fmt.Errorf("llm exploded")}
	ts, mgr := setupReflectTestRouter(t, mock)
	defer ts.Close()

	ctx := context.Background()
	_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "test", Scope: "test"})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"question": "q", "scope": "test"})
	resp, err := http.Post(ts.URL+"/v1/reflect", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}
```

- [x] **Step 2: 运行测试**

Run: `cd /d/workspace/AI_P/mem0 && go test ./testing/api/... -v -count=1 -run TestReflectAPI`
Expected: 所有测试 PASS

- [x] **Step 3: Commit**

```bash
git add testing/api/reflect_test.go
git commit -m "test(api): add Reflect API integration tests"
```

---

## Task 10: 全量测试验证 / Full Test Suite

- [x] **Step 1: 运行所有测试**

Run: `cd /d/workspace/AI_P/mem0 && go test ./testing/... -v -count=1`
Expected: 所有测试 PASS，包括之前的 store/memory/search/api 测试不受影响

- [x] **Step 2: 运行 go vet**

Run: `cd /d/workspace/AI_P/mem0 && go vet ./...`
Expected: 无问题

- [x] **Step 3: 运行 go fmt**

Run: `cd /d/workspace/AI_P/mem0 && go fmt ./...`
Expected: 无格式问题或已自动修复

- [x] **Step 4: 最终 Commit（如果 fmt 有变更）**

```bash
git add -A
git commit -m "style: format code after Reflect Engine implementation"
```
