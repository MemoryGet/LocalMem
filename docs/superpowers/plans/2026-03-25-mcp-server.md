# IClude MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone MCP server (`iclude mcp`) that exposes IClude memory capabilities as MCP Tools, Resources, and Prompts over HTTP+SSE transport, connectable by Claude CLI, Cursor, Windsurf, and any MCP-compatible client.

**Architecture:** Two independent Go binaries (`cmd/server/` REST API + `cmd/mcp/` MCP server) sharing the same `internal/` packages via a new `internal/bootstrap/wiring.go`. The MCP server implements MCP 2024-11-05 SSE transport (`GET /sse` + `POST /messages`) using only stdlib `net/http` — no Gin, no third-party MCP SDK.

**Tech Stack:** Go 1.25, stdlib `net/http` + `encoding/json`, existing `internal/memory`, `internal/search`, `internal/reflect`, `internal/model`, `internal/store`, `internal/config`, `internal/logger`.

**Spec:** `docs/superpowers/specs/2026-03-25-mcp-server-design.md`

---

## File Map

### New files
| File | Purpose |
|------|---------|
| `internal/bootstrap/wiring.go` | Shared `Init()` function extracted from `cmd/server/main.go` |
| `internal/mcp/protocol.go` | JSON-RPC 2.0 types + MCP constants + definition structs |
| `internal/mcp/handler.go` | `ToolHandler`, `ResourceHandler`, `PromptHandler` interfaces |
| `internal/mcp/registry.go` | Registration + dispatch for all handler types |
| `internal/mcp/session.go` | Per-client state, identity context, JSON-RPC dispatch |
| `internal/mcp/server.go` | HTTP ServeMux, SSE transport, session lifecycle |
| `internal/mcp/tools/retain.go` | `iclude_retain` tool |
| `internal/mcp/tools/recall.go` | `iclude_recall` tool |
| `internal/mcp/tools/reflect.go` | `iclude_reflect` tool |
| `internal/mcp/tools/ingest_conversation.go` | `iclude_ingest_conversation` tool |
| `internal/mcp/tools/timeline.go` | `iclude_timeline` tool |
| `internal/mcp/resources/recent.go` | `iclude://context/recent` resource |
| `internal/mcp/resources/session_context.go` | `iclude://context/session/{id}` resource |
| `internal/mcp/prompts/memory_context.go` | `memory_context` prompt template |
| `cmd/mcp/main.go` | MCP server entry point |
| `testing/mcp/registry_test.go` | Registry dispatch unit tests |
| `testing/mcp/server_test.go` | SSE framing + session lifecycle unit tests |
| `testing/mcp/tools_test.go` | Tool handler table-driven unit tests |
| `testing/mcp/resources_test.go` | Resource handler unit tests |
| `testing/mcp/integration_test.go` | Full MCP handshake via `httptest.Server` |

### Modified files
| File | Change |
|------|--------|
| `internal/config/config.go` | Add `MCPConfig` struct + `MCP MCPConfig` field to `Config` |
| `config.yaml` | Add `mcp:` section |
| `deploy/config.yaml` | Add `mcp:` section |
| `cmd/server/main.go` | Refactor to call `bootstrap.Init()` — same behaviour, less code |

---

## Task 1: Add MCPConfig to config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`
- Modify: `deploy/config.yaml`

- [x] **Step 1: Add MCPConfig struct and field**

In `internal/config/config.go`, after the `HeartbeatConfig` definition, add:

```go
// MCPConfig MCP 服务器配置 / MCP server configuration
type MCPConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	Port           int    `mapstructure:"port"`
	DefaultTeamID  string `mapstructure:"default_team_id"`
	DefaultOwnerID string `mapstructure:"default_owner_id"`
}
```

Add `MCP MCPConfig \`mapstructure:"mcp"\`` to the `Config` struct after `Heartbeat HeartbeatConfig`.

- [x] **Step 2: Add Viper defaults**

In `LoadConfig()` in `internal/config/config.go`, add after the existing `viper.SetDefault` calls:

```go
viper.SetDefault("mcp.enabled", false)
viper.SetDefault("mcp.port", 8081)
viper.SetDefault("mcp.default_team_id", "default")
viper.SetDefault("mcp.default_owner_id", "mcp-user")
```

- [x] **Step 3: Add mcp section to config.yaml**

Append to `config.yaml` after the `heartbeat:` block:

```yaml
# MCP 服务器配置 / MCP server configuration
mcp:
  enabled: false                   # 手动开启 / Opt-in
  port: 8081
  default_team_id: "default"
  default_owner_id: "mcp-user"
```

Do the same for `deploy/config.yaml`.

- [x] **Step 4: Verify syntax**

```bash
gofmt -e internal/config/config.go
```

Expected: file content printed with no errors.

- [x] **Step 5: Commit**

```bash
git add internal/config/config.go config.yaml deploy/config.yaml
git commit -m "feat(config): add MCPConfig for MCP server"
```

---

## Task 2: Extract bootstrap.Init()

**Files:**
- Create: `internal/bootstrap/wiring.go`

- [x] **Step 1: Create the package directory**

```bash
mkdir -p internal/bootstrap
```

- [x] **Step 2: Write wiring.go**

```go
// Package bootstrap 应用组件共享初始化 / Shared application component bootstrapping
package bootstrap

import (
	"context"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/embed"
	"iclude/internal/heartbeat"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/memory"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/scheduler"
	"iclude/internal/search"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Deps 所有已初始化的业务组件 / All initialized business components
type Deps struct {
	Stores         *store.Stores
	MemManager     *memory.Manager
	Retriever      *search.Retriever
	ContextManager *memory.ContextManager     // nil if ContextStore unavailable
	GraphManager   *memory.GraphManager       // nil if GraphStore unavailable
	DocProcessor   *document.Processor        // nil if DocumentStore unavailable
	ReflectEngine  *reflectpkg.ReflectEngine  // nil if LLM unavailable
	Extractor      *memory.Extractor          // nil if LLM or GraphStore unavailable
	Scheduler      *scheduler.Scheduler
	SchedCancel    context.CancelFunc
	Config         config.Config
}

// Init 根据配置初始化所有业务组件 / Initialize all business components from config
// 返回 cleanup 函数关闭所有资源 / Returns cleanup func that closes all resources
func Init(ctx context.Context, cfg config.Config) (*Deps, func(), error) {
	// Embedder（Qdrant 启用时才需要）
	var embedder store.Embedder
	if cfg.Storage.Qdrant.Enabled {
		embCfg := cfg.LLM.Embedding
		var apiKeyOrURL string
		switch embCfg.Provider {
		case "openai":
			apiKeyOrURL = cfg.LLM.OpenAI.APIKey
		case "ollama":
			apiKeyOrURL = cfg.LLM.Ollama.BaseURL
		}
		var err error
		embedder, err = embed.NewEmbedder(embCfg.Provider, embCfg.Model, apiKeyOrURL)
		if err != nil {
			logger.Warn("failed to create embedder, vector features disabled", zap.Error(err))
		} else {
			logger.Info("embedder initialized",
				zap.String("provider", embCfg.Provider),
				zap.String("model", embCfg.Model),
			)
		}
	}

	// 存储初始化
	stores, err := store.InitStores(ctx, cfg, embedder)
	if err != nil {
		return nil, nil, err
	}

	// LLM Provider
	var llmProvider llm.Provider
	switch {
	case cfg.LLM.OpenAI.APIKey != "":
		baseURL := cfg.LLM.OpenAI.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		llmProvider = llm.NewOpenAIProvider(baseURL, cfg.LLM.OpenAI.APIKey, cfg.LLM.OpenAI.Model)
		logger.Info("llm provider initialized", zap.String("provider", "openai"))
	case cfg.LLM.Ollama.BaseURL != "":
		ollamaBase := strings.TrimSuffix(cfg.LLM.Ollama.BaseURL, "/") + "/v1"
		ollamaModel := cfg.LLM.Ollama.Model
		if ollamaModel == "" {
			ollamaModel = cfg.LLM.OpenAI.Model
		}
		llmProvider = llm.NewOpenAIProvider(ollamaBase, "", ollamaModel)
		logger.Info("llm provider initialized", zap.String("provider", "ollama"))
	}

	// Graph Manager
	var graphManager *memory.GraphManager
	if stores.GraphStore != nil {
		graphManager = memory.NewGraphManager(stores.GraphStore)
	}

	// Extractor
	var extractor *memory.Extractor
	if llmProvider != nil && graphManager != nil {
		extractor = memory.NewExtractor(llmProvider, graphManager, stores.MemoryStore, cfg.Extract)
	}

	// Query Preprocessor
	var preprocessor *search.Preprocessor
	if cfg.Retrieval.Preprocess.Enabled {
		preprocessor = search.NewPreprocessor(stores.Tokenizer, stores.GraphStore, llmProvider, cfg.Retrieval)
	}

	// Access tracker
	accessTracker := memory.NewAccessTracker(stores.MemoryStore, 10000)

	// Consolidator
	var consolidator *memory.Consolidator
	if stores.VectorStore != nil && llmProvider != nil && cfg.Consolidation.Enabled {
		consolidator = memory.NewConsolidator(stores.MemoryStore, stores.VectorStore, llmProvider)
	}

	// Business managers
	memManager := memory.NewManager(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.TagStore, stores.ContextStore, extractor)
	ret := search.NewRetriever(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.GraphStore, llmProvider, cfg.Retrieval, preprocessor, accessTracker)

	var ctxManager *memory.ContextManager
	if stores.ContextStore != nil {
		ctxManager = memory.NewContextManager(stores.ContextStore)
	}

	var docProcessor *document.Processor
	if stores.DocumentStore != nil {
		docProcessor = document.NewProcessor(stores.DocumentStore, stores.MemoryStore, stores.Embedder)
	}

	var reflectEngine *reflectpkg.ReflectEngine
	if llmProvider != nil {
		reflectEngine = reflectpkg.NewReflectEngine(ret, memManager, llmProvider, cfg.Reflect)
	}

	// Scheduler
	sched := scheduler.New()
	schedCtx, schedCancel := context.WithCancel(context.Background())
	if cfg.Scheduler.Enabled {
		sched.Register("access-flush", cfg.Scheduler.AccessFlushInterval, accessTracker.Flush)
		sched.Register("cleanup", cfg.Scheduler.CleanupInterval, func(ctx context.Context) error {
			if _, err := stores.MemoryStore.CleanupExpired(ctx); err != nil {
				logger.Warn("scheduler: cleanup expired failed", zap.Error(err))
			}
			if _, err := stores.MemoryStore.PurgeDeleted(ctx, 30*24*time.Hour); err != nil {
				logger.Warn("scheduler: purge deleted failed", zap.Error(err))
			}
			return nil
		})
		if consolidator != nil {
			sched.Register("consolidation", cfg.Scheduler.ConsolidationInterval, consolidator.Run)
		}
		if cfg.Heartbeat.Enabled {
			hbEngine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, stores.VectorStore, llmProvider)
			sched.Register("heartbeat", cfg.Heartbeat.Interval, hbEngine.Run)
		}
		go sched.Run(schedCtx)
	}

	deps := &Deps{
		Stores:         stores,
		MemManager:     memManager,
		Retriever:      ret,
		ContextManager: ctxManager,
		GraphManager:   graphManager,
		DocProcessor:   docProcessor,
		ReflectEngine:  reflectEngine,
		Extractor:      extractor,
		Scheduler:      sched,
		SchedCancel:    schedCancel,
		Config:         cfg,
	}

	cleanup := func() {
		schedCancel()
		sched.Wait(10 * time.Second)
		stores.Close()
	}

	return deps, cleanup, nil
}
```

- [x] **Step 3: Verify syntax**

```bash
gofmt -e internal/bootstrap/wiring.go
```

Expected: file content with no errors.

- [x] **Step 4: Commit**

```bash
git add internal/bootstrap/wiring.go
git commit -m "feat(bootstrap): extract shared Init() from cmd/server/main.go"
```

---

## Task 3: Refactor cmd/server/main.go to use bootstrap.Init()

**Files:**
- Modify: `cmd/server/main.go`

- [x] **Step 1: Replace main.go with bootstrap-calling version**

Rewrite `cmd/server/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"iclude/internal/api"
	"iclude/internal/bootstrap"
	"iclude/internal/config"
	"iclude/internal/logger"

	"go.uber.org/zap"
)

func main() {
	logger.InitLogger()
	defer logger.GetLogger().Sync()

	if err := config.LoadConfig(); err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	cfg := config.GetConfig()

	deps, cleanup, err := bootstrap.Init(context.Background(), cfg)
	if err != nil {
		logger.Fatal("failed to initialize", zap.Error(err))
	}
	defer cleanup()

	router := api.SetupRouter(&api.RouterDeps{
		MemManager:     deps.MemManager,
		Retriever:      deps.Retriever,
		ContextManager: deps.ContextManager,
		GraphManager:   deps.GraphManager,
		DocProcessor:   deps.DocProcessor,
		TagStore:       deps.Stores.TagStore,
		ReflectEngine:  deps.ReflectEngine,
		Extractor:      deps.Extractor,
		AuthConfig:     cfg.Auth,
		ReflectConfig:  cfg.Reflect,
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{Addr: addr, Handler: router}

	go func() {
		logger.Info("server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server listen failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", zap.Error(err))
	}
	logger.Info("server stopped")
}
```

- [x] **Step 2: Verify syntax**

```bash
gofmt -e cmd/server/main.go
```

Expected: no errors.

- [x] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "refactor(server): delegate wiring to bootstrap.Init()"
```

---

## Task 4: MCP protocol types

**Files:**
- Create: `internal/mcp/protocol.go`

- [x] **Step 1: Write protocol.go**

```go
// Package mcp MCP 协议层 / MCP protocol layer — JSON-RPC 2.0 types and MCP method constants
package mcp

import "encoding/json"

// MCP method constants
const (
	MethodInitialize    = "initialize"
	MethodPing          = "ping"
	MethodToolsList     = "tools/list"
	MethodToolsCall     = "tools/call"
	MethodResourcesList = "resources/list"
	MethodResourcesRead = "resources/read"
	MethodPromptsList   = "prompts/list"
	MethodPromptsGet    = "prompts/get"
)

// MCPProtocolVersion MCP 协议版本 / MCP protocol version for SSE transport
const MCPProtocolVersion = "2024-11-05"

// JSONRPCRequest JSON-RPC 2.0 请求 / JSON-RPC 2.0 request envelope
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse JSON-RPC 2.0 响应 / JSON-RPC 2.0 response envelope
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError JSON-RPC 错误体 / JSON-RPC error object
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ContentBlock 内容块 / Content block (text type)
type ContentBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text,omitempty"`
}

// ToolResult 工具调用结果 / Tool call result
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ToolDefinition MCP tools/list 响应项 / MCP tools/list response item
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ResourceDefinition MCP resources/list 响应项 / MCP resources/list response item
type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// PromptDefinition MCP prompts/list 响应项 / MCP prompts/list response item
type PromptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument 提示模板参数定义 / Prompt argument definition
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptMessage 提示模板消息 / Prompt message
type PromptMessage struct {
	Role    string       `json:"role"`
	Content ContentBlock `json:"content"`
}

// PromptResult 提示模板渲染结果 / Prompt template render result
type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// CallToolParams tools/call 参数 / tools/call params
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ReadResourceParams resources/read 参数 / resources/read params
type ReadResourceParams struct {
	URI string `json:"uri"`
}

// GetPromptParams prompts/get 参数 / prompts/get params
type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// TextResult 创建文本内容结果 / Create text content result
func TextResult(text string) *ToolResult {
	return &ToolResult{Content: []ContentBlock{{Type: "text", Text: text}}}
}

// ErrorResult 创建错误结果 / Create error result
func ErrorResult(msg string) *ToolResult {
	return &ToolResult{Content: []ContentBlock{{Type: "text", Text: msg}}, IsError: true}
}

// okResponse 构建成功 JSON-RPC 响应 / Build successful JSON-RPC response
func okResponse(id json.RawMessage, result any) *JSONRPCResponse {
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// errResponse 构建错误 JSON-RPC 响应 / Build error JSON-RPC response
func errResponse(id json.RawMessage, code int, message string) *JSONRPCResponse {
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &JSONRPCError{Code: code, Message: message}}
}
```

- [x] **Step 2: Verify syntax**

```bash
gofmt -e internal/mcp/protocol.go
```

- [x] **Step 3: Commit**

```bash
git add internal/mcp/protocol.go
git commit -m "feat(mcp): add JSON-RPC 2.0 protocol types and MCP constants"
```

---

## Task 5: Handler interfaces

**Files:**
- Create: `internal/mcp/handler.go`

- [x] **Step 1: Write handler.go**

```go
// Package mcp 处理器接口定义 / Handler interface definitions for MCP tools, resources, and prompts
package mcp

import (
	"context"
	"encoding/json"
)

// ToolHandler MCP 工具处理器接口 / MCP tool handler interface
type ToolHandler interface {
	// Definition 返回工具定义（name, description, JSON Schema）/ Return tool definition
	Definition() ToolDefinition
	// Execute 执行工具调用 / Execute the tool call
	Execute(ctx context.Context, arguments json.RawMessage) (*ToolResult, error)
}

// ResourceHandler MCP 资源处理器接口 / MCP resource handler interface
type ResourceHandler interface {
	// Definition 返回资源定义 / Return resource definition
	Definition() ResourceDefinition
	// Match 判断是否处理此 URI / Whether this handler matches the given URI
	Match(uri string) bool
	// Read 读取资源内容 / Read resource content
	Read(ctx context.Context, uri string) ([]ContentBlock, error)
}

// PromptHandler MCP 提示模板处理器接口 / MCP prompt handler interface
type PromptHandler interface {
	// Definition 返回模板定义 / Return prompt definition
	Definition() PromptDefinition
	// Get 渲染模板 / Render the prompt template
	Get(ctx context.Context, arguments map[string]string) (*PromptResult, error)
}
```

- [x] **Step 2: Verify syntax**

```bash
gofmt -e internal/mcp/handler.go
```

- [x] **Step 3: Commit**

```bash
git add internal/mcp/handler.go
git commit -m "feat(mcp): add ToolHandler, ResourceHandler, PromptHandler interfaces"
```

---

## Task 6: Registry

**Files:**
- Create: `internal/mcp/registry.go`
- Create: `testing/mcp/registry_test.go`

- [x] **Step 1: Write failing registry tests**

```go
// Package mcp_test 注册表测试 / Registry tests
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTool 测试用工具 stub / Tool stub for tests
type stubTool struct{ name string }

func (s *stubTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{Name: s.name, Description: "stub", InputSchema: json.RawMessage(`{}`)}
}
func (s *stubTool) Execute(_ context.Context, _ json.RawMessage) (*mcp.ToolResult, error) {
	return mcp.TextResult("ok:" + s.name), nil
}

// stubResource 测试用资源 stub / Resource stub for tests
type stubResource struct{ uri string }

func (s *stubResource) Definition() mcp.ResourceDefinition {
	return mcp.ResourceDefinition{URI: s.uri, Name: s.uri}
}
func (s *stubResource) Match(uri string) bool { return uri == s.uri }
func (s *stubResource) Read(_ context.Context, _ string) ([]mcp.ContentBlock, error) {
	return []mcp.ContentBlock{{Type: "text", Text: "data:" + s.uri}}, nil
}

// stubPrompt 测试用提示模板 stub / Prompt stub for tests
type stubPrompt struct{ name string }

func (s *stubPrompt) Definition() mcp.PromptDefinition {
	return mcp.PromptDefinition{Name: s.name}
}
func (s *stubPrompt) Get(_ context.Context, _ map[string]string) (*mcp.PromptResult, error) {
	return &mcp.PromptResult{Messages: []mcp.PromptMessage{{Role: "user", Content: mcp.ContentBlock{Type: "text", Text: s.name}}}}, nil
}

func TestRegistry_CallTool_found(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stubTool{name: "my_tool"})
	result, err := reg.CallTool(context.Background(), "my_tool", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "ok:my_tool", result.Content[0].Text)
}

func TestRegistry_CallTool_notFound(t *testing.T) {
	reg := mcp.NewRegistry()
	result, err := reg.CallTool(context.Background(), "missing", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestRegistry_ListTools(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stubTool{name: "a"})
	reg.RegisterTool(&stubTool{name: "b"})
	tools := reg.ListTools()
	assert.Len(t, tools, 2)
}

func TestRegistry_ReadResource_matched(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterResource(&stubResource{uri: "iclude://context/recent"})
	blocks, err := reg.ReadResource(context.Background(), "iclude://context/recent")
	require.NoError(t, err)
	assert.Equal(t, "data:iclude://context/recent", blocks[0].Text)
}

func TestRegistry_ReadResource_notFound(t *testing.T) {
	reg := mcp.NewRegistry()
	_, err := reg.ReadResource(context.Background(), "iclude://unknown")
	assert.Error(t, err)
}

func TestRegistry_GetPrompt(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterPrompt(&stubPrompt{name: "memory_context"})
	result, err := reg.GetPrompt(context.Background(), "memory_context", nil)
	require.NoError(t, err)
	assert.Equal(t, "memory_context", result.Messages[0].Content.Text)
}
```

- [x] **Step 2: Run tests — expect compile failure**

```bash
go test ./testing/mcp/... 2>&1 | head -20
```

Expected: `cannot find package "iclude/internal/mcp"` or similar.

- [x] **Step 3: Write registry.go**

```go
// Package mcp 工具/资源/提示模板注册表 / Tool, Resource, and Prompt registry with dispatch
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Registry 注册表 / Registry for tools, resources, and prompts
type Registry struct {
	tools     map[string]ToolHandler
	resources []ResourceHandler
	prompts   map[string]PromptHandler
}

// NewRegistry 创建注册表 / Create a new registry
func NewRegistry() *Registry {
	return &Registry{
		tools:   make(map[string]ToolHandler),
		prompts: make(map[string]PromptHandler),
	}
}

// RegisterTool 注册工具 / Register a tool handler
func (r *Registry) RegisterTool(h ToolHandler) {
	r.tools[h.Definition().Name] = h
}

// RegisterResource 注册资源 / Register a resource handler
func (r *Registry) RegisterResource(h ResourceHandler) {
	r.resources = append(r.resources, h)
}

// RegisterPrompt 注册提示模板 / Register a prompt handler
func (r *Registry) RegisterPrompt(h PromptHandler) {
	r.prompts[h.Definition().Name] = h
}

// ListTools 列举所有工具定义 / List all registered tool definitions
func (r *Registry) ListTools() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.tools))
	for _, h := range r.tools {
		out = append(out, h.Definition())
	}
	return out
}

// ListResources 列举所有资源定义 / List all registered resource definitions
func (r *Registry) ListResources() []ResourceDefinition {
	out := make([]ResourceDefinition, 0, len(r.resources))
	for _, h := range r.resources {
		out = append(out, h.Definition())
	}
	return out
}

// ListPrompts 列举所有提示模板定义 / List all registered prompt definitions
func (r *Registry) ListPrompts() []PromptDefinition {
	out := make([]PromptDefinition, 0, len(r.prompts))
	for _, h := range r.prompts {
		out = append(out, h.Definition())
	}
	return out
}

// CallTool 调度工具调用 / Dispatch a tool call by name
func (r *Registry) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error) {
	h, ok := r.tools[name]
	if !ok {
		return ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return h.Execute(ctx, args)
}

// ReadResource 读取资源内容 / Dispatch a resource read by URI
func (r *Registry) ReadResource(ctx context.Context, uri string) ([]ContentBlock, error) {
	for _, h := range r.resources {
		if h.Match(uri) {
			return h.Read(ctx, uri)
		}
	}
	return nil, fmt.Errorf("unknown resource URI: %s", uri)
}

// GetPrompt 渲染提示模板 / Dispatch a prompt get by name
func (r *Registry) GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResult, error) {
	h, ok := r.prompts[name]
	if !ok {
		return nil, fmt.Errorf("unknown prompt: %s", name)
	}
	return h.Get(ctx, args)
}
```

- [x] **Step 4: Run tests — expect pass**

```bash
go test ./testing/mcp/... -run TestRegistry -v
```

Expected: all `TestRegistry_*` tests PASS.

- [x] **Step 5: Commit**

```bash
git add internal/mcp/registry.go testing/mcp/registry_test.go
git commit -m "feat(mcp): add Registry with tool/resource/prompt dispatch"
```

---

## Task 7: Session — identity context + JSON-RPC dispatch

**Files:**
- Create: `internal/mcp/session.go`
- Create: `testing/mcp/server_test.go` (partial — session dispatch tests)

- [x] **Step 1: Write failing session dispatch tests**

```go
// Package mcp_test MCP 服务器与会话测试 / MCP server and session tests
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSession_IdentityContext(t *testing.T) {
	id := &model.Identity{TeamID: "team1", OwnerID: "user1"}
	ctx := mcp.WithIdentity(context.Background(), id)
	got := mcp.IdentityFromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, "team1", got.TeamID)
	assert.Equal(t, "user1", got.OwnerID)
}

func TestSession_IdentityContext_nil(t *testing.T) {
	got := mcp.IdentityFromContext(context.Background())
	assert.Nil(t, got)
}

func TestSession_HandleRequest_initialize(t *testing.T) {
	reg := mcp.NewRegistry()
	id := &model.Identity{TeamID: "t", OwnerID: "u"}
	sess := mcp.NewSession("s1", reg, id)

	req := &mcp.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  mcp.MethodInitialize,
	}
	resp := sess.HandleRequest(context.Background(), req)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)
}

func TestSession_HandleRequest_toolsList(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stubTool{name: "test_tool"})
	sess := mcp.NewSession("s2", reg, &model.Identity{})

	req := &mcp.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  mcp.MethodToolsList,
	}
	resp := sess.HandleRequest(context.Background(), req)
	assert.Nil(t, resp.Error)
}

func TestSession_HandleRequest_unknownMethod(t *testing.T) {
	reg := mcp.NewRegistry()
	sess := mcp.NewSession("s3", reg, &model.Identity{})

	req := &mcp.JSONRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "unknown/method"}
	resp := sess.HandleRequest(context.Background(), req)
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
}
```

- [x] **Step 2: Run tests — expect compile failure**

```bash
go test ./testing/mcp/... -run TestSession 2>&1 | head -10
```

- [x] **Step 3: Write session.go**

```go
// Package mcp MCP 客户端会话 / Per-client MCP session with identity context and JSON-RPC dispatch
package mcp

import (
	"context"
	"encoding/json"

	"iclude/internal/model"
)

// identityCtxKey 私有 context key，防止碰撞 / Unexported context key type prevents collisions
type identityCtxKey struct{}

// WithIdentity 注入身份到 context / Inject identity into context
func WithIdentity(ctx context.Context, id *model.Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext 从 context 获取身份 / Get identity from context; nil if absent
func IdentityFromContext(ctx context.Context) *model.Identity {
	id, _ := ctx.Value(identityCtxKey{}).(*model.Identity)
	return id
}

// Session 单个 MCP 客户端会话 / Single MCP client session
type Session struct {
	id       string
	registry *Registry
	identity *model.Identity
	OutCh    chan []byte // 序列化后的 JSON-RPC 响应 / Serialized JSON-RPC responses
	done     chan struct{}
}

// NewSession 创建会话 / Create a new session
func NewSession(id string, registry *Registry, identity *model.Identity) *Session {
	return &Session{
		id:       id,
		registry: registry,
		identity: identity,
		OutCh:    make(chan []byte, 64),
		done:     make(chan struct{}),
	}
}

// HandleRequest 处理 JSON-RPC 请求，返回响应 / Handle a JSON-RPC request and return response
func (s *Session) HandleRequest(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	// 注入当前会话身份 / Inject session identity into context
	ctx = WithIdentity(ctx, s.identity)

	switch req.Method {
	case MethodInitialize:
		return s.handleInitialize(req)
	case MethodPing:
		return okResponse(req.ID, map[string]any{})
	case MethodToolsList:
		return okResponse(req.ID, map[string]any{"tools": s.registry.ListTools()})
	case MethodToolsCall:
		return s.handleToolsCall(ctx, req)
	case MethodResourcesList:
		return okResponse(req.ID, map[string]any{"resources": s.registry.ListResources()})
	case MethodResourcesRead:
		return s.handleResourcesRead(ctx, req)
	case MethodPromptsList:
		return okResponse(req.ID, map[string]any{"prompts": s.registry.ListPrompts()})
	case MethodPromptsGet:
		return s.handlePromptsGet(ctx, req)
	default:
		return errResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

// Close 关闭会话 / Close the session
func (s *Session) Close() {
	select {
	case <-s.done:
	default:
		close(s.done)
		close(s.OutCh)
	}
}

// Done 返回会话关闭信号 / Return session close signal channel
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) handleInitialize(req *JSONRPCRequest) *JSONRPCResponse {
	return okResponse(req.ID, map[string]any{
		"protocolVersion": MCPProtocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"subscribe": false, "listChanged": false},
			"prompts":   map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{"name": "iclude-mcp", "version": "1.0.0"},
	})
}

func (s *Session) handleToolsCall(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	result, err := s.registry.CallTool(ctx, params.Name, params.Arguments)
	if err != nil {
		return errResponse(req.ID, -32603, "tool execution error: "+err.Error())
	}
	return okResponse(req.ID, result)
}

func (s *Session) handleResourcesRead(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	var params ReadResourceParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	blocks, err := s.registry.ReadResource(ctx, params.URI)
	if err != nil {
		return errResponse(req.ID, -32603, err.Error())
	}
	return okResponse(req.ID, map[string]any{"contents": blocks})
}

func (s *Session) handlePromptsGet(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	var params GetPromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	result, err := s.registry.GetPrompt(ctx, params.Name, params.Arguments)
	if err != nil {
		return errResponse(req.ID, -32603, err.Error())
	}
	return okResponse(req.ID, result)
}
```

- [x] **Step 4: Run tests — expect pass**

```bash
go test ./testing/mcp/... -run TestSession -v
```

Expected: all `TestSession_*` PASS.

- [x] **Step 5: Commit**

```bash
git add internal/mcp/session.go testing/mcp/server_test.go
git commit -m "feat(mcp): add Session with identity context and JSON-RPC dispatch"
```

---

## Task 8: HTTP + SSE Server

**Files:**
- Create: `internal/mcp/server.go`

- [x] **Step 1: Write server.go**

```go
// Package mcp HTTP+SSE 传输层 / HTTP+SSE transport for MCP 2024-11-05
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Server MCP HTTP 服务器 / MCP HTTP server with SSE transport
type Server struct {
	registry    *Registry
	defaultID   *model.Identity
	sessions    sync.Map // sessionID → *Session
	mux         *http.ServeMux
	pingInterval time.Duration
}

// NewServer 创建 MCP 服务器 / Create a new MCP server
func NewServer(registry *Registry, defaultID *model.Identity) *Server {
	s := &Server{
		registry:    registry,
		defaultID:   defaultID,
		mux:         http.NewServeMux(),
		pingInterval: 30 * time.Second,
	}
	s.mux.HandleFunc("/sse", s.handleSSE)
	s.mux.HandleFunc("/messages", s.handleMessages)
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	return s
}

// Handler 返回 http.Handler / Return the HTTP handler
func (s *Server) Handler() http.Handler { return s.mux }

// handleSSE GET /sse — 建立 SSE 长连接 / Establish SSE long-lived connection
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sessionID := uuid.New().String()
	identity := s.identityFromRequest(r)
	sess := NewSession(sessionID, s.registry, identity)
	s.sessions.Store(sessionID, sess)
	defer func() {
		s.sessions.Delete(sessionID)
		sess.Close()
	}()

	// 发送 endpoint 事件 / Send endpoint event
	messagesURL := fmt.Sprintf("/messages?session=%s", sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", messagesURL)
	flusher.Flush()

	// SSE 写循环 / SSE write loop
	pingTicker := time.NewTicker(s.pingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sess.Done():
			return
		case <-pingTicker.C:
			fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		case data, open := <-sess.OutCh:
			if !open {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleMessages POST /messages?session={id} — 接收 JSON-RPC 请求 / Receive JSON-RPC requests
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session")
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	sess := val.(*Session)

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	resp := sess.HandleRequest(r.Context(), &req)
	data, err := json.Marshal(resp)
	if err != nil {
		logger.Warn("mcp: failed to marshal response", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	select {
	case sess.OutCh <- data:
	default:
		logger.Warn("mcp: session output channel full, dropping response",
			zap.String("session_id", sessionID))
	}

	w.WriteHeader(http.StatusAccepted)
}

// identityFromRequest 从请求头或默认配置解析身份 / Resolve identity from request or config default
func (s *Server) identityFromRequest(r *http.Request) *model.Identity {
	teamID := r.Header.Get("X-Team-ID")
	ownerID := r.Header.Get("X-Owner-ID")
	if teamID == "" {
		teamID = s.defaultID.TeamID
	}
	if ownerID == "" {
		ownerID = s.defaultID.OwnerID
	}
	return &model.Identity{TeamID: teamID, OwnerID: ownerID}
}
```

> Note: `github.com/google/uuid` — check if already in go.mod. If not, use a simple rand-based UUID or add the dependency:
> ```bash
> grep "google/uuid" go.mod
> ```
> If absent: `go get github.com/google/uuid` OR replace with stdlib implementation:
> ```go
> import "fmt"; import "crypto/rand"
> func newUUID() string {
>     b := make([]byte, 16); rand.Read(b)
>     return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4],b[4:6],b[6:8],b[8:10],b[10:])
> }
> ```

- [x] **Step 2: Verify syntax**

```bash
gofmt -e internal/mcp/server.go
```

- [x] **Step 3: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat(mcp): add HTTP+SSE transport server"
```

---

## Task 9: Tool — iclude_retain

**Files:**
- Create: `internal/mcp/tools/retain.go`
- Create: `testing/mcp/tools_test.go` (first test)

- [x] **Step 1: Write failing test**

```go
// Package mcp_test MCP 工具处理器测试 / MCP tool handler tests
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockManager 测试用 memory.Manager stub / memory.Manager stub for tests
type mockManager struct {
	created *model.Memory
	err     error
}

func (m *mockManager) Create(_ context.Context, req *model.CreateMemoryRequest) (*model.Memory, error) {
	if m.err != nil {
		return nil, m.err
	}
	mem := &model.Memory{ID: "mem-1", Content: req.Content, Kind: req.Kind}
	m.created = mem
	return mem, nil
}

func TestRetainTool_Execute_success(t *testing.T) {
	mgr := &mockManager{}
	tool := tools.NewRetainTool(mgr)

	args, _ := json.Marshal(map[string]any{
		"content": "user prefers dark mode",
		"kind":    "preference",
		"scope":   "user-alice",
	})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "mem-1")
}

func TestRetainTool_Execute_missingContent(t *testing.T) {
	tool := tools.NewRetainTool(&mockManager{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestRetainTool_Definition(t *testing.T) {
	tool := tools.NewRetainTool(&mockManager{})
	def := tool.Definition()
	assert.Equal(t, "iclude_retain", def.Name)
	assert.NotEmpty(t, def.InputSchema)
}
```

- [x] **Step 2: Write retain.go**

```go
// Package tools MCP 工具处理器 / MCP tool handlers
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// MemoryCreator 记忆创建接口 / Interface for creating memories (subset of memory.Manager)
type MemoryCreator interface {
	Create(ctx context.Context, req *model.CreateMemoryRequest) (*model.Memory, error)
}

// RetainTool iclude_retain 工具 / iclude_retain tool handler
type RetainTool struct{ manager MemoryCreator }

// NewRetainTool 创建 retain 工具 / Create retain tool
func NewRetainTool(manager MemoryCreator) *RetainTool { return &RetainTool{manager: manager} }

type retainArgs struct {
	Content     string   `json:"content"`
	Kind        string   `json:"kind,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	AutoExtract bool     `json:"auto_extract,omitempty"`
	SourceType  string   `json:"source_type,omitempty"`
}

// Definition 返回工具定义 / Return tool definition
func (t *RetainTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_retain",
		Description: "Save a memory to IClude. Use for facts, preferences, decisions, and conversation summaries worth remembering across sessions.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content":{"type":"string","description":"Memory content to save"},
				"kind":{"type":"string","enum":["note","fact","skill","profile","conversation","mental_model"]},
				"scope":{"type":"string","description":"Namespace scope, e.g. user/alice or project/myapp"},
				"tags":{"type":"array","items":{"type":"string"}},
				"auto_extract":{"type":"boolean","description":"Auto-extract entities and relations via LLM"},
				"source_type":{"type":"string","enum":["manual","conversation","document","api"]}
			},
			"required":["content"]
		}`),
	}
}

// Execute 执行工具调用 / Execute the tool call
func (t *RetainTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args retainArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.Content == "" {
		return mcp.ErrorResult("content is required"), nil
	}

	id := mcp.IdentityFromContext(ctx)
	req := &model.CreateMemoryRequest{
		Content:     args.Content,
		Kind:        args.Kind,
		Scope:       args.Scope,
		Tags:        args.Tags,
		AutoExtract: args.AutoExtract,
		SourceType:  args.SourceType,
	}
	if id != nil {
		req.OwnerID = id.OwnerID
	}

	mem, err := t.manager.Create(ctx, req)
	if err != nil {
		return mcp.ErrorResult("failed to save memory: " + err.Error()), nil
	}

	out, _ := json.Marshal(map[string]any{"id": mem.ID, "kind": mem.Kind, "scope": mem.Scope})
	return mcp.TextResult(fmt.Sprintf("Memory saved: %s", string(out))), nil
}
```

- [x] **Step 3: Run tests — expect pass**

```bash
go test ./testing/mcp/... -run TestRetainTool -v
```

- [x] **Step 4: Commit**

```bash
git add internal/mcp/tools/retain.go testing/mcp/tools_test.go
git commit -m "feat(mcp/tools): add iclude_retain tool"
```

---

## Task 10: Tool — iclude_recall

**Files:**
- Modify: `internal/mcp/tools/recall.go` (new)
- Modify: `testing/mcp/tools_test.go` (add tests)

- [x] **Step 1: Add recall tests to tools_test.go**

Add to `testing/mcp/tools_test.go`:

```go
// mockRetriever 检索器 stub / Retriever stub for tests
type mockRetriever struct {
	results []*model.SearchResult
	err     error
}

func (m *mockRetriever) Retrieve(_ context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func TestRecallTool_Execute_success(t *testing.T) {
	ret := &mockRetriever{results: []*model.SearchResult{
		{Memory: &model.Memory{ID: "m1", Content: "user likes Go"}, Score: 0.9},
	}}
	tool := tools.NewRecallTool(ret)

	args, _ := json.Marshal(map[string]any{"query": "programming preferences", "limit": 5})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "m1")
}

func TestRecallTool_Execute_missingQuery(t *testing.T) {
	tool := tools.NewRecallTool(&mockRetriever{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}
```

- [x] **Step 2: Write recall.go**

```go
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// MemoryRetriever 检索接口 / Interface for retrieving memories
type MemoryRetriever interface {
	Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error)
}

// RecallTool iclude_recall 工具 / iclude_recall tool handler
type RecallTool struct{ retriever MemoryRetriever }

// NewRecallTool 创建 recall 工具 / Create recall tool
func NewRecallTool(retriever MemoryRetriever) *RecallTool { return &RecallTool{retriever: retriever} }

type recallArgs struct {
	Query      string         `json:"query"`
	Scope      string         `json:"scope,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Filters    map[string]any `json:"filters,omitempty"`
	MmrEnabled *bool          `json:"mmr_enabled,omitempty"`
}

func (t *RecallTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_recall",
		Description: "Retrieve memories from IClude using semantic + full-text search. Call at the start of each turn with the user's message to load relevant context.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string","description":"Search query text"},
				"scope":{"type":"string","description":"Namespace scope filter"},
				"limit":{"type":"integer","minimum":1,"maximum":50,"default":10},
				"filters":{"type":"object","description":"Structured filters: kind, tags, min_strength, happened_after, include_expired"},
				"mmr_enabled":{"type":"boolean","description":"Enable MMR diversity re-ranking"}
			},
			"required":["query"]
		}`),
	}
}

func (t *RecallTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args recallArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.Query == "" {
		return mcp.ErrorResult("query is required"), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	id := mcp.IdentityFromContext(ctx)
	req := &model.RetrieveRequest{
		Query:      args.Query,
		Limit:      limit,
		MmrEnabled: args.MmrEnabled,
	}
	if id != nil {
		req.TeamID = id.TeamID
	}
	// Build filters from structured map + scope shorthand
	if len(args.Filters) > 0 {
		raw, _ := json.Marshal(args.Filters)
		var sf model.SearchFilters
		_ = json.Unmarshal(raw, &sf)
		req.Filters = &sf
	}
	if args.Scope != "" {
		if req.Filters == nil {
			req.Filters = &model.SearchFilters{}
		}
		if req.Filters.Scope == "" {
			req.Filters.Scope = args.Scope
		}
	}

	results, err := t.retriever.Retrieve(ctx, req)
	if err != nil {
		return mcp.ErrorResult("retrieval failed: " + err.Error()), nil
	}

	out, _ := json.Marshal(results)
	return mcp.TextResult(string(out)), nil
}
```

- [x] **Step 3: Run tests — expect pass**

```bash
go test ./testing/mcp/... -run TestRecallTool -v
```

- [x] **Step 4: Commit**

```bash
git add internal/mcp/tools/recall.go testing/mcp/tools_test.go
git commit -m "feat(mcp/tools): add iclude_recall tool"
```

---

## Task 11: Tool — iclude_reflect

**Files:**
- Create: `internal/mcp/tools/reflect.go`
- Modify: `testing/mcp/tools_test.go`

- [x] **Step 1: Add reflect test**

```go
// mockReflectEngine 反思引擎 stub / ReflectEngine stub
type mockReflectEngine struct {
	result string
	err    error
}

func (m *mockReflectEngine) Reflect(_ context.Context, req *model.ReflectRequest) (*model.ReflectResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &model.ReflectResponse{Result: m.result, Metadata: model.ReflectMeta{RoundsUsed: 1}}, nil
}

func TestReflectTool_Execute_success(t *testing.T) {
	eng := &mockReflectEngine{result: "The answer is 42"}
	tool := tools.NewReflectTool(eng)

	args, _ := json.Marshal(map[string]any{"question": "What is the meaning of life?"})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "The answer is 42")
}

func TestReflectTool_Execute_missingQuestion(t *testing.T) {
	tool := tools.NewReflectTool(&mockReflectEngine{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.True(t, result.IsError)
}
```

- [x] **Step 2: Write reflect.go**

```go
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// Reflector 反思推理接口 / Interface for reflect engine
type Reflector interface {
	Reflect(ctx context.Context, req *model.ReflectRequest) (*model.ReflectResponse, error)
}

// ReflectTool iclude_reflect 工具 / iclude_reflect tool handler
type ReflectTool struct{ engine Reflector }

// NewReflectTool 创建 reflect 工具 / Create reflect tool
func NewReflectTool(engine Reflector) *ReflectTool { return &ReflectTool{engine: engine} }

type reflectArgs struct {
	Question  string `json:"question"`
	Scope     string `json:"scope,omitempty"`
	MaxRounds int    `json:"max_rounds,omitempty"`
}

func (t *ReflectTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_reflect",
		Description: "Multi-round LLM reasoning over memories. Use for complex questions that require synthesizing multiple memories or multi-hop reasoning.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"question":{"type":"string","description":"The question to reason about"},
				"scope":{"type":"string","description":"Limit retrieval to this scope"},
				"max_rounds":{"type":"integer","minimum":1,"maximum":5,"default":3}
			},
			"required":["question"]
		}`),
	}
}

func (t *ReflectTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args reflectArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.Question == "" {
		return mcp.ErrorResult("question is required"), nil
	}
	if args.MaxRounds <= 0 {
		args.MaxRounds = 3
	}

	req := &model.ReflectRequest{
		Question:  args.Question,
		Scope:     args.Scope,
		MaxRounds: args.MaxRounds,
	}
	resp, err := t.engine.Reflect(ctx, req)
	if err != nil {
		return mcp.ErrorResult("reflect failed: " + err.Error()), nil
	}

	out, _ := json.Marshal(map[string]any{
		"result":      resp.Result,
		"rounds_used": resp.Metadata.RoundsUsed,
		"sources":     resp.Sources,
	})
	return mcp.TextResult(string(out)), nil
}
```

- [x] **Step 3: Run tests — expect pass**

```bash
go test ./testing/mcp/... -run TestReflectTool -v
```

- [x] **Step 4: Commit**

```bash
git add internal/mcp/tools/reflect.go testing/mcp/tools_test.go
git commit -m "feat(mcp/tools): add iclude_reflect tool"
```

---

## Task 12: Tools — iclude_ingest_conversation + iclude_timeline

**Files:**
- Create: `internal/mcp/tools/ingest_conversation.go`
- Create: `internal/mcp/tools/timeline.go`
- Modify: `testing/mcp/tools_test.go`

- [x] **Step 1: Add tests for both tools**

Add to `testing/mcp/tools_test.go`:

```go
// mockConversationIngester 对话摄取接口 stub
type mockConversationIngester struct{ ctxID string; err error }

func (m *mockConversationIngester) IngestConversation(_ context.Context, req *model.IngestConversationRequest, identity *model.Identity) (string, []*model.Memory, error) {
	if m.err != nil { return "", nil, m.err }
	mems := make([]*model.Memory, len(req.Messages))
	for i := range mems { mems[i] = &model.Memory{} }
	return m.ctxID, mems, nil
}

func TestIngestConversationTool_Execute(t *testing.T) {
	ingester := &mockConversationIngester{ctxID: "ctx-abc"}
	tool := tools.NewIngestConversationTool(ingester)
	args, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hi there"},
		},
		"provider": "claude",
	})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "ctx-abc")
}

// mockTimelineRetriever 时间线检索 stub
type mockTimelineRetriever struct{ memories []*model.Memory; err error }

func (m *mockTimelineRetriever) Timeline(_ context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	if m.err != nil { return nil, m.err }
	return m.memories, nil
}

func TestTimelineTool_Execute(t *testing.T) {
	ret := &mockTimelineRetriever{memories: []*model.Memory{{ID: "m1", Content: "old fact"}}}
	tool := tools.NewTimelineTool(ret)
	args, _ := json.Marshal(map[string]any{"scope": "project-x", "limit": 10})
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}
```

- [x] **Step 2: Write ingest_conversation.go**

```go
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// ConversationIngester 对话摄取接口 / Interface for ingesting conversations
type ConversationIngester interface {
	IngestConversation(ctx context.Context, req *model.IngestConversationRequest, identity *model.Identity) (string, []*model.Memory, error)
}

// IngestConversationTool iclude_ingest_conversation 工具
type IngestConversationTool struct{ manager ConversationIngester }

// NewIngestConversationTool 创建对话摄取工具 / Create ingest conversation tool
func NewIngestConversationTool(manager ConversationIngester) *IngestConversationTool {
	return &IngestConversationTool{manager: manager}
}

type ingestArgs struct {
	Messages   []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Provider   string `json:"provider,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Scope      string `json:"scope,omitempty"`
	ContextID  string `json:"context_id,omitempty"`
}

func (t *IngestConversationTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_ingest_conversation",
		Description: "Batch-save conversation turns as memories. Call after each exchange to persist the conversation.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"messages":{"type":"array","items":{"type":"object","properties":{"role":{"type":"string"},"content":{"type":"string"}},"required":["role","content"]}},
				"provider":{"type":"string","enum":["claude","openai","generic"],"default":"generic"},
				"external_id":{"type":"string","description":"External thread ID for deduplication"},
				"scope":{"type":"string"},
				"context_id":{"type":"string","description":"Attach to existing context node"}
			},
			"required":["messages"]
		}`),
	}
}

func (t *IngestConversationTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args ingestArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if len(args.Messages) == 0 {
		return mcp.ErrorResult("messages is required and must not be empty"), nil
	}

	msgs := make([]model.ConversationMessage, len(args.Messages))
	for i, m := range args.Messages {
		msgs[i] = model.ConversationMessage{Role: m.Role, Content: m.Content}
	}

	provider := args.Provider
	if provider == "" {
		provider = "generic"
	}
	req := &model.IngestConversationRequest{
		Provider:   provider,
		ExternalID: args.ExternalID,
		Scope:      args.Scope,
		ContextID:  args.ContextID,
		Messages:   msgs,
	}

	id := mcp.IdentityFromContext(ctx)
	ctxID, mems, err := t.manager.IngestConversation(ctx, req, id)
	if err != nil {
		return mcp.ErrorResult("ingest failed: " + err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"context_id": ctxID, "saved": len(mems)})
	return mcp.TextResult(string(out)), nil
}
```

- [x] **Step 3: Write timeline.go**

```go
package tools

import (
	"context"
	"encoding/json"
	"time"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// TimelineQuerier 时间线查询接口 / Interface for timeline queries
type TimelineQuerier interface {
	Timeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error)
}

// TimelineTool iclude_timeline 工具
type TimelineTool struct{ retriever TimelineQuerier }

// NewTimelineTool 创建时间线工具 / Create timeline tool
func NewTimelineTool(retriever TimelineQuerier) *TimelineTool { return &TimelineTool{retriever: retriever} }

type timelineArgs struct {
	Scope  string `json:"scope,omitempty"`
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (t *TimelineTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_timeline",
		Description: "Query memories in chronological order. Useful for reviewing recent activity or events within a time range.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"scope":{"type":"string"},
				"after":{"type":"string","format":"date-time","description":"ISO8601 start time"},
				"before":{"type":"string","format":"date-time","description":"ISO8601 end time"},
				"limit":{"type":"integer","minimum":1,"maximum":100,"default":20}
			}
		}`),
	}
}

func (t *TimelineTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args timelineArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	id := mcp.IdentityFromContext(ctx)
	req := &model.TimelineRequest{Scope: args.Scope, Limit: limit}
	if id != nil {
		req.TeamID = id.TeamID
	}
	if args.After != "" {
		if t, err := time.Parse(time.RFC3339, args.After); err == nil {
			req.After = &t
		}
	}
	if args.Before != "" {
		if t, err := time.Parse(time.RFC3339, args.Before); err == nil {
			req.Before = &t
		}
	}

	memories, err := t.retriever.Timeline(ctx, req)
	if err != nil {
		return mcp.ErrorResult("timeline failed: " + err.Error()), nil
	}
	out, _ := json.Marshal(memories)
	return mcp.TextResult(string(out)), nil
}
```

- [x] **Step 4: Run tests**

```bash
go test ./testing/mcp/... -run "TestIngest|TestTimeline" -v
```

- [x] **Step 5: Commit**

```bash
git add internal/mcp/tools/ingest_conversation.go internal/mcp/tools/timeline.go testing/mcp/tools_test.go
git commit -m "feat(mcp/tools): add iclude_ingest_conversation and iclude_timeline tools"
```

---

## Task 13: Resources

**Files:**
- Create: `internal/mcp/resources/recent.go`
- Create: `internal/mcp/resources/session_context.go`
- Create: `testing/mcp/resources_test.go`

- [x] **Step 1: Write failing resource tests**

```go
// Package mcp_test MCP 资源处理器测试 / MCP resource handler tests
package mcp_test

import (
	"context"
	"testing"

	"iclude/internal/mcp"
	"iclude/internal/mcp/resources"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTimelineQuerier for resource tests
type mockTLQuerier struct{ memories []*model.Memory }

func (m *mockTLQuerier) Timeline(_ context.Context, _ *model.TimelineRequest) ([]*model.Memory, error) {
	return m.memories, nil
}

func TestRecentResource_Match(t *testing.T) {
	r := resources.NewRecentResource(&mockTLQuerier{})
	assert.True(t, r.Match("iclude://context/recent"))
	assert.False(t, r.Match("iclude://context/session/abc"))
}

func TestRecentResource_Read(t *testing.T) {
	querier := &mockTLQuerier{memories: []*model.Memory{{ID: "m1", Content: "fact one"}}}
	r := resources.NewRecentResource(querier)
	ctx := mcp.WithIdentity(context.Background(), &model.Identity{TeamID: "t", OwnerID: "u"})
	blocks, err := r.Read(ctx, "iclude://context/recent")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Contains(t, blocks[0].Text, "m1")
}

func TestSessionContextResource_Match(t *testing.T) {
	r := resources.NewSessionContextResource(&mockTLQuerier{})
	assert.True(t, r.Match("iclude://context/session/abc123"))
	assert.False(t, r.Match("iclude://context/recent"))
}
```

- [x] **Step 2: Write recent.go**

```go
// Package resources MCP 资源处理器 / MCP resource handlers
package resources

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// TimelineReader 时间线读取接口 / Interface for timeline reads
type TimelineReader interface {
	Timeline(ctx context.Context, req *model.TimelineRequest) ([]*model.Memory, error)
}

// RecentResource iclude://context/recent 资源
type RecentResource struct{ retriever TimelineReader }

// NewRecentResource 创建最近记忆资源处理器 / Create recent memories resource handler
func NewRecentResource(retriever TimelineReader) *RecentResource {
	return &RecentResource{retriever: retriever}
}

func (r *RecentResource) Definition() mcp.ResourceDefinition {
	return mcp.ResourceDefinition{
		URI:         "iclude://context/recent",
		Name:        "Recent Memories",
		Description: "20 most recent memories — inject at session start for context pre-loading",
		MimeType:    "application/json",
	}
}

func (r *RecentResource) Match(uri string) bool { return uri == "iclude://context/recent" }

func (r *RecentResource) Read(ctx context.Context, _ string) ([]mcp.ContentBlock, error) {
	id := mcp.IdentityFromContext(ctx)
	req := &model.TimelineRequest{Limit: 20}
	if id != nil {
		req.TeamID = id.TeamID
	}
	memories, err := r.retriever.Timeline(ctx, req)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(memories)
	return []mcp.ContentBlock{{Type: "text", Text: string(data)}}, nil
}
```

- [x] **Step 3: Write session_context.go**

```go
package resources

import (
	"context"
	"encoding/json"
	"strings"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

const sessionURIPrefix = "iclude://context/session/"

// SessionContextResource iclude://context/session/{id} 资源
type SessionContextResource struct{ retriever TimelineReader }

// NewSessionContextResource 创建会话上下文资源处理器 / Create session context resource handler
func NewSessionContextResource(retriever TimelineReader) *SessionContextResource {
	return &SessionContextResource{retriever: retriever}
}

func (r *SessionContextResource) Definition() mcp.ResourceDefinition {
	return mcp.ResourceDefinition{
		URI:         "iclude://context/session/{session_id}",
		Name:        "Session Context",
		Description: "Memories attached to a specific session or context node",
		MimeType:    "application/json",
	}
}

func (r *SessionContextResource) Match(uri string) bool {
	return strings.HasPrefix(uri, sessionURIPrefix) && len(uri) > len(sessionURIPrefix)
}

func (r *SessionContextResource) Read(ctx context.Context, uri string) ([]mcp.ContentBlock, error) {
	sessionID := strings.TrimPrefix(uri, sessionURIPrefix)
	id := mcp.IdentityFromContext(ctx)
	req := &model.TimelineRequest{Scope: sessionID, Limit: 50}
	if id != nil {
		req.TeamID = id.TeamID
	}
	memories, err := r.retriever.Timeline(ctx, req)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(memories)
	return []mcp.ContentBlock{{Type: "text", Text: string(data)}}, nil
}
```

- [x] **Step 4: Run tests**

```bash
go test ./testing/mcp/... -run "TestRecentResource|TestSessionContextResource" -v
```

- [x] **Step 5: Commit**

```bash
git add internal/mcp/resources/ testing/mcp/resources_test.go
git commit -m "feat(mcp/resources): add recent and session_context resource handlers"
```

---

## Task 14: Prompt — memory_context

**Files:**
- Create: `internal/mcp/prompts/memory_context.go`

- [x] **Step 1: Write memory_context.go**

```go
// Package prompts MCP 提示模板处理器 / MCP prompt handlers
package prompts

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// MemoryContextPrompt memory_context 提示模板
type MemoryContextPrompt struct{ retriever MemoryRetriever }

// MemoryRetriever 检索接口 / Retriever interface for prompt
type MemoryRetriever interface {
	Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error)
}

// NewMemoryContextPrompt 创建记忆上下文提示模板 / Create memory context prompt handler
func NewMemoryContextPrompt(retriever MemoryRetriever) *MemoryContextPrompt {
	return &MemoryContextPrompt{retriever: retriever}
}

func (p *MemoryContextPrompt) Definition() mcp.PromptDefinition {
	return mcp.PromptDefinition{
		Name:        "memory_context",
		Description: "System prompt that injects relevant memories as context. Use at session start to pre-load IClude memories.",
		Arguments: []mcp.PromptArgument{
			{Name: "query", Description: "Optional query to retrieve relevant memories"},
			{Name: "scope", Description: "Memory scope filter"},
			{Name: "limit", Description: "Max memories to include (default 10)"},
		},
	}
}

func (p *MemoryContextPrompt) Get(ctx context.Context, args map[string]string) (*mcp.PromptResult, error) {
	query := args["query"]
	scope := args["scope"]
	limit := 10
	if l, err := strconv.Atoi(args["limit"]); err == nil && l > 0 {
		limit = l
	}

	var memorySummary string
	if query != "" {
		req := &model.RetrieveRequest{Query: query, Limit: limit}
		if scope != "" {
			req.Filters = &model.SearchFilters{Scope: scope}
		}
		results, err := p.retriever.Retrieve(ctx, req)
		if err == nil && len(results) > 0 {
			data, _ := json.Marshal(results)
			memorySummary = string(data)
		}
	}

	systemText := fmt.Sprintf(`You have access to a persistent memory system via IClude tools.

MEMORY PROTOCOL:
1. At the start of each response, call iclude_recall with the user's message to retrieve relevant memories.
2. Use retrieved memories as additional context when answering.
3. After responding, call iclude_retain to save any new facts, preferences, or decisions with kind="conversation" and auto_extract=true.
4. For complex questions requiring synthesis of multiple memories, call iclude_reflect.

%s`, func() string {
		if memorySummary != "" {
			return "PRELOADED MEMORIES:\n" + memorySummary
		}
		return "No memories preloaded. Use iclude_recall to retrieve relevant context."
	}())

	return &mcp.PromptResult{
		Description: "IClude memory context system prompt",
		Messages: []mcp.PromptMessage{
			{Role: "user", Content: mcp.ContentBlock{Type: "text", Text: systemText}},
		},
	}, nil
}
```

- [x] **Step 2: Verify syntax**

```bash
gofmt -e internal/mcp/prompts/memory_context.go
```

- [x] **Step 3: Commit**

```bash
git add internal/mcp/prompts/memory_context.go
git commit -m "feat(mcp/prompts): add memory_context prompt template"
```

---

## Task 15: cmd/mcp/main.go

**Files:**
- Create: `cmd/mcp/main.go`

- [x] **Step 1: Write main.go**

```go
// Package main IClude MCP Server 入口 / IClude MCP Server entry point
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"iclude/internal/bootstrap"
	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/mcp"
	"iclude/internal/mcp/prompts"
	"iclude/internal/mcp/resources"
	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"go.uber.org/zap"
)

func main() {
	logger.InitLogger()
	defer logger.GetLogger().Sync()

	if err := config.LoadConfig(); err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	cfg := config.GetConfig()

	deps, cleanup, err := bootstrap.Init(context.Background(), cfg)
	if err != nil {
		logger.Fatal("failed to initialize", zap.Error(err))
	}
	defer cleanup()

	// 构建 Registry / Build MCP registry
	reg := mcp.NewRegistry()

	// 注册工具 / Register tools
	reg.RegisterTool(tools.NewRetainTool(deps.MemManager))
	reg.RegisterTool(tools.NewRecallTool(deps.Retriever))
	reg.RegisterTool(tools.NewIngestConversationTool(deps.MemManager))
	reg.RegisterTool(tools.NewTimelineTool(deps.Retriever))
	if deps.ReflectEngine != nil {
		reg.RegisterTool(tools.NewReflectTool(deps.ReflectEngine))
	}

	// 注册资源 / Register resources
	reg.RegisterResource(resources.NewRecentResource(deps.Retriever))
	reg.RegisterResource(resources.NewSessionContextResource(deps.Retriever))

	// 注册提示模板 / Register prompts
	reg.RegisterPrompt(prompts.NewMemoryContextPrompt(deps.Retriever))

	// MCP 默认身份 / Default MCP identity from config
	defaultID := &model.Identity{
		TeamID:  cfg.MCP.DefaultTeamID,
		OwnerID: cfg.MCP.DefaultOwnerID,
	}

	// 启动 MCP 服务 / Start MCP server
	server := mcp.NewServer(reg, defaultID)
	addr := fmt.Sprintf(":%d", cfg.MCP.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      server.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE 连接不限制写超时 / No write timeout for SSE
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("mcp server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("mcp server listen failed", zap.Error(err))
		}
	}()

	// 优雅关闭 / Graceful shutdown (scheduler first, then HTTP)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down mcp server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("mcp server forced shutdown", zap.Error(err))
	}
	logger.Info("mcp server stopped")
}
```

- [x] **Step 2: Verify syntax**

```bash
gofmt -e cmd/mcp/main.go
```

- [x] **Step 3: Check interface compatibility**

Verify that `deps.MemManager` satisfies `tools.MemoryCreator`, `tools.ConversationIngester` and that `deps.Retriever` satisfies `tools.MemoryRetriever`, `tools.TimelineQuerier`, `resources.TimelineReader`, `prompts.MemoryRetriever`. Check method signatures exist:

```bash
grep -n "func.*Manager.*Create\|func.*Manager.*IngestConversation\|func.*Retriever.*Retrieve\|func.*Retriever.*Timeline" internal/memory/manager.go internal/search/retriever.go
```

Resolve any signature mismatches before proceeding.

- [x] **Step 4: Commit**

```bash
git add cmd/mcp/main.go
git commit -m "feat(mcp): add cmd/mcp/main.go entry point"
```

---

## Task 16: Integration Test

**Files:**
- Create: `testing/mcp/integration_test.go`

- [x] **Step 1: Write integration test**

```go
// Package mcp_test MCP 集成测试 / MCP integration test — full handshake via httptest.Server
package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"iclude/internal/mcp"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildTestServer() *httptest.Server {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stubTool{name: "iclude_retain"})
	reg.RegisterTool(&stubTool{name: "iclude_recall"})
	id := &model.Identity{TeamID: "test", OwnerID: "test-user"}
	srv := mcp.NewServer(reg, id)
	return httptest.NewServer(srv.Handler())
}

func sendJSONRPC(t *testing.T, baseURL, sessionID, method string, params any) *mcp.JSONRPCResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	url := baseURL + "/messages?session=" + sessionID
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	return nil // response arrives via SSE
}

func TestIntegration_FullHandshake(t *testing.T) {
	ts := buildTestServer()
	defer ts.Close()

	// 1. Connect SSE
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/sse", nil)
	req.Header.Set("Accept", "text/event-stream")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// 2. Read endpoint event to get session ID
	scanner := bufio.NewScanner(resp.Body)
	var sessionID string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: /messages?session=") {
			sessionID = strings.TrimPrefix(line, "data: /messages?session=")
			break
		}
	}
	require.NotEmpty(t, sessionID, "should receive endpoint event with session ID")

	// 3. Send initialize
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05", "clientInfo": map[string]any{"name": "test"}},
	})
	postResp, err := http.Post(ts.URL+"/messages?session="+sessionID, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, postResp.StatusCode)
	postResp.Body.Close()

	// 4. Read initialize response from SSE
	var initResp mcp.JSONRPCResponse
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(data), &initResp); err == nil && initResp.Result != nil {
				break
			}
		}
	}
	assert.Nil(t, initResp.Error)
	assert.NotNil(t, initResp.Result)

	// 5. Send tools/list
	body, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	postResp, err = http.Post(ts.URL+"/messages?session="+sessionID, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, postResp.StatusCode)
	postResp.Body.Close()

	// 6. Read tools/list response
	var listResp mcp.JSONRPCResponse
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(data), &listResp); err == nil && listResp.Result != nil {
				break
			}
		}
	}
	assert.Nil(t, listResp.Error)
}

func TestIntegration_Messages_SessionNotFound(t *testing.T) {
	ts := buildTestServer()
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	resp, err := http.Post(ts.URL+"/messages?session=nonexistent", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
```

- [x] **Step 2: Run integration tests**

```bash
go test ./testing/mcp/... -run TestIntegration -v -timeout 30s
```

Expected: `TestIntegration_FullHandshake` and `TestIntegration_Messages_SessionNotFound` PASS.

- [x] **Step 3: Run all mcp tests**

```bash
go test ./testing/mcp/... -v
```

Expected: all tests PASS.

- [x] **Step 4: Commit**

```bash
git add testing/mcp/integration_test.go
git commit -m "test(mcp): add full handshake integration test"
```

---

## Task 17: Final validation

- [x] **Step 1: Run full test suite**

```bash
go test ./testing/... -timeout 120s 2>&1 | tail -20
```

Expected: no regressions in existing tests.

- [x] **Step 2: Verify no new dependencies added**

```bash
git diff HEAD~10 go.mod go.sum | grep "^+" | grep -v "^+++"
```

Expected: only `github.com/google/uuid` if used (acceptable), or nothing if stdlib UUID used.

- [x] **Step 3: Format all new files**

```bash
gofmt -w internal/bootstrap/ internal/mcp/ cmd/mcp/
```

- [x] **Step 4: Update Claude CLI settings**

Add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "iclude": {
      "type": "sse",
      "url": "http://localhost:8081/sse"
    }
  }
}
```

- [x] **Step 5: Final commit**

```bash
git add -A
git commit -m "feat(mcp): complete MCP server implementation — 5 tools, 2 resources, 1 prompt, SSE transport"
```

---

## Quick Reference

### Start the servers

```bash
# Terminal 1: IClude REST API
go run ./cmd/server/

# Terminal 2: IClude MCP Server
go run ./cmd/mcp/
```

### Test Claude CLI connection

```bash
claude mcp list     # should show iclude tools
claude mcp call iclude iclude_recall '{"query":"test"}'
```

### Run only MCP tests

```bash
go test ./testing/mcp/... -v
```
